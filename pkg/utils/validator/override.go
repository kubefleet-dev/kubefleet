/*
Copyright 2025 The KubeFleet Authors.

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

package validator

import (
	"fmt"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

// validateOverridePolicy validates the override policy fields that CEL cannot enforce.
// Specifically, this checks that remove operations do not have a value set,
// because the Value field uses apiextensionsv1.JSON which maps to
// x-kubernetes-preserve-unknown-fields in the CRD schema and is opaque to CEL.
func validateOverridePolicy(policy *placementv1beta1.OverridePolicy) []error {
	var errs []error
	for i, rule := range policy.OverrideRules {
		for j, patch := range rule.JSONPatchOverrides {
			if patch.Operator == placementv1beta1.JSONPatchOverrideOpRemove && len(patch.Value.Raw) != 0 {
				errs = append(errs, fmt.Errorf("overrideRules[%d].jsonPatchOverrides[%d]: remove operation cannot have a value", i, j))
			}
		}
	}
	return errs
}
