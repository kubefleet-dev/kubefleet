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

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestValidateClusterResourcePlacementForEviction(t *testing.T) {
	tests := map[string]struct {
		crp     *placementv1beta1.ClusterResourcePlacement
		wantErr error
	}{
		"valid CRP": {
			crp: &placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-crp",
				},
			},
			wantErr: nil,
		},
		"valid CRP with PickAll policy": {
			crp: &placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-crp",
				},
				Spec: placementv1beta1.ClusterResourcePlacementSpec{
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType: placementv1beta1.PickAllPlacementType,
					},
				},
			},
			wantErr: nil,
		},
		"valid CRP with PickN policy": {
			crp: &placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-crp",
				},
				Spec: placementv1beta1.ClusterResourcePlacementSpec{
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType:    placementv1beta1.PickNPlacementType,
						NumberOfClusters: ptr.To(int32(1)),
					},
				},
			},
			wantErr: nil,
		},
		"invalid CRP with PickFixed policy": {
			crp: &placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-crp",
				},
				Spec: placementv1beta1.ClusterResourcePlacementSpec{
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType: placementv1beta1.PickFixedPlacementType,
						ClusterNames: []string{
							"cluster1",
						},
					},
				},
			},
			wantErr: fmt.Errorf("cluster resource placement policy type PickFixed is not supported"),
		},
		"CRP with deletion timestamp": {
			crp: &placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-crp",
					DeletionTimestamp: &metav1.Time{},
				},
			},
			wantErr: fmt.Errorf("cluster resource placement test-crp is being deleted"),
		},
	}
	for testName, testCase := range tests {
		t.Run(testName, func(t *testing.T) {
			gotErr := ValidateClusterResourcePlacementForEviction(*testCase.crp)
			if testCase.wantErr != nil && !strings.Contains(gotErr.Error(), testCase.wantErr.Error()) {
				t.Errorf("ValidateClusterResourcePlacementForEviction() got %v, want %v", gotErr.Error(), testCase.wantErr.Error())
			}
			if testCase.wantErr == nil && gotErr != nil {
				t.Errorf("ValidateClusterResourcePlacementForEviction() got %v, want nil", gotErr)
			}
		})
	}
}
