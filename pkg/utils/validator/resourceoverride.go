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

// Package validator provides utils to validate ResourceOverride resources.
package validator

import (
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/util/errors"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

// ValidateResourceOverride validates resource override fields and returns error.
// Note: Most field-level validations (selector uniqueness, clusterSelector constraints,
// overrideType/jsonPatchOverrides consistency, and JSON patch path restrictions)
// are now enforced by CEL rules on the CRD.
// This validator handles cross-object validations that CEL cannot perform, plus the
// remove-op value check which CEL cannot enforce because the Value field uses
// apiextensionsv1.JSON (x-kubernetes-preserve-unknown-fields, opaque to CEL).
func ValidateResourceOverride(ro placementv1beta1.ResourceOverride, roList *placementv1beta1.ResourceOverrideList) error {
	allErr := make([]error, 0)

	// Check if the override count limit for the resources has been reached.
	if err := validateResourceOverrideResourceLimit(ro, roList); err != nil {
		allErr = append(allErr, err)
	}

	if ro.Spec.Policy != nil {
		allErr = append(allErr, validateOverridePolicy(ro.Spec.Policy)...)
	}

	return apierrors.NewAggregate(allErr)
}

// validateResourceOverrideResourceLimit checks if there is only 1 resource override per resource,
// assuming the resource will be selected by the name only.
func validateResourceOverrideResourceLimit(ro placementv1beta1.ResourceOverride, roList *placementv1beta1.ResourceOverrideList) error {
	// Check if roList is nil or empty, no need to check for resource limit.
	if roList == nil || len(roList.Items) == 0 {
		return nil
	}
	overrideMap := make(map[placementv1beta1.ResourceSelector]string)
	// Add overrides and its selectors to the map.
	for _, override := range roList.Items {
		selectors := override.Spec.ResourceSelectors
		for _, selector := range selectors {
			overrideMap[selector] = override.GetName()
		}
	}

	allErr := make([]error, 0)
	// Check if any of the ro selectors exist in the override map.
	for _, roSelector := range ro.Spec.ResourceSelectors {
		if overrideMap[roSelector] != "" {
			// Ignore the same resource override.
			if ro.GetName() == overrideMap[roSelector] {
				continue
			}
			allErr = append(allErr, fmt.Errorf("invalid resource selector %+v: the resource has been selected by both %v and %v, which is not supported", roSelector, ro.GetName(), overrideMap[roSelector]))
		}
	}
	return apierrors.NewAggregate(allErr)
}
