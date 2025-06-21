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
	"errors"
	"fmt"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/scheduler/queue"
)

func TestListBindingsFromKey(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name         string
		placementKey queue.PlacementKey
		objects      []client.Object
		wantErr      bool
		wantCount    int
		errType      string
	}{
		{
			name:         "cluster-scoped placement key - no bindings found",
			placementKey: queue.PlacementKey("test-placement"),
			objects:      []client.Object{},
			wantErr:      false,
			wantCount:    0,
		},
		{
			name:         "cluster-scoped placement key - single binding found",
			placementKey: queue.PlacementKey("test-placement"),
			objects: []client.Object{
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-binding-1",
						Labels: map[string]string{
							placementv1beta1.CRPTrackingLabel: "test-placement",
						},
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster: "cluster-1",
					},
				},
			},
			wantErr:   false,
			wantCount: 1,
		},
		{
			name:         "cluster-scoped placement key - multiple bindings found",
			placementKey: queue.PlacementKey("test-placement"),
			objects: []client.Object{
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-binding-1",
						Labels: map[string]string{
							placementv1beta1.CRPTrackingLabel: "test-placement",
						},
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster: "cluster-1",
					},
				},
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-binding-2",
						Labels: map[string]string{
							placementv1beta1.CRPTrackingLabel: "test-placement",
						},
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster: "cluster-2",
					},
				},
			},
			wantErr:   false,
			wantCount: 2,
		},
		{
			name:         "cluster-scoped placement key - excludes non-matching bindings",
			placementKey: queue.PlacementKey("test-placement"),
			objects: []client.Object{
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-binding-1",
						Labels: map[string]string{
							placementv1beta1.CRPTrackingLabel: "test-placement",
						},
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster: "cluster-1",
					},
				},
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name: "other-binding",
						Labels: map[string]string{
							placementv1beta1.CRPTrackingLabel: "other-placement",
						},
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster: "cluster-2",
					},
				},
			},
			wantErr:   false,
			wantCount: 1,
		},
		{
			name:         "namespaced placement key - single binding found",
			placementKey: queue.PlacementKey("test-namespace/test-placement"),
			objects: []client.Object{
				&placementv1beta1.ResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-binding-1",
						Namespace: "test-namespace",
						Labels: map[string]string{
							placementv1beta1.CRPTrackingLabel: "test-namespace/test-placement",
						},
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster: "cluster-1",
					},
				},
			},
			wantErr:   false,
			wantCount: 1,
		},
		{
			name:         "namespaced placement key - excludes wrong namespace",
			placementKey: queue.PlacementKey("test-namespace/test-placement"),
			objects: []client.Object{
				&placementv1beta1.ResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-binding-1",
						Namespace: "test-namespace",
						Labels: map[string]string{
							placementv1beta1.CRPTrackingLabel: "test-namespace/test-placement",
						},
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster: "cluster-1",
					},
				},
				&placementv1beta1.ResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "other-binding",
						Namespace: "other-namespace",
						Labels: map[string]string{
							placementv1beta1.CRPTrackingLabel: "test-namespace/test-placement",
						},
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster: "cluster-2",
					},
				},
			},
			wantErr:   false,
			wantCount: 1,
		},
		{
			name:         "invalid placement key format - too many separators",
			placementKey: queue.PlacementKey("namespace/placement/extra"),
			objects:      []client.Object{},
			wantErr:      true,
			errType:      "UnexpectedBehaviorError",
		},
		{
			name:         "invalid placement key format - empty parts",
			placementKey: queue.PlacementKey("namespace/"),
			objects:      []client.Object{},
			wantErr:      true,
			errType:      "UnexpectedBehaviorError",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = placementv1beta1.AddToScheme(scheme)

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.objects...).
				Build()

			got, err := ListBindingsFromKey(ctx, fakeClient, tt.placementKey)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("Expected error but got nil")
				}
				if tt.errType == "UnexpectedBehaviorError" {
					if !errors.Is(err, ErrUnexpectedBehavior) {
						t.Errorf("Expected ErrUnexpectedBehavior but got: %v", err)
					}
					if !strings.Contains(err.Error(), "invalid placement key format") {
						t.Errorf("Expected error message to contain 'invalid placement key format' but got: %s", err.Error())
					}
				}
				return
			}

			if err != nil {
				t.Fatalf("Expected no error but got: %v", err)
			}

			if len(got) != tt.wantCount {
				t.Errorf("Expected %d bindings but got %d", tt.wantCount, len(got))
			}

			// Verify that returned bindings have correct labels
			for _, binding := range got {
				labels := binding.GetLabels()
				if labels[placementv1beta1.CRPTrackingLabel] != string(tt.placementKey) {
					t.Errorf("Expected CRPTrackingLabel to be %s but got %s", string(tt.placementKey), labels[placementv1beta1.CRPTrackingLabel])
				}
			}
		})
	}
}

func TestListBindingsFromKey_ClientError(t *testing.T) {
	ctx := context.Background()

	// Create a client that will return an error
	scheme := runtime.NewScheme()
	_ = placementv1beta1.AddToScheme(scheme)

	// Use a fake client but override List to return error
	fakeClient := &failingClient{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
	}

	_, err := ListBindingsFromKey(ctx, fakeClient, queue.PlacementKey("test-placement"))

	if err == nil {
		t.Fatalf("Expected error but got nil")
	}

	if !errors.Is(err, ErrAPIServerError) {
		t.Errorf("Expected ErrAPIServerError but got: %v", err)
	}
}

// failingClient is a test helper that wraps a client and makes List calls fail
type failingClient struct {
	client.Client
}

func (c *failingClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return fmt.Errorf("simulated client error")
}

