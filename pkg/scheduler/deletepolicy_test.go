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

package scheduler

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	fleetv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

// TestCleanUpAllBindingsForDeletePolicy tests the cleanUpAllBindingsFor method with DeletePolicy functionality.
func TestCleanUpAllBindingsForDeletePolicy(t *testing.T) {
	now := metav1.Now()

	testCases := []struct {
		name                  string
		placement             func() fleetv1beta1.PlacementObj
		existingBindings      []fleetv1beta1.BindingObj
		wantFinalizers        []string
		wantRemainingBindings []fleetv1beta1.BindingObj
	}{
		{
			name: "cluster-scoped placement cleanup with Keep policy preserves bindings",
			placement: func() fleetv1beta1.PlacementObj {
				return &fleetv1beta1.ClusterResourcePlacement{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "test-crp",
						DeletionTimestamp: &now,
						Finalizers:        []string{fleetv1beta1.SchedulerCleanupFinalizer},
					},
					Spec: fleetv1beta1.PlacementSpec{
						ResourceSelectors: []fleetv1beta1.ClusterResourceSelector{
							{
								Group:   "",
								Version: "v1",
								Kind:    "Namespace",
								Name:    "test-namespace",
							},
						},
						DeletePolicy: fleetv1beta1.DeletePolicyKeep,
					},
				}
			},
			existingBindings: []fleetv1beta1.BindingObj{
				&fleetv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name: "bindingName1",
						Labels: map[string]string{
							fleetv1beta1.PlacementTrackingLabel: "test-crp",
						},
						Finalizers: []string{fleetv1beta1.SchedulerBindingCleanupFinalizer},
					},
				},
				&fleetv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name: "bindingName2",
						Labels: map[string]string{
							fleetv1beta1.PlacementTrackingLabel: "test-crp",
						},
						Finalizers: []string{fleetv1beta1.SchedulerBindingCleanupFinalizer},
					},
				},
			},
			wantFinalizers: []string{},
			wantRemainingBindings: []fleetv1beta1.BindingObj{
				&fleetv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name: "bindingName1",
						Labels: map[string]string{
							fleetv1beta1.PlacementTrackingLabel: "test-crp",
						},
						Finalizers: []string{}, // Finalizer should be removed
					},
				},
				&fleetv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name: "bindingName2",
						Labels: map[string]string{
							fleetv1beta1.PlacementTrackingLabel: "test-crp",
						},
						Finalizers: []string{}, // Finalizer should be removed
					},
				},
			},
		},
		{
			name: "cluster-scoped placement cleanup with Delete policy removes bindings",
			placement: func() fleetv1beta1.PlacementObj {
				return &fleetv1beta1.ClusterResourcePlacement{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "test-crp",
						DeletionTimestamp: &now,
						Finalizers:        []string{fleetv1beta1.SchedulerCleanupFinalizer},
					},
					Spec: fleetv1beta1.PlacementSpec{
						ResourceSelectors: []fleetv1beta1.ClusterResourceSelector{
							{
								Group:   "",
								Version: "v1",
								Kind:    "Namespace",
								Name:    "test-namespace",
							},
						},
						DeletePolicy: fleetv1beta1.DeletePolicyDelete,
					},
				}
			},
			existingBindings: []fleetv1beta1.BindingObj{
				&fleetv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name: "bindingName1",
						Labels: map[string]string{
							fleetv1beta1.PlacementTrackingLabel: "test-crp",
						},
						Finalizers: []string{fleetv1beta1.SchedulerBindingCleanupFinalizer},
					},
				},
			},
			wantFinalizers:        []string{},
			wantRemainingBindings: []fleetv1beta1.BindingObj{},
		},
		{
			name: "cluster-scoped placement cleanup with default behavior (no DeletePolicy) removes bindings",
			placement: func() fleetv1beta1.PlacementObj {
				return &fleetv1beta1.ClusterResourcePlacement{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "test-crp",
						DeletionTimestamp: &now,
						Finalizers:        []string{fleetv1beta1.SchedulerCleanupFinalizer},
					},
					Spec: fleetv1beta1.PlacementSpec{
						ResourceSelectors: []fleetv1beta1.ClusterResourceSelector{
							{
								Group:   "",
								Version: "v1",
								Kind:    "Namespace",
								Name:    "test-namespace",
							},
						},
						// DeletePolicy not specified, should default to Delete behavior
					},
				}
			},
			existingBindings: []fleetv1beta1.BindingObj{
				&fleetv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name: "bindingName1",
						Labels: map[string]string{
							fleetv1beta1.PlacementTrackingLabel: "test-crp",
						},
						Finalizers: []string{fleetv1beta1.SchedulerBindingCleanupFinalizer},
					},
				},
			},
			wantFinalizers:        []string{},
			wantRemainingBindings: []fleetv1beta1.BindingObj{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			placement := tc.placement()
			// Create a fake client with the placement and existing bindings
			fakeClientBuilder := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(placement)
			for _, binding := range tc.existingBindings {
				fakeClientBuilder.WithObjects(binding)
			}
			fakeClient := fakeClientBuilder.Build()

			s := &Scheduler{
				client:         fakeClient,
				uncachedReader: fakeClient,
			}

			ctx := context.Background()
			if err := s.cleanUpAllBindingsFor(ctx, placement); err != nil {
				t.Fatalf("cleanUpAllBindingsFor() = %v, want no error", err)
			}

			// Verify the finalizer was removed from placement
			gotFinalizers := placement.GetFinalizers()
			if !cmp.Equal(gotFinalizers, tc.wantFinalizers) {
				t.Errorf("Expected finalizer %v, got %v", tc.wantFinalizers, gotFinalizers)
			}

			// Verify bindings state
			var gotBindings fleetv1beta1.BindingObjList
			gotBindings = &fleetv1beta1.ClusterResourceBindingList{}

			if err := fakeClient.List(ctx, gotBindings); err != nil {
				t.Fatalf("Failed to list bindings: %v", err)
			}

			gotBindingsList := gotBindings.GetBindingObjs()

			// Check the number of remaining bindings
			if len(gotBindingsList) != len(tc.wantRemainingBindings) {
				t.Errorf("Expected %d remaining bindings, got %d", len(tc.wantRemainingBindings), len(gotBindingsList))
				return
			}

			// Check each binding matches expectations
			for i, gotBinding := range gotBindingsList {
				if i >= len(tc.wantRemainingBindings) {
					break
				}
				wantBinding := tc.wantRemainingBindings[i]

				if gotBinding.GetName() != wantBinding.GetName() {
					t.Errorf("Binding %d: expected name %s, got %s", i, wantBinding.GetName(), gotBinding.GetName())
				}

				// Check finalizers - handle nil vs empty slice
				gotFinalizers := gotBinding.GetFinalizers()
				wantFinalizers := wantBinding.GetFinalizers()
				if len(gotFinalizers) == 0 && len(wantFinalizers) == 0 {
					// Both are empty, that's fine
				} else if !cmp.Equal(gotFinalizers, wantFinalizers) {
					t.Errorf("Binding %d (%s): expected finalizers %v, got %v", i, gotBinding.GetName(), wantFinalizers, gotFinalizers)
				}

				if !cmp.Equal(gotBinding.GetLabels(), wantBinding.GetLabels()) {
					t.Errorf("Binding %d: expected labels %v, got %v", i, wantBinding.GetLabels(), gotBinding.GetLabels())
				}
			}
		})
	}
}
