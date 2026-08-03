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

package orasmanifestswatcher

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	experimentalv1beta1 "github.com/kubefleet-dev/kubefleet/apis/experimental/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/errors"
)

const (
	controllerName = "ORASManifestsWatcher"

	orasManifestsForPlacementFinalizer = "experimental.kubefleet.dev/oras-manifests-for-placement"

	orasManifestsForPlacementAnnotationKey = "experimental.kubefleet.dev/place-to-regions"

	placementPolicyNameFmt = "%s"
)

type Reconciler struct {
	HubClient client.Client
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	startTime := time.Now()
	klog.V(2).InfoS("Reconciliation starts", "ORASManifests", req.NamespacedName, "controller", controllerName)
	defer func() {
		latency := time.Since(startTime).Milliseconds()
		klog.V(2).InfoS("Reconciliation ends", "ORASManifests", req.NamespacedName, "controller", controllerName, "latency", latency)
	}()

	// Retrieve the ORASManifests object.
	orasManifests := &experimentalv1beta1.ORASManifests{}
	err := r.HubClient.Get(ctx, req.NamespacedName, orasManifests)
	switch {
	case apierrors.IsNotFound(err):
		// The ORASManifests object cannot be found; it may have been deleted already. No need for
		// further reconciliation.
		klog.V(2).InfoS("ORASManifests cannot be found", "ORASManifests", req.NamespacedName, "controller", controllerName)
		return ctrl.Result{}, nil
	case err != nil:
		// An error occurred while trying to retrieve the ORASManifests object; retry later.
		wrappedErr := errors.NewAPIServerError(err, "", true, "ORASManifests", req.NamespacedName, "controller", controllerName)
		klog.ErrorS(wrappedErr, "Failed to get ORASManifests object", errors.Args(wrappedErr)...)
		return ctrl.Result{}, wrappedErr
	}

	if !orasManifests.DeletionTimestamp.IsZero() {
		if err := r.deletePlacementPolicyFor(ctx, orasManifests); err != nil {
			klog.ErrorS(err, "Failed to delete placement policy for the ORASManifests", append(errors.Args(err), "ORASManifests", req.NamespacedName, "controller", controllerName)...)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	placeToVal, ok := orasManifests.Annotations[orasManifestsForPlacementAnnotationKey]
	if !ok {
		// The place-to annotation is not present; the ORASManifests is not marked for placement.
		klog.V(2).InfoS("ORASManifests is not marked for placement as the place-to annotation is not present", "ORASManifests", req.NamespacedName, "controller", controllerName)

		// In case the ORASManifests was previously marked for placement but now is not,
		// delete the corresponding placement object (if any) and remove the finalizer.
		if err := r.deletePlacementPolicyFor(ctx, orasManifests); err != nil {
			klog.ErrorS(err, "Failed to delete placement policy for the ORASManifests that is no longer marked for placement", append(errors.Args(err), "ORASManifests", req.NamespacedName, "controller", controllerName)...)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Check that the ORASManifests has an artifact digest resolved in its status before creating
	// a placement policy. Without the digest the content has not been resolved yet.
	if orasManifests.Status.OCIArtifactDetails == nil || orasManifests.Status.OCIArtifactDetails.Digest == "" {
		requeueErr := fmt.Errorf("ORASManifests %s does not have an artifact digest in its status yet; requeueing", req.NamespacedName)
		klog.V(2).InfoS("ORASManifests does not have an artifact digest yet, requeueing", "ORASManifests", req.NamespacedName, "controller", controllerName)
		return ctrl.Result{}, requeueErr
	}

	// Add a finalizer to the ORASManifests.
	if !controllerutil.ContainsFinalizer(orasManifests, orasManifestsForPlacementFinalizer) {
		controllerutil.AddFinalizer(orasManifests, orasManifestsForPlacementFinalizer)
		if err := r.HubClient.Update(ctx, orasManifests); err != nil {
			wrappedErr := errors.NewAPIServerError(err, "", false, "ORASManifests", req.NamespacedName, "controller", controllerName)
			klog.ErrorS(wrappedErr, "Failed to add finalizer to the ORASManifests", errors.Args(wrappedErr)...)
			return ctrl.Result{}, wrappedErr
		}
	}

	placeTos := strings.Split(placeToVal, ",")
	// Sort the target names for deterministic processing.
	slices.SortFunc(placeTos, func(x, y string) int {
		return strings.Compare(x, y)
	})

	placement := &experimentalv1beta1.PlacementPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf(placementPolicyNameFmt, orasManifests.Name),
			Namespace: orasManifests.Namespace,
		},
	}
	resOp, err := ctrl.CreateOrUpdate(ctx, r.HubClient, placement, func() error {
		// Add the cluster by region selector.
		clusterByRegionSelectors := make([]experimentalv1beta1.ClusterSelector, 0, len(placeTos))
		for _, placeTo := range placeTos {
			clusterByRegionSelectors = append(clusterByRegionSelectors, experimentalv1beta1.ClusterSelector{
				Terms: []experimentalv1beta1.LabelAndClusterPropertySelectorTerm{
					{
						MatchLabels: map[string]string{
							"topology.kubernetes.io/region": placeTo,
						},
					},
				},
			})
		}
		placement.Spec.ClusterSelectors = clusterByRegionSelectors

		// Add the resource selector referencing the ORASManifests object itself.
		placement.Spec.ResourceSelectors = []experimentalv1beta1.SameNamespacedObjectReference{
			{
				Kind:       "ORASManifests",
				APIGroup:   "experimental.kubefleet.dev",
				APIVersion: "v1beta1",
				Name:       orasManifests.Name,
			},
		}
		return nil
	})
	if err != nil {
		wrappedErr := errors.NewAPIServerError(err, "", false, "ORASManifests", req.NamespacedName, "placementPolicy", client.ObjectKeyFromObject(placement), "op", resOp, "controller", controllerName)
		klog.ErrorS(wrappedErr, "Failed to create or update placement policy for the ORASManifests", errors.Args(wrappedErr)...)
		return ctrl.Result{}, wrappedErr
	}
	klog.V(2).InfoS("Created or updated placement policy for the ORASManifests", "ORASManifests", req.NamespacedName, "placementPolicy", client.ObjectKeyFromObject(placement), "op", resOp, "controller", controllerName)
	return ctrl.Result{}, nil
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&experimentalv1beta1.ORASManifests{}).
		Complete(r)
}
