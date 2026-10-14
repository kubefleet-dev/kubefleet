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
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/propertyprovider"
)

// openClass builds a class whose vocabulary admits the region label with any value, the shape most
// tests in this package select on.
func openClass() *kfplacementv1alpha1.ClusterProviderClass {
	return classWithVocabulary("open", &kfplacementv1alpha1.SelectorVocabulary{
		LabelKeys: []kfplacementv1alpha1.LabelKeyRule{{Key: regionLabel}},
	})
}

func classWithVocabulary(name string, vocabulary *kfplacementv1alpha1.SelectorVocabulary) *kfplacementv1alpha1.ClusterProviderClass {
	return &kfplacementv1alpha1.ClusterProviderClass{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       kfplacementv1alpha1.ClusterProviderClassSpec{ProvisionerName: "test.kubefleet.dev", SelectorVocabulary: vocabulary},
	}
}

func TestVocabularyViolation(t *testing.T) {
	bounded := classWithVocabulary("bounded", &kfplacementv1alpha1.SelectorVocabulary{
		LabelKeys: []kfplacementv1alpha1.LabelKeyRule{
			{Key: regionLabel, Values: []string{"eastus", "westus"}},
			{Key: "env"},
		},
		PropertyKeys: []kfplacementv1alpha1.PropertyKeyRule{
			{Key: propertyprovider.NodeCountProperty, Min: ptr.To("1"), Max: ptr.To("100")},
			{Key: "custom/tier"},
		},
	})
	broken := classWithVocabulary("broken", &kfplacementv1alpha1.SelectorVocabulary{
		PropertyKeys: []kfplacementv1alpha1.PropertyKeyRule{{Key: propertyprovider.NodeCountProperty, Max: ptr.To("lots")}},
	})
	inverted := classWithVocabulary("inverted", &kfplacementv1alpha1.SelectorVocabulary{
		PropertyKeys: []kfplacementv1alpha1.PropertyKeyRule{{Key: propertyprovider.NodeCountProperty, Min: ptr.To("100"), Max: ptr.To("1")}},
	})
	labelExpr := func(key string, op kfplacementv1alpha1.LabelClusterPropertyExpressionOperator, values ...string) kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm {
		return kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{
			MatchLabelExpressions: []kfplacementv1alpha1.LabelClusterPropertyExpression{{Key: key, Operator: op, Values: values}},
		}
	}
	propertyExpr := func(key string, op kfplacementv1alpha1.LabelClusterPropertyExpressionOperator, values ...string) kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm {
		return kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{
			MatchClusterPropertyExpressions: []kfplacementv1alpha1.LabelClusterPropertyExpression{{Key: key, Operator: op, Values: values}},
		}
	}

	testCases := []struct {
		name  string
		class *kfplacementv1alpha1.ClusterProviderClass
		terms []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm
		want  string
	}{
		{name: "no terms are always claimable, even with no vocabulary", class: classWithVocabulary("bare", nil), want: ""},
		{
			name:  "any term is unclaimable with no vocabulary",
			class: classWithVocabulary("bare", nil),
			terms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{{MatchLabels: map[string]string{regionLabel: "eastus"}}},
			want:  `the cluster provider class "bare" declares no selector vocabulary, so only a selector with no terms can be claimed`,
		},
		{name: "admitted label with an admitted value", class: bounded, terms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{{MatchLabels: map[string]string{regionLabel: "eastus"}}}, want: ""},
		{name: "admitted label with any value", class: bounded, terms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{{MatchLabels: map[string]string{"env": "whatever"}}}, want: ""},
		{
			name:  "label key outside the vocabulary",
			class: bounded,
			terms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{{MatchLabels: map[string]string{"team": "a"}}},
			want:  `the label key "team" is not in the selector vocabulary of the cluster provider class "bounded"`,
		},
		{
			name:  "label value outside the vocabulary",
			class: bounded,
			terms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{{MatchLabels: map[string]string{regionLabel: "antarctica"}}},
			want:  `the value "antarctica" for label key "topology.kubernetes.io/region" is not in the selector vocabulary of the cluster provider class "bounded"`,
		},
		{
			name:  "the first unclaimable key in sorted order is reported",
			class: bounded,
			terms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{{MatchLabels: map[string]string{"zzz": "a", "aaa": "b"}}},
			want:  `the label key "aaa" is not in the selector vocabulary of the cluster provider class "bounded"`,
		},
		{name: "label expression In with admitted values", class: bounded, terms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{labelExpr(regionLabel, kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorIn, "eastus", "westus")}, want: ""},
		{
			name:  "label expression In with a value outside the vocabulary",
			class: bounded,
			terms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{labelExpr(regionLabel, kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorIn, "eastus", "mars")},
			want:  `the value "mars" for label key "topology.kubernetes.io/region" is not in the selector vocabulary of the cluster provider class "bounded"`,
		},
		{
			name:  "label expression NotIn says what not to provision",
			class: bounded,
			terms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{labelExpr(regionLabel, kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorNotIn, "eastus")},
			want:  `the label expression on key "topology.kubernetes.io/region" uses operator NotIn, which does not say what to provision; only In is claimable`,
		},
		{
			name:  "inverted bounds hold no quantity, so even Ge 0 is unmet",
			class: inverted,
			terms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{propertyExpr(propertyprovider.NodeCountProperty, kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorGe, "0")},
			want:  `the bounds [100, 1] of property key "kubernetes-fleet.io/node-count" in the cluster provider class "inverted" are inverted, so no quantity lies within them`,
		},
		{name: "property within bounds", class: bounded, terms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{propertyExpr(propertyprovider.NodeCountProperty, kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorGe, "8")}, want: ""},
		{name: "Eq on the bound", class: bounded, terms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{propertyExpr(propertyprovider.NodeCountProperty, kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorEq, "100")}, want: ""},
		{
			name:  "Eq above the max bound",
			class: bounded,
			terms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{propertyExpr(propertyprovider.NodeCountProperty, kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorEq, "101")},
			want:  `the comparison Eq 101 on property key "kubernetes-fleet.io/node-count" cannot be met within the bounds [1, 100] of the cluster provider class "bounded"`,
		},
		{
			name:  "Ge above the max bound",
			class: bounded,
			terms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{propertyExpr(propertyprovider.NodeCountProperty, kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorGe, "101")},
			want:  `the comparison Ge 101 on property key "kubernetes-fleet.io/node-count" cannot be met within the bounds [1, 100] of the cluster provider class "bounded"`,
		},
		{name: "Ge on the max bound is met by the bound itself", class: bounded, terms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{propertyExpr(propertyprovider.NodeCountProperty, kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorGe, "100")}, want: ""},
		{name: "Ge below the min bound is a threshold the bounds clear", class: bounded, terms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{propertyExpr(propertyprovider.NodeCountProperty, kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorGe, "0")}, want: ""},
		{
			name:  "Gt on the max bound needs room above it",
			class: bounded,
			terms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{propertyExpr(propertyprovider.NodeCountProperty, kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorGt, "100")},
			want:  `the comparison Gt 100 on property key "kubernetes-fleet.io/node-count" cannot be met within the bounds [1, 100] of the cluster provider class "bounded"`,
		},
		{name: "Gt just under the max bound", class: bounded, terms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{propertyExpr(propertyprovider.NodeCountProperty, kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorGt, "99")}, want: ""},
		{
			name:  "Le below the min bound",
			class: bounded,
			terms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{propertyExpr(propertyprovider.NodeCountProperty, kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorLe, "0")},
			want:  `the comparison Le 0 on property key "kubernetes-fleet.io/node-count" cannot be met within the bounds [1, 100] of the cluster provider class "bounded"`,
		},
		{name: "Le above the max bound is a threshold the bounds clear", class: bounded, terms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{propertyExpr(propertyprovider.NodeCountProperty, kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorLe, "200")}, want: ""},
		{
			name:  "Lt on the min bound needs room below it",
			class: bounded,
			terms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{propertyExpr(propertyprovider.NodeCountProperty, kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorLt, "1")},
			want:  `the comparison Lt 1 on property key "kubernetes-fleet.io/node-count" cannot be met within the bounds [1, 100] of the cluster provider class "bounded"`,
		},
		{name: "Lt just above the min bound", class: bounded, terms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{propertyExpr(propertyprovider.NodeCountProperty, kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorLt, "2")}, want: ""},
		{
			name:  "a value that is not a quantity is reported, not panicked on",
			class: bounded,
			terms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{propertyExpr(propertyprovider.NodeCountProperty, kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorEq, "many")},
			want:  `the value "many" for property key "kubernetes-fleet.io/node-count" is not a valid quantity`,
		},
		{name: "unbounded property key", class: bounded, terms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{propertyExpr("custom/tier", kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorEq, "3")}, want: ""},
		{
			name:  "property key outside the vocabulary",
			class: bounded,
			terms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{propertyExpr("custom/other", kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorEq, "3")},
			want:  `the property key "custom/other" is not in the selector vocabulary of the cluster provider class "bounded"`,
		},
		{
			name:  "property operator that says what not to provision",
			class: bounded,
			terms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{propertyExpr(propertyprovider.NodeCountProperty, kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorNe, "3")},
			want:  `the property expression on key "kubernetes-fleet.io/node-count" uses operator Ne, which does not say what to provision; only Gt, Ge, Lt, Le, and Eq are claimable`,
		},
		{
			name:  "a class bound that is not a quantity is the class's fault",
			class: broken,
			terms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{propertyExpr(propertyprovider.NodeCountProperty, kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorEq, "3")},
			want:  `the max bound "lots" for property key "kubernetes-fleet.io/node-count" in the cluster provider class "broken" is not a valid quantity`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := vocabularyViolation(tc.class, tc.terms); got != tc.want {
				t.Errorf("vocabularyViolation(%s, %v) = %q, want %q", tc.class.Name, tc.terms, got, tc.want)
			}
		})
	}
}

