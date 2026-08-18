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
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	fleetv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/controller"
)

func TestHandleTombStoneObj(t *testing.T) {
	var (
		secretObj = &corev1.Secret{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Secret",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "foo",
				Name:      "bar",
			},
		}
		clusterRoleObj = &rbacv1.ClusterRole{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Role",
				APIVersion: "rbac.authorization.k8s.io/v1beta1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "bar",
			},
		}

		deletedRole = &rbacv1.Role{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Role",
				APIVersion: "rbac.authorization.k8s.io/v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "foo",
				Name:      "bar",
			},
		}
	)
	tests := []struct {
		name    string
		object  interface{}
		wantErr bool
		want    client.Object
	}{
		{
			name:    "namespace scoped resource in core group",
			object:  secretObj,
			wantErr: false,
			want:    secretObj,
		},
		{
			name:    "cluster scoped resource",
			object:  clusterRoleObj,
			wantErr: false,
			want:    clusterRoleObj,
		},
		{
			name: "tomestone object",
			object: cache.DeletedFinalStateUnknown{
				Key: "foo",
				Obj: deletedRole,
			},
			wantErr: false,
			want:    deletedRole,
		},
		{
			name: "none runtime object should be error",
			object: fleetv1beta1.ResourceIdentifier{
				Namespace: "foo",
				Name:      "bar",
			},
			wantErr: true,
		},
		{
			name:    "nil object should be error",
			object:  nil,
			wantErr: true,
		},
	}

	for _, test := range tests {
		tt := test
		t.Run(tt.name, func(t *testing.T) {
			got, err := handleTombStoneObj(tt.object)
			if (err != nil) != tt.wantErr {
				t.Errorf("handleTombStoneObj() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("handleTombStoneObj() got = %v, want %v", got, tt.want)
			}
		})
	}
}

var _ controller.Controller = &fakeController{}

// fakeController just record if there is an enqueue request or not
type fakeController struct {
	Enqueued bool
}

func (t *fakeController) Enqueue(_ interface{}) {
	t.Enqueued = true
}

// Run is a no-op; the fake is only used to verify that Enqueue is called.
func (t *fakeController) Run(_ context.Context, _ int) error {
	return nil
}

var _ controller.Controller = &recordingController{}

// recordingController stands in for a real controller queue, remembering what was handed to it.
type recordingController struct {
	mu      sync.Mutex
	objects []interface{}
}

func (c *recordingController) Enqueue(obj interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.objects = append(c.objects, obj)
}

func (c *recordingController) Run(context.Context, int) error { return nil }

// names returns the name of every object enqueued, which is enough to tell the objects in these
// tests apart.
func (c *recordingController) names(t *testing.T) []string {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	names := make([]string, 0, len(c.objects))
	for _, obj := range c.objects {
		accessor, err := meta.Accessor(obj)
		if err != nil {
			t.Fatalf("meta.Accessor(%v) = %v, want no error", obj, err)
		}
		names = append(names, accessor.GetName())
	}
	return names
}

// watchedResource builds a resource of the kind the dynamic informers hand to the event handlers.
func watchedResource(name, resourceVersion string, annotated bool) *unstructured.Unstructured {
	object := &unstructured.Unstructured{}
	object.SetGroupVersionKind(deploymentGVK())
	object.SetNamespace("prod")
	object.SetName(name)
	object.SetResourceVersion(resourceVersion)
	if annotated {
		object.SetAnnotations(map[string]string{kfplacementv1alpha1.ClusterSelectorsAnnotation: "env=staging"})
	}
	return object
}

func deploymentGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
}

