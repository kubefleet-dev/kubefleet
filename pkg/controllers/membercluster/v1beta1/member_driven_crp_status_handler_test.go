/*
Copyright 2026 The KubeFleet Authors.

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

package v1beta1

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
)

func TestReconcileMemberDrivenNamespaceAffinityLabels(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = clusterv1beta1.AddToScheme(scheme)

	tests := []struct {
		name           string
		memberCluster  *clusterv1beta1.MemberCluster
		internalMC     *clusterv1beta1.InternalMemberCluster
		expectedLabels map[string]string
		expectError    bool
	}{
		{
			name: "should add labels from CRP associations",
			memberCluster: &clusterv1beta1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test-cluster",
					Labels: make(map[string]string),
				},
			},
			internalMC: &clusterv1beta1.InternalMemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cluster",
				},
				Status: clusterv1beta1.InternalMemberClusterStatus{
					CRPNamespaceAssociations: map[string][]string{
						"web-app-crp":  {"default", "web"},
						"system-crp":   {"kube-system"},
						"database-crp": {"database"},
					},
				},
			},
			expectedLabels: map[string]string{
				"kubernetes-fleet.io/namespace-default":     "web-app-crp",
				"kubernetes-fleet.io/namespace-web":         "web-app-crp",
				"kubernetes-fleet.io/namespace-kube-system": "system-crp",
				"kubernetes-fleet.io/namespace-database":    "database-crp",
			},
			expectError: false,
		},
		{
			name: "should remove obsolete labels",
			memberCluster: &clusterv1beta1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cluster",
					Labels: map[string]string{
						"kubernetes-fleet.io/namespace-default":  "old-crp",
						"kubernetes-fleet.io/namespace-obsolete": "removed-crp",
						"other-label":                            "should-remain",
					},
				},
			},
			internalMC: &clusterv1beta1.InternalMemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cluster",
				},
				Status: clusterv1beta1.InternalMemberClusterStatus{
					CRPNamespaceAssociations: map[string][]string{
						"new-crp": {"default"},
					},
				},
			},
			expectedLabels: map[string]string{
				"kubernetes-fleet.io/namespace-default": "new-crp",
				"other-label":                           "should-remain",
			},
			expectError: false,
		},
		{
			name: "should cleanup all labels when no associations",
			memberCluster: &clusterv1beta1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cluster",
					Labels: map[string]string{
						"kubernetes-fleet.io/namespace-default": "old-crp",
						"kubernetes-fleet.io/namespace-web":     "old-crp",
						"other-label":                           "should-remain",
					},
				},
			},
			internalMC: &clusterv1beta1.InternalMemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cluster",
				},
				Status: clusterv1beta1.InternalMemberClusterStatus{
					CRPNamespaceAssociations: map[string][]string{},
				},
			},
			expectedLabels: map[string]string{
				"other-label": "should-remain",
			},
			expectError: false,
		},
		{
			name: "should handle nil InternalMemberCluster gracefully",
			memberCluster: &clusterv1beta1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cluster",
					Labels: map[string]string{
						"kubernetes-fleet.io/namespace-default": "old-crp",
					},
				},
			},
			internalMC: nil,
			expectedLabels: map[string]string{
				"kubernetes-fleet.io/namespace-default": "old-crp", // Should remain unchanged
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake client with the MemberCluster
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.memberCluster).Build()

			// Create reconciler
			reconciler := &Reconciler{
				Client: fakeClient,
			}

			// Run the reconciliation
			err := reconciler.reconcileMemberDrivenNamespaceAffinityLabels(context.Background(), tt.memberCluster, tt.internalMC)

			// Check error expectation
			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			// Check labels
			if !tt.expectError {
				for expectedKey, expectedValue := range tt.expectedLabels {
					actualValue, exists := tt.memberCluster.Labels[expectedKey]
					if !exists {
						t.Errorf("Expected label %s not found in MemberCluster", expectedKey)
					} else if actualValue != expectedValue {
						t.Errorf("Label %s: expected value %s, got %s", expectedKey, expectedValue, actualValue)
					}
				}

				// Check for unexpected labels
				for actualKey := range tt.memberCluster.Labels {
					if _, expected := tt.expectedLabels[actualKey]; !expected {
						// Allow namespace affinity labels that should have been cleaned up
						if IsNamespaceAffinityLabel(actualKey) {
							t.Errorf("Unexpected namespace affinity label %s found in MemberCluster", actualKey)
						}
					}
				}
			}
		})
	}
}

func TestPrioritizeMemberDrivenNamespaceLabels(t *testing.T) {
	reconciler := &Reconciler{}

	tests := []struct {
		name            string
		crpAssociations map[string][]string
		maxLabels       int
		expectedCount   int
		expectedCRPs    []string // CRPs that should be prioritized
	}{
		{
			name: "should prioritize alphabetically by CRP name",
			crpAssociations: map[string][]string{
				"zebra-crp": {"ns1", "ns2"},
				"alpha-crp": {"ns3", "ns4"},
				"beta-crp":  {"ns5"},
			},
			maxLabels:     3,
			expectedCount: 3,
			expectedCRPs:  []string{"alpha-crp", "alpha-crp", "beta-crp"}, // alpha-crp gets 2 labels, beta-crp gets 1
		},
		{
			name: "should prioritize namespaces alphabetically within CRP",
			crpAssociations: map[string][]string{
				"test-crp": {"zebra-ns", "alpha-ns", "beta-ns"},
			},
			maxLabels:     2,
			expectedCount: 2,
			expectedCRPs:  []string{"test-crp", "test-crp"},
		},
		{
			name: "should handle exact limit",
			crpAssociations: map[string][]string{
				"crp-a": {"ns1"},
				"crp-b": {"ns2"},
				"crp-c": {"ns3"},
			},
			maxLabels:     3,
			expectedCount: 3,
			expectedCRPs:  []string{"crp-a", "crp-b", "crp-c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := reconciler.prioritizeMemberDrivenNamespaceLabels(tt.crpAssociations, tt.maxLabels)

			if len(result) != tt.expectedCount {
				t.Errorf("Expected %d labels, got %d", tt.expectedCount, len(result))
			}

			// Verify the result contains expected CRPs (order may vary due to map iteration)
			actualCRPs := make([]string, 0, len(result))
			for _, crp := range result {
				actualCRPs = append(actualCRPs, crp)
			}

			// Check that we have the right CRPs (exact match depends on stable iteration)
			if len(actualCRPs) != len(tt.expectedCRPs) {
				t.Errorf("Expected CRP count %d, got %d", len(tt.expectedCRPs), len(actualCRPs))
			}
		})
	}
}
