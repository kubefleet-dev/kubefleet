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

package placementpolicy

import (
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	experimentalv1beta1 "github.com/kubefleet-dev/kubefleet/apis/experimental/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/reimagined/placementresourcesnapshot"
	bindingmanagertools "github.com/kubefleet-dev/kubefleet/pkg/reimagined/utils/bindingmanager"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/errors"
)

const (
	controllerName = "PlacementPolicy"

	placementPolicyCleanupFinalizer = "experimental.kubefleet.dev/placement-policy-cleanup"

	placementBindingNameFmt = "%s-%s"
)

var (
	wantBindingManager = &experimentalv1beta1.BindingManager{
		ControllerName: controllerName,
	}
)

type Reconciler struct {
	HubClient client.Client

	PlacementResourceSnapshotManager *placementresourcesnapshot.Manager

	MaxSnapshotCreationWaitTime time.Duration
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	startTime := time.Now()
	klog.V(2).InfoS("Reconciliation starts", "placementPolicy", req.NamespacedName, "controller", controllerName)
	defer func() {
		latency := time.Since(startTime).Milliseconds()
		klog.V(2).InfoS("Reconciliation ends", "placementPolicy", req.NamespacedName, "controller", controllerName, "latency", latency)
	}()

	// Retrieve the PlacementPolicy object.
	placement := &experimentalv1beta1.PlacementPolicy{}
	err := r.HubClient.Get(ctx, req.NamespacedName, placement)
	switch {
	case apierrors.IsNotFound(err):
		// The placement cannot be found; it may have been deleted already. No need for
		// further reconciliation.
		klog.V(2).InfoS("The placement object cannot be found", "placementPolicy", req.NamespacedName, "controller", controllerName)
		return ctrl.Result{}, nil
	case err != nil:
		// An error occurred when trying to retrieve the placement; retry later.
		wrappedErr := errors.NewAPIServerError(err, "", true, "placementPolicy", req.NamespacedName, "controller", controllerName)
		klog.ErrorS(wrappedErr, "Failed to get the placement object", errors.Args(wrappedErr)...)
		return ctrl.Result{}, wrappedErr
	}

	if !placement.DeletionTimestamp.IsZero() {
		// Drop the finalizer if it exists.
		if controllerutil.ContainsFinalizer(placement, placementPolicyCleanupFinalizer) {
			controllerutil.RemoveFinalizer(placement, placementPolicyCleanupFinalizer)

			if err := r.HubClient.Update(ctx, placement); err != nil {
				wrappedErr := errors.NewAPIServerError(err, "", false, "placementPolicy", req.NamespacedName, "controller", controllerName)
				klog.ErrorS(wrappedErr, "Failed to remove finalizer from the deleted placement object", errors.Args(wrappedErr)...)
				return ctrl.Result{}, wrappedErr
			}
			klog.V(2).InfoS("Removed finalizer from the deleted placement object", "placementPolicy", req.NamespacedName, "controller", controllerName)
		}
		return ctrl.Result{}, nil

		// Other resources are cleaned up via their owner references.
	}

	// Add the cleanup finalizer if not already added.
	if !controllerutil.ContainsFinalizer(placement, placementPolicyCleanupFinalizer) {
		controllerutil.AddFinalizer(placement, placementPolicyCleanupFinalizer)
		if err := r.HubClient.Update(ctx, placement); err != nil {
			wrappedErr := errors.NewAPIServerError(err, "", false, "placementPolicy", req.NamespacedName, "controller", controllerName)
			klog.ErrorS(wrappedErr, "Failed to add cleanup finalizer", errors.Args(wrappedErr)...)
			return ctrl.Result{}, wrappedErr
		}
	}

	// Request a new resource snapshot if one hasn't been created yet for this placement.
	latestResourceSnapshot, isLatestResourceSnapshotPresentAndUpToDate, err := r.retrieveLatestResourceSnapshot(ctx, placement)
	if err != nil {
		klog.ErrorS(err, "Failed to check if the latest resource snapshot is up to date", errors.Args(err)...)
		return ctrl.Result{}, err
	}

	// Do some very basic scheduling.

	// Prep the selectors.
	selectors := placement.Spec.ClusterSelectors
	_, selectorSetByHash, err := prepareClusterSelectorsForScheduling(selectors)
	if err != nil {
		klog.ErrorS(err, "Failed to prepare cluster selectors for scheduling",
			append(errors.Args(err), "placementPolicy", req.NamespacedName)...)
		return ctrl.Result{}, err
	}

	// List all existing placement bindings.
	bindingList := &experimentalv1beta1.PlacementBindingList{}
	labelSelectors := client.MatchingLabels{
		experimentalv1beta1.PlacementBindingOwnedByLabelKey: placement.Name,
	}
	if err := r.HubClient.List(ctx, bindingList, labelSelectors, client.InNamespace(placement.Namespace)); err != nil {
		wrappedErr := errors.NewAPIServerError(err, "failed to list placement bindings for the placement policy", false, "placementPolicy", klog.KObj(placement))
		klog.ErrorS(wrappedErr, "Failed to list placement bindings for the placement policy", errors.Args(wrappedErr)...)
		return ctrl.Result{}, wrappedErr
	}
	allBindings := bindingList.Items

	// If there are bindings that are marked for deletion but not deleted yet, wait until
	// they disappear.
	for idx := range allBindings {
		binding := allBindings[idx]
		if !binding.DeletionTimestamp.IsZero() {
			klog.V(2).InfoS("There are bindings that are marked for deletion but not deleted yet; requeue until they are fully removed",
				"placementPolicy", req.NamespacedName, "binding", klog.KObj(&binding))
			return ctrl.Result{RequeueAfter: time.Second * 10}, nil
		}
	}

	// Cross reference the selectors and the bindings; find the selectors that no longer have bindings, and
	// the bindings that no longer match with any of the selectors.
	danglingBindings := crossReferenceClusterSelectorsAndBindings(selectorSetByHash, allBindings)

	isSchedulingNeeded := needsScheduling(selectorSetByHash, danglingBindings)
	shouldRequeue := false
	if isSchedulingNeeded {
		shouldRequeue, err = r.scheduleOnce(
			ctx,
			placement,
			latestResourceSnapshot,
			selectors, selectorSetByHash,
			allBindings,
			danglingBindings,
		)
		if err != nil {
			wrappedErr := errors.Wraps(err, "", "placementPolicy", klog.KObj(placement))
			klog.ErrorS(wrappedErr, "Failed to perform scheduling for the placement policy", errors.Args(wrappedErr)...)
			return ctrl.Result{}, wrappedErr
		}
	}

	// Relinquish the binding manager role anyway so that other controllers can manipulate bindings if needed.
	if err := bindingmanagertools.RelinquishBindingManagerRoleAnyway(ctx, r.HubClient, placement, wantBindingManager); err != nil {
		wrappedErr := errors.Wraps(err, "", "placementPolicy", klog.KObj(placement))
		klog.ErrorS(wrappedErr, "Failed to relinquish the binding manager role for the placement policy", errors.Args(wrappedErr)...)
		return ctrl.Result{}, wrappedErr
	}

	// Update the placement status.
	//
	// Note that status is updated even if scheduling is not needed and cannot be performed at the moment.
	if err := r.refreshStatus(
		ctx,
		placement,
		latestResourceSnapshot, isLatestResourceSnapshotPresentAndUpToDate,
		isSchedulingNeeded, selectors,
		allBindings, danglingBindings,
	); err != nil {
		wrappedErr := errors.Wraps(err, "", "placementPolicy", klog.KObj(placement))
		klog.ErrorS(wrappedErr, "Failed to update placement policy status", errors.Args(wrappedErr)...)
		return ctrl.Result{}, wrappedErr
	}
	if shouldRequeue {
		return ctrl.Result{RequeueAfter: time.Second * 15}, nil
	}
	return ctrl.Result{}, nil
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&experimentalv1beta1.PlacementPolicy{}).
		Owns(&experimentalv1beta1.PlacementResourceSnapshot{}).
		Owns(&experimentalv1beta1.PlacementBinding{}).
		Owns(&experimentalv1beta1.ClusterRequest{}).
		// Note: also watch for member cluster creation. For the demo, ignore this as the
		// controller auto-requeues when there is an active cluster request.
		Complete(r)
}
