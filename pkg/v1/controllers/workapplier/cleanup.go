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
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	placementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/errors"
	"github.com/kubefleet-dev/kubefleet/pkg/v1/controllers/utils/ownerreferences"
)

// cleanupWhenPlacementDeleted performs cleanup ops when the placement policy that spawns all the linked
// work objects have been deleted, based on the given SyncStrategy.
func (r *Reconciler) cleanupWhenPlacementDeleted(ctx context.Context,
	linkedWorks []*placementv1alpha1.Work, leftOverWorks []*placementv1alpha1.Work) (*time.Duration, error) {
	primaryWork := linkedWorks[0]
	setDefaultSyncStrategy(primaryWork)

	deletePolicy := metav1.DeletePropagationForeground
	if primaryWork.Spec.SyncStrategy.WhenPlacementDeleted == placementv1alpha1.WhenPlacementDeletedOptionOrphanResources {
		deletePolicy = metav1.DeletePropagationOrphan
	}

	// All the manifests across the linked work objects are owned by the appliedWork object of the primary work.
	appliedWork := &placementv1alpha1.AppliedWork{
		ObjectMeta: metav1.ObjectMeta{Name: primaryWork.Name},
	}
	err := r.spokeClient.Get(ctx, client.ObjectKey{Name: primaryWork.Name}, appliedWork)
	switch {
	case apierrors.IsNotFound(err):
		// The primary appliedWork object is absent; no manifest cleanup is needed.
		klog.V(2).InfoS("The primary appliedWork object is not found; no manifest cleanup is needed", "appliedWork", klog.KObj(appliedWork))
	case err != nil:
		// An unexpected error has occurred.
		return nil, errors.NewAPIServerError(err, "failed to retrieve the appliedWork object", true, "appliedWork", klog.KObj(appliedWork))
	case appliedWork.DeletionTimestamp.IsZero():
		// The appliedWork object exists and has not been marked for deletion yet; delete it.
		if err := r.spokeClient.Delete(ctx, appliedWork, &client.DeleteOptions{PropagationPolicy: &deletePolicy}); err != nil && !apierrors.IsNotFound(err) {
			// An unexpected error has occurred.
			return nil, errors.NewAPIServerError(err, "failed to delete the appliedWork object", false,
				"appliedWork", klog.KObj(appliedWork), "propagationPolicy", deletePolicy)
		}
		klog.V(2).InfoS("The primary appliedWork object has been marked for deletion; wait for the deletion to complete",
			"appliedWork", klog.KObj(appliedWork), "propagationPolicy", deletePolicy)
		return &r.cleanupRequeueAfter, nil
	default:
		// The appliedWork object has been marked for deletion; wait for the deletion to complete.
		if time.Since(appliedWork.DeletionTimestamp.Time) < r.cleanupWaitTime {
			// Keep waiting.
			return &r.cleanupRequeueAfter, nil
		}

		// The appliedWork object has been stuck in the pending deletion state for longer than the cleanup wait time;
		// manually update owner references from all the applied manifests so that they no longer block the deletion
		// of the appliedWork object. Once the appliedWork object is gone, GC will handle the cleanup of the manifests.
		if err := r.manuallyUpdateOwnerReferencesOnManifests(ctx, appliedWork, linkedWorks, leftOverWorks); err != nil {
			return nil, errors.Wraps(err, "failed to manually remove owner references from manifests")
		}

		// Since the time limit has been exceeded, the work applier will not wait until the appliedWork object is
		// actually gone; Kubernetes GC will clean things up under normal circumstances.
	}

	// Remove the cleanup finalizer from all work objects.
	for _, works := range [][]*placementv1alpha1.Work{linkedWorks, leftOverWorks} {
		for idx := range works {
			work := works[idx]
			if !controllerutil.ContainsFinalizer(work, workApplierCleanupFinalizer) {
				continue
			}

			controllerutil.RemoveFinalizer(work, workApplierCleanupFinalizer)
			if err := r.hubClient.Update(ctx, work); err != nil {
				return nil, errors.NewAPIServerError(err, "failed to remove the cleanup finalizer from the work", false,
					"work", klog.KObj(work))
			}
			klog.V(2).InfoS("Removed the cleanup finalizer from the work", "work", klog.KObj(work))
		}
	}

	return nil, nil
}

