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

// ResourceReconciler reconciles a ResourceOverride object.
type ResourceReconciler struct {
	Reconciler
}

// Reconcile triggers a single reconcile round when the override has changed.
func (r *ResourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	name := req.NamespacedName
	resourceOverride := placementv1beta1.ResourceOverride{}
	overrideRef := klog.KRef(name.Namespace, name.Name)

	startTime := time.Now()
	klog.V(2).InfoS("Reconciliation starts", "resourceOverride", overrideRef)
	defer func() {
		latency := time.Since(startTime).Milliseconds()
		klog.V(2).InfoS("Reconciliation ends", "resourceOverride", overrideRef, "latency", latency)
	}()

	if err := r.Client.Get(ctx, name, &resourceOverride); err != nil {
		if errors.IsNotFound(err) {
			klog.V(4).InfoS("Ignoring notFound resourceOverride", "resourceOverride", overrideRef)
			return ctrl.Result{}, nil
		}
		klog.ErrorS(err, "Failed to get resourceOverride", "resourceOverride", overrideRef)
		return ctrl.Result{}, controller.NewAPIServerError(true, err)
	}

	// Check if the resourceOverride is being deleted
	if resourceOverride.DeletionTimestamp != nil {
		klog.V(4).InfoS("The resourceOverride is being deleted", "resourceOverride", overrideRef)
		return ctrl.Result{}, r.handleOverrideDeleting(ctx, &placementv1beta1.ResourceOverrideSnapshot{}, &resourceOverride)
	}

	// Ensure that we have the finalizer so we can delete all the related snapshots on cleanup
	err := r.ensureFinalizer(ctx, &resourceOverride)
	if err != nil {
		klog.ErrorS(err, "Failed to ensure the finalizer", "resourceOverride", overrideRef)
		return ctrl.Result{}, err
	}

	// create or update the overrideSnapshot
	return ctrl.Result{}, r.ensureResourceOverrideSnapshot(ctx, &resourceOverride, 10)
}

