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

// Package validator provides utils to validate ClusterResourcePlacementEviction resources.
package validator

import (
	"fmt"

	fleetv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// ValidateClusterResourcePlacementForEviction validates cluster resource placement fields for eviction and returns error.
func ValidateClusterResourcePlacementForEviction(crp fleetv1beta1.ClusterResourcePlacement, db fleetv1beta1.ClusterResourcePlacementDisruptionBudget) error {
	allErr := make([]error, 0)

	// Check Cluster Resource Placement is not deleting
	if crp.DeletionTimestamp != nil {
		allErr = append(allErr, fmt.Errorf("cluster resource placement %s is being deleted", crp.Name))
	}
	// Check Cluster Resource Placement Policy
	if crp.Spec.Policy != nil {
		if crp.Spec.Policy.PlacementType == fleetv1beta1.PickFixedPlacementType {
			allErr = append(allErr, fmt.Errorf("cluster resource placement policy type %s is not supported", crp.Spec.Policy.PlacementType))
		}

		// handle special case for PickAll CRP.
		if crp.Spec.Policy.PlacementType == fleetv1beta1.PickAllPlacementType {
			if err := validateClusterResourcePlacementDisruptionBudgetForPickAll(db); err != nil {
				allErr = append(allErr, fmt.Errorf("cluster resource placement policy type %s is not supported with disruption budget", crp.Spec.Policy.PlacementType))
			}
		}
	}

	return errors.NewAggregate(allErr)
}

func validateClusterResourcePlacementDisruptionBudgetForPickAll(db fleetv1beta1.ClusterResourcePlacementDisruptionBudget) error {
	allErr := make([]error, 0)

	if db.Spec.MaxUnavailable != nil {
		allErr = append(allErr, fmt.Errorf("cluster resource placement policy type PickAll is not supported with any specified max unavailable %v", db.Spec.MaxUnavailable))
	}

	if db.Spec.MinAvailable != nil && db.Spec.MinAvailable.Type == intstr.String {
		allErr = append(allErr, fmt.Errorf("cluster resource placement policy type PickAll is not supported with min available as a percentage %v", db.Spec.MinAvailable.StrVal))
	}

	return errors.NewAggregate(allErr)
}
