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
	"fmt"
	"maps"
	"slices"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
)

// resolveClass returns the ClusterProviderClass the policy's cluster claims go to: the class the
// policy names, else the fleet's single default class. When there is none, the second value says
// why, in the words the Scheduled condition reports; a policy without a class issues no claims
// but is otherwise scheduled as usual (the FEP's "no cluster requests if proper support is
// unavailable").
func (r *Reconciler) resolveClass(ctx context.Context, policy kfplacementv1alpha1.PlacementPolicyAccessor) (*kfplacementv1alpha1.ClusterProviderClass, string, error) {
	if name := policy.GetSpec().ClusterProviderClassName; name != nil {
		class := &kfplacementv1alpha1.ClusterProviderClass{}
		if err := r.Get(ctx, client.ObjectKey{Name: *name}, class); err != nil {
			if errors.IsNotFound(err) {
				return nil, fmt.Sprintf("no new cluster claims are issued: the cluster provider class %q does not exist", *name), nil
			}
			return nil, "", err
		}
		return class, "", nil
	}

	classes := &kfplacementv1alpha1.ClusterProviderClassList{}
	if err := r.List(ctx, classes); err != nil {
		return nil, "", err
	}
	var defaults []*kfplacementv1alpha1.ClusterProviderClass
	for i := range classes.Items {
		if classes.Items[i].Annotations[kfplacementv1alpha1.IsDefaultClusterProviderClassAnnotation] == "true" {
			defaults = append(defaults, &classes.Items[i])
		}
	}
	switch len(defaults) {
	case 1:
		return defaults[0], "", nil
	case 0:
		return nil, "no new cluster claims are issued: the policy names no cluster provider class and the fleet has no default class", nil
	default:
		names := make([]string, 0, len(defaults))
		for _, class := range defaults {
			names = append(names, class.Name)
		}
		slices.Sort(names)
		return nil, fmt.Sprintf("no new cluster claims are issued: the policy names no cluster provider class and more than one class is marked as the default (%v)", names), nil
	}
}

// vocabularyViolation reports the first selector term a provider could not act on, as a message
// naming the term, or "" when every term is within the class's vocabulary. The terms are judged in
// a fixed order so that the message is stable from one reconcile to the next.
//
// A provider turns a selector into a cluster: a label it is told to set, a property it is told to
// deliver. Only terms that say what to provision are claimable -- label matchers, label
// expressions with In, and bounded property comparisons -- and only on keys and values the class
// admits; an absence, exclusion, or set-membership test says what not to provision, which no
// blueprint can act on. A class with no vocabulary at all admits only a selector with no terms.
func vocabularyViolation(class *kfplacementv1alpha1.ClusterProviderClass, terms []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm) string {
	if len(terms) == 0 {
		return ""
	}
	vocabulary := class.Spec.SelectorVocabulary
	if vocabulary == nil {
		return fmt.Sprintf("the cluster provider class %q declares no selector vocabulary, so only a selector with no terms can be claimed", class.Name)
	}
	labelRules := make(map[string]kfplacementv1alpha1.LabelKeyRule, len(vocabulary.LabelKeys))
	for _, rule := range vocabulary.LabelKeys {
		labelRules[rule.Key] = rule
	}
	propertyRules := make(map[string]kfplacementv1alpha1.PropertyKeyRule, len(vocabulary.PropertyKeys))
	for _, rule := range vocabulary.PropertyKeys {
		propertyRules[rule.Key] = rule
	}

	for i := range terms {
		term := &terms[i]
		for _, key := range slices.Sorted(maps.Keys(term.MatchLabels)) {
			if msg := labelViolation(class.Name, labelRules, key, []string{term.MatchLabels[key]}); msg != "" {
				return msg
			}
		}
		for j := range term.MatchLabelExpressions {
			expr := &term.MatchLabelExpressions[j]
			if expr.Operator != kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorIn {
				return fmt.Sprintf("the label expression on key %q uses operator %s, which does not say what to provision; only In is claimable", expr.Key, expr.Operator)
			}
			if msg := labelViolation(class.Name, labelRules, expr.Key, expr.Values); msg != "" {
				return msg
			}
		}
		for j := range term.MatchClusterPropertyExpressions {
			if msg := propertyViolation(class.Name, propertyRules, &term.MatchClusterPropertyExpressions[j]); msg != "" {
				return msg
			}
		}
	}
	return ""
}

