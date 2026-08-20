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
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/utils/ptr"

	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
)

func policyAdapterFor(name, namespace string) policyObject {
	if namespace == "" {
		return clusterPlacementPolicyAdapter{&kfplacementv1alpha1.ClusterPlacementPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: name},
		}}
	}
	return placementPolicyAdapter{&kfplacementv1alpha1.PlacementPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}}
}

func TestClaimName(t *testing.T) {
	policyA := policyAdapterFor("app", "tenant-a")
	policyB := policyAdapterFor("app", "tenant-b")
	clusterScoped := policyAdapterFor("app", "")

	nameA := claimName(policyA, 0)
	nameB := claimName(policyB, 0)
	nameCluster := claimName(clusterScoped, 0)

	if nameA == nameB {
		t.Errorf("claimName(tenant-a/app, 0) = claimName(tenant-b/app, 0) = %s, want distinct names", nameA)
	}
	if nameA == nameCluster {
		t.Errorf("claimName(tenant-a/app, 0) = claimName(cluster-scoped app, 0) = %s, want distinct names", nameA)
	}
	if got := claimName(policyA, 0); got != nameA {
		t.Errorf("claimName(tenant-a/app, 0) = %s on the second call, want the deterministic %s", got, nameA)
	}
	if nameIdx1 := claimName(policyA, 1); nameIdx1 == nameA {
		t.Errorf("claimName(tenant-a/app, 1) = claimName(tenant-a/app, 0) = %s, want distinct names per selector", nameA)
	}

	longPolicy := policyAdapterFor(strings.Repeat("x", 250), "tenant-a")
	if got := claimName(longPolicy, 0); len(got) > 253 {
		t.Errorf("claimName(long policy, 0) has length %d, want at most 253", len(got))
	}
}

// TestPolicyNameLabelValue guards the ownership label against the 63-byte limit on label
// values: object names may be far longer, and an invalid label value makes claim creation fail
// outright.
func TestPolicyNameLabelValue(t *testing.T) {
	longName := strings.Repeat("x", 250)
	sameFirst54 := strings.Repeat("x", 54) + strings.Repeat("y", 196)

	testCases := []struct {
		name       string
		policyName string
		want       string
	}{
		{
			name:       "short name is used as is",
			policyName: "app",
			want:       "app",
		},
		{
			name:       "name at the limit is used as is",
			policyName: strings.Repeat("a", validation.LabelValueMaxLength),
			want:       strings.Repeat("a", validation.LabelValueMaxLength),
		},
		{
			name:       "trailing separators are trimmed before the hash suffix",
			policyName: strings.Repeat("a", labelValuePrefixMaxLength-1) + "-" + strings.Repeat("b", 40),
			want:       fmt.Sprintf("%s-%s", strings.Repeat("a", labelValuePrefixMaxLength-1), hashOf(strings.Repeat("a", labelValuePrefixMaxLength-1)+"-"+strings.Repeat("b", 40))),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := policyNameLabelValue(tc.policyName)
			if got != tc.want {
				t.Errorf("policyNameLabelValue(%q) = %q, want %q", tc.policyName, got, tc.want)
			}
		})
	}

	t.Run("long names produce valid, distinct label values", func(t *testing.T) {
		for _, name := range []string{longName, sameFirst54} {
			got := policyNameLabelValue(name)
			if errs := validation.IsValidLabelValue(got); len(errs) > 0 {
				t.Errorf("policyNameLabelValue(len %d) = %q, want a valid label value, got errors %v", len(name), got, errs)
			}
		}
		if a, b := policyNameLabelValue(longName), policyNameLabelValue(sameFirst54); a == b {
			t.Errorf("policyNameLabelValue collided for two names sharing a prefix: both %q", a)
		}
	})
}

