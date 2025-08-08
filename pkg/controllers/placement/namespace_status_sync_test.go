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

package placement

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

func TestExtractNamespaceFromResourceSelectors(t *testing.T) {
	testCases := []struct {
		name      string
		placement placementv1beta1.ClusterResourcePlacement
		want      string
	}{
		{
			name: "NamespaceAccessible with namespace selector",
			placement: placementv1beta1.ClusterResourcePlacement{
				Spec: placementv1beta1.PlacementSpec{
					StatusReportingScope: placementv1beta1.NamespaceAccessible,
					ResourceSelectors: []placementv1beta1.ClusterResourceSelector{
						{
							Group:   "",
							Version: "v1",
							Kind:    "Namespace",
							Name:    "test-namespace",
						},
					},
				},
			},
			want: "test-namespace",
		},
		{
			name: "ClusterScopeOnly should return empty",
			placement: placementv1beta1.ClusterResourcePlacement{
				Spec: placementv1beta1.PlacementSpec{
					StatusReportingScope: placementv1beta1.ClusterScopeOnly,
					ResourceSelectors: []placementv1beta1.ClusterResourceSelector{
						{
							Group:   "",
							Version: "v1",
							Kind:    "Namespace",
							Name:    "test-namespace",
						},
					},
				},
			},
			want: "",
		},
		{
			name: "NamespaceAccessible without namespace selector",
			placement: placementv1beta1.ClusterResourcePlacement{
				Spec: placementv1beta1.PlacementSpec{
					StatusReportingScope: placementv1beta1.NamespaceAccessible,
					ResourceSelectors: []placementv1beta1.ClusterResourceSelector{
						{
							Group:   "apps",
							Version: "v1",
							Kind:    "Deployment",
							Name:    "test-deployment",
						},
					},
				},
			},
			want: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractNamespaceFromResourceSelectors(&tc.placement)
			if got != tc.want {
				t.Errorf("extractNamespaceFromResourceSelectors() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSyncClusterResourcePlacementStatus(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add client go scheme: %v", err)
	}
	if err := placementv1beta1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add fleet scheme: %v", err)
	}

	type expectation int
	const (
		expectCreation expectation = iota
		expectUpdate
		expectNoOperation
	)

	testCases := []struct {
		name            string
		placementObj    placementv1beta1.PlacementObj
		existingObjects []client.Object
		want            expectation
	}{
		{
			name: "Create new ClusterResourcePlacementStatus",
			placementObj: &placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-crp",
				},
				Spec: placementv1beta1.PlacementSpec{
					StatusReportingScope: placementv1beta1.NamespaceAccessible,
					ResourceSelectors: []placementv1beta1.ClusterResourceSelector{
						{
							Group:   "",
							Version: "v1",
							Kind:    "Namespace",
							Name:    "test-namespace",
						},
					},
				},
				Status: placementv1beta1.PlacementStatus{
					ObservedResourceIndex: "test-index",
					Conditions: []metav1.Condition{
						{
							Type:   "TestCondition",
							Status: metav1.ConditionTrue,
							Reason: "TestReason",
						},
					},
				},
			},
			existingObjects: []client.Object{
				&corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-namespace",
					},
				},
			},
			want: expectCreation,
		},
		{
			name: "Update existing ClusterResourcePlacementStatus",
			placementObj: &placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-crp",
				},
				Spec: placementv1beta1.PlacementSpec{
					StatusReportingScope: placementv1beta1.NamespaceAccessible,
					ResourceSelectors: []placementv1beta1.ClusterResourceSelector{
						{
							Group:   "",
							Version: "v1",
							Kind:    "Namespace",
							Name:    "test-namespace",
						},
					},
				},
				Status: placementv1beta1.PlacementStatus{
					ObservedResourceIndex: "updated-index",
					Conditions: []metav1.Condition{
						{
							Type:   "UpdatedCondition",
							Status: metav1.ConditionTrue,
							Reason: "UpdatedReason",
						},
					},
				},
			},
			existingObjects: []client.Object{
				&corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-namespace",
					},
				},
				&placementv1beta1.ClusterResourcePlacementStatus{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-crp",
						Namespace: "test-namespace",
					},
					Status: placementv1beta1.PlacementStatus{
						ObservedResourceIndex: "old-index",
					},
				},
			},
			want: expectUpdate,
		},
		{
			name: "ClusterScopeOnly should not sync",
			placementObj: &placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-crp",
				},
				Spec: placementv1beta1.PlacementSpec{
					StatusReportingScope: placementv1beta1.ClusterScopeOnly,
					ResourceSelectors: []placementv1beta1.ClusterResourceSelector{
						{
							Group:   "",
							Version: "v1",
							Kind:    "Namespace",
							Name:    "test-namespace",
						},
					},
				},
			},
			want: expectNoOperation,
		},
		{
			name: "ResourcePlacement should not sync",
			placementObj: &placementv1beta1.ResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rp",
					Namespace: "test-namespace",
				},
				Spec: placementv1beta1.PlacementSpec{
					StatusReportingScope: placementv1beta1.NamespaceAccessible,
					ResourceSelectors: []placementv1beta1.ClusterResourceSelector{
						{
							Group:   "",
							Version: "v1",
							Kind:    "Namespace",
							Name:    "test-namespace",
						},
					},
				},
			},
			want: expectNoOperation,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tc.existingObjects...).
				WithStatusSubresource(&placementv1beta1.ClusterResourcePlacementStatus{}).
				Build()

			reconciler := &Reconciler{
				Client: fakeClient,
				Scheme: scheme,
			}

			err := reconciler.syncClusterResourcePlacementStatus(context.Background(), tc.placementObj)
			if err != nil {
				t.Fatalf("syncClusterResourcePlacementStatus() failed: %v", err)
			}

			if tc.want == expectNoOperation {
				// Verify no ClusterResourcePlacementStatus was created or updated
				return
			}

			// Verify the ClusterResourcePlacementStatus exists
			crp, ok := tc.placementObj.(*placementv1beta1.ClusterResourcePlacement)
			if !ok {
				return // ResourcePlacement case
			}

			targetNamespace := extractNamespaceFromResourceSelectors(tc.placementObj)
			if targetNamespace == "" {
				return // No sync expected
			}

			crpStatus := &placementv1beta1.ClusterResourcePlacementStatus{}
			err = fakeClient.Get(context.Background(), types.NamespacedName{
				Name:      crp.Name,
				Namespace: targetNamespace,
			}, crpStatus)

			if tc.want == expectCreation || tc.want == expectUpdate {
				if err != nil {
					t.Fatalf("expected ClusterResourcePlacementStatus to exist but got error: %v", err)
				}

				// Use cmp.Diff to compare the key fields
				wantStatus := placementv1beta1.ClusterResourcePlacementStatus{
					ObjectMeta: metav1.ObjectMeta{
						Name:      crp.Name,
						Namespace: targetNamespace,
					},
					Status: crp.Status,
				}

				// Ignore metadata fields that Kubernetes sets automatically
				if diff := cmp.Diff(wantStatus, *crpStatus, cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion", "UID", "CreationTimestamp", "Generation", "ManagedFields", "OwnerReferences")); diff != "" {
					t.Errorf("ClusterResourcePlacementStatus mismatch (-want +got):\n%s", diff)
				}

				// Verify owner reference is set correctly for creation case
				if tc.want == expectCreation {
					if len(crpStatus.OwnerReferences) == 0 {
						t.Error("Expected owner reference to be set, but none found")
					} else {
						ownerRef := crpStatus.OwnerReferences[0]
						if ownerRef.Name != crp.Name {
							t.Errorf("Expected owner reference name %s, got %s", crp.Name, ownerRef.Name)
						}
						if ownerRef.Kind != "ClusterResourcePlacement" {
							t.Errorf("Expected owner reference kind ClusterResourcePlacement, got %s", ownerRef.Kind)
						}
						if ownerRef.Controller == nil || !*ownerRef.Controller {
							t.Error("Expected owner reference to be marked as controller")
						}
					}
				}
			}
		})
	}
}
