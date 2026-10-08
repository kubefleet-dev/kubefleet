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
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/utils/ptr"

	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
)

func policyFor(name, namespace string) kfplacementv1alpha1.PlacementPolicyAccessor {
	if namespace == "" {
		return &kfplacementv1alpha1.ClusterPlacementPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: name},
		}
	}
	return &kfplacementv1alpha1.PlacementPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
}

func TestClaimName(t *testing.T) {
	policyA := policyFor("app", "tenant-a")
	policyB := policyFor("app", "tenant-b")
	clusterScoped := policyFor("app", "")

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

	longPolicy := policyFor(strings.Repeat("x", 250), "tenant-a")
	if got := claimName(longPolicy, 0); len(got) > 253 {
		t.Errorf("claimName(long policy, 0) has length %d, want at most 253", len(got))
	}
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
			got := claimName(policyFor(tc.policyName, "tenant-a"), 0)
			if errs := validation.IsDNS1123Subdomain(got); len(errs) > 0 {
				t.Errorf("claimName(%q, 0) = %q, want a valid DNS-1123 subdomain, got errors %v", tc.policyName, got, errs)
			}
		})
	}
}

func TestDesiredClaims(t *testing.T) {
	policy := policyFor("app", "tenant-a")
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
			want: []desiredClaim{{name: claimName(policyFor("app", "tenant-a"), 0), terms: regionTerms("eastus")}},
		},
		{
			name: "every unfulfilled selector is wanted; the limits apply at issuing, not here",
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
			want: []desiredClaim{
				{name: claimName(policyFor("app", "tenant-a"), 0), terms: regionTerms("eastus")},
				{name: claimName(policyFor("app", "tenant-a"), 1), terms: regionTerms("westus")},
			},
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
			want: []desiredClaim{{name: claimName(policyFor("app", "tenant-a"), 1), terms: regionTerms("westus")}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := desiredClaims(policy, tc.outcomes, openClass(), "")
			// The outcome field is a pointer back into tc.outcomes that carries no identity of its
			// own to assert here, and the class is the same open class for every case; the
			// name/terms selection is what this test pins. Their wiring is exercised by
			// TestClaimReadyToRotate, TestDesiredClaimsVocabulary, and the integration tests.
			if diff := cmp.Diff(got, tc.want, cmp.AllowUnexported(desiredClaim{}), cmpopts.IgnoreFields(desiredClaim{}, "outcome", "class")); diff != "" {
				t.Errorf("desiredClaims(%v) mismatch (-got, +want):\n%s", tc.outcomes, diff)
			}
		})
	}
}

func TestClaimStillWanted(t *testing.T) {
	terms := func(region string) []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm {
		return []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{{MatchLabels: map[string]string{regionLabel: region}}}
	}
	claimOf := func(region, class string) *kfplacementv1alpha1.ClusterClaim {
		return &kfplacementv1alpha1.ClusterClaim{Spec: kfplacementv1alpha1.ClusterClaimSpec{ClusterSelectorTerms: terms(region), ClusterProviderClassName: class}}
	}
	standard := classWithVocabulary("standard", nil)
	testCases := []struct {
		name  string
		claim *kfplacementv1alpha1.ClusterClaim
		want  desiredClaim
		kept  bool
	}{
		{name: "same terms and class", claim: claimOf("eastus", "standard"), want: desiredClaim{terms: terms("eastus"), class: standard}, kept: true},
		{name: "terms changed", claim: claimOf("eastus", "standard"), want: desiredClaim{terms: terms("westus"), class: standard}, kept: false},
		{name: "class changed", claim: claimOf("eastus", "legacy"), want: desiredClaim{terms: terms("eastus"), class: standard}, kept: false},
		{name: "no class resolves: the stamp is not judged", claim: claimOf("eastus", "standard"), want: desiredClaim{terms: terms("eastus"), blocked: "no class"}, kept: true},
		{name: "vocabulary narrowed under the claim: still kept", claim: claimOf("eastus", "standard"), want: desiredClaim{terms: terms("eastus"), class: standard, blocked: "outside the vocabulary"}, kept: true},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := claimStillWanted(tc.claim, tc.want); got != tc.kept {
				t.Errorf("claimStillWanted(%v, %v) = %t, want %t", tc.claim.Spec, tc.want, got, tc.kept)
			}
		})
	}
}