// TestClaimNameValidity guards the generated claim name against the 253-character object name
// limit and DNS-1123 subdomain rules, including names whose truncation point lands on a
// separator.
func TestClaimNameValidity(t *testing.T) {
	testCases := []struct {
		name       string
		policyName string
	}{
		{
			name:       "short name",
			policyName: "app",
		},
		{
			name:       "long name",
			policyName: strings.Repeat("x", 250),
		},
		{
			name:       "dot at the truncation boundary",
			policyName: strings.Repeat("a", claimNameBaseMaxLength-1) + "." + strings.Repeat("a", 50),
		},
		{
			name:       "dash at the truncation boundary",
			policyName: strings.Repeat("a", claimNameBaseMaxLength-1) + "-" + strings.Repeat("a", 50),
		},
		{
			name:       "run of separators at the truncation boundary",
			policyName: strings.Repeat("a", claimNameBaseMaxLength-3) + "-.-" + strings.Repeat("a", 50),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := claimName(policyAdapterFor(tc.policyName, "tenant-a"), 0)
			if errs := validation.IsDNS1123Subdomain(got); len(errs) > 0 {
				t.Errorf("claimName(%q, 0) = %q, want a valid DNS-1123 subdomain, got errors %v", tc.policyName, got, errs)
			}
		})
	}
}

// TestPolicyNameLabelValueValidityAtBoundaries checks that label values stay valid when the
// truncation point lands on a separator.
func TestPolicyNameLabelValueValidityAtBoundaries(t *testing.T) {
	for _, sep := range []string{".", "-", "_"} {
		name := strings.Repeat("a", labelValuePrefixMaxLength-1) + sep + strings.Repeat("a", 100)
		got := policyNameLabelValue(name)
		if errs := validation.IsValidLabelValue(got); len(errs) > 0 {
			t.Errorf("policyNameLabelValue(name with %q at the boundary) = %q, want a valid label value, got errors %v", sep, got, errs)
		}
	}
}

func TestDesiredClaims(t *testing.T) {
	policy := policyAdapterFor("app", "tenant-a")
	regionTerms := func(region string) []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm {
		return []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{
			{MatchLabels: map[string]string{regionLabel: region}},
		}
	}

	testCases := []struct {
		name     string
		outcomes []selectorOutcome
		want     []desiredClaim
	}{
		{
			name: "one claim for the first unfulfilled selector",
			outcomes: []selectorOutcome{
				{
					counts:          resolvedCounts{desired: 1, minimum: 1},
					terms:           regionTerms("eastus"),
					whenUnfulfilled: kfplacementv1alpha1.WhenUnfulfilledOptionAddClusterClaim,
				},
			},
			want: []desiredClaim{{name: claimName(policyAdapterFor("app", "tenant-a"), 0), terms: regionTerms("eastus")}},
		},
		{
			name: "budget caps at one claim across multiple unfulfilled selectors",
			outcomes: []selectorOutcome{
				{
					counts:          resolvedCounts{desired: 1, minimum: 1},
					terms:           regionTerms("eastus"),
					whenUnfulfilled: kfplacementv1alpha1.WhenUnfulfilledOptionAddClusterClaim,
				},
				{
					counts:          resolvedCounts{desired: 1, minimum: 1},
					terms:           regionTerms("westus"),
					whenUnfulfilled: kfplacementv1alpha1.WhenUnfulfilledOptionAddClusterClaim,
				},
			},
			want: []desiredClaim{{name: claimName(policyAdapterFor("app", "tenant-a"), 0), terms: regionTerms("eastus")}},
		},
		{
			name: "fulfilled selectors and KeepSearching selectors yield no claims",
			outcomes: []selectorOutcome{
				{
					counts:          resolvedCounts{desired: 1, minimum: 1},
					matched:         []string{"a"},
					terms:           regionTerms("eastus"),
					whenUnfulfilled: kfplacementv1alpha1.WhenUnfulfilledOptionAddClusterClaim,
				},
				{
					counts:          resolvedCounts{desired: 1, minimum: 1},
					terms:           regionTerms("westus"),
					whenUnfulfilled: kfplacementv1alpha1.WhenUnfulfilledOptionKeepSearching,
				},
			},
			want: []desiredClaim{},
		},
		{
			name: "a fulfilled earlier selector passes the budget to a later one",
			outcomes: []selectorOutcome{
				{
					counts:          resolvedCounts{desired: 1, minimum: 1},
					matched:         []string{"a"},
					terms:           regionTerms("eastus"),
					whenUnfulfilled: kfplacementv1alpha1.WhenUnfulfilledOptionAddClusterClaim,
				},
				{
					counts:          resolvedCounts{desired: 2, minimum: 2},
					matched:         []string{"a"},
					terms:           regionTerms("westus"),
					whenUnfulfilled: kfplacementv1alpha1.WhenUnfulfilledOptionAddClusterClaim,
				},
			},
			want: []desiredClaim{{name: claimName(policyAdapterFor("app", "tenant-a"), 1), terms: regionTerms("westus")}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := desiredClaims(policy, tc.outcomes)
			// The outcome field is a pointer back into tc.outcomes that carries no identity of its
			// own to assert here; the name/terms selection is what this test pins. Its wiring is
			// exercised by TestClaimReadyToRotate and the integration tests.
			if diff := cmp.Diff(got, tc.want, cmp.AllowUnexported(desiredClaim{}), cmpopts.IgnoreFields(desiredClaim{}, "outcome")); diff != "" {
				t.Errorf("desiredClaims(%v) mismatch (-got, +want):\n%s", tc.outcomes, diff)
			}
		})
	}
}

