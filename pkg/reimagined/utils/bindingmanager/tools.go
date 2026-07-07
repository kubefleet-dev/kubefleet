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

package bindingmanager

import (
	"context"
	"reflect"

	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	experimentalv1beta1 "github.com/kubefleet-dev/kubefleet/apis/experimental/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/errors"
)

func ClaimAsBindingManager(
	ctx context.Context,
	hubClient client.Client,
	placement *experimentalv1beta1.PlacementPolicy,
	wantBindingManager *experimentalv1beta1.BindingManager,
) (bool, error) {
	if wantBindingManager == nil {
		return false, errors.Wraps(nil, "cannot make a claim with an empty binding manager")
	}

	// Should apply anyway to ensure that the change is always respected.
	bindingManager := placement.Status.BindingManager
	if bindingManager == nil {
		// There is no active binding manager; claim the role.
		//
		// The request would fail if the list of binding managers has been updated by another agent.
		placement.Status.BindingManager = wantBindingManager
		if err := hubClient.Status().Update(ctx, placement); err != nil {
			wrappedErr := errors.NewAPIServerError(err, "", false)
			return false, wrappedErr
		}
		return true, nil
	}

	if bindingManager.ControllerName != wantBindingManager.ControllerName {
		// The binding manager role has already been claimed by another controller; in this case, give up the
		// current attempt and retry after soem time.
		return false, nil
	}

	// The current controller has already claimed the binding manager role. It might want to add a new
	// object reference to the manager claim, or want to check if the claim is still valid.

	if reflect.DeepEqual(bindingManager, wantBindingManager) {
		// The expected claim is consistent with the observed claim. Verify if the view is still up-to-date
		// by submitting a dry-run patch to the status.
		placementToPatch := placement.DeepCopy()
		placementToPatch.Status.BindingManager = nil

		if err := hubClient.Status().Patch(
			ctx,
			placementToPatch,
			client.MergeFromWithOptions(placement, client.MergeFromWithOptimisticLock{}),
			client.DryRunAll,
		); err != nil {
			wrappedErr := errors.NewAPIServerError(err, "", false)
			klog.ErrorS(wrappedErr, "The current view of binding managers might be stale, or the freshness verification attempt has failed", errors.Args(wrappedErr)...)
			return false, wrappedErr
		}
		return true, nil
	}

	// The controller requests a new claim; update the status.
	//
	// Note that this request would fail if the binding manager view is stale.
	placement.Status.BindingManager = wantBindingManager
	if err := hubClient.Status().Update(ctx, placement); err != nil {
		wrappedErr := errors.NewAPIServerError(err, "", false)
		return false, wrappedErr
	}
	return true, nil
}

func RelinquishBindingManagerRoleAnyway(
	ctx context.Context,
	hubClient client.Client,
	placement *experimentalv1beta1.PlacementPolicy,
	wantBindingManager *experimentalv1beta1.BindingManager,
) error {
	if wantBindingManager == nil {
		return errors.Wraps(nil, "cannot relinquish the binding manager role from an empty claim")
	}

	if placement.Status.BindingManager == nil || !reflect.DeepEqual(placement.Status.BindingManager, wantBindingManager) {
		// The current controller no longer holds the binding manager role based on the current view
		// of the placement object. However, the view might be stale; do a dry-run patch to the status to verify
		// its freshness.
		placementToPatch := placement.DeepCopy()
		placementToPatch.Status.BindingManager = nil
		if err := hubClient.Status().Patch(
			ctx,
			placementToPatch,
			client.MergeFromWithOptions(placement, client.MergeFromWithOptimisticLock{}),
			client.DryRunAll,
		); err != nil {
			wrappedErr := errors.NewAPIServerError(err, "", false)
			klog.ErrorS(wrappedErr, "The current view of binding managers might be stale, or the freshness verification attempt has failed", errors.Args(wrappedErr)...)
			return wrappedErr
		}
		// The view is up-to-date; the current controller no longer holds the binding manager role. No further action is needed.
		return nil
	}

	// The current controller still holds the binding manager role based on the current view of the placement object.
	// Remove the claim.
	//
	// Note that this request would fail if the binding manager view is stale.
	placement.Status.BindingManager = nil
	if err := hubClient.Status().Update(ctx, placement); err != nil {
		wrappedErr := errors.NewAPIServerError(err, "", false)
		return wrappedErr
	}
	return nil
}
