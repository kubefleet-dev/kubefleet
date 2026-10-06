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

package fulfiller

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/naming"
)

const (
	// clusterNamePrefixMaxLength bounds how much of the claim name survives in the cluster name.
	// The hub creates a namespace named fleet-member-<cluster>, a DNS label of at most 63
	// characters, which leaves 50 for the cluster name: 36 of prefix, a dash, and 12 of hash.
	clusterNamePrefixMaxLength = 36
	clusterNameHashLength      = 12
)

// ClusterNameFor derives the name of the cluster provisioned for a claim. The prefix keeps the
// name readable; the hash of the claim's UID keeps two claims that share a name — KubeFleet
// re-issues a claim under the same name for the next cluster of the same selector — from sharing
// a cluster. The result is a DNS label of at most 49 characters.
func ClusterNameFor(claim *kfplacementv1alpha1.ClusterClaim) string {
	return fmt.Sprintf("%s-%s",
		naming.Sanitize(naming.Truncate(claim.Name, clusterNamePrefixMaxLength)),
		naming.Hash(string(claim.UID))[:clusterNameHashLength])
}

// OwnershipLabels are the labels a provider must set on the MemberCluster it registers for the
// request. The framework finds the clusters it owns for a claim name through the first, which is
// shortened when the claim name does not fit a label value, and tells the one provisioned for
// this very claim apart through the second.
func (req Request) OwnershipLabels() map[string]string {
	return map[string]string{
		kfplacementv1alpha1.FulfilledClaimNameLabel: naming.LabelValue(req.ClaimName),
		kfplacementv1alpha1.FulfilledClaimUIDLabel:  string(req.ClaimUID),
	}
}

// joined reports whether a member cluster has ever joined the fleet: its member agent reported
// Joined=True. The agent writes that only on a successful join and never clears it on heartbeat
// loss (it goes Unknown or False only on a failed join or an explicit leave), so a cluster that
// joined and then went dark still says Joined=True, while a cluster that never joined has no such
// condition at all. ObservedGeneration is deliberately ignored, as the eligibility checker does:
// the hub rewrites it on every copy from the internal member cluster.
func joined(cluster *clusterv1beta1.MemberCluster) bool {
	cond := cluster.GetAgentCondition(clusterv1beta1.MemberAgent, clusterv1beta1.AgentJoined)
	return cond != nil && cond.Status == metav1.ConditionTrue
}