// TestEventHandlersEnqueueForAnnotationPlacement covers which events reach the annotation-based
// placement controller. Getting this wrong is not visible in the controller itself: an event that is
// never enqueued leaves the generated policy exactly as it was, with nothing to show that anything
// was missed.
func TestEventHandlersEnqueueForAnnotationPlacement(t *testing.T) {
	testCases := []struct {
		name string
		// event runs the handler under test against the detector.
		event func(*ChangeDetector)
		// wantResourceChange and wantAnnotationPlacement hold the names enqueued to each queue.
		wantResourceChange      []string
		wantAnnotationPlacement []string
	}{
		{
			name: "an added resource with the annotation is enqueued to both",
			event: func(d *ChangeDetector) {
				d.onResourceAdded(watchedResource("annotated", "1", true))
			},
			wantResourceChange:      []string{"annotated"},
			wantAnnotationPlacement: []string{"annotated"},
		},
		{
			name: "an added resource without the annotation is kept out of the placement queue",
			event: func(d *ChangeDetector) {
				d.onResourceAdded(watchedResource("plain", "1", false))
			},
			wantResourceChange:      []string{"plain"},
			wantAnnotationPlacement: nil,
		},
		{
			// The event this whole filter has to get right: looking only at the new object would
			// drop it, and the policy generated from the old annotation would never be deleted.
			name: "removing the annotation is still enqueued",
			event: func(d *ChangeDetector) {
				d.onResourceUpdated(watchedResource("annotated", "1", true), watchedResource("annotated", "2", false))
			},
			wantResourceChange:      []string{"annotated"},
			wantAnnotationPlacement: []string{"annotated"},
		},
		{
			name: "adding the annotation is enqueued",
			event: func(d *ChangeDetector) {
				d.onResourceUpdated(watchedResource("annotated", "1", false), watchedResource("annotated", "2", true))
			},
			wantResourceChange:      []string{"annotated"},
			wantAnnotationPlacement: []string{"annotated"},
		},
		{
			name: "an update to a resource that never carried the annotation is kept out",
			event: func(d *ChangeDetector) {
				d.onResourceUpdated(watchedResource("plain", "1", false), watchedResource("plain", "2", false))
			},
			wantResourceChange:      []string{"plain"},
			wantAnnotationPlacement: nil,
		},
		{
			name: "an update that changed nothing is enqueued nowhere",
			event: func(d *ChangeDetector) {
				d.onResourceUpdated(watchedResource("annotated", "1", true), watchedResource("annotated", "1", true))
			},
			wantResourceChange:      nil,
			wantAnnotationPlacement: nil,
		},
		{
			// Garbage collection removes the generated policy through its owner reference, so a
			// reconcile here could only look for a resource that is already gone.
			name: "a deleted resource is left to garbage collection",
			event: func(d *ChangeDetector) {
				d.onResourceDeleted(watchedResource("annotated", "1", true))
			},
			wantResourceChange:      []string{"annotated"},
			wantAnnotationPlacement: nil,
		},
		{
			name: "a deleted resource arriving as a tombstone is left to garbage collection",
			event: func(d *ChangeDetector) {
				d.onResourceDeleted(cache.DeletedFinalStateUnknown{Key: "prod/annotated", Obj: watchedResource("annotated", "1", true)})
			},
			wantResourceChange:      []string{"annotated"},
			wantAnnotationPlacement: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resourceChange, annotationPlacement := &recordingController{}, &recordingController{}
			detector := &ChangeDetector{
				ResourceChangeController:      resourceChange,
				AnnotationPlacementController: annotationPlacement,
			}

			tc.event(detector)

			if diff := cmp.Diff(resourceChange.names(t), tc.wantResourceChange, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("resource change queue mismatch (-got, +want):\n%s", diff)
			}
			if diff := cmp.Diff(annotationPlacement.names(t), tc.wantAnnotationPlacement, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("annotation placement queue mismatch (-got, +want):\n%s", diff)
			}
		})
	}
}

// TestEventHandlersWithAnnotationPlacementDisabled covers the hub agent running without the feature,
// where the controller is nil. Every event still has to reach the resource change controller.
func TestEventHandlersWithAnnotationPlacementDisabled(t *testing.T) {
	resourceChange := &recordingController{}
	detector := &ChangeDetector{ResourceChangeController: resourceChange}

	annotated := watchedResource("annotated", "1", true)
	detector.onResourceAdded(annotated)
	detector.onResourceUpdated(annotated, watchedResource("annotated", "2", false))
	detector.onResourceDeleted(annotated)

	want := []string{"annotated", "annotated", "annotated"}
	if diff := cmp.Diff(resourceChange.names(t), want); diff != "" {
		t.Errorf("resource change queue mismatch (-got, +want):\n%s", diff)
	}
}
