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

package workapplier

import (
	"context"
	"fmt"
	"strconv"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	placementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/controller"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/errors"
	"github.com/kubefleet-dev/kubefleet/pkg/v1/controllers/utils/fieldindexers"
)

// retrieveLinkedWorks returns all the work objects that share the same owner placement binding as the given
// primary work object, including the primary work object itself.
//
// The set is returned only when it is complete and consistent, that is, its size matches the linked work count
// recorded on the primary work object and every work object is linked to the same primary placement resource
// snapshot; otherwise a transient error is returned, as the set is expected to converge on its own.
//
// The first work object in the array is always the primary work object.
func (r *Reconciler) retrieveLinkedWorks(ctx context.Context, primaryWork *placementv1alpha1.Work) ([]*placementv1alpha1.Work, error) {
	ownedBy := primaryWork.GetLabels()[placementv1alpha1.WorkOwnedByPlacementBindingLabelKey]
	if ownedBy == "" {
		return nil, errors.NewUnexpectedError(nil, "the primary work is missing the owner placement binding label")
	}
	// An empty owner namespace signals a cluster-scoped placement binding.
	ownerNS := primaryWork.GetLabels()[placementv1alpha1.WorkOwnerNamespaceLabelKey]

	wantLinkedWorkCountVal := primaryWork.GetAnnotations()[placementv1alpha1.LinkedWorkCountAnnotationKey]
	wantLinkedWorkCount, err := strconv.Atoi(wantLinkedWorkCountVal)
	if err != nil {
		return nil, errors.NewUnexpectedError(err, "failed to parse the linked work count annotation on the primary work",
			"linkedWorkCount", wantLinkedWorkCountVal)
	}

	wantPrimarySnapshotName := primaryWork.GetAnnotations()[placementv1alpha1.WorkLinkedToPrimaryPlacementResourceSnapshotAnnotationKey]
	if wantPrimarySnapshotName == "" {
		return nil, errors.NewUnexpectedError(nil, "the primary work is missing the primary placement resource snapshot annotation")
	}

	ownedByFieldVal := fmt.Sprintf(fieldindexers.WorkOwnedByPlacementBindingCustomFieldValFormat, ownerNS, ownedBy)
	workList := &placementv1alpha1.WorkList{}
	if err := r.hubClient.List(ctx, workList,
		client.InNamespace(primaryWork.Namespace),
		client.MatchingFields{fieldindexers.WorkOwnedByPlacementBindingCustomFieldName: ownedByFieldVal},
	); err != nil {
		return nil, errors.NewAPIServerError(err, "failed to list the work objects owned by the placement binding", true,
			"ownerPlacementBinding", ownedByFieldVal)
	}

	if len(workList.Items) != wantLinkedWorkCount {
		return nil, errors.NewTransientError(nil, "the number of linked work objects found is inconsistent with the count recorded on the primary work",
			"ownerPlacementBinding", ownedByFieldVal,
			"observedLinkedWorkCount", len(workList.Items), "wantLinkedWorkCount", wantLinkedWorkCount)
	}

	works := make([]*placementv1alpha1.Work, 0, len(workList.Items))
	works = append(works, primaryWork)
	for idx := range workList.Items {
		linkedWork := &workList.Items[idx]
		primarySnapshotName := linkedWork.GetAnnotations()[placementv1alpha1.WorkLinkedToPrimaryPlacementResourceSnapshotAnnotationKey]
		if primarySnapshotName != wantPrimarySnapshotName {
			return nil, errors.NewTransientError(nil, "a linked work object is not linked to the same primary placement resource snapshot as the primary work",
				"linkedWork", klog.KObj(linkedWork),
				"observedPlacementResourceSnapshot", primarySnapshotName, "expectedPrimaryPlacementResourceSnapshot", wantPrimarySnapshotName)
		}

		if linkedWork.Name != primaryWork.Name {
			works = append(works, linkedWork)
		}
	}

	return works, nil
}

