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

package placementmigrationrequest

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	experimentalv1beta1 "github.com/kubefleet-dev/kubefleet/apis/experimental/v1beta1"
	bindingmanagertools "github.com/kubefleet-dev/kubefleet/pkg/reimagined/utils/bindingmanager"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/errors"
)

func (r *Reconciler) commit(ctx context.Context, req *experimentalv1beta1.PlacementMigrationRequest) (ctrl.Result, error) {
	// If the migration request has no finalizer set, there is no need to perform the commit process.
	if !controllerutil.ContainsFinalizer(req, workloadMigrationRequestCleanupFinalizer) {
		klog.V(2).InfoS("No finalizer is set on the workload migration request; skipping the commit process", "workloadMigrationRequest", klog.KObj(req))
		return ctrl.Result{}, nil
	}

	// If the migration request hasn't been initialized yet, there is no need to perform the commit process.
	//
	// However, the controller must relinquish its role as the binding manager for all affected workload
	// placements, if applicable.
	initCond := meta.FindStatusCondition(req.Status.Conditions, experimentalv1beta1.PlacementMigrationRequestCondTypeInitialized)
	if initCond == nil || initCond.Status != metav1.ConditionTrue {
		klog.V(2).InfoS("The workload migration request has not been initialized yet; skipping the commit process but relinquishing the binding manager role for all affected workload placements if applicable", "workloadMigrationRequest", klog.KObj(req))
		if err := r.relinquishBindingManagerRoleForAllAffectedPlacements(ctx, req); err != nil {
			klog.ErrorS(err, "Failed to relinquish the binding manager role for all affected workload placements when committing a workload migration request", errors.Args(err)...)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Commit each migration attempt as it is.
	migrationAttempts := req.Status.PlacementsToMigrate
	for idx := range migrationAttempts {
		attempt := &migrationAttempts[idx]

		rolledBackCond := meta.FindStatusCondition(attempt.Conditions, experimentalv1beta1.PlacementMigrationAttemptCondTypeRolledBack)
		completedCond := meta.FindStatusCondition(attempt.Conditions, experimentalv1beta1.PlacementMigrationAttemptCondTypeCompleted)
		isRolledBack := rolledBackCond != nil && rolledBackCond.Status == metav1.ConditionTrue
		isCompleted := completedCond != nil && completedCond.Status == metav1.ConditionTrue

		switch {
		case isRolledBack:
			// The migration attempt has been rolled back. Commit it by deleting the to binding.
			// No action is needed on the from binding as it has been surged back in the rollback
			// process.
			if err := r.dropToBindingFor(ctx, attempt); err != nil {
				klog.ErrorS(err, "Failed to drop the to binding for a rolled back migration attempt when committing a workload migration request", append(errors.Args(err), "workloadMigrationRequest", klog.KObj(req), "migrationAttempt", attempt)...)
				return ctrl.Result{}, err
			}
			continue
		case !req.Spec.Rollback && isCompleted:
			// The migration attempt has been completed and the migration request has not been rolled
			// back. Commit it by dropping the created in place for label on the to binding, and
			// deleting the from binding (which has been suspended).
			if err := r.promoteToBindingFor(ctx, attempt); err != nil {
				klog.ErrorS(err, "Failed to promote the to binding for a completed migration attempt when committing a workload migration request", append(errors.Args(err), "workloadMigrationRequest", klog.KObj(req), "migrationAttempt", attempt)...)
				return ctrl.Result{}, err
			}

			if err := r.dropFromBindingFor(ctx, attempt); err != nil {
				klog.ErrorS(err, "Failed to drop the from binding for a completed migration attempt when committing a workload migration request", append(errors.Args(err), "workloadMigrationRequest", klog.KObj(req), "migrationAttempt", attempt)...)
				return ctrl.Result{}, err
			}
		default:
			// The migration attempt (or the rollback of the attempt) is still in progress or has not started yet.
			// Commit the state as it is, specifically:
			// a) if the from binding is suspended, delete it;
			// b) if the to binding is present hasn't been suspended, keep it as it is and drop the created in place for label;
			//    otherwise, delete it.
			if err := r.dropFromBindingIfSuspendedFor(ctx, attempt); err != nil {
				klog.ErrorS(err, "Failed to drop the from binding if it is suspended for a migration attempt when committing a workload migration request", append(errors.Args(err), "workloadMigrationRequest", klog.KObj(req), "migrationAttempt", attempt)...)
				return ctrl.Result{}, err
			}
			if err := r.promoteToBindingIfNotSuspenedOrDropToBindingIfSuspendedFor(ctx, attempt); err != nil {
				klog.ErrorS(err, "Failed to promote the to binding if it is not suspended or drop the to binding if it is suspended for a migration attempt when committing a workload migration request", append(errors.Args(err), "workloadMigrationRequest", klog.KObj(req), "migrationAttempt", attempt)...)
				return ctrl.Result{}, err
			}
		}
	}

	// Relinquish the binding manager role for all affected workload placements.
	if err := r.relinquishBindingManagerRoleForAllAffectedPlacements(ctx, req); err != nil {
		klog.ErrorS(err, "Failed to relinquish the binding manager role for all affected workload placements when committing a workload migration request", errors.Args(err)...)
		return ctrl.Result{}, err
	}

	// Drop the cleanup finalizer on the workload migration request so that it can be deleted.
	controllerutil.RemoveFinalizer(req, workloadMigrationRequestCleanupFinalizer)
	if err := r.HubClient.Update(ctx, req); err != nil {
		wrappedErr := errors.NewAPIServerError(err, "", true, "workloadMigrationRequest", klog.KObj(req))
		klog.ErrorS(wrappedErr, "Failed to remove the cleanup finalizer from the workload migration request when committing the workload migration request", errors.Args(wrappedErr)...)
		return ctrl.Result{}, wrappedErr
	}
	return ctrl.Result{}, nil
}

func (r *Reconciler) relinquishBindingManagerRoleForAllAffectedPlacements(
	ctx context.Context,
	req *experimentalv1beta1.PlacementMigrationRequest,
) error {
	wantBindingManager := &experimentalv1beta1.BindingManager{
		ControllerName: controllerName,
		ObjectRefs: []experimentalv1beta1.SameNamespacedObjectReference{
			{
				Name:       req.Name,
				APIGroup:   experimentalv1beta1.GroupVersion.Group,
				APIVersion: experimentalv1beta1.GroupVersion.Version,
				Kind:       "PlacementMigrationRequest",
				Resource:   "placementmigrationrequests",
			},
		},
	}

	migrationAttempts := req.Status.PlacementsToMigrate
	for idx := range migrationAttempts {
		attempt := &migrationAttempts[idx]

		// Retrieve the corresponding workload placement.
		placement := &experimentalv1beta1.PlacementPolicy{}
		placementNamespacedName := client.ObjectKey{
			Name:      attempt.PlacementPolicyRef.Name,
			Namespace: attempt.PlacementPolicyRef.Namespace,
		}
		if err := r.HubClient.Get(ctx, placementNamespacedName, placement); err != nil {
			if apierrors.IsNotFound(err) {
				// If the workload placement doesn't exist, skip to the next one.
				klog.V(2).InfoS("The workload placement corresponding to a from binding referenced by a migration attempt does not exist; skipping relinquishing the binding manager role for the corresponding workload placement",
					"workloadMigrationRequest", klog.KObj(req), "placementNamespacedName", placementNamespacedName)
				continue
			}
			wrappedErr := errors.NewAPIServerError(err,
				"Failed to get the workload placement corresponding to a from binding referenced by a migration attempt as part of the commit process for a workload migration request", true,
				"workloadMigrationRequest", klog.KObj(req), "placementNamespacedName", placementNamespacedName)
			return wrappedErr
		}

		// Relinquish the binding manager role for the workload placement.
		if err := bindingmanagertools.RelinquishBindingManagerRoleAnyway(ctx, r.HubClient, placement, wantBindingManager); err != nil {
			wrappedErr := errors.Wraps(err, "failed to relinquish the binding manager role for a workload placement as part of the commit process for a workload migration request", "workloadPlacement", klog.KObj(placement))
			return wrappedErr
		}
	}
	return nil
}

func (r *Reconciler) dropToBindingFor(
	ctx context.Context,
	attempt *experimentalv1beta1.PerPlacementMigrationStatus,
) error {
	toBindingList := &experimentalv1beta1.PlacementBindingList{}
	labelSelector := client.MatchingLabels{
		experimentalv1beta1.PlacementBindingMigratedFromKey: attempt.PlacementBindingRef.Name,
	}
	if err := r.HubClient.List(ctx, toBindingList, labelSelector, client.InNamespace(attempt.PlacementBindingRef.Namespace)); err != nil {
		wrappedErr := errors.NewAPIServerError(err, "failed to list to bindings for a migration attempt when dropping the to binding for the migration attempt", true,
			"workloadMigrationAttempt", attempt)
		return wrappedErr
	}
	toBindings := toBindingList.Items

	// There should only be one such to binding. If there are more than one, log a warning and delete all of them.
	if len(toBindings) == 0 {
		klog.V(2).InfoS("There is no to binding created in place for the migration attempt; nothing to drop", "workloadMigrationAttempt", attempt)
		return nil
	}

	if len(toBindings) > 1 {
		klog.V(2).InfoS("There are more than one to bindings created in place for the migration attempt; there should be at most one. Deleting all of them", "workloadMigrationAttempt", attempt, "toBindings", klog.KObjSlice(toBindings))
	}
	for idx := range toBindings {
		toBinding := &toBindings[idx]
		if err := r.HubClient.Delete(ctx, toBinding); err != nil && !apierrors.IsNotFound(err) {
			wrappedErr := errors.NewAPIServerError(err, "failed to delete a to binding when dropping the to binding for a migration attempt", true,
				"workloadMigrationAttempt", attempt, "toBinding", klog.KObj(toBinding))
			return wrappedErr
		}
		klog.V(2).InfoS("Deleted a to binding when committing a migration attempt", "workloadMigrationAttempt", attempt, "toBinding", klog.KObj(toBinding))
	}
	return nil
}

func (r *Reconciler) promoteToBindingFor(
	ctx context.Context,
	attempt *experimentalv1beta1.PerPlacementMigrationStatus,
) error {
	toBindingList := &experimentalv1beta1.PlacementBindingList{}
	labelSelector := client.MatchingLabels{
		experimentalv1beta1.PlacementBindingMigratedFromKey: attempt.PlacementBindingRef.Name,
	}
	if err := r.HubClient.List(ctx, toBindingList, labelSelector, client.InNamespace(attempt.PlacementBindingRef.Namespace)); err != nil {
		wrappedErr := errors.NewAPIServerError(err, "failed to list to bindings for a migration attempt when promoting the to binding for the migration attempt", true,
			"workloadMigrationAttempt", attempt)
		return wrappedErr
	}
	toBindings := toBindingList.Items

	// There should only be one such to binding. If there are more than one, log a warning and promote the first one.
	if len(toBindings) == 0 {
		klog.V(2).InfoS("There is no to binding created in place for the migration attempt; nothing to promote", "workloadMigrationAttempt", attempt)
		return nil
	}

	if len(toBindings) > 1 {
		klog.V(2).InfoS("There are more than one to bindings created in place for the migration attempt; there should be at most one. Promoting the first one", "workloadMigrationAttempt", attempt, "toBindings", klog.KObjSlice(toBindings))
	}
	toBinding := &toBindings[0]

	// Drop the created in place for label on the to binding.
	labels := toBinding.GetLabels()
	delete(labels, experimentalv1beta1.PlacementBindingMigratedFromKey)
	toBinding.SetLabels(labels)
	if err := r.HubClient.Update(ctx, toBinding); err != nil {
		wrappedErr := errors.NewAPIServerError(err,
			"failed to update the to binding when promoting the to binding for a migration attempt", true,
			"toBinding", klog.KObj(toBinding))
		return wrappedErr
	}
	klog.V(2).InfoS("Promoted the to binding for a migration attempt by dropping the created in place for label on the to binding", "workloadMigrationAttempt", attempt, "toBinding", klog.KObj(toBinding))
	return nil
}

func (r *Reconciler) dropFromBindingFor(
	ctx context.Context,
	attempt *experimentalv1beta1.PerPlacementMigrationStatus,
) error {
	fromBinding := &experimentalv1beta1.PlacementBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      attempt.PlacementBindingRef.Name,
			Namespace: attempt.PlacementBindingRef.Namespace,
		},
	}

	if err := r.HubClient.Delete(ctx, fromBinding); err != nil && !apierrors.IsNotFound(err) {
		wrappedErr := errors.NewAPIServerError(err,
			"failed to delete the from binding when dropping the from binding for a migration attempt", true,
			"fromBinding", klog.KObj(fromBinding))
		return wrappedErr
	}
	klog.V(2).InfoS("Dropped the from binding for a migration attempt", "workloadMigrationAttempt", attempt, "fromBinding", klog.KObj(fromBinding))
	return nil
}

