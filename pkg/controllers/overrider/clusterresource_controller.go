/*
Copyright 2025 The KubeFleet Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package overrider features controllers to reconcile the override objects.
package overrider

import (
	"context"
	"fmt"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/controller"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/labels"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/resource"
)

// ClusterResourceReconciler reconciles a clusterResourceOverride object.
type ClusterResourceReconciler struct {
	Reconciler
}

// Reconcile triggers a single reconcile round when the override has changed.
func (r *ClusterResourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	name := req.NamespacedName
	clusterOverride := placementv1beta1.ClusterResourceOverride{}
	overrideRef := klog.KRef(name.Namespace, name.Name)

	startTime := time.Now()
	klog.V(2).InfoS("Reconciliation starts", "clusterResourceOverride", overrideRef)
	defer func() {
		latency := time.Since(startTime).Milliseconds()
		klog.V(2).InfoS("Reconciliation ends", "clusterResourceOverride", overrideRef, "latency", latency)
	}()

	if err := r.Client.Get(ctx, name, &clusterOverride); err != nil {
		if errors.IsNotFound(err) {
			klog.V(4).InfoS("Ignoring notFound clusterResourceOverride", "clusterResourceOverride", overrideRef)
			return ctrl.Result{}, nil
		}
		klog.ErrorS(err, "Failed to get clusterResourceOverride", "clusterResourceOverride", overrideRef)
		return ctrl.Result{}, controller.NewAPIServerError(true, err)
	}

	// Check if the clusterResourceOverride is being deleted
	if clusterOverride.DeletionTimestamp != nil {
		klog.V(4).InfoS("The clusterResourceOverride is being deleted", "clusterResourceOverride", overrideRef)
		return ctrl.Result{}, r.handleOverrideDeleting(ctx, &placementv1beta1.ClusterResourceOverrideSnapshot{}, &clusterOverride)
	}

	// Ensure that we have the finalizer so we can delete all the related snapshots on cleanup
	if err := r.ensureFinalizer(ctx, &clusterOverride); err != nil {
		klog.ErrorS(err, "Failed to ensure the finalizer", "clusterResourceOverride", overrideRef)
		return ctrl.Result{}, err
	}

	// create or update the overrideSnapshot
	return ctrl.Result{}, r.ensureClusterResourceOverrideSnapshot(ctx, &clusterOverride, 10)
}

func (r *ClusterResourceReconciler) ensureClusterResourceOverrideSnapshot(ctx context.Context, cro *placementv1beta1.ClusterResourceOverride, revisionHistoryLimit int) error {
	croKObj := klog.KObj(cro)
	overridePolicy := cro.Spec
	overrideSpecHash, err := resource.HashOf(overridePolicy)
	if err != nil {
		klog.ErrorS(err, "Failed to generate policy hash of clusterResourceOverride", "clusterResourceOverride", croKObj)
		return controller.NewUnexpectedBehaviorError(err)
	}
	// we need to list the snapshots anyway since we need to remove the extra snapshots if there are too many of them.
	snapshotList, err := r.listSortedOverrideSnapshots(ctx, cro)
	if err != nil {
		return err
	}
	// delete redundant snapshot revisions before creating a new snapshot to guarantee that the number of snapshots
	// won't exceed the limit.
	if err = r.removeExtraSnapshot(ctx, snapshotList, revisionHistoryLimit); err != nil {
		return err
	}

	latestSnapshotIndex := -1 // so index starts at 0
	var latestSnapshot *placementv1beta1.ClusterResourceOverrideSnapshot
	if len(snapshotList.Items) != 0 {
		// Convert the last (highest-index) unstructured snapshot to the typed object.
		latestSnapshot = &placementv1beta1.ClusterResourceOverrideSnapshot{}
		if err = runtime.DefaultUnstructuredConverter.FromUnstructured(snapshotList.Items[len(snapshotList.Items)-1].Object, latestSnapshot); err != nil {
			klog.ErrorS(err, "Invalid overrideSnapshot", "clusterResourceOverride", croKObj, "overrideSnapshot", klog.KObj(&snapshotList.Items[len(snapshotList.Items)-1]))
			return controller.NewUnexpectedBehaviorError(err)
		}
		if string(latestSnapshot.Spec.OverrideHash) == overrideSpecHash {
			// The content has not changed; no new snapshot is needed.
			// Ensure the highest-index snapshot is marked latest, then audit siblings to clean
			// up any duplicate latest=true left by a prior partial run.
			if err := r.ensureSnapshotLatest(ctx, latestSnapshot); err != nil {
				return err
			}
			return r.cleanupStaleLatestSiblings(ctx, snapshotList)
		}
		latestSnapshotIndex, err = labels.ExtractIndex(latestSnapshot, placementv1beta1.OverrideIndexLabel)
		if err != nil {
			klog.ErrorS(err, "Failed to parse the override index label", "clusterResourceOverride", croKObj, "overrideSnapshot", klog.KObj(latestSnapshot))
			return controller.NewUnexpectedBehaviorError(err)
		}
	}

	// Create the new snapshot (latest=true) BEFORE flipping the old one to false. This avoids a
	// zero-latest window: if we crash between operations, at worst we leave two snapshots with
	// latest=true, which is resolved by read-time dedup and cleaned up by cleanupStaleLatestSiblings
	// on the next reconcile that hits the hash-match path above.
	latestSnapshotIndex++
	newSnapshot := &placementv1beta1.ClusterResourceOverrideSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf(placementv1beta1.OverrideSnapshotNameFmt, cro.Name, latestSnapshotIndex),
			Labels: map[string]string{
				placementv1beta1.OverrideTrackingLabel: cro.Name,
				placementv1beta1.IsLatestSnapshotLabel: strconv.FormatBool(true),
				placementv1beta1.OverrideIndexLabel:    strconv.Itoa(latestSnapshotIndex),
			},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(cro, placementv1beta1.GroupVersion.WithKind(placementv1beta1.ClusterResourceOverrideKind)),
			},
		},
		Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
			OverrideSpec: overridePolicy,
			OverrideHash: []byte(overrideSpecHash),
		},
	}
	if err := r.Client.Create(ctx, newSnapshot); err != nil {
		if !errors.IsAlreadyExists(err) {
			klog.ErrorS(err, "Failed to create new overrideSnapshot", "newOverrideSnapshot", klog.KObj(newSnapshot))
			return controller.NewAPIServerError(false, err)
		}
		// AlreadyExists here normally means a prior reconcile succeeded the Create but failed to
		// flip the old snapshot. The deterministic name {ParentName}-{index} plus the hash check
		// above mean the existing object should have identical content. Verify before proceeding,
		// because etcd restore from backup, manual edits, or a future hash-function change could
		// leave an existing object whose hash no longer matches the current spec.
		//
		// Read through the uncached client: the cached read can lag the server's write to the
		// just-Created object and return NotFound, forcing a needless requeue.
		existing := &placementv1beta1.ClusterResourceOverrideSnapshot{}
		if getErr := r.UncachedReader.Get(ctx, types.NamespacedName{Name: newSnapshot.Name}, existing); getErr != nil {
			klog.ErrorS(getErr, "Failed to get existing overrideSnapshot for hash verification", "clusterResourceOverride", croKObj, "newOverrideSnapshot", klog.KObj(newSnapshot))
			return controller.NewAPIServerError(false, getErr)
		}
		if string(existing.Spec.OverrideHash) != overrideSpecHash {
			mismatchErr := fmt.Errorf("existing overrideSnapshot %s has hash %q, want %q", newSnapshot.Name, string(existing.Spec.OverrideHash), overrideSpecHash)
			klog.ErrorS(mismatchErr, "Existing overrideSnapshot has different content than expected; will requeue and retry", "clusterResourceOverride", croKObj)
			// Surface to the operator via an Event on the parent CRO so it shows up in
			// `kubectl describe`. Without this, a permanent mismatch (manual edit, hash-fn break)
			// would requeue forever with only log noise to indicate the problem.
			if r.recorder != nil {
				r.recorder.Eventf(cro, corev1.EventTypeWarning, "OverrideSnapshotHashMismatch",
					"existing snapshot %s has a different hash than the current spec; retrying. If this persists, the snapshot needs operator inspection (etcd restore from backup, manual edit, or hash-function change).", newSnapshot.Name)
			}
			// Treat as expected-behavior: the most likely real-world trigger is etcd restore from
			// backup, where retry resolves once the parent CRO converges. A truly unrecoverable
			// state (manual edit, hash-fn break) will keep producing this log on every retry,
			// which is enough for operators to notice without flooding stack traces.
			return controller.NewExpectedBehaviorError(mismatchErr)
		}
		klog.V(2).InfoS("Snapshot already exists with matching content; recovering from prior partial reconcile", "clusterResourceOverride", croKObj, "newOverrideSnapshot", klog.KObj(newSnapshot))
	} else {
		klog.V(2).InfoS("Created new overrideSnapshot", "clusterResourceOverride", croKObj, "newOverrideSnapshot", klog.KObj(newSnapshot))
	}

	// Demote the previous latest snapshot. Tolerate IsNotFound from a concurrent prune or parent
	// deletion: in that case there is nothing to demote, but we still want to run the sibling
	// audit below to clean up any stale latest=true left by earlier crashes.
	if latestSnapshot != nil && latestSnapshot.Labels[placementv1beta1.IsLatestSnapshotLabel] == strconv.FormatBool(true) {
		latestSnapshot.Labels[placementv1beta1.IsLatestSnapshotLabel] = strconv.FormatBool(false)
		if err := r.Client.Update(ctx, latestSnapshot); err != nil {
			if errors.IsNotFound(err) {
				klog.V(2).InfoS("Old overrideSnapshot already gone; skipping demotion", "clusterResourceOverride", croKObj, "overrideSnapshot", klog.KObj(latestSnapshot))
			} else {
				klog.ErrorS(err, "Failed to set the isLatestSnapshot label to false on previous snapshot", "clusterResourceOverride", croKObj, "overrideSnapshot", klog.KObj(latestSnapshot))
				return controller.NewUpdateIgnoreConflictError(err)
			}
		} else {
			klog.V(2).InfoS("Marked previous overrideSnapshot as inactive", "clusterResourceOverride", croKObj, "overrideSnapshot", klog.KObj(latestSnapshot))
		}
	}

	// Audit older siblings for stale latest=true left by prior crashes. cleanupStaleLatestSiblings
	// preserves the highest-index item in snapshotList (the previous latest, which we just demoted
	// in-memory and on the server) and flips any older sibling still carrying latest=true.
	return r.cleanupStaleLatestSiblings(ctx, snapshotList)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterResourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.recorder = mgr.GetEventRecorderFor("clusterresourceoverride-controller")
	return ctrl.NewControllerManagedBy(mgr).
		Named("clusterresourceoverride-controller").
		For(&placementv1beta1.ClusterResourceOverride{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}
