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
	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
)

// eligibilityChecker is the subset of the scheduler's cluster eligibility gate that the
// placement policy controller depends on. Selector fulfillment is judged against this predicate
// rather than raw label matching, so that a cluster only counts once it is actually usable for
// scheduling (member agent online, heartbeating, and joined); taking it as an interface keeps
// the predicate pluggable and fakeable in tests.
type eligibilityChecker interface {
	IsEligible(cluster *clusterv1beta1.MemberCluster) (eligible bool, reason string)
}