func (r *Reconciler) dropFromBindingIfSuspendedFor(
	ctx context.Context,
	attempt *experimentalv1beta1.PerPlacementMigrationStatus,
) error {
	fromBinding := &experimentalv1beta1.PlacementBinding{}
	fromBindingNamespacedName := client.ObjectKey{
		Name:      attempt.PlacementBindingRef.Name,
		Namespace: attempt.PlacementBindingRef.Namespace,
	}
	if err := r.HubClient.Get(ctx, fromBindingNamespacedName, fromBinding); err != nil {
		if apierrors.IsNotFound(err) {
			// If the from binding doesn't exist, there is nothing to drop.
			return nil
		}
		wrappedErr := errors.NewAPIServerError(err,
			"failed to get the from binding when committing a migration attempt", true,
			"fromBindingNamespacedName", fromBindingNamespacedName)
		return wrappedErr
	}

	if fromBinding.Spec.Suspended {
		if err := r.HubClient.Delete(ctx, fromBinding); err != nil && !apierrors.IsNotFound(err) {
			wrappedErr := errors.NewAPIServerError(err,
				"failed to delete the from binding when committing a migration attempt", true,
				"fromBinding", klog.KObj(fromBinding))
			return wrappedErr
		}
		klog.V(2).InfoS("Dropped the from binding for a migration attempt as it is suspended", "workloadMigrationAttempt", attempt, "fromBinding", klog.KObj(fromBinding))
	}
	return nil
}

