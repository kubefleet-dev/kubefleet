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

// Package placementpolicy implements the FEP-0001 placement policy controller, which reconciles
// the PlacementPolicy and ClusterPlacementPolicy API objects (placement.kubefleet.dev API group):
// it resolves cluster selectors against the current member cluster inventory, reports scheduling
// status on the policy objects, and manages the lifecycle of ClusterClaim API objects for
// selectors that cannot be fulfilled.
package placementpolicy

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog/v2"
	runtime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
)

// Reconciler reconciles PlacementPolicy and ClusterPlacementPolicy objects.
//
// One Reconciler instance serves both APIs: requests for cluster-scoped ClusterPlacementPolicy
// objects carry no namespace, which is how the two are told apart.
type Reconciler struct {
	client.Client
}

// Reconcile runs a single reconciliation round for a PlacementPolicy or ClusterPlacementPolicy object.
func (r *Reconciler) Reconcile(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	startTime := time.Now()
	klog.V(2).InfoS("Placement policy reconciliation starts", "placementPolicy", req.NamespacedName)
	defer func() {
		latency := time.Since(startTime).Milliseconds()
		klog.V(2).InfoS("Placement policy reconciliation ends", "placementPolicy", req.NamespacedName, "latency", latency)
	}()

	policy, err := r.fetchPolicy(ctx, req)
	if err != nil {
		if errors.IsNotFound(err) {
			klog.V(4).InfoS("Ignoring not-found placement policy", "placementPolicy", req.NamespacedName)
			return runtime.Result{}, nil
		}
		klog.ErrorS(err, "Failed to get the placement policy object", "placementPolicy", req.NamespacedName)
		return runtime.Result{}, err
	}
	klog.V(2).InfoS("Observed a placement policy", "placementPolicy", req.NamespacedName, "generation", policy.GetGeneration())

	return runtime.Result{}, nil
}

// fetchPolicy retrieves the policy object for the given request; requests without a namespace
// concern the cluster-scoped ClusterPlacementPolicy API. Errors, including not-found ones, are
// returned as is for the caller to inspect.
func (r *Reconciler) fetchPolicy(ctx context.Context, req runtime.Request) (client.Object, error) {
	var policy client.Object
	if req.Namespace == "" {
		policy = &kfplacementv1alpha1.ClusterPlacementPolicy{}
	} else {
		policy = &kfplacementv1alpha1.PlacementPolicy{}
	}
	if err := r.Get(ctx, req.NamespacedName, policy); err != nil {
		return nil, err
	}
	return policy, nil
}

// SetupWithManagerForPlacementPolicy registers the reconciler with the manager for the
// namespaced PlacementPolicy API.
func (r *Reconciler) SetupWithManagerForPlacementPolicy(mgr runtime.Manager) error {
	return runtime.NewControllerManagedBy(mgr).
		Named("placement-policy-controller").
		For(&kfplacementv1alpha1.PlacementPolicy{}).
		Complete(r)
}

// SetupWithManagerForClusterPlacementPolicy registers the reconciler with the manager for the
// cluster-scoped ClusterPlacementPolicy API.
func (r *Reconciler) SetupWithManagerForClusterPlacementPolicy(mgr runtime.Manager) error {
	return runtime.NewControllerManagedBy(mgr).
		Named("cluster-placement-policy-controller").
		For(&kfplacementv1alpha1.ClusterPlacementPolicy{}).
		Complete(r)
}