func (r *Reconciler) manuallyUpdateOwnerReferencesOnManifests(ctx context.Context,
	appliedWork *placementv1alpha1.AppliedWork,
	linkedWorks []*placementv1alpha1.Work,
	leftOverWorks []*placementv1alpha1.Work) error {
	appliedWorkOwnerRef := &metav1.OwnerReference{
		APIVersion: placementv1alpha1.GroupVersion.String(),
		Kind:       "AppliedWork",
		Name:       appliedWork.Name,
		UID:        appliedWork.UID,
	}

	// Loop through all work objects (linked and leftover), identify all applied (or pre-processed) manifests
	// in the status, and add them as owner reference removal candidates.
	manifestsToRemoveOwnerRef := []placementv1alpha1.ManifestIdentifier{}
	for _, works := range [][]*placementv1alpha1.Work{linkedWorks, leftOverWorks} {
		for workIdx := range works {
			work := works[workIdx]
			for manifestIdx := range work.Status.Manifests {
				manifestStatus := &work.Status.Manifests[manifestIdx]
				appliedCond := meta.FindStatusCondition(manifestStatus.Conditions, placementv1alpha1.WorkCondTypeApplied)
				if appliedCond == nil || (appliedCond.Status != metav1.ConditionTrue && appliedCond.Reason != placementv1alpha1.WorkAppliedCondPreparingToProcessReason) {
					continue
				}

				manifestsToRemoveOwnerRef = append(manifestsToRemoveOwnerRef, manifestStatus.Identifier)
			}
		}
	}

	// Update the owner references of all the identified manifests so that they no longer block appliedWork object's
	// deletion.
	errs := make([]error, len(manifestsToRemoveOwnerRef))
	removeOwnerRefFromManifest := func(piece int) {
		identifier := manifestsToRemoveOwnerRef[piece]
		gvr := schema.GroupVersionResource{
			Group:    identifier.APIGroup,
			Version:  identifier.APIVersion,
			Resource: identifier.Resource,
		}
		manifestRef := klog.KRef(identifier.Namespace, identifier.Name)
		manifestObj, err := r.spokeDynamicClient.Resource(gvr).Namespace(identifier.Namespace).Get(ctx, identifier.Name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return
			}
			errs[piece] = errors.NewAPIServerError(err, "failed to retrieve a manifest while unblocking appliedWork deletion", false,
				"gvr", gvr, "manifestObj", manifestRef, "appliedWork", klog.KObj(appliedWork))
			return
		}

		ownerRefs := manifestObj.GetOwnerReferences()
		if len(ownerRefs) == 0 {
			// The applied manifest is no longer owned by KubeFleet; no further action is needed.
			return
		}
		updated := false
		for ownerIdx := range ownerRefs {
			if ownerreferences.AreEqual(&ownerRefs[ownerIdx], appliedWorkOwnerRef) && ptr.Deref(ownerRefs[ownerIdx].BlockOwnerDeletion, false) {
				// Set BlockOwnerDeletion to false to allow the appliedWork object to be deleted without being blocked
				// by this manifest.
				ownerRefs[ownerIdx].BlockOwnerDeletion = ptr.To(false)
				updated = true
			}
		}
		if !updated {
			return
		}

		manifestObj.SetOwnerReferences(ownerRefs)
		if _, err := r.spokeDynamicClient.Resource(gvr).Namespace(identifier.Namespace).Update(ctx, manifestObj, metav1.UpdateOptions{}); err != nil && !apierrors.IsNotFound(err) {
			errs[piece] = errors.NewAPIServerError(err, "failed to unblock a manifest owner reference", false,
				"gvr", gvr, "manifestObj", manifestRef, "appliedWork", klog.KObj(appliedWork))
		}
	}
	r.parallelizer.ParallelizeUntil(ctx, len(manifestsToRemoveOwnerRef), removeOwnerRefFromManifest, "manuallyRemoveOwnerReferencesFromManifests")

	if aggregatedErrs := utilerrors.NewAggregate(errs); aggregatedErrs != nil {
		return errors.Wraps(nil, "failed to remove owner references from manifests",
			"appliedWork", klog.KObj(appliedWork), "errs", aggregatedErrs)
	}
	return nil
}

func (r *Reconciler) cleanupLeftOverWorks(ctx context.Context,
	leftOverWorks []*placementv1alpha1.Work, seenManifIDs sets.Set[string], appliedWorkOwnerRef *metav1.OwnerReference) error {
	for idx := range leftOverWorks {
		leftOverWork := leftOverWorks[idx]
		if !controllerutil.ContainsFinalizer(leftOverWork, workApplierCleanupFinalizer) {
			// The left-over work does not have the cleanup finalizer; no cleanup is needed.
			continue
		}

		// Identify manifests that are no longer seen and need to be removed.
		manifestsToDelete := make([]placementv1alpha1.ManifestIdentifier, 0)
		for manifestIdx := range leftOverWork.Status.Manifests {
			manifestStatus := &leftOverWork.Status.Manifests[manifestIdx]
			if seenManifIDs.Has(formatManifestIdentifierStr(&manifestStatus.Identifier)) {
				// There exists a corner case where a manifest, though present on a left-over work, is still present
				// on one of the linked work objects; in this case, such manifests would not be removed.
				continue
			}

			// Check if the manifest has been applied (or pre-processed) before; if so, attempt to remove it.
			appliedCond := meta.FindStatusCondition(manifestStatus.Conditions, placementv1alpha1.WorkCondTypeApplied)
			if appliedCond != nil && (appliedCond.Status == metav1.ConditionTrue || appliedCond.Reason == placementv1alpha1.WorkAppliedCondPreparingToProcessReason) {
				manifestsToDelete = append(manifestsToDelete, manifestStatus.Identifier)
			}
		}

		errs := make([]error, len(manifestsToDelete))
		removeManifest := func(piece int) {
			manifest := manifestsToDelete[piece]
			if err := r.removeOneLeftOverManifest(ctx, manifest, appliedWorkOwnerRef); err != nil {
				errs[piece] = errors.Wraps(err, "failed to remove a left-over manifest", "manifestId", manifest, "work", klog.KObj(leftOverWork))
			}
		}
		r.parallelizer.ParallelizeUntil(ctx, len(manifestsToDelete), removeManifest, "cleanupLeftOverWorks")
		aggregatedErrs := utilerrors.NewAggregate(errs)
		if aggregatedErrs != nil {
			return errors.Wraps(nil, "failed to remove manifests on a left-over work", "work", klog.KObj(leftOverWork), "errs", aggregatedErrs)
		}

		controllerutil.RemoveFinalizer(leftOverWork, workApplierCleanupFinalizer)
		if err := r.hubClient.Update(ctx, leftOverWork); err != nil {
			return errors.NewAPIServerError(err, "failed to remove the cleanup finalizer from the left-over work", false,
				"work", klog.KObj(leftOverWork))
		}
		klog.V(2).InfoS("Cleaned up a left-over work", "work", klog.KObj(leftOverWork))
	}
	return nil
}