func TestFetchBindingFromKey(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name         string
		placementKey queue.PlacementKey
		objects      []client.Object
		wantErr      bool
		wantType     string // "ClusterResourceBinding" or "ResourceBinding"
		errType      string
	}{
		{
			name:         "cluster-scoped binding - found",
			placementKey: queue.PlacementKey("test-binding"),
			objects: []client.Object{
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-binding",
						Labels: map[string]string{
							placementv1beta1.CRPTrackingLabel: "test-placement",
						},
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster: "cluster-1",
					},
				},
			},
			wantErr:  false,
			wantType: "ClusterResourceBinding",
		},
		{
			name:         "cluster-scoped binding - not found",
			placementKey: queue.PlacementKey("nonexistent-binding"),
			objects:      []client.Object{},
			wantErr:      true,
		},
		{
			name:         "namespaced binding - found",
			placementKey: queue.PlacementKey("test-namespace/test-binding"),
			objects: []client.Object{
				&placementv1beta1.ResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-binding",
						Namespace: "test-namespace",
						Labels: map[string]string{
							placementv1beta1.CRPTrackingLabel: "test-placement",
						},
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster: "cluster-1",
					},
				},
			},
			wantErr:  false,
			wantType: "ResourceBinding",
		},
		{
			name:         "namespaced binding - not found",
			placementKey: queue.PlacementKey("test-namespace/nonexistent-binding"),
			objects:      []client.Object{},
			wantErr:      true,
		},
		{
			name:         "namespaced binding - wrong namespace",
			placementKey: queue.PlacementKey("wrong-namespace/test-binding"),
			objects: []client.Object{
				&placementv1beta1.ResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-binding",
						Namespace: "test-namespace", // different namespace
						Labels: map[string]string{
							placementv1beta1.CRPTrackingLabel: "test-placement",
						},
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster: "cluster-1",
					},
				},
			},
			wantErr: true,
		},
		{
			name:         "invalid placement key format - too many separators",
			placementKey: queue.PlacementKey("namespace/binding/extra"),
			objects:      []client.Object{},
			wantErr:      true,
			errType:      "UnexpectedBehaviorError",
		},
		{
			name:         "invalid placement key format - empty parts",
			placementKey: queue.PlacementKey("namespace/"),
			objects:      []client.Object{},
			wantErr:      true,
			errType:      "UnexpectedBehaviorError",
		},
		{
			name:         "invalid placement key format - empty namespace",
			placementKey: queue.PlacementKey("/binding"),
			objects:      []client.Object{},
			wantErr:      true,
			errType:      "UnexpectedBehaviorError",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = placementv1beta1.AddToScheme(scheme)

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.objects...).
				Build()

			got, err := FetchBindingFromKey(ctx, fakeClient, tt.placementKey)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("Expected error but got nil")
				}
				if tt.errType == "UnexpectedBehaviorError" {
					if !errors.Is(err, ErrUnexpectedBehavior) {
						t.Errorf("Expected ErrUnexpectedBehavior but got: %v", err)
					}
					if !strings.Contains(err.Error(), "invalid placement key format") {
						t.Errorf("Expected error message to contain 'invalid placement key format' but got: %s", err.Error())
					}
				}
				return
			}

			if err != nil {
				t.Fatalf("Expected no error but got: %v", err)
			}

			if got == nil {
				t.Fatalf("Expected binding but got nil")
			}

			// Check the concrete type
			switch tt.wantType {
			case "ClusterResourceBinding":
				if _, ok := got.(*placementv1beta1.ClusterResourceBinding); !ok {
					t.Errorf("Expected type ClusterResourceBinding, but got: %T", got)
				}
				// Verify it's cluster-scoped (no namespace)
				if got.GetNamespace() != "" {
					t.Errorf("Expected cluster-scoped binding (empty namespace), but got namespace: %s", got.GetNamespace())
				}
			case "ResourceBinding":
				if _, ok := got.(*placementv1beta1.ResourceBinding); !ok {
					t.Errorf("Expected type ResourceBinding, but got: %T", got)
				}
				// Verify it's namespaced
				if got.GetNamespace() == "" {
					t.Errorf("Expected namespaced binding, but got cluster-scoped binding")
				}
			default:
				if tt.wantType != "" {
					t.Errorf("Unexpected wantType: %s", tt.wantType)
				}
			}

			// Verify the binding name matches what we expect from the placement key
			keyStr := string(tt.placementKey)
			expectedName := keyStr
			if strings.Contains(keyStr, "/") {
				parts := strings.Split(keyStr, "/")
				expectedName = parts[1] // name part for namespaced resources
			}
			if got.GetName() != expectedName {
				t.Errorf("Expected binding name %s, but got %s", expectedName, got.GetName())
			}
		})
	}
}

func TestFetchBindingFromKey_ClientError(t *testing.T) {
	ctx := context.Background()

	// Create a client that will return an error
	scheme := runtime.NewScheme()
	_ = placementv1beta1.AddToScheme(scheme)

	// Use a failing client for Get operations
	fakeClient := &failingGetClient{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
	}

	_, err := FetchBindingFromKey(ctx, fakeClient, queue.PlacementKey("test-binding"))

	if err == nil {
		t.Fatalf("Expected error but got nil")
	}

	// The error should be the simulated client error, not wrapped as APIServerError
	// since FetchBindingFromKey doesn't wrap the Get errors
	if !strings.Contains(err.Error(), "simulated get error") {
		t.Errorf("Expected error to contain 'simulated get error' but got: %s", err.Error())
	}
}

// failingGetClient is a test helper that wraps a client and makes Get calls fail
type failingGetClient struct {
	client.Client
}

func (c *failingGetClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return fmt.Errorf("simulated get error")
}