func labelViolation(className string, rules map[string]kfplacementv1alpha1.LabelKeyRule, key string, values []string) string {
	rule, allowed := rules[key]
	if !allowed {
		return fmt.Sprintf("the label key %q is not in the selector vocabulary of the cluster provider class %q", key, className)
	}
	if len(rule.Values) == 0 {
		return ""
	}
	for _, value := range values {
		if !slices.Contains(rule.Values, value) {
			return fmt.Sprintf("the value %q for label key %q is not in the selector vocabulary of the cluster provider class %q", value, key, className)
		}
	}
	return ""
}

func propertyViolation(className string, rules map[string]kfplacementv1alpha1.PropertyKeyRule, expr *kfplacementv1alpha1.LabelClusterPropertyExpression) string {
	rule, allowed := rules[expr.Key]
	if !allowed {
		return fmt.Sprintf("the property key %q is not in the selector vocabulary of the cluster provider class %q", expr.Key, className)
	}
	switch expr.Operator {
	case kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorGt,
		kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorGe,
		kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorLt,
		kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorLe,
		kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorEq:
	default:
		return fmt.Sprintf("the property expression on key %q uses operator %s, which does not say what to provision; only Gt, Ge, Lt, Le, and Eq are claimable", expr.Key, expr.Operator)
	}
	// validateTerms vets the arity and the quantity syntax before any claim is wanted; the parse
	// is repeated rather than trusted so that a reordering upstream can never turn into a panic.
	if len(expr.Values) != 1 {
		return fmt.Sprintf("the property expression on key %q has %d values, want exactly one", expr.Key, len(expr.Values))
	}
	value, err := resource.ParseQuantity(expr.Values[0])
	if err != nil {
		return fmt.Sprintf("the value %q for property key %q is not a valid quantity", expr.Values[0], expr.Key)
	}
	bounds := [2]*resource.Quantity{}
	for i, limit := range []*string{rule.Min, rule.Max} {
		if limit == nil {
			continue
		}
		parsed, err := resource.ParseQuantity(*limit)
		if err != nil {
			return fmt.Sprintf("the %s bound %q for property key %q in the cluster provider class %q is not a valid quantity", [2]string{"min", "max"}[i], *limit, expr.Key, className)
		}
		bounds[i] = &parsed
	}
	lower, upper := bounds[0], bounds[1]
	// A comparison is claimable when some quantity within the class's bounds satisfies it: the
	// value is a threshold, not the cluster the provider delivers. Eq names a point, which must lie
	// within the bounds; Gt/Ge need room above the threshold below the max; Lt/Le need room below
	// it above the min.
	var unmet bool
	switch expr.Operator {
	case kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorEq:
		unmet = (lower != nil && value.Cmp(*lower) < 0) || (upper != nil && value.Cmp(*upper) > 0)
	case kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorGt:
		unmet = upper != nil && value.Cmp(*upper) >= 0
	case kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorGe:
		unmet = upper != nil && value.Cmp(*upper) > 0
	case kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorLt:
		unmet = lower != nil && value.Cmp(*lower) <= 0
	case kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorLe:
		unmet = lower != nil && value.Cmp(*lower) < 0
	}
	if unmet {
		return fmt.Sprintf("the comparison %s %s on property key %q cannot be met within the bounds [%s, %s] of the cluster provider class %q", expr.Operator, expr.Values[0], expr.Key, boundOrOpen(rule.Min), boundOrOpen(rule.Max), className)
	}
	return ""
}

func boundOrOpen(bound *string) string {
	if bound == nil {
		return "-"
	}
	return *bound
}
