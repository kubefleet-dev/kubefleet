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

package informer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
)

// mockInformerManager is a simple mock for testing
type mockInformerManager struct {
	allResources []schema.GroupVersionResource
	syncedMap    map[schema.GroupVersionResource]bool
}

func (m *mockInformerManager) AddDynamicResources(resources []APIResourceMeta, handler cache.ResourceEventHandler, listComplete bool) {
}
func (m *mockInformerManager) AddStaticResource(resource APIResourceMeta, handler cache.ResourceEventHandler) {
}
func (m *mockInformerManager) IsInformerSynced(resource schema.GroupVersionResource) bool {
	if m.syncedMap == nil {
		return false
	}
	synced, exists := m.syncedMap[resource]
	return exists && synced
}
func (m *mockInformerManager) Start() {}
func (m *mockInformerManager) Stop()  {}
func (m *mockInformerManager) Lister(resource schema.GroupVersionResource) cache.GenericLister {
	return nil
}
func (m *mockInformerManager) GetNameSpaceScopedResources() []schema.GroupVersionResource { return nil }
func (m *mockInformerManager) GetAllResources() []schema.GroupVersionResource {
	return m.allResources
}
func (m *mockInformerManager) IsClusterScopedResources(resource schema.GroupVersionKind) bool {
	return false
}
func (m *mockInformerManager) WaitForCacheSync()            {}
func (m *mockInformerManager) GetClient() dynamic.Interface { return nil }

func TestReadinessChecker(t *testing.T) {
	tests := []struct {
		name             string
		resourceInformer Manager
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
			resourceInformer: &mockInformerManager{
				allResources: []schema.GroupVersionResource{},
			},
			expectError:   true,
			errorContains: "no resources registered",
		},
		{
			name: "all informers synced",
			resourceInformer: &mockInformerManager{
				allResources: []schema.GroupVersionResource{
					{Group: "", Version: "v1", Resource: "configmaps"},
					{Group: "", Version: "v1", Resource: "secrets"},
					{Group: "", Version: "v1", Resource: "namespaces"},
				},
				syncedMap: map[schema.GroupVersionResource]bool{
					{Group: "", Version: "v1", Resource: "configmaps"}: true,
					{Group: "", Version: "v1", Resource: "secrets"}:    true,
					{Group: "", Version: "v1", Resource: "namespaces"}: true,
				},
			},
			expectError: false,
		},
		{
			name: "some informers not synced",
			resourceInformer: &mockInformerManager{
				allResources: []schema.GroupVersionResource{
					{Group: "", Version: "v1", Resource: "configmaps"},
					{Group: "", Version: "v1", Resource: "secrets"},
					{Group: "", Version: "v1", Resource: "namespaces"},
				},
				syncedMap: map[schema.GroupVersionResource]bool{
					{Group: "", Version: "v1", Resource: "configmaps"}: true,
					{Group: "", Version: "v1", Resource: "secrets"}:    false,
					{Group: "", Version: "v1", Resource: "namespaces"}: true,
				},
			},
			expectError:   true,
			errorContains: "informers not synced yet",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := ReadinessChecker(tt.resourceInformer)
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

func TestReadinessChecker_PartialSync(t *testing.T) {
	// Test the case where we have multiple resources but only some are synced
	mockManager := &mockInformerManager{
		allResources: []schema.GroupVersionResource{
			{Group: "", Version: "v1", Resource: "configmaps"},
			{Group: "", Version: "v1", Resource: "secrets"},
			{Group: "apps", Version: "v1", Resource: "deployments"},
			{Group: "", Version: "v1", Resource: "namespaces"},
		},
		syncedMap: map[schema.GroupVersionResource]bool{
			{Group: "", Version: "v1", Resource: "configmaps"}:      false,
			{Group: "", Version: "v1", Resource: "secrets"}:         false,
			{Group: "apps", Version: "v1", Resource: "deployments"}: false,
			{Group: "", Version: "v1", Resource: "namespaces"}:      false,
		},
	}

	checker := ReadinessChecker(mockManager)
	err := checker(nil)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "informers not synced yet")
	// Should report 4 unsynced
	assert.Contains(t, err.Error(), "4/4")
}

func TestReadinessChecker_AllSyncedMultipleResources(t *testing.T) {
	// Test with many resources all synced
	mockManager := &mockInformerManager{
		allResources: []schema.GroupVersionResource{
			{Group: "", Version: "v1", Resource: "configmaps"},
			{Group: "", Version: "v1", Resource: "secrets"},
			{Group: "", Version: "v1", Resource: "services"},
			{Group: "apps", Version: "v1", Resource: "deployments"},
			{Group: "apps", Version: "v1", Resource: "statefulsets"},
			{Group: "", Version: "v1", Resource: "namespaces"},
			{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"},
		},
		syncedMap: map[schema.GroupVersionResource]bool{
			{Group: "", Version: "v1", Resource: "configmaps"}:                            true,
			{Group: "", Version: "v1", Resource: "secrets"}:                               true,
			{Group: "", Version: "v1", Resource: "services"}:                              true,
			{Group: "apps", Version: "v1", Resource: "deployments"}:                       true,
			{Group: "apps", Version: "v1", Resource: "statefulsets"}:                      true,
			{Group: "", Version: "v1", Resource: "namespaces"}:                            true,
			{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"}: true,
		},
	}

	checker := ReadinessChecker(mockManager)
	err := checker(nil)

	assert.NoError(t, err, "Should be ready when all informers are synced")
}
