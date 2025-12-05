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

package webhook

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"

	"github.com/kubefleet-dev/kubefleet/pkg/utils/informer"
	testinformer "github.com/kubefleet-dev/kubefleet/test/utils/informer"
)

func TestResourceInformerReadinessChecker(t *testing.T) {
	tests := []struct {
		name             string
		resourceInformer informer.Manager
		expectError      bool
		errorContains    string
	}{
		{
			name:             "nil informer",
			resourceInformer: nil,
			expectError:      true,
			errorContains:    "resource informer not initialized",
		},
		{
			name: "no resources registered",
			resourceInformer: &testinformer.FakeManager{
				APIResources: map[schema.GroupVersionKind]bool{},
			},
			expectError:   true,
			errorContains: "no resources registered",
		},
		{
			name: "all informers synced",
			resourceInformer: &testinformer.FakeManager{
				APIResources: map[schema.GroupVersionKind]bool{
					{Group: "", Version: "v1", Kind: "ConfigMap"}: false, // namespace-scoped
					{Group: "", Version: "v1", Kind: "Secret"}:    false, // namespace-scoped
					{Group: "", Version: "v1", Kind: "Namespace"}: true,  // cluster-scoped
				},
				IsClusterScopedResource: true, // true = map stores cluster-scoped resources
				InformerSynced:          ptr.To(true),
			},
			expectError: false,
		},
		{
			name: "some informers not synced",
			resourceInformer: &testinformer.FakeManager{
				APIResources: map[schema.GroupVersionKind]bool{
					{Group: "", Version: "v1", Kind: "ConfigMap"}: false, // namespace-scoped
					{Group: "", Version: "v1", Kind: "Secret"}:    false, // namespace-scoped
					{Group: "", Version: "v1", Kind: "Namespace"}: true,  // cluster-scoped
				},
				IsClusterScopedResource: true,
				InformerSynced:          ptr.To(false),
			},
			expectError:   true,
			errorContains: "informers not synced yet",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := ResourceInformerReadinessChecker(tt.resourceInformer)
			err := checker(nil)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestResourceInformerReadinessChecker_PartialSync(t *testing.T) {
	// Test the case where we have multiple resources but only some are synced
	fakeManager := &testinformer.FakeManager{
		APIResources: map[schema.GroupVersionKind]bool{
			{Group: "", Version: "v1", Kind: "ConfigMap"}:      false, // namespace-scoped
			{Group: "", Version: "v1", Kind: "Secret"}:         false, // namespace-scoped
			{Group: "apps", Version: "v1", Kind: "Deployment"}: false, // namespace-scoped
			{Group: "", Version: "v1", Kind: "Namespace"}:      true,  // cluster-scoped
		},
		IsClusterScopedResource: true,
		InformerSynced:          ptr.To(false),
		// This will make IsInformerSynced return false for all resources
	}

	checker := ResourceInformerReadinessChecker(fakeManager)
	err := checker(nil)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "informers not synced yet")
	// Should report 4 unsynced (3 namespace-scoped + 1 cluster-scoped)
	assert.Contains(t, err.Error(), "4/4")
}

func TestResourceInformerReadinessChecker_AllSyncedMultipleResources(t *testing.T) {
	// Test with many resources all synced
	fakeManager := &testinformer.FakeManager{
		APIResources: map[schema.GroupVersionKind]bool{
			{Group: "", Version: "v1", Kind: "ConfigMap"}:                            false, // namespace-scoped
			{Group: "", Version: "v1", Kind: "Secret"}:                               false, // namespace-scoped
			{Group: "", Version: "v1", Kind: "Service"}:                              false, // namespace-scoped
			{Group: "apps", Version: "v1", Kind: "Deployment"}:                       false, // namespace-scoped
			{Group: "apps", Version: "v1", Kind: "StatefulSet"}:                      false, // namespace-scoped
			{Group: "", Version: "v1", Kind: "Namespace"}:                            true,  // cluster-scoped
			{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRole"}: true,  // cluster-scoped
		},
		IsClusterScopedResource: true,
		InformerSynced:          ptr.To(true),
	}

	checker := ResourceInformerReadinessChecker(fakeManager)
	err := checker(nil)

	assert.NoError(t, err, "Should be ready when all informers are synced")
}