func (r *Reconciler) promoteToBindingIfNotSuspenedOrDropToBindingIfSuspendedFor(
	ctx context.Context,
	attempt *experimentalv1beta1.PerPlacementMigrationStatus,
) error {
	toBindingList := &experimentalv1beta1.PlacementBindingList{}
	labelSelector := client.MatchingLabels{
		experimentalv1beta1.PlacementBindingMigratedFromKey: attempt.PlacementBindingRef.Name,
	}
	if err := r.HubClient.List(ctx, toBindingList, labelSelector, client.InNamespace(attempt.PlacementBindingRef.Namespace)); err != nil {
		wrappedErr := errors.NewAPIServerError(err, "failed to list to bindings when committing a migration attempt", true)
		return wrappedErr
	}
	toBindings := toBindingList.Items

	// There should only be one such to binding. If there are more than one, log a warning and process the first one.
	if len(toBindings) == 0 {
		klog.V(2).InfoS("There is no to binding created in place for the migration attempt; nothing to promote or drop", "workloadMigrationAttempt", attempt)
		return nil
	}

	if len(toBindings) > 1 {
		klog.V(2).InfoS("There are more than one to bindings created in place for the migration attempt; there should be at most one. Processing the first one", "workloadMigrationAttempt", attempt, "toBindings", klog.KObjSlice(toBindings))
	}
	toBinding := &toBindings[0]

	if toBinding.Spec.Suspended {
		// The to binding is suspended. Drop it.
		if err := r.HubClient.Delete(ctx, toBinding); err != nil && !apierrors.IsNotFound(err) {
			wrappedErr := errors.NewAPIServerError(err,
				"failed to delete the to binding when dropping the to binding for a migration attempt", true,
				"toBinding", klog.KObj(toBinding))
			return wrappedErr
		}
	} else {
		// The to binding is not suspended. Promote it by dropping the created in place for label on the to binding.
		labels := toBinding.GetLabels()
		delete(labels, experimentalv1beta1.PlacementBindingMigratedFromKey)
		toBinding.SetLabels(labels)
		if err := r.HubClient.Update(ctx, toBinding); err != nil {
			wrappedErr := errors.NewAPIServerError(err,
				"failed to update the to binding when promoting the to binding for a migration attempt", true,
				"toBinding", klog.KObj(toBinding))
			return wrappedErr
		}
	}
	return nil
}
