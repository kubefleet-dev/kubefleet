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
	"sync/atomic"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	placementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/errors"
	parallelizerutil "github.com/kubefleet-dev/kubefleet/pkg/utils/parallelizer"
)

const (
	controllerName = "work-applier"

	workAppliedCleanupFinalizer = "placement.kubefleet.dev/work-cleanup"
)

type applyResultType string

const (
	// The result types for apply op failures.
	ApplyResTypeDecodingErred                  applyResultType = "DecodingErred"
	ApplyResTypeFoundGenerateName              applyResultType = "FoundGenerateName"
	ApplyResTypeDuplicated                     applyResultType = "Duplicated"
	ApplyResTypeFailedToFindObjInMemberCluster applyResultType = "FailedToFindObjInMemberCluster"
	ApplyResTypeFailedToTakeOver               applyResultType = "FailedToTakeOver"
	ApplyResTypeNotTakenOver                   applyResultType = "NotTakenOver"
	ApplyResTypeFailedToRunDriftDetection      applyResultType = "FailedToRunDriftDetection"
	ApplyResTypeFoundDrifts                    applyResultType = "FoundDrifts"
	ApplyResTypeFoundDriftsInDegradedMode      applyResultType = "FoundDriftsInDegradedMode"
	ApplyResTypeFailedToApply                  applyResultType = "FailedToApply"

	// The result type and description for successful apply ops.
	ApplyResTypeApplied applyResultType = "Applied"
)

type availabilityCheckResultType string

type manifestProcessingState struct {
	// The manifest data in its raw form (not yet decoded).
	manifest *placementv1alpha1.Manifest

	// The manifest identifier.
	//
	// If the manifest data cannot be decoded as a Kubernetes API object at all, the identifier
	// will feature only the ordinal of the manifest data (its rank in the list of the resources).
	// If the manifest data can be decoded as a Kubernetes API object, but the API is not available
	// on the member cluster, the resource field of the identifier will be empty.
	id *placementv1alpha1.ManifestIdentifier
	// A string representation of the resource identifier (sans the resources field).
	// This is only populated if the manifest data can be successfully decoded.
	//
	// It is of the format `GV=[API_GROUP]/[VERSION], Kind=[KIND], Namespace=[NAMESPACE], Name=[NAME]`,
	// where [API_GROUP], [VERSION], [KIND], [NAMESPACE], and [NAME] are the API group/version/kind of the
	// manifest object, and its owner namespace (if applicable)/name, respectively.
	idStr string

	// The manifest data, decoded as a Kubernetes API object.
	manifestObj *unstructured.Unstructured

	// The object in the member cluster that corresponds to the manifest object.
	inMemberClusterObj *unstructured.Unstructured

	// The GVR of the manifest object.
	gvr *schema.GroupVersionResource

	// The result of the apply operation.
	applyRes applyResultType
	// The result of the availability check operation.
	availabilityCheckRes availabilityCheckResultType
	// The error that occurred during the apply operation, if any.
	applyErr error
	// The error that occurred during the availability check operation, if any.
	availabilityCheckErr error
	// The diffs detected in the apply operation.
	diffs []placementv1alpha1.PatchDetail

	// A link back to the work object that includes the manifest.
	fromWorkObj *placementv1alpha1.Work
	// The expected owner reference for the manifest object.
	ownedBy *metav1.OwnerReference
}

type workObjectProcessingState struct {
	// The work object being processed.
	work *placementv1alpha1.Work

	// The corresponding appliedWork object for the work object being processed.
	appliedWork         *placementv1alpha1.AppliedWork
	appliedWorkOwnerRef *metav1.OwnerReference

	// The processing state of each manifest within the work object.
	manifestProcessingStates []*manifestProcessingState
}

