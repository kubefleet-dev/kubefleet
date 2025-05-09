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
	"github.com/kubefleet-dev/kubefleet/pkg/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestValidateClusterResourcePlacementEviction(t *testing.T) {
	tests := map[string]struct {
		crpe    *placementv1beta1.ClusterResourcePlacementEviction
		wantErr error
	}{
		"valid CRPE": {
			crpe: &placementv1beta1.ClusterResourcePlacementEviction{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-crpe",
				},
				Spec: placementv1beta1.PlacementEvictionSpec{
					PlacementName: "test-crp",
					ClusterName:   "test-cluster",
				},
			},
			wantErr: nil,
		},
		"CRPE with no placement name ": {
			crpe: &placementv1beta1.ClusterResourcePlacementEviction{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-crpe",
				},
				Spec: placementv1beta1.PlacementEvictionSpec{
					ClusterName: "test-cluster",
				},
			},
			wantErr: fmt.Errorf("cluster resource placement name is required"),
		},
		"CRPE with no cluster name ": {
			crpe: &placementv1beta1.ClusterResourcePlacementEviction{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-crpe",
				},
				Spec: placementv1beta1.PlacementEvictionSpec{
					PlacementName: "test-crp",
				},
			},
			wantErr: fmt.Errorf("cluster name is required"),
		},
		"CRPE with placement name too long": {
			crpe: &placementv1beta1.ClusterResourcePlacementEviction{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-crpe",
				},
				Spec: placementv1beta1.PlacementEvictionSpec{
					PlacementName: strings.Repeat("a", 256),
					ClusterName:   "test-cluster",
				},
			},
			wantErr: fmt.Errorf("cluster resource placement name %s is too long", strings.Repeat("a", 256)),
		},
		"CRPE with cluster name too long": {
			crpe: &placementv1beta1.ClusterResourcePlacementEviction{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-crpe",
				},
				Spec: placementv1beta1.PlacementEvictionSpec{
					PlacementName: "test-crp",
					ClusterName:   strings.Repeat("a", 256),
				},
			},
			wantErr: fmt.Errorf("cluster name %s is too long", strings.Repeat("a", 256)),
		},
	}
	for testName, testCase := range tests {
		t.Run(testName, func(t *testing.T) {
			RestMapper = utils.TestMapper{}
			gotErr := ValidateClusterResourcePlacementEviction(*testCase.crpe)
			if testCase.wantErr != nil && !strings.Contains(gotErr.Error(), testCase.wantErr.Error()) {
				t.Errorf("ValidateClusterResourcePlacementEviction() got %v, want %v", gotErr.Error(), testCase.wantErr.Error())
			}
			if testCase.wantErr == nil && gotErr != nil {
				t.Errorf("ValidateClusterResourcePlacementEviction() got %v, want nil", gotErr)
			}
		})
	}
}

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

func TestValidateClusterResourceBindingForEviction(t *testing.T) {
	tests := map[string]struct {
		crbList *placementv1beta1.ClusterResourceBindingList
		crpe    *placementv1beta1.ClusterResourcePlacementEviction
		wantErr error
	}{
		"valid CRB": {
			crbList: &placementv1beta1.ClusterResourceBindingList{
				Items: []placementv1beta1.ClusterResourceBinding{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-crb",
						},
						Spec: placementv1beta1.ResourceBindingSpec{
							State:         placementv1beta1.BindingStateScheduled,
							TargetCluster: "test-cluster",
						},
					},
				},
			},
			crpe: &placementv1beta1.ClusterResourcePlacementEviction{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-crpe",
				},
				Spec: placementv1beta1.PlacementEvictionSpec{
					PlacementName: "test-crp",
					ClusterName:   "test-cluster",
				},
			},
			wantErr: nil,
		},
		"multiple CRB for the same target cluster": {
			crbList: &placementv1beta1.ClusterResourceBindingList{
				Items: []placementv1beta1.ClusterResourceBinding{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-crb-1",
						},
						Spec: placementv1beta1.ResourceBindingSpec{
							State:         placementv1beta1.BindingStateScheduled,
							TargetCluster: "test-cluster",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-crb-2",
						},
						Spec: placementv1beta1.ResourceBindingSpec{
							State:         placementv1beta1.BindingStateScheduled,
							TargetCluster: "test-cluster",
						},
					},
				},
			},
			crpe: &placementv1beta1.ClusterResourcePlacementEviction{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-crpe",
				},
				Spec: placementv1beta1.PlacementEvictionSpec{
					PlacementName: "test-crp",
					ClusterName:   "test-cluster",
				},
			},
			wantErr: fmt.Errorf("multiple ClusterResourceBindings found for the same target cluster test-cluster"),
		},
	}
	for testName, testCase := range tests {
		t.Run(testName, func(t *testing.T) {
			gotErr := ValidateClusterResourceBindingForEviction(*testCase.crbList, *testCase.crpe)
			if testCase.wantErr != nil && !strings.Contains(gotErr.Error(), testCase.wantErr.Error()) {
				t.Errorf("ValidateClusterResourceBindingForEviction() got %v, want %v", gotErr.Error(), testCase.wantErr.Error())
			}
			if testCase.wantErr == nil && gotErr != nil {
				t.Errorf("ValidateClusterResourceBindingForEviction() got %v, want nil", gotErr)
			}
		})
	}
}
