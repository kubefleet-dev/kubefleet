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

package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	fleetv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/scheduler/queue"
)

func TestResolvePlacementFromKey(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, fleetv1beta1.AddToScheme(scheme))

	tests := []struct {
		name          string
		placementKey  queue.PlacementKey
		objects       []client.Object
		expectedType  string
		expectedName  string
		expectedNS    string
		expectCluster bool
		expectError   bool
	}{
		{
			name:         "cluster resource placement",
			placementKey: queue.PlacementKey("test-crp"),
			objects: []client.Object{
				&fleetv1beta1.ClusterResourcePlacement{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-crp",
					},
				},
			},
			expectedType:  "*v1beta1.ClusterResourcePlacement",
			expectedName:  "test-crp",
			expectedNS:    "",
			expectCluster: true,
			expectError:   false,
		},
		{
			name:         "namespaced resource placement",
			placementKey: queue.PlacementKey("test-ns/test-rp"),
			objects: []client.Object{
				&fleetv1beta1.ResourcePlacement{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-rp",
						Namespace: "test-ns",
					},
				},
			},
			expectedType:  "*v1beta1.ResourcePlacement",
			expectedName:  "test-rp",
			expectedNS:    "test-ns",
			expectCluster: false,
			expectError:   false,
		},
		{
			name:         "cluster resource placement not found",
			placementKey: queue.PlacementKey("nonexistent-crp"),
			objects:      []client.Object{},
			expectError:  true,
		},
		{
			name:         "namespaced resource placement not found",
			placementKey: queue.PlacementKey("test-ns/nonexistent-rp"),
			objects:      []client.Object{},
			expectError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.objects...).
				Build()

			placement, err := FetchPlacementFromKey(context.Background(), fakeClient, tt.placementKey)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, placement)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, placement)

			// Determine if this is a cluster-scoped placement based on namespace
			isCluster := placement.GetNamespace() == ""
			assert.Equal(t, tt.expectCluster, isCluster)
			assert.Equal(t, tt.expectedName, placement.GetName())
			assert.Equal(t, tt.expectedNS, placement.GetNamespace())

			// Check the concrete type
			switch tt.expectedType {
			case "*v1beta1.ClusterResourcePlacement":
				_, ok := placement.(*fleetv1beta1.ClusterResourcePlacement)
				assert.True(t, ok, "Expected ClusterResourcePlacement type")
			case "*v1beta1.ResourcePlacement":
				_, ok := placement.(*fleetv1beta1.ResourcePlacement)
				assert.True(t, ok, "Expected ResourcePlacement type")
			}
		})
	}
}

func TestGetPlacementKeyFromObj(t *testing.T) {
	tests := []struct {
		name        string
		placement   interface{}
		expectedKey queue.PlacementKey
	}{
		{
			name: "cluster resource placement",
			placement: &fleetv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-crp",
				},
			},
			expectedKey: queue.PlacementKey("test-crp"),
		},
		{
			name: "namespaced resource placement",
			placement: &fleetv1beta1.ResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rp",
					Namespace: "test-ns",
				},
			},
			expectedKey: queue.PlacementKey("test-ns/test-rp"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var placementObj fleetv1beta1.PlacementObj
			switch p := tt.placement.(type) {
			case *fleetv1beta1.ClusterResourcePlacement:
				placementObj = p
			case *fleetv1beta1.ResourcePlacement:
				placementObj = p
			}

			key := GetPlacementKeyFromObj(placementObj)
			assert.Equal(t, tt.expectedKey, key)
		})
	}
}