// addCleanupFinalizerTo adds the cleanup finalizer to the given work objects, so that the applied resources
// on the member cluster side can be cleaned up before the work objects are removed.
func (r *Reconciler) addCleanupFinalizerTo(ctx context.Context, linkedWorks []*placementv1alpha1.Work) error {
	for idx := range linkedWorks {
		linkedWork := linkedWorks[idx]
		if controllerutil.ContainsFinalizer(linkedWork, workAppliedCleanupFinalizer) {
			continue
		}

		controllerutil.AddFinalizer(linkedWork, workAppliedCleanupFinalizer)
		if err := r.hubClient.Update(ctx, linkedWork); err != nil {
			// Reset the finalizer list to its previous state.
			controllerutil.RemoveFinalizer(linkedWork, workAppliedCleanupFinalizer)
			return errors.NewAPIServerError(err, "failed to add the cleanup finalizer to the work", false,
				"work", klog.KObj(linkedWork))
		}
		klog.V(2).InfoS("Added the cleanup finalizer to the work", "work", klog.KObj(linkedWork))
	}
	return nil
}

func (r *Reconciler) ensureAppliedWorks(ctx context.Context, linkedWorks []*placementv1alpha1.Work) ([]*placementv1alpha1.AppliedWork, error) {
	appliedWorks := make([]*placementv1alpha1.AppliedWork, 0, len(linkedWorks))

	for idx := range linkedWorks {
		work := linkedWorks[idx]

		// Check if an appliedWork object already exists for the work object.
		//
		// Since we only create an appliedWork object after adding the finalizer to the Work object,
		// usually it is safe for us to assume that if the finalizer is absent, the appliedWork object should
		// not exist. This is not the case with the work applier though, as the controller features a
		// Leave method that will strip all Work objects off their finalizers, which is called when the
		// member cluster leaves the fleet. If the member cluster chooses to re-join the fleet, the controller
		// will see a work object with no finalizer but with an appliedWork object. Because of this, here we always
		// check for the existence of the appliedWork object, with or without the finalizer.
		appliedWork := &placementv1alpha1.AppliedWork{}
		err := r.spokeClient.Get(ctx, types.NamespacedName{Name: work.Name}, appliedWork)
		switch {
		case err == nil:
			// The AppliedWork already exists; no further action is needed.
			klog.V(2).InfoS("Found an appliedWork object for the work object", "work", klog.KObj(work), "appliedWork", klog.KObj(appliedWork))
			appliedWorks = append(appliedWorks, appliedWork)
		case !apierrors.IsNotFound(err):
			klog.ErrorS(err, "Failed to retrieve the appliedWork object", "appliedWork", klog.KObj(work))
			return nil, errors.NewAPIServerError(err, "failed to retrieve the appliedWork object", true,
				"appliedWork", klog.KObj(work))
		}

		// The appliedWork object does not exist; create one.
		appliedWork = &placementv1alpha1.AppliedWork{
			ObjectMeta: metav1.ObjectMeta{
				Name: work.Name,
			},
			Spec: placementv1alpha1.AppliedWorkSpec{
				WorkName:      work.Name,
				WorkNamespace: work.Namespace,
			},
		}
		if err := r.spokeClient.Create(ctx, appliedWork); err != nil {
			// Note: the controller must retry on AppliedWork AlreadyExists errors; otherwise the
			// controller will run the reconciliation loop with an AppliedWork that has no UID,
			// which might lead to takeover failures in later steps.
			klog.ErrorS(err, "Failed to create an AppliedWork object for the Work object", "appliedWork", klog.KObj(appliedWork), "work", klog.KObj(work))
			return nil, controller.NewAPIServerError(false, err)
		}
		klog.V(2).InfoS("Created an AppliedWork for the Work object", "work", klog.KObj(work), "appliedWork", klog.KObj(appliedWork))
		appliedWorks = append(appliedWorks, appliedWork)
	}

	return appliedWorks, nil
}