// TestDesiredClaimsVocabulary pins that a selector outside the class's vocabulary, or a policy
// with no class, gets no claim issued -- yet stays wanted, so an outstanding claim is kept -- and
// that the note names the first blocked selector, and nothing when no claim is wanted.
func TestDesiredClaimsVocabulary(t *testing.T) {
	policy := policyFor("app", "tenant-a")
	regionTerms := func(region string) []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm {
		return []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{{MatchLabels: map[string]string{regionLabel: region}}}
	}
	unfulfilled := func(terms []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm) selectorOutcome {
		return selectorOutcome{counts: resolvedCounts{desired: 1, minimum: 1}, terms: terms, whenUnfulfilled: kfplacementv1alpha1.WhenUnfulfilledOptionAddClusterClaim}
	}
	satisfied := func(terms []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm) selectorOutcome {
		o := unfulfilled(terms)
		o.matched = []string{"a"}
		return o
	}
	eastOnly := classWithVocabulary("east-only", &kfplacementv1alpha1.SelectorVocabulary{
		LabelKeys: []kfplacementv1alpha1.LabelKeyRule{{Key: regionLabel, Values: []string{"eastus"}}},
	})
	const noClass = "no new cluster claims are issued: the fleet has no default class"
	antarcticaBlocked := `no new cluster claim is issued for cluster selector 0: the value "antarctica" for label key "topology.kubernetes.io/region" is not in the selector vocabulary of the cluster provider class "east-only"`

	testCases := []struct {
		name         string
		class        *kfplacementv1alpha1.ClusterProviderClass
		noClass      string
		outcomes     []selectorOutcome
		wantWanted   []string
		wantIssuable []string
		wantNote     string
	}{
		{
			name:         "no class: wanted but not issuable, and the note says why",
			noClass:      noClass,
			outcomes:     []selectorOutcome{unfulfilled(regionTerms("eastus"))},
			wantWanted:   []string{claimName(policy, 0)},
			wantIssuable: []string{},
			wantNote:     noClass,
		},
		{
			name:         "no class and nothing wanted: no note",
			noClass:      noClass,
			outcomes:     []selectorOutcome{satisfied(regionTerms("eastus"))},
			wantWanted:   []string{},
			wantIssuable: []string{},
		},
		{
			name:         "claimable selector",
			class:        eastOnly,
			outcomes:     []selectorOutcome{unfulfilled(regionTerms("eastus"))},
			wantWanted:   []string{claimName(policy, 0)},
			wantIssuable: []string{claimName(policy, 0)},
		},
		{
			name:         "unclaimable selector is wanted but not issuable, and reported",
			class:        eastOnly,
			outcomes:     []selectorOutcome{unfulfilled(regionTerms("antarctica"))},
			wantWanted:   []string{claimName(policy, 0)},
			wantIssuable: []string{},
			wantNote:     antarcticaBlocked,
		},
		{
			name:         "the claimable selector after an unclaimable one is issuable",
			class:        eastOnly,
			outcomes:     []selectorOutcome{unfulfilled(regionTerms("antarctica")), unfulfilled(regionTerms("eastus"))},
			wantWanted:   []string{claimName(policy, 0), claimName(policy, 1)},
			wantIssuable: []string{claimName(policy, 1)},
			wantNote:     antarcticaBlocked,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, note := desiredClaims(policy, tc.outcomes, tc.class, tc.noClass)
			wanted, issuable := []string{}, []string{}
			for _, w := range got {
				wanted = append(wanted, w.name)
				if w.blocked == "" {
					issuable = append(issuable, w.name)
				}
				if w.class != tc.class {
					t.Errorf("desiredClaims() stamped class %v, want %v", w.class, tc.class)
				}
			}
			if diff := cmp.Diff(wanted, tc.wantWanted); diff != "" {
				t.Errorf("desiredClaims() wanted names mismatch (-got, +want):\n%s", diff)
			}
			if diff := cmp.Diff(issuable, tc.wantIssuable); diff != "" {
				t.Errorf("desiredClaims() issuable names mismatch (-got, +want):\n%s", diff)
			}
			if note != tc.wantNote {
				t.Errorf("desiredClaims() note = %q, want %q", note, tc.wantNote)
			}
		})
	}
}

