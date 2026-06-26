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

package overrider

import (
	"context"
	stderrors "errors"
	"fmt"
	"strconv"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/controller"
)

// TestEnsureResourceOverrideSnapshotAlreadyExistsDelete is the namespaced mirror of the CRO
// AlreadyExists Delete-branch coverage; see clusterresource_controller_test.go for the rationale.
func TestEnsureResourceOverrideSnapshotAlreadyExistsDelete(t *testing.T) {
	t.Parallel()
	s := runtime.NewScheme()
	if err := placementv1beta1.AddToScheme(s); err != nil {
		t.Fatalf("scheme: %v", err)
	}
	tests := []struct {
		name           string
		deleteErr      error
		wantSentinel   error
		wantSnapDelete bool // whether the controller is expected to attempt a Delete on the mismatched snapshot
	}{
		{
			name:           "delete succeeds returns expected-behavior error",
			wantSentinel:   controller.ErrExpectedBehavior,
			wantSnapDelete: true,
		},
		{
			name: "delete returning IsNotFound is swallowed and still returns expected-behavior error",
			deleteErr: apierrors.NewNotFound(
				schema.GroupResource{Group: placementv1beta1.GroupVersion.Group, Resource: "resourceoverridesnapshots"},
				"already-gone"),
			wantSentinel:   controller.ErrExpectedBehavior,
			wantSnapDelete: true,
		},
		{
			name:           "delete returning non-NotFound error returns API server error",
			deleteErr:      apierrors.NewInternalError(fmt.Errorf("simulated transient")),
			wantSentinel:   controller.ErrAPIServerError,
			wantSnapDelete: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ns := "ae-ns"
			ro := &placementv1beta1.ResourceOverride{
				TypeMeta: metav1.TypeMeta{
					APIVersion: placementv1beta1.GroupVersion.String(),
					Kind:       placementv1beta1.ResourceOverrideKind,
				},
				ObjectMeta: metav1.ObjectMeta{Name: "ae-ro", Namespace: ns, UID: "fake-uid"},
				Spec: placementv1beta1.ResourceOverrideSpec{
					ResourceSelectors: []placementv1beta1.ResourceSelector{
						{Group: "", Version: "v1", Kind: "ConfigMap", Name: "cm"},
					},
				},
			}

			snap0 := &placementv1beta1.ResourceOverrideSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf(placementv1beta1.OverrideSnapshotNameFmt, ro.Name, 0),
					Namespace: ns,
					Labels: map[string]string{
						placementv1beta1.OverrideTrackingLabel: ro.Name,
						placementv1beta1.IsLatestSnapshotLabel: strconv.FormatBool(true),
						placementv1beta1.OverrideIndexLabel:    "0",
					},
				},
				Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
					OverrideSpec: ro.Spec,
					OverrideHash: []byte("stale-hash"),
				},
			}
			snap1 := &placementv1beta1.ResourceOverrideSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf(placementv1beta1.OverrideSnapshotNameFmt, ro.Name, 1),
					Namespace: ns,
					Labels: map[string]string{
						placementv1beta1.IsLatestSnapshotLabel: strconv.FormatBool(true),
						placementv1beta1.OverrideIndexLabel:    "1",
					},
				},
				Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
					OverrideHash: []byte("different-hash"),
				},
			}

			deleteCalls := 0
			interceptors := interceptor.Funcs{
				Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					if _, ok := obj.(*placementv1beta1.ResourceOverrideSnapshot); ok && obj.GetName() == snap1.Name {
						deleteCalls++
						if tc.deleteErr != nil {
							return tc.deleteErr
						}
					}
					return c.Delete(ctx, obj, opts...)
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(s).
				WithObjects(snap0, snap1).
				WithInterceptorFuncs(interceptors).
				Build()

			r := &ResourceReconciler{
				Reconciler: Reconciler{
					Client:         fakeClient,
					UncachedReader: fakeClient,
					recorder:       record.NewFakeRecorder(10),
				},
			}

			err := r.ensureResourceOverrideSnapshot(context.Background(), ro, 10)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !stderrors.Is(err, tc.wantSentinel) {
				t.Errorf("error = %v, want sentinel %v", err, tc.wantSentinel)
			}
			if tc.wantSnapDelete && deleteCalls == 0 {
				t.Errorf("expected the controller to attempt deletion of the mismatched snapshot, but it didn't")
			}
		})
	}
}