func TestClaimReadyToRotate(t *testing.T) {
	completed := func() []metav1.Condition {
		return []metav1.Condition{{Type: kfplacementv1alpha1.ClusterClaimCondTypeCompleted, Status: metav1.ConditionTrue, Reason: "Provisioned"}}
	}
	claim := func(conds []metav1.Condition, provisioned *string) *kfplacementv1alpha1.ClusterClaim {
		return &kfplacementv1alpha1.ClusterClaim{
			Status: kfplacementv1alpha1.ClusterClaimStatus{Conditions: conds, ProvisionedClusterName: provisioned},
		}
	}
	// A count-of-3 selector with one eligible cluster: a deficit remains.
	deficit := &selectorOutcome{counts: resolvedCounts{desired: 3, minimum: 3}, matched: []string{"c1"}}

	testCases := []struct {
		name    string
		claim   *kfplacementv1alpha1.ClusterClaim
		outcome *selectorOutcome
		want    bool
	}{
		{
			name:    "completed with an eligible cluster while a deficit remains rotates",
			claim:   claim(completed(), ptr.To("c1")),
			outcome: deficit,
			want:    true,
		},
		{
			name:    "not yet completed does not rotate",
			claim:   claim(nil, nil),
			outcome: deficit,
			want:    false,
		},
		{
			// The provisioner completed but its cluster has not joined/become eligible yet: waiting
			// here is what keeps a second claim from being issued mid-join (no double-provisioning).
			name:    "completed but the provisioned cluster is not eligible yet does not rotate",
			claim:   claim(completed(), ptr.To("c2")),
			outcome: deficit,
			want:    false,
		},
		{
			name:    "completed with no provisioned cluster recorded does not rotate",
			claim:   claim(completed(), nil),
			outcome: deficit,
			want:    false,
		},
		{
			// The selector is already at its desired count; the claim is withdrawn (not rotated) by
			// the fulfilled path, so rotation must not fire.
			name:    "satisfied in full does not rotate",
			claim:   claim(completed(), ptr.To("c1")),
			outcome: &selectorOutcome{counts: resolvedCounts{desired: 1, minimum: 1}, matched: []string{"c1"}},
			want:    false,
		},
		{
			// count: All with a minCount floor is satisfied in full at the floor, so a completed
			// claim below the floor still has a deficit and rotates, mirroring the integer case.
			name:    "select-all below its minCount floor rotates",
			claim:   claim(completed(), ptr.To("c1")),
			outcome: &selectorOutcome{counts: resolvedCounts{selectAll: true, minimum: 2}, matched: []string{"c1"}},
			want:    true,
		},
		{
			name:    "a nil outcome does not rotate",
			claim:   claim(completed(), ptr.To("c1")),
			outcome: nil,
			want:    false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := claimReadyToRotate(tc.claim, tc.outcome); got != tc.want {
				t.Errorf("claimReadyToRotate() = %v, want %v", got, tc.want)
			}
		})
	}
}
