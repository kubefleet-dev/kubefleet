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

package main

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	toolsutils "github.com/kubefleet-dev/kubefleet/tools/utils"
)

func TestUncordon(t *testing.T) {
	taint1 := clusterv1beta1.Taint{
		Key:    "test-key1",
		Value:  "test-value1",
		Effect: corev1.TaintEffectNoSchedule,
	}
	taint2 := clusterv1beta1.Taint{
		Key:    "test-key1",
		Value:  "test-value1",
		Effect: corev1.TaintEffectNoSchedule,
	}

	// Define test cases
	testCases := []struct {
		name          string
		initialTaints []clusterv1beta1.Taint
		wantTaints    []clusterv1beta1.Taint
		wantErr       error
	}{
		{
			name:          "no taints present",
			initialTaints: []clusterv1beta1.Taint{},
			wantTaints:    []clusterv1beta1.Taint{},
			wantErr:       nil,
		},
		{
			name:          "cordon taint present",
			initialTaints: []clusterv1beta1.Taint{taint1, toolsutils.CordonTaint, taint2},
			wantTaints:    []clusterv1beta1.Taint{taint1, taint2},
			wantErr:       nil,
		},
		{
			name:          "cordon taint not present",
			initialTaints: []clusterv1beta1.Taint{taint1, taint2},
			wantTaints:    []clusterv1beta1.Taint{taint1, taint2},
			wantErr:       nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			s := serviceScheme(t)
			// Create a fake client with initial objects
			mc := &clusterv1beta1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cluster",
				},
				Spec: clusterv1beta1.MemberClusterSpec{
					Taints: tc.initialTaints,
				},
			}
			fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(mc).Build()

			// Initialize UncordonCluster
			uncordonCluster := helper{
				hubClient:   fakeClient,
				clusterName: "test-cluster",
			}

			// Call the Uncordon function
			gotErr := uncordonCluster.Uncordon(context.Background())
			if tc.wantErr == nil {
				if gotErr != nil {
					t.Errorf("Uncordon test %s failed, got error %v, want error %v", tc.name, gotErr, tc.wantErr)
				}
				// Verify the taints
				gotMC := &clusterv1beta1.MemberCluster{}
				if err := fakeClient.Get(context.Background(), client.ObjectKey{Name: "test-cluster"}, gotMC); err != nil {
					t.Errorf("failed to get member cluster: %v", err)
				}
				if diff := cmp.Diff(tc.wantTaints, gotMC.Spec.Taints, cmpopts.EquateEmpty()); diff != "" {
					t.Errorf("Uncordon test %s failed, got taints %v, want taints %v", tc.name, gotMC.Spec.Taints, tc.wantTaints)
				}
			} else if gotErr == nil || gotErr.Error() != tc.wantErr.Error() {
				t.Errorf("Uncordon test %s failed, got error %v, want error %v", tc.name, gotErr, tc.wantErr)
			}
		})
	}
}

func serviceScheme(t *testing.T) *runtime.Scheme {
	scheme := runtime.NewScheme()
	if err := clusterv1beta1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add cluster v1beta1 scheme: %v", err)
	}
	return scheme
}
