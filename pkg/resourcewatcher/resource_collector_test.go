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

package resourcewatcher

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/restmapper"

	"github.com/kubefleet-dev/kubefleet/pkg/utils"
)

func TestShouldWatchResource(t *testing.T) {
	tests := []struct {
		name           string
		gvr            schema.GroupVersionResource
		resourceConfig *utils.ResourceConfig
		setupMapper    func() meta.RESTMapper
		expected       bool
	}{
		{
			name: "returns true when resourceConfig is nil",
			gvr: schema.GroupVersionResource{
				Group:    "",
				Version:  "v1",
				Resource: "configmaps",
			},
			resourceConfig: nil,
			setupMapper: func() meta.RESTMapper {
				groupResources := []*restmapper.APIGroupResources{
					{
						Group: metav1.APIGroup{
							Name: "",
							Versions: []metav1.GroupVersionForDiscovery{
								{GroupVersion: "v1", Version: "v1"},
							},
							PreferredVersion: metav1.GroupVersionForDiscovery{
								GroupVersion: "v1",
								Version:      "v1",
							},
						},
						VersionedResources: map[string][]metav1.APIResource{
							"v1": {
								{
									Name:       "configmaps",
									Kind:       "ConfigMap",
									Namespaced: true,
									Verbs:      []string{"list", "watch", "get"},
								},
							},
						},
					},
				}
				return restmapper.NewDiscoveryRESTMapper(groupResources)
			},
			expected: true,
		},
		{
			name: "returns true when resource is not disabled",
			gvr: schema.GroupVersionResource{
				Group:    "",
				Version:  "v1",
				Resource: "configmaps",
			},
			resourceConfig: func() *utils.ResourceConfig {
				rc := utils.NewResourceConfig(false)
				// Disable secrets, but not configmaps
				_ = rc.Parse("v1/Secret")
				return rc
			}(),
			setupMapper: func() meta.RESTMapper {
				groupResources := []*restmapper.APIGroupResources{
					{
						Group: metav1.APIGroup{
							Name: "",
							Versions: []metav1.GroupVersionForDiscovery{
								{GroupVersion: "v1", Version: "v1"},
							},
							PreferredVersion: metav1.GroupVersionForDiscovery{
								GroupVersion: "v1",
								Version:      "v1",
							},
						},
						VersionedResources: map[string][]metav1.APIResource{
							"v1": {
								{
									Name:       "configmaps",
									Kind:       "ConfigMap",
									Namespaced: true,
									Verbs:      []string{"list", "watch", "get"},
								},
							},
						},
					},
				}
				return restmapper.NewDiscoveryRESTMapper(groupResources)
			},
			expected: true,
		},
		{
			name: "returns false when resource is disabled",
			gvr: schema.GroupVersionResource{
				Group:    "",
				Version:  "v1",
				Resource: "secrets",
			},
			resourceConfig: func() *utils.ResourceConfig {
				rc := utils.NewResourceConfig(false)
				_ = rc.Parse("v1/Secret")
				return rc
			}(),
			setupMapper: func() meta.RESTMapper {
				groupResources := []*restmapper.APIGroupResources{
					{
						Group: metav1.APIGroup{
							Name: "",
							Versions: []metav1.GroupVersionForDiscovery{
								{GroupVersion: "v1", Version: "v1"},
							},
							PreferredVersion: metav1.GroupVersionForDiscovery{
								GroupVersion: "v1",
								Version:      "v1",
							},
						},
						VersionedResources: map[string][]metav1.APIResource{
							"v1": {
								{
									Name:       "secrets",
									Kind:       "Secret",
									Namespaced: true,
									Verbs:      []string{"list", "watch", "get"},
								},
							},
						},
					},
				}
				return restmapper.NewDiscoveryRESTMapper(groupResources)
			},
			expected: false,
		},
		{
			name: "returns false when GVR mapping fails",
			gvr: schema.GroupVersionResource{
				Group:    "invalid.group",
				Version:  "v1",
				Resource: "nonexistent",
			},
			resourceConfig: utils.NewResourceConfig(false),
			setupMapper: func() meta.RESTMapper {
				// Empty mapper - will fail to map the GVR
				groupResources := []*restmapper.APIGroupResources{}
				return restmapper.NewDiscoveryRESTMapper(groupResources)
			},
			expected: false,
		},
		{
			name: "handles apps group resources correctly",
			gvr: schema.GroupVersionResource{
				Group:    "apps",
				Version:  "v1",
				Resource: "deployments",
			},
			resourceConfig: nil,
			setupMapper: func() meta.RESTMapper {
				groupResources := []*restmapper.APIGroupResources{
					{
						Group: metav1.APIGroup{
							Name: "apps",
							Versions: []metav1.GroupVersionForDiscovery{
								{GroupVersion: "apps/v1", Version: "v1"},
							},
							PreferredVersion: metav1.GroupVersionForDiscovery{
								GroupVersion: "apps/v1",
								Version:      "v1",
							},
						},
						VersionedResources: map[string][]metav1.APIResource{
							"v1": {
								{
									Name:       "deployments",
									Kind:       "Deployment",
									Namespaced: true,
									Verbs:      []string{"list", "watch", "get"},
								},
							},
						},
					},
				}
				return restmapper.NewDiscoveryRESTMapper(groupResources)
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restMapper := tt.setupMapper()
			result := shouldWatchResource(tt.gvr, restMapper, tt.resourceConfig)
			if result != tt.expected {
				t.Errorf("shouldWatchResource() = %v, want %v", result, tt.expected)
			}
		})
	}
}