func (r *ResourceReconciler) ensureResourceOverrideSnapshot(ctx context.Context, ro *placementv1beta1.ResourceOverride, revisionHistoryLimit int) error {
	roKObj := klog.KObj(ro)
	overridePolicy := ro.Spec
	overrideSpecHash, err := resource.HashOf(overridePolicy)
	if err != nil {
		klog.ErrorS(err, "Failed to generate policy hash of ResourceOverride", "resourceOverride", roKObj)
		return controller.NewUnexpectedBehaviorError(err)
	}
	// We always list the snapshots so we can prune any that exceed the revision history limit.
	snapshotList, err := r.listSortedOverrideSnapshots(ctx, ro)
	if err != nil {
		return err
	}
	if err = r.removeExtraSnapshot(ctx, snapshotList, revisionHistoryLimit); err != nil {
		return err
	}

	latestSnapshotIndex := -1
	var latestSnapshot *placementv1beta1.ResourceOverrideSnapshot
	if len(snapshotList.Items) != 0 {
		// Convert the last (highest-index) unstructured snapshot to the typed object.
		latestSnapshot = &placementv1beta1.ResourceOverrideSnapshot{}
		if err = runtime.DefaultUnstructuredConverter.FromUnstructured(snapshotList.Items[len(snapshotList.Items)-1].Object, latestSnapshot); err != nil {
			klog.ErrorS(err, "Invalid overrideSnapshot", "resourceOverride", roKObj, "overrideSnapshot", klog.KObj(&snapshotList.Items[len(snapshotList.Items)-1]))
			return controller.NewUnexpectedBehaviorError(err)
		}
		if string(latestSnapshot.Spec.OverrideHash) == overrideSpecHash {
			// The content has not changed; no new snapshot is needed. Ensure the highest-index
			// snapshot is marked latest, then audit siblings to clean up any duplicate latest=true
			// left by a prior partial run.
			if err := r.ensureSnapshotLatest(ctx, latestSnapshot); err != nil {
				return err
			}
			return r.cleanupStaleLatestSiblings(ctx, snapshotList)
		}
		latestSnapshotIndex, err = labels.ExtractIndex(latestSnapshot, placementv1beta1.OverrideIndexLabel)
		if err != nil {
			klog.ErrorS(err, "Failed to parse the override index label", "resourceOverride", roKObj, "overrideSnapshot", klog.KObj(latestSnapshot))
			return controller.NewUnexpectedBehaviorError(err)
		}
	}

	// Create the new snapshot (latest=true) BEFORE flipping the old one to false. See the CRO
	// controller for the rationale; this avoids a zero-latest window across crashes.
	latestSnapshotIndex++
	newSnapshot := &placementv1beta1.ResourceOverrideSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf(placementv1beta1.OverrideSnapshotNameFmt, ro.Name, latestSnapshotIndex),
			Namespace: ro.Namespace,
			Labels: map[string]string{
				placementv1beta1.OverrideTrackingLabel: ro.Name,
				placementv1beta1.IsLatestSnapshotLabel: strconv.FormatBool(true),
				placementv1beta1.OverrideIndexLabel:    strconv.Itoa(latestSnapshotIndex),
			},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(ro, placementv1beta1.GroupVersion.WithKind(placementv1beta1.ResourceOverrideKind)),
			},
		},
		Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
			OverrideSpec: overridePolicy,
			OverrideHash: []byte(overrideSpecHash),
		},
	}
	if err = r.Client.Create(ctx, newSnapshot); err != nil {
		if !errors.IsAlreadyExists(err) {
			klog.ErrorS(err, "Failed to create new overrideSnapshot", "newOverrideSnapshot", klog.KObj(newSnapshot))
			return controller.NewAPIServerError(false, err)
		}
		// AlreadyExists here normally means a prior reconcile succeeded the Create but failed to
		// flip the old snapshot. Verify the existing object's hash before proceeding so we don't
		// silently promote stale content (etcd restore from backup, manual edit, future hash bug).
		// Read through the uncached client: the cached read can lag the just-Created object.
		existing := &placementv1beta1.ResourceOverrideSnapshot{}
		if getErr := r.UncachedReader.Get(ctx, types.NamespacedName{Name: newSnapshot.Name, Namespace: newSnapshot.Namespace}, existing); getErr != nil {
			klog.ErrorS(getErr, "Failed to get existing overrideSnapshot for hash verification", "resourceOverride", roKObj, "newOverrideSnapshot", klog.KObj(newSnapshot))
			return controller.NewAPIServerError(false, getErr)
		}
		if string(existing.Spec.OverrideHash) != overrideSpecHash {
			mismatchErr := fmt.Errorf("existing overrideSnapshot %s/%s has hash %q, want %q", existing.Namespace, existing.Name, string(existing.Spec.OverrideHash), overrideSpecHash)
			klog.ErrorS(mismatchErr, "Existing overrideSnapshot has different content than expected; will requeue and retry", "resourceOverride", roKObj)
			// Surface to the operator via an Event on the parent RO; see CRO controller for rationale.
			if r.recorder != nil {
				r.recorder.Eventf(ro, corev1.EventTypeWarning, "OverrideSnapshotHashMismatch",
					"existing snapshot %s/%s has a different hash than the current spec; retrying. If this persists, the snapshot needs operator inspection (etcd restore from backup, manual edit, or hash-function change).", newSnapshot.Namespace, newSnapshot.Name)
			}
			// Treat as expected-behavior so retries don't carry stack traces; see CRO controller
			// for the rationale.
			return controller.NewExpectedBehaviorError(mismatchErr)
		}
		klog.V(2).InfoS("Snapshot already exists with matching content; recovering from prior partial reconcile", "resourceOverride", roKObj, "newOverrideSnapshot", klog.KObj(newSnapshot))
	} else {
		klog.V(2).InfoS("Created new overrideSnapshot", "resourceOverride", roKObj, "newOverrideSnapshot", klog.KObj(newSnapshot))
	}

	// Demote the previous latest snapshot. Tolerate IsNotFound so we still reach the sibling audit.
	if latestSnapshot != nil && latestSnapshot.Labels[placementv1beta1.IsLatestSnapshotLabel] == strconv.FormatBool(true) {
		latestSnapshot.Labels[placementv1beta1.IsLatestSnapshotLabel] = strconv.FormatBool(false)
		if err := r.Client.Update(ctx, latestSnapshot); err != nil {
			if errors.IsNotFound(err) {
				klog.V(2).InfoS("Old overrideSnapshot already gone; skipping demotion", "resourceOverride", roKObj, "overrideSnapshot", klog.KObj(latestSnapshot))
			} else {
				klog.ErrorS(err, "Failed to set the isLatestSnapshot label to false on previous snapshot", "resourceOverride", roKObj, "overrideSnapshot", klog.KObj(latestSnapshot))
				return controller.NewUpdateIgnoreConflictError(err)
			}
		} else {
			klog.V(2).InfoS("Marked previous overrideSnapshot as inactive", "resourceOverride", roKObj, "overrideSnapshot", klog.KObj(latestSnapshot))
		}
	}

	// Audit older siblings for stale latest=true left by prior crashes.
	return r.cleanupStaleLatestSiblings(ctx, snapshotList)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ResourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.recorder = mgr.GetEventRecorderFor("resourceoverride-controller")
	return ctrl.NewControllerManagedBy(mgr).
		Named("resourceoverride-controller").
		For(&placementv1beta1.ResourceOverride{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}