type Reconciler struct {
	hubClient          client.Client
	spokeDynamicClient dynamic.Interface
	spokeClient        client.Client

	restMapper meta.RESTMapper

	parallelizer parallelizerutil.Parallelizer

	ready atomic.Bool
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if !r.ready.Load() {
		klog.V(2).InfoS("Work applier is not yet ready to start; the member agent might still be connecting to the hub cluster",
			"work", req.NamespacedName)
		return ctrl.Result{RequeueAfter: time.Second * 5}, nil
	}

	startTime := time.Now()
	klog.V(2).InfoS("Reconciliation starts", "work", req.NamespacedName, "controller", controllerName)
	defer func() {
		latency := time.Since(startTime).Milliseconds()
		klog.V(2).InfoS("Reconciliation ends", "work", req.NamespacedName, "latency", latency, "controller", controllerName)
	}()

	// Retrieve the work object.
	work := &placementv1alpha1.Work{}
	err := r.hubClient.Get(ctx, req.NamespacedName, work)
	switch {
	case apierrors.IsNotFound(err):
		klog.V(2).InfoS("The work object cannot be found", "work", req.NamespacedName, "controller", controllerName)
		return ctrl.Result{}, nil
	case err != nil:
		wrappedErr := errors.NewAPIServerError(err, "", true, "work", req.NamespacedName, "controller", controllerName)
		klog.ErrorS(err, "Failed to retrieve the work", errors.Args(wrappedErr)...)
		return ctrl.Result{}, wrappedErr
	}

	// Check if the work object is a primary one, i.e., it has the count annotation set.
	_, found := work.GetAnnotations()[placementv1alpha1.LinkedWorkCountAnnotationKey]
	if !found {
		klog.V(2).InfoS("The work object is not a primary one; skipping reconciliation", "work", req.NamespacedName, "controller", controllerName)
		return ctrl.Result{}, nil
	}

	// Retrieve all linked work objects.
	works, err := r.retrieveLinkedWorks(ctx, work)
	if err != nil {
		wrappedErr := errors.Wraps(err, "", "primaryWork", klog.KObj(work), "controller", controllerName)
		klog.ErrorS(err, "Failed to retrieve linked work objects", errors.Args(wrappedErr)...)
		return ctrl.Result{}, wrappedErr
	}

	// Clean things up if the work object has been marked for deletion.
	if !work.DeletionTimestamp.IsZero() {
		// Perform cleanup logic here.
		panic("not yet implemented")
	}

	// Add cleanup finalizer to all linked work objects.
	if err := r.addCleanupFinalizerTo(ctx, works); err != nil {
		wrappedErr := errors.Wraps(err, "", "primaryWork", klog.KObj(work), "controller", controllerName)
		klog.ErrorS(err, "Failed to add cleanup finalizer to linked work objects", errors.Args(wrappedErr)...)
		return ctrl.Result{}, wrappedErr
	}

	// Ensure one appliedWork object for each linked work object. These objects are created as owners for
	// applied manifests.
	//
	// Note that this method returns a list of appliedWork objects in the same order as the passed-in linked
	// work objects.
	appliedWorks, err := r.ensureAppliedWorks(ctx, works)
	if err != nil {
		wrappedErr := errors.Wraps(err, "", "primaryWork", klog.KObj(work), "controller", controllerName)
		klog.ErrorS(err, "Failed to ensure appliedWork objects for linked work objects", errors.Args(wrappedErr)...)
		return ctrl.Result{}, wrappedErr
	}

	// Prepare the processing states for the work objects and their manifests.
	workObjProcessingStates, manifestProcessingStates := prepareWorkObjectAndManifestProcessingStates(works, appliedWorks)

	// Pre-process the manifests to apply.
	//
	// In this step, the work applier will:
	// a) decode the manifests; and
	// b) write ahead the manifest processing attempts; and
	// c) remove any applied manifests left over from previous runs.
	if err := r.preProcessWorkObjects(ctx, workObjProcessingStates); err != nil {
		wrappedErr := errors.Wraps(err, "", "primaryWork", klog.KObj(work), "controller", controllerName)
		klog.ErrorS(err, "Failed to pre-process work objects", errors.Args(wrappedErr)...)
		return ctrl.Result{}, wrappedErr
	}

	// Set default values in the work object spec to avoid additional validation logic in the later steps.
	setDefaultSyncStrategy(work)

	// Process the manifests.
	//
	// In this step, the work applier will:
	// a) find if there has been a corresponding object in the member cluster for each manifest;
	// b) take over the object if applicable;
	// c) check for diffs/drifts if applicable;
	// e) apply each manifest.
	if err := r.processManifests(ctx, manifestProcessingStates); err != nil {
		wrappedErr := errors.Wraps(err, "", "primaryWork", klog.KObj(work), "controller", controllerName)
		klog.ErrorS(err, "Failed to process manifests", errors.Args(wrappedErr)...)
		return ctrl.Result{}, wrappedErr
	}

	return ctrl.Result{}, nil
}
