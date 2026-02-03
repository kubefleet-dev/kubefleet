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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

func TestHasSuccessfulNamespacePlacement(t *testing.T) {
	tests := []struct {
		name     string
		status   placementv1beta1.PerClusterPlacementStatus
		expected bool
	}{
		{
			name: "has Applied condition True",
			status: placementv1beta1.PerClusterPlacementStatus{
				ClusterName: "cluster-1",
				Conditions: []metav1.Condition{
					{
						Type:   string(placementv1beta1.PerClusterAppliedConditionType),
						Status: metav1.ConditionTrue,
					},
				},
			},
			expected: true,
		},
		{
			name: "has Applied condition False",
			status: placementv1beta1.PerClusterPlacementStatus{
				ClusterName: "cluster-1",
				Conditions: []metav1.Condition{
					{
						Type:   string(placementv1beta1.PerClusterAppliedConditionType),
						Status: metav1.ConditionFalse,
					},
				},
			},
			expected: false,
		},
		{
			name: "has Applied condition Unknown",
			status: placementv1beta1.PerClusterPlacementStatus{
				ClusterName: "cluster-1",
				Conditions: []metav1.Condition{
					{
						Type:   string(placementv1beta1.PerClusterAppliedConditionType),
						Status: metav1.ConditionUnknown,
					},
				},
			},
			expected: false,
		},
		{
			name: "no Applied condition",
			status: placementv1beta1.PerClusterPlacementStatus{
				ClusterName: "cluster-1",
				Conditions: []metav1.Condition{
					{
						Type:   "OtherCondition",
						Status: metav1.ConditionTrue,
					},
				},
			},
			expected: false,
		},
		{
			name: "no conditions",
			status: placementv1beta1.PerClusterPlacementStatus{
				ClusterName: "cluster-1",
				Conditions:  []metav1.Condition{},
			},
			expected: false,
		},
		{
			name: "multiple conditions with Applied True",
			status: placementv1beta1.PerClusterPlacementStatus{
				ClusterName: "cluster-1",
				Conditions: []metav1.Condition{
					{
						Type:   "OtherCondition",
						Status: metav1.ConditionFalse,
					},
					{
						Type:   string(placementv1beta1.PerClusterAppliedConditionType),
						Status: metav1.ConditionTrue,
					},
				},
			},
			expected: true,
		},
	}

	reconciler := &Reconciler{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := reconciler.hasSuccessfulNamespacePlacement(tt.status)
			if result != tt.expected {
				t.Errorf("hasSuccessfulNamespacePlacement() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestExtractSuccessfulNamespacesFromStatus(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = clusterv1beta1.AddToScheme(scheme)
	_ = placementv1beta1.AddToScheme(scheme)

	tests := []struct {
		name             string
		crp              *placementv1beta1.ClusterResourcePlacement
		status           placementv1beta1.PerClusterPlacementStatus
		expected         []string
		expectCRPMissing bool
	}{
		{
			name: "direct namespace selection - success",
			crp: &placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{Name: "test-crp"},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
						{
							Group:   "",
							Version: "v1",
							Kind:    "Namespace",
							Name:    "test-ns",
						},
					},
				},
				Status: placementv1beta1.PlacementStatus{
					SelectedResources: []placementv1beta1.ResourceIdentifier{
						{
							Group:   "",
							Version: "v1",
							Kind:    "Namespace",
							Name:    "test-ns",
						},
					},
				},
			},
			status: placementv1beta1.PerClusterPlacementStatus{
				ClusterName: "cluster-1",
			},
			expected: []string{"test-ns"},
		},
		{
			name: "multiple namespace selection - success",
			crp: &placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{Name: "test-crp"},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
						{
							Group:   "",
							Version: "v1",
							Kind:    "Namespace",
							Name:    "ns-1",
						},
						{
							Group:   "",
							Version: "v1",
							Kind:    "Namespace",
							Name:    "ns-2",
						},
					},
				},
				Status: placementv1beta1.PlacementStatus{
					SelectedResources: []placementv1beta1.ResourceIdentifier{
						{
							Group:   "",
							Version: "v1",
							Kind:    "Namespace",
							Name:    "ns-1",
						},
						{
							Group:   "",
							Version: "v1",
							Kind:    "Namespace",
							Name:    "ns-2",
						},
					},
				},
			},
			status: placementv1beta1.PerClusterPlacementStatus{
				ClusterName: "cluster-1",
			},
			expected: []string{"ns-1", "ns-2"},
		},
		{
			name: "mixed resource selectors - only namespaces in selected resources",
			crp: &placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{Name: "test-crp"},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
						{
							Group:   "",
							Version: "v1",
							Kind:    "Namespace",
							Name:    "test-ns",
						},
						{
							Group:   "apps",
							Version: "v1",
							Kind:    "Deployment",
							Name:    "test-deployment",
						},
					},
				},
				Status: placementv1beta1.PlacementStatus{
					SelectedResources: []placementv1beta1.ResourceIdentifier{
						{
							Group:   "",
							Version: "v1",
							Kind:    "Namespace",
							Name:    "test-ns",
						},
						{
							Group:   "apps",
							Version: "v1",
							Kind:    "Deployment",
							Name:    "test-deployment",
						},
					},
				},
			},
			status: placementv1beta1.PerClusterPlacementStatus{
				ClusterName: "cluster-1",
			},
			expected: []string{"test-ns"},
		},
		{
			name: "no namespace selectors in selected resources",
			crp: &placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{Name: "test-crp"},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
						{
							Group:   "apps",
							Version: "v1",
							Kind:    "Deployment",
							Name:    "test-deployment",
						},
					},
				},
				Status: placementv1beta1.PlacementStatus{
					SelectedResources: []placementv1beta1.ResourceIdentifier{
						{
							Group:   "apps",
							Version: "v1",
							Kind:    "Deployment",
							Name:    "test-deployment",
						},
					},
				},
			},
			status: placementv1beta1.PerClusterPlacementStatus{
				ClusterName: "cluster-1",
			},
			expected: []string{},
		},
		{
			name: "namespace selector exists but namespace not selected (non-existent)",
			crp: &placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{Name: "test-crp"},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
						{
							Group:   "",
							Version: "v1",
							Kind:    "Namespace",
							Name:    "non-existent-ns",
						},
					},
				},
				Status: placementv1beta1.PlacementStatus{
					SelectedResources: []placementv1beta1.ResourceIdentifier{}, // Empty - namespace wasn't found
				},
			},
			status: placementv1beta1.PerClusterPlacementStatus{
				ClusterName: "cluster-1",
			},
			expected: []string{}, // Should be empty because namespace wasn't actually selected
		},
		{
			name: "namespace selection with failures",
			crp: &placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{Name: "test-crp"},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
						{
							Group:   "",
							Version: "v1",
							Kind:    "Namespace",
							Name:    "ns-1",
						},
						{
							Group:   "",
							Version: "v1",
							Kind:    "Namespace",
							Name:    "ns-2",
						},
					},
				},
				Status: placementv1beta1.PlacementStatus{
					SelectedResources: []placementv1beta1.ResourceIdentifier{
						{
							Group:   "",
							Version: "v1",
							Kind:    "Namespace",
							Name:    "ns-1",
						},
						{
							Group:   "",
							Version: "v1",
							Kind:    "Namespace",
							Name:    "ns-2",
						},
					},
				},
			},
			status: placementv1beta1.PerClusterPlacementStatus{
				ClusterName: "cluster-1",
				FailedPlacements: []placementv1beta1.FailedResourcePlacement{
					{
						ResourceIdentifier: placementv1beta1.ResourceIdentifier{
							Group:   "",
							Version: "v1",
							Kind:    "Namespace",
							Name:    "ns-1",
						},
						Condition: metav1.Condition{
							Type:   "Applied",
							Status: metav1.ConditionFalse,
							Reason: "some error",
						},
					},
				},
			},
			expected: []string{"ns-2"}, // ns-1 should be filtered out due to failure
		},
		{
			name:             "CRP not found",
			crp:              nil, // Will not be created in fake client
			expectCRPMissing: true,
			status: placementv1beta1.PerClusterPlacementStatus{
				ClusterName: "cluster-1",
			},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fakeClient client.Client
			if tt.expectCRPMissing {
				fakeClient = fake.NewClientBuilder().WithScheme(scheme).Build()
			} else {
				fakeClient = fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.crp).Build()
			}

			reconciler := &Reconciler{Client: fakeClient}
			ctx := context.Background()

			var crpName string
			if tt.crp != nil {
				crpName = tt.crp.Name
			} else {
				crpName = "nonexistent-crp"
			}

			result := reconciler.extractSuccessfulNamespacesFromStatus(ctx, crpName, tt.status)

			if len(result) != len(tt.expected) {
				t.Errorf("extractSuccessfulNamespacesFromStatus() returned %d namespaces, want %d", len(result), len(tt.expected))
				t.Errorf("Got: %v, Want: %v", result, tt.expected)
				return
			}

			// Convert to sets for comparison
			resultSet := make(map[string]bool)
			for _, ns := range result {
				resultSet[ns] = true
			}
			expectedSet := make(map[string]bool)
			for _, ns := range tt.expected {
				expectedSet[ns] = true
			}

			for ns := range expectedSet {
				if !resultSet[ns] {
					t.Errorf("Expected namespace %v not found in result", ns)
				}
			}
			for ns := range resultSet {
				if !expectedSet[ns] {
					t.Errorf("Unexpected namespace %v found in result", ns)
				}
			}
		})
	}
}

