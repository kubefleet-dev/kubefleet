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
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	fleetv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

func TestValidateClusterResourcePlacementDisruptionBudget(t *testing.T) {
	tests := map[string]struct {
		db      *fleetv1beta1.ClusterResourcePlacementDisruptionBudget
		crp     *fleetv1beta1.ClusterResourcePlacement
		wantErr error
	}{
		"valid crpdb with PickAll placement type crp": {
			db: &fleetv1beta1.ClusterResourcePlacementDisruptionBudget{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-crp",
				},
				Spec: fleetv1beta1.PlacementDisruptionBudgetSpec{
					MaxUnavailable: nil,
					MinAvailable:   nil,
				},
			},
			crp: &fleetv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-crp",
				},
				Spec: fleetv1beta1.ClusterResourcePlacementSpec{
					Policy: &fleetv1beta1.PlacementPolicy{
						PlacementType: fleetv1beta1.PickAllPlacementType,
					},
				},
			},
			wantErr: nil,
		},
		"invalid crpdb with PickAll placement type crp (MinAvailable as Percentage)": {
			db: &fleetv1beta1.ClusterResourcePlacementDisruptionBudget{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-crp",
				},
				Spec: fleetv1beta1.PlacementDisruptionBudgetSpec{
					MaxUnavailable: nil,
					MinAvailable: &intstr.IntOrString{
						Type:   intstr.String,
						StrVal: "50%",
					},
				},
			},
			crp: &fleetv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-crp",
				},
				Spec: fleetv1beta1.ClusterResourcePlacementSpec{
					Policy: &fleetv1beta1.PlacementPolicy{
						PlacementType: fleetv1beta1.PickAllPlacementType,
					},
				},
			},
			wantErr: fmt.Errorf("cluster resource placement policy type PickAll is not supported with min available as a percentage %v", &intstr.IntOrString{
				Type:   intstr.String,
				StrVal: "50%",
			}),
		},
		"invalid crpdb with PickAll placement type crp (MaxUnavailable as Percentage)": {
			db: &fleetv1beta1.ClusterResourcePlacementDisruptionBudget{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-crp",
				},
				Spec: fleetv1beta1.PlacementDisruptionBudgetSpec{
					MaxUnavailable: &intstr.IntOrString{
						Type:   intstr.String,
						StrVal: "75%",
					},
					MinAvailable: nil,
				},
			},
			crp: &fleetv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-crp",
				},
				Spec: fleetv1beta1.ClusterResourcePlacementSpec{
					Policy: &fleetv1beta1.PlacementPolicy{
						PlacementType: fleetv1beta1.PickAllPlacementType,
					},
				},
			},
			wantErr: fmt.Errorf("cluster resource placement policy type PickAll is not supported with any specified max unavailable %v", &intstr.IntOrString{
				Type:   intstr.String,
				StrVal: "75%",
			}),
		},
		"invalid crpdb with PickAll placement type crp (MaxUnavailable as Integer)": {
			db: &fleetv1beta1.ClusterResourcePlacementDisruptionBudget{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-crp",
				},
				Spec: fleetv1beta1.PlacementDisruptionBudgetSpec{
					MaxUnavailable: &intstr.IntOrString{
						Type:   intstr.Int,
						IntVal: 1,
					},
					MinAvailable: nil,
				},
			},
			crp: &fleetv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-crp",
				},
				Spec: fleetv1beta1.ClusterResourcePlacementSpec{
					Policy: &fleetv1beta1.PlacementPolicy{
						PlacementType: fleetv1beta1.PickAllPlacementType,
					},
				},
			},
			wantErr: fmt.Errorf("cluster resource placement policy type PickAll is not supported with any specified max unavailable %v", &intstr.IntOrString{
				Type:   intstr.Int,
				IntVal: 1,
			}),
		},
		"valid crpdb with PickN placement type crp": {
			db: &fleetv1beta1.ClusterResourcePlacementDisruptionBudget{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-crp",
				},
				Spec: fleetv1beta1.PlacementDisruptionBudgetSpec{
					MaxUnavailable: &intstr.IntOrString{
						Type:   intstr.Int,
						IntVal: 1,
					},
					MinAvailable: &intstr.IntOrString{
						Type:   intstr.String,
						StrVal: "50%",
					},
				},
			},
			crp: &fleetv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-crp",
				},
				Spec: fleetv1beta1.ClusterResourcePlacementSpec{
					Policy: &fleetv1beta1.PlacementPolicy{
						PlacementType:    fleetv1beta1.PickNPlacementType,
						NumberOfClusters: ptr.To(int32(1)),
					},
				},
			},
			wantErr: nil,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			gotErr := ValidateClusterResourcePlacementDisruptionBudget(tt.db, tt.crp)
			if tt.wantErr != nil && !strings.Contains(gotErr.Error(), tt.wantErr.Error()) {
				t.Errorf("ValidateClusterResourcePlacementDisruptionBudget() got %v, want %v", gotErr.Error(), tt.wantErr.Error())
			}
			if tt.wantErr == nil && gotErr != nil {
				t.Errorf("ValidateClusterResourcePlacementDisruptionBudget() got %v, want nil", gotErr)
			}
		})
	}
}