func TestResolveClass(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := kfplacementv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() = %v, want no error", err)
	}
	defaultClass := func(name string) *kfplacementv1alpha1.ClusterProviderClass {
		class := classWithVocabulary(name, nil)
		class.Annotations = map[string]string{kfplacementv1alpha1.IsDefaultClusterProviderClassAnnotation: "true"}
		return class
	}
	policyNaming := func(class string) kfplacementv1alpha1.PlacementPolicyAccessor {
		policy := policyFor("app", "tenant-a")
		policy.GetSpec().ClusterProviderClassName = ptr.To(class)
		return policy
	}

	testCases := []struct {
		name      string
		classes   []*kfplacementv1alpha1.ClusterProviderClass
		policy    kfplacementv1alpha1.PlacementPolicyAccessor
		wantClass string
		wantWhy   string
	}{
		{name: "explicit class", classes: []*kfplacementv1alpha1.ClusterProviderClass{classWithVocabulary("standard", nil), defaultClass("other")}, policy: policyNaming("standard"), wantClass: "standard"},
		{name: "explicit class that does not exist", classes: []*kfplacementv1alpha1.ClusterProviderClass{defaultClass("other")}, policy: policyNaming("missing"), wantWhy: `no new cluster claims are issued: the cluster provider class "missing" does not exist`},
		{name: "single default", classes: []*kfplacementv1alpha1.ClusterProviderClass{classWithVocabulary("standard", nil), defaultClass("fleet-default")}, policy: policyFor("app", "tenant-a"), wantClass: "fleet-default"},
		{name: "no default", classes: []*kfplacementv1alpha1.ClusterProviderClass{classWithVocabulary("standard", nil)}, policy: policyFor("app", "tenant-a"), wantWhy: "no new cluster claims are issued: the policy names no cluster provider class and the fleet has no default class"},
		{name: "two defaults", classes: []*kfplacementv1alpha1.ClusterProviderClass{defaultClass("b"), defaultClass("a")}, policy: policyFor("app", "tenant-a"), wantWhy: "no new cluster claims are issued: the policy names no cluster provider class and more than one class is marked as the default ([a b])"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme)
			for _, class := range tc.classes {
				builder = builder.WithObjects(class)
			}
			r := NewReconciler(builder.Build(), nil, nil)
			class, why, err := r.resolveClass(context.Background(), tc.policy)
			if err != nil {
				t.Fatalf("resolveClass() = %v, want no error", err)
			}
			gotClass := ""
			if class != nil {
				gotClass = class.Name
			}
			if diff := cmp.Diff([]string{gotClass, why}, []string{tc.wantClass, tc.wantWhy}, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("resolveClass() (class, why) mismatch (-got, +want):\n%s", diff)
			}
		})
	}
}
