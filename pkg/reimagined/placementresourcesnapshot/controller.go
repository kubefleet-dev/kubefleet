/*
Copyright 2026 The KubeFleet Authors.

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

package placementresourcesnapshot

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	experimentalv1beta1 "github.com/kubefleet-dev/kubefleet/apis/experimental/v1beta1"
	errors "github.com/kubefleet-dev/kubefleet/pkg/utils/errors"
)

type Reconciler struct {
	client          client.Client
	snapshotManager *Manager

	maxSnapshotCreationWaitTime time.Duration
}

func NewPlacementResourceSnapshotReqReconciler(
	client client.Client,
	snapshotManager *Manager,
	maxSnapshotCreationWaitTimeSeconds int,
) *Reconciler {
	return &Reconciler{
		client:                      client,
		snapshotManager:             snapshotManager,
		maxSnapshotCreationWaitTime: time.Duration(maxSnapshotCreationWaitTimeSeconds) * time.Second,
	}
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	startTime := time.Now()
	klog.V(2).InfoS("Reconciliation started",
		"placementResourceSnapshotRequest", req.NamespacedName)
	defer func() {
		latency := time.Since(startTime).Milliseconds()
		klog.V(2).InfoS("Reconciliation ended",
			"placementResourceSnapshotRequest", req.NamespacedName,
			"latencyMilliseconds", latency)
	}()

	placementResSnapshotReq := &experimentalv1beta1.PlacementResourceSnapshotRequest{}
	err := r.client.Get(ctx, req.NamespacedName, placementResSnapshotReq)
	switch {
	case apierrors.IsNotFound(err):
		// The request was not found. It might have been deleted after the reconcile request was queued.
		// In this case, the controller simply ignores the error and ends the reconciliation.
		klog.V(2).InfoS("The placement resource snapshot request object is not found; no further processing is needed",
			"placementResourceSnapshotRequest", req.NamespacedName)
		return ctrl.Result{}, nil
	case err != nil:
		// An error occurred while trying to retrieve the request. We should requeue the request to try again.
		wrappedErr := errors.NewAPIServerError(err, "", true, "placementResourceSnapshotRequest", req.NamespacedName)
		klog.ErrorS(wrappedErr, "Failed to retrieve the placement resource snapshot request object", errors.Args(wrappedErr)...)
		return ctrl.Result{}, wrappedErr
	default:
		completedCond := meta.FindStatusCondition(placementResSnapshotReq.Status.Conditions, experimentalv1beta1.PlacementResourceSnapshotRequestCondTypeCompleted)
		if completedCond != nil && completedCond.Status == metav1.ConditionTrue {
			// The request has already been completed. No further processing is needed.
			klog.V(2).InfoS("The placement resource snapshot request has already been completed; no further processing is needed",
				"placementResourceSnapshotRequest", klog.KObj(placementResSnapshotReq))
			return ctrl.Result{}, nil
		}
	}

	placementPolicy := &experimentalv1beta1.PlacementPolicy{}
	placementPolicyKey := client.ObjectKey{
		Name:      placementResSnapshotReq.Spec.PlacementPolicyRef.Name,
		Namespace: placementResSnapshotReq.Namespace,
	}
	err = r.client.Get(ctx, placementPolicyKey, placementPolicy)
	switch {
	case apierrors.IsNotFound(err):
		// The placement policy is not found. Abandon the request and update its status condition to indicate the failure.
		klog.V(2).InfoS("No placement policy is found; abandon the new placement resource snapshot request",
			"placementResourceSnapshotRequest", klog.KObj(placementResSnapshotReq),
			"placementPolicy", placementPolicyKey)

		meta.SetStatusCondition(&placementResSnapshotReq.Status.Conditions, metav1.Condition{
			Type:               experimentalv1beta1.PlacementResourceSnapshotRequestCondTypeCompleted,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: placementResSnapshotReq.GetGeneration(),
			Reason:             experimentalv1beta1.PlacementResourceSnapshotRequestCompletedReasonPlacementPolicyNotFound,
			Message:            fmt.Sprintf("No placement policy is found with name %q in namespace %q", placementPolicyKey.Name, placementPolicyKey.Namespace),
		})
		if err := r.client.Status().Update(ctx, placementResSnapshotReq); err != nil {
			wrappedErr := errors.NewAPIServerError(err,
				"failed to update the status of the placement resource snapshot request",
				false,
				"placementResourceSnapshotRequest", klog.KObj(placementResSnapshotReq),
				"placementPolicy", placementPolicyKey)
			klog.ErrorS(wrappedErr, "Failed to update status for placement resource snapshot request after determining the referenced placement policy does not exist", errors.Args(wrappedErr)...)
			return ctrl.Result{}, wrappedErr
		}
		return ctrl.Result{}, nil
	case err != nil:
		wrappedErr := errors.NewAPIServerError(err,
			"failed to retrieve the placement policy object referenced by the placement resource snapshot request", true,
			"placementResourceSnapshotRequest", klog.KObj(placementResSnapshotReq), "placementPolicy", placementPolicyKey)
		klog.ErrorS(wrappedErr, "Failed to retrieve the placement policy object referenced by the placement resource snapshot request",
			errors.Args(wrappedErr)...)
		return ctrl.Result{}, wrappedErr
	}

	// Ask the placement resource snapshot manager to add a new snapshot for the placement policy.
	newSnapshot, err := r.snapshotManager.RequestAndWaitForNewSnapshot(ctx, placementPolicy, r.maxSnapshotCreationWaitTime)
	if err != nil {
		wrappedErr := errors.Wraps(err, "failed to request the placement resource snapshot manager to add a new snapshot for the placement policy",
			"placementPolicy", klog.KObj(placementPolicy),
			"placementResourceSnapshotRequest", klog.KObj(placementResSnapshotReq))
		klog.ErrorS(wrappedErr, "Failed to request the placement resource snapshot manager to add a new snapshot for the placement policy", errors.Args(wrappedErr)...)

		// Abandon the request right away. To retry, create a new request object.
		meta.SetStatusCondition(&placementResSnapshotReq.Status.Conditions, metav1.Condition{
			Type:               experimentalv1beta1.PlacementResourceSnapshotRequestCondTypeCompleted,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: placementResSnapshotReq.GetGeneration(),
			Reason:             experimentalv1beta1.PlacementResourceSnapshotRequestCompletedReasonErred,
			Message:            fmt.Sprintf("Failed to request the placement resource snapshot manager to add a new snapshot for the placement policy: %v", wrappedErr),
		})
		if err := r.client.Status().Update(ctx, placementResSnapshotReq); err != nil {
			wrappedErr := errors.NewAPIServerError(err,
				"failed to update the status of the placement resource snapshot request",
				false,
				"placementResourceSnapshotRequest", klog.KObj(placementResSnapshotReq),
				"placementPolicy", klog.KObj(placementPolicy))
			klog.ErrorS(wrappedErr, "Failed to update status for placement resource snapshot request after failing to request the placement resource snapshot manager to add a new snapshot for the placement policy", errors.Args(wrappedErr)...)
			return ctrl.Result{}, wrappedErr
		}
		return ctrl.Result{}, nil
	}

	// Successfully added the new snapshot for the placement policy. Update the request status to indicate the completion.
	meta.SetStatusCondition(&placementResSnapshotReq.Status.Conditions, metav1.Condition{
		Type:               experimentalv1beta1.PlacementResourceSnapshotRequestCondTypeCompleted,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: placementResSnapshotReq.GetGeneration(),
		Reason:             experimentalv1beta1.PlacementResourceSnapshotRequestCompletedReasonSuccess,
		Message:            "Successfully added new snapshot for the placement policy",
	})
	placementResSnapshotReq.Status.PlacementResourceSnapshotName = newSnapshot.Name

	if err := r.client.Status().Update(ctx, placementResSnapshotReq); err != nil {
		wrappedErr := errors.NewAPIServerError(err,
			"failed to update status of PlacementResourceSnapshotRequest object", false,
			"placementResourceSnapshotRequest", klog.KObj(placementResSnapshotReq))
		klog.ErrorS(wrappedErr, "Failed to update status for placement resource snapshot request", errors.Args(wrappedErr)...)
		return ctrl.Result{}, wrappedErr
	}
	return ctrl.Result{}, nil
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&experimentalv1beta1.PlacementResourceSnapshotRequest{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}
