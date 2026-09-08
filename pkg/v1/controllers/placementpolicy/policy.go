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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
)

// policyObject abstracts over the PlacementPolicy and ClusterPlacementPolicy APIs, which share
// their spec and status shapes but are distinct Kubernetes kinds at different scopes. The
// reconciliation logic operates on this interface so that both kinds flow through a single code
// path; the two adapters below are the only scope-aware pieces.
type policyObject interface {
	// Only the metadata accessors are exposed, deliberately not the full client.Object: client
	// write calls reflect over the concrete registered type and fail on an adapter value, so
	// the interface is kept narrow enough that passing it to such a call cannot compile;
	// Unwrap is the one sanctioned bridge.
	metav1.Object

	// PolicySpec returns the policy's spec; the returned pointer aliases the underlying object.
	PolicySpec() *kfplacementv1alpha1.PlacementPolicySpec
	// PolicyStatus returns the policy's status; the returned pointer aliases the underlying
	// object, so mutations feed directly into a subsequent status update call.
	PolicyStatus() *kfplacementv1alpha1.PlacementPolicyStatus
	// Unwrap returns the underlying API object; client calls (e.g., Status().Update) must be
	// given the registered concrete pointer type, not the adapter value.
	Unwrap() client.Object
}

// placementPolicyAdapter adapts the namespaced PlacementPolicy API to the policyObject interface.
type placementPolicyAdapter struct {
	*kfplacementv1alpha1.PlacementPolicy
}

func (a placementPolicyAdapter) PolicySpec() *kfplacementv1alpha1.PlacementPolicySpec {
	return &a.Spec
}

func (a placementPolicyAdapter) PolicyStatus() *kfplacementv1alpha1.PlacementPolicyStatus {
	return &a.Status
}

func (a placementPolicyAdapter) Unwrap() client.Object {
	return a.PlacementPolicy
}

// clusterPlacementPolicyAdapter adapts the cluster-scoped ClusterPlacementPolicy API to the
// policyObject interface.
type clusterPlacementPolicyAdapter struct {
	*kfplacementv1alpha1.ClusterPlacementPolicy
}

func (a clusterPlacementPolicyAdapter) PolicySpec() *kfplacementv1alpha1.PlacementPolicySpec {
	return &a.Spec
}

func (a clusterPlacementPolicyAdapter) PolicyStatus() *kfplacementv1alpha1.PlacementPolicyStatus {
	return &a.Status
}

func (a clusterPlacementPolicyAdapter) Unwrap() client.Object {
	return a.ClusterPlacementPolicy
}

// eligibilityChecker is the subset of the scheduler's cluster eligibility gate that the
// placement policy controller depends on. Selector fulfillment is judged against this predicate
// rather than raw label matching, so that a cluster only counts once it is actually usable for
// scheduling (member agent online, heartbeating, and joined); taking it as an interface keeps
// the predicate pluggable and fakeable in tests.
type eligibilityChecker interface {
	IsEligible(cluster *clusterv1beta1.MemberCluster) (eligible bool, reason string)
}
