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
	"strings"
	"testing"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

func TestBuildNamespaceAffinityLabelKey(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		expected  string
	}{
		{
			name:      "basic namespace",
			namespace: "test-ns",
			expected:  "kubernetes-fleet.io/namespace-test-ns",
		},
		{
			name:      "system namespace",
			namespace: "kube-system",
			expected:  "kubernetes-fleet.io/namespace-kube-system",
		},
		{
			name:      "default namespace",
			namespace: "default",
			expected:  "kubernetes-fleet.io/namespace-default",
		},
		{
			name:      "long namespace name",
			namespace: "very-long-namespace-name-that-should-work",
			expected:  "kubernetes-fleet.io/namespace-very-long-namespace-name-that-should-work",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildNamespaceAffinityLabelKey(tt.namespace)
			if result != tt.expected {
				t.Errorf("BuildNamespaceAffinityLabelKey() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestParseNamespaceFromAffinityLabel(t *testing.T) {
	tests := []struct {
		name     string
		labelKey string
		expected string
	}{
		{
			name:     "valid namespace affinity label",
			labelKey: "kubernetes-fleet.io/namespace-test-ns",
			expected: "test-ns",
		},
		{
			name:     "system namespace",
			labelKey: "kubernetes-fleet.io/namespace-kube-system",
			expected: "kube-system",
		},
		{
			name:     "default namespace",
			labelKey: "kubernetes-fleet.io/namespace-default",
			expected: "default",
		},
		{
			name:     "invalid label - different prefix",
			labelKey: "example.com/namespace-test-ns",
			expected: "",
		},
		{
			name:     "invalid label - missing namespace prefix",
			labelKey: "kubernetes-fleet.io/test-ns",
			expected: "",
		},
		{
			name:     "empty label key",
			labelKey: "",
			expected: "",
		},
		{
			name:     "namespace with dashes",
			labelKey: "kubernetes-fleet.io/namespace-my-test-namespace",
			expected: "my-test-namespace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseNamespaceFromAffinityLabel(tt.labelKey)
			if result != tt.expected {
				t.Errorf("ParseNamespaceFromAffinityLabel() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestIsNamespaceAffinityLabel(t *testing.T) {
	tests := []struct {
		name     string
		labelKey string
		expected bool
	}{
		{
			name:     "valid namespace affinity label",
			labelKey: "kubernetes-fleet.io/namespace-test-ns",
			expected: true,
		},
		{
			name:     "valid with system namespace",
			labelKey: "kubernetes-fleet.io/namespace-kube-system",
			expected: true,
		},
		{
			name:     "invalid - different domain",
			labelKey: "example.com/namespace-test-ns",
			expected: false,
		},
		{
			name:     "invalid - missing namespace prefix",
			labelKey: "kubernetes-fleet.io/test-ns",
			expected: false,
		},
		{
			name:     "invalid - different fleet label",
			labelKey: "kubernetes-fleet.io/cluster-name",
			expected: false,
		},
		{
			name:     "empty string",
			labelKey: "",
			expected: false,
		},
		{
			name:     "only prefix",
			labelKey: "kubernetes-fleet.io/namespace-",
			expected: false,
		},
		{
			name:     "double dash namespace",
			labelKey: "kubernetes-fleet.io/namespace--invalid",
			expected: false, // According to K8s naming conventions
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsNamespaceAffinityLabel(tt.labelKey)
			if result != tt.expected {
				t.Errorf("IsNamespaceAffinityLabel() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestNamespaceAffinityConstants(t *testing.T) {
	// Test that constants have expected values
	expectedPrefix := placementv1beta1.FleetPrefix + "namespace-"
	if NamespaceAffinityLabelKeyPrefix != expectedPrefix {
		t.Errorf("NamespaceAffinityLabelKeyPrefix = %v, want %v", NamespaceAffinityLabelKeyPrefix, expectedPrefix)
	}

	// Test that max limit is reasonable
	if MaxNamespaceLabelsPerCluster <= 0 {
		t.Errorf("MaxNamespaceLabelsPerCluster should be positive, got %v", MaxNamespaceLabelsPerCluster)
	}
	if MaxNamespaceLabelsPerCluster > 1000 {
		t.Errorf("MaxNamespaceLabelsPerCluster seems too high, got %v", MaxNamespaceLabelsPerCluster)
	}
}

func TestNamespaceAffinityLabelKeyPrefixIntegration(t *testing.T) {
	// Test integration between BuildNamespaceAffinityLabelKey and the prefix constant
	namespace := "test-ns"
	expectedKey := NamespaceAffinityLabelKeyPrefix + namespace
	actualKey := BuildNamespaceAffinityLabelKey(namespace)

	if actualKey != expectedKey {
		t.Errorf("BuildNamespaceAffinityLabelKey integration failed. Expected %v, got %v", expectedKey, actualKey)
	}

	// Test that IsNamespaceAffinityLabel recognizes keys built with BuildNamespaceAffinityLabelKey
	if !IsNamespaceAffinityLabel(actualKey) {
		t.Errorf("IsNamespaceAffinityLabel should return true for key built with BuildNamespaceAffinityLabelKey")
	}

	// Test that ParseNamespaceFromAffinityLabel can extract the namespace correctly
	extractedNamespace := ParseNamespaceFromAffinityLabel(actualKey)
	if extractedNamespace != namespace {
		t.Errorf("ParseNamespaceFromAffinityLabel failed to extract namespace. Expected %v, got %v", namespace, extractedNamespace)
	}
}

func TestRoundTripNamespaceAffinity(t *testing.T) {
	testCases := []string{
		"default",
		"kube-system",
		"fleet-system",
		"app-namespace",
		"test-ns-with-dashes",
		"a",
		"namespace123",
	}

	for _, namespace := range testCases {
		t.Run("namespace-"+namespace, func(t *testing.T) {
			// Build label key
			labelKey := BuildNamespaceAffinityLabelKey(namespace)

			// Verify it's recognized as namespace affinity label
			if !IsNamespaceAffinityLabel(labelKey) {
				t.Errorf("Built label key %v should be recognized as namespace affinity label", labelKey)
			}

			// Parse namespace back
			parsedNamespace := ParseNamespaceFromAffinityLabel(labelKey)
			if parsedNamespace != namespace {
				t.Errorf("Round-trip failed for namespace %v. Parsed: %v", namespace, parsedNamespace)
			}
		})
	}
}

func TestNamespaceAffinityLabelValidation(t *testing.T) {
	// Test edge cases and validation
	tests := []struct {
		name          string
		namespace     string
		shouldBeValid bool
	}{
		{
			name:          "empty namespace",
			namespace:     "",
			shouldBeValid: false,
		},
		{
			name:          "very long namespace",
			namespace:     strings.Repeat("a", 253), // K8s limit is 253 chars
			shouldBeValid: true,
		},
		{
			name:          "namespace with dots",
			namespace:     "app.namespace",
			shouldBeValid: true,
		},
		{
			name:          "namespace starting with dash",
			namespace:     "-invalid",
			shouldBeValid: false, // K8s doesn't allow leading dash
		},
		{
			name:          "namespace ending with dash",
			namespace:     "invalid-",
			shouldBeValid: false, // K8s doesn't allow trailing dash
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labelKey := BuildNamespaceAffinityLabelKey(tt.namespace)
			isValid := IsNamespaceAffinityLabel(labelKey)

			if tt.shouldBeValid && !isValid {
				t.Errorf("Expected namespace %v to produce valid label, but got invalid", tt.namespace)
			}
			if !tt.shouldBeValid && isValid {
				t.Errorf("Expected namespace %v to produce invalid label, but got valid", tt.namespace)
			}
		})
	}
}
