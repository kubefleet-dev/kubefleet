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

package admissionpolicymanager

import (
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestClusterClaimApprovalGeneratorPoliciesWithBindings(t *testing.T) {
	approvedOf := func(obj string) string {
		return `(has(` + obj + `.status) && has(` + obj + `.status.conditions) ? ` + obj + `.status.conditions : []).filter(c, c.type == "Approved")`
	}
	wantExpression := `(` + approvedOf("object") + ` == ` + approvedOf("oldObject") + `) || (authorizer.group("placement.kubefleet.dev").resource("clusterclaims").name(object.metadata.name).check("approve").allowed())`

	gen := &ClusterClaimApprovalValidatingAdmissionPolicyGenerator{}
	if err := gen.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	got, err := gen.PoliciesWithBindings()
	if err != nil {
		t.Fatalf("PoliciesWithBindings() = %v, want no error", err)
	}
	if len(got) != 1 {
		t.Fatalf("PoliciesWithBindings() returned %d policies, want 1", len(got))
	}
	policy := got[0].Policy
	if diff := cmp.Diff(policy.Spec.Validations[0].Expression, wantExpression); diff != "" {
		t.Errorf("PoliciesWithBindings() expression mismatch (-got, +want):\n%s", diff)
	}
	if rules := policy.Spec.MatchConstraints.ResourceRules; len(rules) != 1 || rules[0].Resources[0] != "clusterclaims/status" {
		t.Errorf("PoliciesWithBindings() resource rules = %v, want one rule on clusterclaims/status", rules)
	}
	if bindings := got[0].Bindings; len(bindings) != 1 || bindings[0].Spec.PolicyName != policy.Name {
		t.Errorf("PoliciesWithBindings() bindings = %v, want one binding to %s", bindings, policy.Name)
	}
}

// TestDefaultConfigsLeaveClaimApprovalOff pins that the default configuration deploys no claim
// approval policy: the hub agent's ClusterRole must carry the approve verb before it is enabled.
func TestDefaultConfigsLeaveClaimApprovalOff(t *testing.T) {
	if DefaultPolicyGeneratorConfigs.ClusterClaimApprovalVAPGeneratorConfig != nil {
		t.Errorf("DefaultPolicyGeneratorConfigs enables the claim approval generator, want it off")
	}
	if DefaultPolicyGeneratorConfigs.EnabledGenerators().Has(ClusterClaimApprovalVAPGeneratorName) {
		t.Errorf("EnabledGenerators() includes %s by default, want it off", ClusterClaimApprovalVAPGeneratorName)
	}
	if !AllGenerators().Has(ClusterClaimApprovalVAPGeneratorName) {
		t.Errorf("AllGenerators() = %v, want it to include %s", AllGenerators().UnsortedList(), ClusterClaimApprovalVAPGeneratorName)
	}
}

// TestAllGeneratorsListsEveryConfigField pins the hand-maintained generator list against the
// configuration struct, so that a generator added to one is not forgotten in the other.
func TestAllGeneratorsListsEveryConfigField(t *testing.T) {
	all := AllGenerators()
	configType := reflect.TypeOf(PolicyGeneratorConfigs{})
	if configType.NumField() != all.Len() {
		t.Errorf("PolicyGeneratorConfigs has %d fields, AllGenerators() lists %d names", configType.NumField(), all.Len())
	}
	for i := 0; i < configType.NumField(); i++ {
		gen, ok := reflect.New(configType.Field(i).Type.Elem()).Interface().(ValidatingAdmissionPolicyGenerator)
		if !ok {
			t.Errorf("field %s is not a ValidatingAdmissionPolicyGenerator", configType.Field(i).Name)
			continue
		}
		if !all.Has(gen.Name()) {
			t.Errorf("AllGenerators() = %v, want it to include %s", all.UnsortedList(), gen.Name())
		}
	}
}
