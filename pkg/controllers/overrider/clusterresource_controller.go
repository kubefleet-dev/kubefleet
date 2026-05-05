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

	latestSnapshotIndex := -1 // index starts at 0
	var latestSnapshot *placementv1beta1.ClusterResourceOverrideSnapshot
	if len(snapshotList.Items) != 0 {
		latestSnapshot = &placementv1beta1.ClusterResourceOverrideSnapshot{}
		if err = runtime.DefaultUnstructuredConverter.FromUnstructured(snapshotList.Items[len(snapshotList.Items)-1].Object, latestSnapshot); err != nil {
			klog.ErrorS(err, "Invalid overrideSnapshot", "clusterResourceOverride", croKObj, "overrideSnapshot", klog.KObj(&snapshotList.Items[len(snapshotList.Items)-1]))
			return controller.NewUnexpectedBehaviorError(err)
		}
		if string(latestSnapshot.Spec.OverrideHash) == overrideSpecHash {
			// Hash matches: re-mark this snapshot latest (idempotent) and audit siblings.
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

	// Create-then-demote: avoids a zero-latest window across crashes. Any transient duplicate
	// is resolved by read-time dedup and the sibling audit on the next reconcile.
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
		// AlreadyExists usually means a prior reconcile created this snapshot but didn't flip
		// the old one. Verify content matches before proceeding; the realistic drift sources
		// are manual edits or a hash-function change between controller versions. Use the
		// uncached reader — the cache can lag the just-Created object.
		existing := &placementv1beta1.ClusterResourceOverrideSnapshot{}
		if getErr := r.UncachedReader.Get(ctx, types.NamespacedName{Name: newSnapshot.Name}, existing); getErr != nil {
			klog.ErrorS(getErr, "Failed to get existing overrideSnapshot for hash verification", "clusterResourceOverride", croKObj, "newOverrideSnapshot", klog.KObj(newSnapshot))
			return controller.NewAPIServerError(false, getErr)
		}
		// Compare stored OverrideHash, not a re-hash of existing.Spec.OverrideSpec: API-server
		// defaulting on Create (selectionScope, scope, overrideType) makes the read-back spec
		// differ from the input, so a recompute would never match.
		if string(existing.Spec.OverrideHash) != overrideSpecHash {
			mismatchErr := fmt.Errorf("existing overrideSnapshot %s has stored hash %q, want %q", newSnapshot.Name, string(existing.Spec.OverrideHash), overrideSpecHash)
			klog.ErrorS(mismatchErr, "Existing overrideSnapshot has different content than the current spec; deleting it so the next reconcile can recreate with correct content", "clusterResourceOverride", croKObj)
			// Event surfaces the recovery in `kubectl describe`.
			if r.recorder != nil {
				r.recorder.Eventf(cro, corev1.EventTypeWarning, "OverrideSnapshotHashMismatch",
					"existing snapshot %s has different content than the current spec; deleting it for the next reconcile to recreate. If this persists, investigate the snapshot for manual edits or a hash-function change between controller versions.", newSnapshot.Name)
			}
			// Drive observed → desired: delete the mismatched snapshot so the next reconcile
			// recreates it with the correct content.
			if delErr := r.Client.Delete(ctx, existing); delErr != nil && !errors.IsNotFound(delErr) {
				klog.ErrorS(delErr, "Failed to delete mismatched overrideSnapshot", "clusterResourceOverride", croKObj, "overrideSnapshot", klog.KObj(existing))
				return controller.NewAPIServerError(false, delErr)
			}
			return controller.NewExpectedBehaviorError(mismatchErr)
		}
		klog.V(2).InfoS("Snapshot already exists with matching content; recovering from prior partial reconcile", "clusterResourceOverride", croKObj, "newOverrideSnapshot", klog.KObj(newSnapshot))
	} else {
		klog.V(2).InfoS("Created new overrideSnapshot", "clusterResourceOverride", croKObj, "newOverrideSnapshot", klog.KObj(newSnapshot))
	}

	// Demote the previous latest. IsNotFound is fine — there's nothing to demote, but we still
	// want to run the sibling audit below.
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

	// Audit older siblings; cleanupStaleLatestSiblings preserves the last (just-demoted) item.
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
