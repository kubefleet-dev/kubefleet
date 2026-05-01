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

// Package validator provides utils to validate ClusterResourceOverride resources.
package validator

import (
	"fmt"

	"k8s.io/apimachinery/pkg/util/errors"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

// ValidateClusterResourceOverride validates cluster resource override fields and returns error.
// Note: Most field-level validations (placement scope, selector constraints, clusterSelector
// constraints, overrideType/jsonPatchOverrides consistency, and JSON patch path restrictions)
// are now enforced by CEL rules on the CRD.
// This validator handles cross-object validations that CEL cannot perform, plus the
// remove-op value check which CEL cannot enforce because the Value field uses
// apiextensionsv1.JSON (x-kubernetes-preserve-unknown-fields, opaque to CEL).
func ValidateClusterResourceOverride(cro placementv1beta1.ClusterResourceOverride, croList *placementv1beta1.ClusterResourceOverrideList) error {
	allErr := make([]error, 0)

	// Check if the override count limit for the resources has been reached
	if err := validateClusterResourceOverrideResourceLimit(cro, croList); err != nil {
		allErr = append(allErr, err)
	}

	if cro.Spec.Policy != nil {
		allErr = append(allErr, validateOverridePolicy(cro.Spec.Policy)...)
	}

	return errors.NewAggregate(allErr)
}

// validateClusterResourceOverrideResourceLimit checks if there is only 1 cluster resource override per resource,
// assuming the resource will be selected by the name only.
func validateClusterResourceOverrideResourceLimit(cro placementv1beta1.ClusterResourceOverride, croList *placementv1beta1.ClusterResourceOverrideList) error {
	// Check if croList is nil or empty, no need to check for resource limit
	if croList == nil || len(croList.Items) == 0 {
		return nil
	}
	overrideMap := make(map[placementv1beta1.ResourceSelectorTerm]string)
	// Add overrides and its selectors to the map
	for _, override := range croList.Items {
		selectors := override.Spec.ClusterResourceSelectors
		for _, selector := range selectors {
			overrideMap[selector] = override.GetName()
		}
	}

	allErr := make([]error, 0)
	// Check if any of the cro selectors exist in the override map
	for _, croSelector := range cro.Spec.ClusterResourceSelectors {
		if overrideMap[croSelector] != "" {
			// Ignore the same cluster resource override
			if cro.GetName() == overrideMap[croSelector] {
				continue
			}
			allErr = append(allErr, fmt.Errorf("invalid resource selector %+v: the resource has been selected by both %v and %v, which is not supported", croSelector, cro.GetName(), overrideMap[croSelector]))
		}
	}
	return errors.NewAggregate(allErr)
}
