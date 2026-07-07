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

package deploymentwatcher

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
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
	controllerName = "DeploymentWatcher"

	deploymentForPlacementFinalizer = "experimental.kubefleet.dev/deployment-for-placement"

	deploymentForPlacementAnnotationKey = "experimental.kubefleet.dev/place-to-regions"

	placementPolicyNameFmt = "%s"
)

type Reconciler struct {
	HubClient client.Client
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	startTime := time.Now()
	klog.V(2).InfoS("Reconciliation starts", "deployment", req.NamespacedName, "controller", controllerName)
	defer func() {
		latency := time.Since(startTime).Milliseconds()
		klog.V(2).InfoS("Reconciliation ends", "deployment", req.NamespacedName, "controller", controllerName, "latency", latency)
	}()

	// Retrieve the Deployment object.
	deploy := &appsv1.Deployment{}
	err := r.HubClient.Get(ctx, req.NamespacedName, deploy)
	switch {
	case apierrors.IsNotFound(err):
		// The Deployment object cannot be found; it may have been deleted already. No need for further
		// reconciliation.
		klog.V(2).InfoS("Deployment cannot be found", "deployment", req.NamespacedName, "controller", controllerName)
		return ctrl.Result{}, nil
	case err != nil:
		// An error occurred while trying to retrieve the Deployment object; retry later.
		wrappedErr := errors.NewAPIServerError(err, "", true, "deployment", req.NamespacedName, "controller", controllerName)
		klog.ErrorS(wrappedErr, "Failed to get Deployment object", errors.Args(wrappedErr)...)
		return ctrl.Result{}, wrappedErr
	}

	if !deploy.DeletionTimestamp.IsZero() {
		if err := r.deletePlacementPolicyFor(ctx, deploy); err != nil {
			klog.ErrorS(err, "Failed to delete placement policy for the deployment", append(errors.Args(err), "deployment", req.NamespacedName, "controller", controllerName)...)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	placeToVal, ok := deploy.Annotations[deploymentForPlacementAnnotationKey]
	if !ok {
		// The place-to annotation is not present; the deployment is not marked for placement.
		klog.V(2).InfoS("Deployment is not marked for placement as the place-to annotation is not present", "deployment", req.NamespacedName, "controller", controllerName)

		// In case the deployment was previously marked for placement but now is not,
		// delete the corresponding placement object (if any) and remove the finalizer from the deployment.
		if err := r.deletePlacementPolicyFor(ctx, deploy); err != nil {
			klog.ErrorS(err, "Failed to delete placement policy for the deployment that is no longer marked for placement", append(errors.Args(err), "deployment", req.NamespacedName, "controller", controllerName)...)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Add a finalizer to the deployment.
	if !controllerutil.ContainsFinalizer(deploy, deploymentForPlacementFinalizer) {
		controllerutil.AddFinalizer(deploy, deploymentForPlacementFinalizer)
		if err := r.HubClient.Update(ctx, deploy); err != nil {
			wrappedErr := errors.NewAPIServerError(err, "", false, "deployment", req.NamespacedName, "controller", controllerName)
			klog.ErrorS(wrappedErr, "Failed to add finalizer to the deployment", errors.Args(wrappedErr)...)
			return ctrl.Result{}, wrappedErr
		}
	}

	placeTos := strings.Split(placeToVal, ",")
	// Sort the target names for deterministic processing.
	slices.SortFunc(placeTos, func(x, y string) int {
		return strings.Compare(x, y)
	})

	additionalResRefs, err := r.collectAdditionalResourceManifests(ctx, deploy)
	if err != nil {
		wrappedErr := errors.Wraps(err, "", "deployment", req.NamespacedName, "controller", controllerName)
		klog.ErrorS(wrappedErr, "Failed to collect additional resource manifests for the deployment", errors.Args(wrappedErr)...)
		return ctrl.Result{}, wrappedErr
	}

	placement := &experimentalv1beta1.PlacementPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf(placementPolicyNameFmt, deploy.Name),
			Namespace: deploy.Namespace,
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

		// Add the resource selectors, starting with the workload (the Deployment itself) followed by
		// any additional resources it references.
		resourceSelectors := make([]experimentalv1beta1.SameNamespacedObjectReference, 0, len(additionalResRefs)+1)
		resourceSelectors = append(resourceSelectors, experimentalv1beta1.SameNamespacedObjectReference{
			Kind:       "Deployment",
			APIGroup:   "apps",
			APIVersion: "v1",
			Resource:   "deployments",
			Name:       deploy.Name,
		})
		resourceSelectors = append(resourceSelectors, additionalResRefs...)
		placement.Spec.ResourceSelectors = resourceSelectors
		return nil
	})
	if err != nil {
		wrappedErr := errors.NewAPIServerError(err, "", false, "deployment", req.NamespacedName, "placementPolicy", client.ObjectKeyFromObject(placement), "op", resOp, "controller", controllerName)
		klog.ErrorS(wrappedErr, "Failed to create or update placement policy for the deployment", errors.Args(wrappedErr)...)
		return ctrl.Result{}, wrappedErr
	}
	klog.V(2).InfoS("Created or updated placement policy for the deployment", "deployment", req.NamespacedName, "placementPolicy", client.ObjectKeyFromObject(placement), "op", resOp, "controller", controllerName)
	return ctrl.Result{}, nil
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.Deployment{}).
		Complete(r)
}