func TestApproveAutomatically(t *testing.T) {
	classWithApproval := func(mode kfplacementv1alpha1.ClusterClaimApprovalMode) *kfplacementv1alpha1.ClusterProviderClass {
		class := classWithVocabulary("auto", nil)
		class.Spec.Approval = mode
		return class
	}
	claimWith := func(conditions ...metav1.Condition) *kfplacementv1alpha1.ClusterClaim {
		return &kfplacementv1alpha1.ClusterClaim{Status: kfplacementv1alpha1.ClusterClaimStatus{Conditions: conditions}}
	}
	denied := metav1.Condition{Type: kfplacementv1alpha1.ClusterClaimCondTypeApproved, Status: metav1.ConditionFalse, Reason: kfplacementv1alpha1.ClusterClaimApprovedCondReasonDenied}
	testCases := []struct {
		name        string
		claim       *kfplacementv1alpha1.ClusterClaim
		class       *kfplacementv1alpha1.ClusterProviderClass
		wantChanged bool
		wantReason  string
	}{
		{name: "automatic class stamps the approval", claim: claimWith(), class: classWithApproval(kfplacementv1alpha1.ClusterClaimApprovalModeAutomatic), wantChanged: true, wantReason: kfplacementv1alpha1.ClusterClaimApprovedCondReasonAutomaticallyApproved},
		{name: "manual class leaves it to an approver", claim: claimWith(), class: classWithApproval(kfplacementv1alpha1.ClusterClaimApprovalModeManual), wantChanged: false},
		{name: "no class resolves", claim: claimWith(), class: nil, wantChanged: false},
		{name: "an existing entry is never overwritten", claim: claimWith(denied), class: classWithApproval(kfplacementv1alpha1.ClusterClaimApprovalModeAutomatic), wantChanged: false, wantReason: kfplacementv1alpha1.ClusterClaimApprovedCondReasonDenied},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := approveAutomatically(tc.claim, tc.class, time.Now()); got != tc.wantChanged {
				t.Errorf("approveAutomatically() = %t, want %t", got, tc.wantChanged)
			}
			gotReason := ""
			if cond := meta.FindStatusCondition(tc.claim.Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeApproved); cond != nil {
				gotReason = cond.Reason
			}
			if gotReason != tc.wantReason {
				t.Errorf("approveAutomatically() left Approved reason %q, want %q", gotReason, tc.wantReason)
			}
		})
	}
}

func TestClaimReadyToRotate(t *testing.T) {
	completed := func() []metav1.Condition {
		return []metav1.Condition{{Type: kfplacementv1alpha1.ClusterClaimCondTypeCompleted, Status: metav1.ConditionTrue, Reason: "Fulfilled"}}
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

// TestClaimBelongsTo checks that ownership survives a version change but not a group change.
func TestClaimBelongsTo(t *testing.T) {
	ref := &kfplacementv1alpha1.ObjectReference{
		APIGroup: kfplacementv1alpha1.GroupVersion.Group, APIVersion: "v1alpha1",
		Kind: kfplacementv1alpha1.PlacementPolicyKind, Namespace: "work", Name: "app",
	}
	withRef := func(mutate func(r *kfplacementv1alpha1.ObjectReference)) *kfplacementv1alpha1.ClusterClaim {
		got := ref.DeepCopy()
		mutate(got)
		return &kfplacementv1alpha1.ClusterClaim{Spec: kfplacementv1alpha1.ClusterClaimSpec{PlacementPolicyRef: got}}
	}
	testCases := []struct {
		name  string
		claim *kfplacementv1alpha1.ClusterClaim
		want  bool
	}{
		{name: "same reference", claim: withRef(func(*kfplacementv1alpha1.ObjectReference) {}), want: true},
		{name: "another version", claim: withRef(func(r *kfplacementv1alpha1.ObjectReference) { r.APIVersion = "v1" }), want: true},
		{name: "another group", claim: withRef(func(r *kfplacementv1alpha1.ObjectReference) { r.APIGroup = "other.example.dev" }), want: false},
		{name: "another name", claim: withRef(func(r *kfplacementv1alpha1.ObjectReference) { r.Name = "other" }), want: false},
		{name: "no reference", claim: &kfplacementv1alpha1.ClusterClaim{}, want: false},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := claimBelongsTo(tc.claim, ref); got != tc.want {
				t.Errorf("claimBelongsTo() = %t, want %t", got, tc.want)
			}
		})
	}
}
