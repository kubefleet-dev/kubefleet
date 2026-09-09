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
	"sigs.k8s.io/controller-runtime/pkg/client"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
)

// policyObject is a placement policy of either scope: the PlacementPolicy and
// ClusterPlacementPolicy kinds share their spec and status shapes, reached through the API's
// accessor, so the reconciliation logic flows both through a single code path.
type policyObject interface {
	client.Object
	kfplacementv1alpha1.PlacementPolicyAccessor
}

// eligibilityChecker is the subset of the scheduler's cluster eligibility gate that the
// placement policy controller depends on. Selector fulfillment is judged against this predicate
// rather than raw label matching, so that a cluster only counts once it is actually usable for
// scheduling (member agent online, heartbeating, and joined); taking it as an interface keeps
// the predicate pluggable and fakeable in tests.
type eligibilityChecker interface {
	IsEligible(cluster *clusterv1beta1.MemberCluster) (eligible bool, reason string)
}
