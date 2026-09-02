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

package clusterclaim

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	placementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
)

// clusterMatchesTerms reports whether a member cluster satisfies any of the
// given selector terms (terms are ORed; requirements within a term are ANDed),
// mirroring the semantics FEP-0001 assigns to ClusterClaim
// spec.clusterSelectorTerms. Per the FEP, no terms means any cluster matches.
//
// Spike scope: label matching only (MatchLabels + MatchLabelExpressions).
// MatchClusterPropertyExpressions needs the cluster-property provider, which
// is out of scope here; a term carrying property expressions matches nothing.
func clusterMatchesTerms(cluster *clusterv1beta1.MemberCluster, terms []placementv1alpha1.ClusterLabelAndPropertySelectorTerm) bool {
	if len(terms) == 0 {
		return true
	}
	for i := range terms {
		if clusterMatchesTerm(cluster, &terms[i]) {
			return true
		}
	}
	return false
}

func clusterMatchesTerm(cluster *clusterv1beta1.MemberCluster, term *placementv1alpha1.ClusterLabelAndPropertySelectorTerm) bool {
	if len(term.MatchClusterPropertyExpressions) > 0 {
		return false
	}

	sel := &metav1.LabelSelector{MatchLabels: term.MatchLabels}
	for _, expr := range term.MatchLabelExpressions {
		op, ok := labelSelectorOp(expr.Operator)
		if !ok {
			// Numeric operators (Gt, Lt, ...) apply to cluster properties only.
			return false
		}
		sel.MatchExpressions = append(sel.MatchExpressions, metav1.LabelSelectorRequirement{
			Key:      expr.Key,
			Operator: op,
			Values:   expr.Values,
		})
	}

	s, err := metav1.LabelSelectorAsSelector(sel)
	if err != nil {
		return false
	}
	return s.Matches(labels.Set(cluster.Labels))
}

func labelSelectorOp(op placementv1alpha1.LabelClusterPropertyExpressionOperator) (metav1.LabelSelectorOperator, bool) {
	switch op {
	case placementv1alpha1.LabelClusterPropertyExpressionOperatorIn:
		return metav1.LabelSelectorOpIn, true
	case placementv1alpha1.LabelClusterPropertyExpressionOperatorNotIn:
		return metav1.LabelSelectorOpNotIn, true
	case placementv1alpha1.LabelClusterPropertyExpressionOperatorExists:
		return metav1.LabelSelectorOpExists, true
	case placementv1alpha1.LabelClusterPropertyExpressionOperatorDoesNotExist:
		return metav1.LabelSelectorOpDoesNotExist, true
	default:
		return "", false
	}
}