func TestCountNamespaceAffinityLabels(t *testing.T) {
	tests := []struct {
		name     string
		labels   map[string]string
		expected int
	}{
		{
			name:     "no labels",
			labels:   map[string]string{},
			expected: 0,
		},
		{
			name:     "nil labels",
			labels:   nil,
			expected: 0,
		},
		{
			name: "only namespace affinity labels",
			labels: map[string]string{
				"kubernetes-fleet.io/namespace-ns1": "crp1",
				"kubernetes-fleet.io/namespace-ns2": "crp2",
			},
			expected: 2,
		},
		{
			name: "mixed labels",
			labels: map[string]string{
				"kubernetes-fleet.io/namespace-ns1": "crp1",
				"kubernetes-fleet.io/namespace-ns2": "crp2",
				"app":                               "test",
				"environment":                       "prod",
				"kubernetes-fleet.io/cluster-name":  "test",
			},
			expected: 2,
		},
		{
			name: "no namespace affinity labels",
			labels: map[string]string{
				"app":                              "test",
				"kubernetes-fleet.io/cluster-name": "test",
			},
			expected: 0,
		},
		{
			name: "invalid namespace affinity labels",
			labels: map[string]string{
				"example.com/namespace-ns1":      "crp1", // wrong domain
				"kubernetes-fleet.io/ns1":        "crp2", // missing namespace- prefix
				"kubernetes-fleet.io/namespace-": "crp3", // empty namespace
			},
			expected: 0,
		},
	}

	reconciler := &Reconciler{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := reconciler.countNamespaceAffinityLabels(tt.labels)
			if result != tt.expected {
				t.Errorf("countNamespaceAffinityLabels() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestPrioritizeNamespaceLabels(t *testing.T) {
	tests := []struct {
		name           string
		successfulCRPs map[string][]string
		maxLabels      int
		expectedCount  int
		expectFirst    string // First CRP name alphabetically should be prioritized
	}{
		{
			name: "within limit",
			successfulCRPs: map[string][]string{
				"crp-b": {"ns1", "ns2"},
				"crp-a": {"ns3"},
			},
			maxLabels:     10,
			expectedCount: 3,
			expectFirst:   "crp-a", // Alphabetically first
		},
		{
			name: "exceeds limit",
			successfulCRPs: map[string][]string{
				"crp-z": {"ns1", "ns2"},
				"crp-a": {"ns3", "ns4"},
				"crp-b": {"ns5"},
			},
			maxLabels:     3,
			expectedCount: 3,
			expectFirst:   "crp-a",
		},
		{
			name: "exactly at limit",
			successfulCRPs: map[string][]string{
				"crp-b": {"ns1"},
				"crp-a": {"ns2"},
			},
			maxLabels:     2,
			expectedCount: 2,
			expectFirst:   "crp-a",
		},
		{
			name:           "empty input",
			successfulCRPs: map[string][]string{},
			maxLabels:      5,
			expectedCount:  0,
		},
	}

	reconciler := &Reconciler{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := reconciler.prioritizeNamespaceLabels(tt.successfulCRPs, tt.maxLabels)

			if len(result) != tt.expectedCount {
				t.Errorf("prioritizeNamespaceLabels() returned %d labels, want %d", len(result), tt.expectedCount)
			}

			if tt.expectFirst != "" {
				// Find a label that should belong to the first CRP
				found := false
				for _, crpValue := range result {
					if crpValue == tt.expectFirst {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected to find labels from CRP %s, but didn't", tt.expectFirst)
				}
			}

			// Verify all results are valid namespace affinity labels
			for labelKey, crpName := range result {
				if !IsNamespaceAffinityLabel(labelKey) {
					t.Errorf("Invalid namespace affinity label generated: %s", labelKey)
				}
				if crpName == "" {
					t.Errorf("Empty CRP name for label %s", labelKey)
				}
			}
		})
	}
}
