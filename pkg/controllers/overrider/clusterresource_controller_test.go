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

// TestEnsureClusterResourceOverrideSnapshotAlreadyExistsDelete uses a fake client with Delete
// interceptors to exercise the three Delete branches inside the AlreadyExists hash-mismatch
// recovery path, which envtest can't reach reliably (real Delete almost always succeeds, so the
// non-NotFound error branch is unreachable there).
//
// The flow: pre-create snapshot 0 (current "latest" with a stale hash) and a hidden snapshot 1
// (no tracking label, divergent hash) so the controller hits the AlreadyExists path on Create
// and then the hash-mismatch branch on the subsequent Get.
func TestEnsureClusterResourceOverrideSnapshotAlreadyExistsDelete(t *testing.T) {
	s := runtime.NewScheme()
	if err := placementv1beta1.AddToScheme(s); err != nil {
		t.Fatalf("scheme: %v", err)
	}

	tests := []struct {
		name           string
		deleteErr      error
		wantSentinel   error
		wantSnapDelete bool // whether the fake client recorded a Delete call
	}{
		{
			name:           "delete succeeds returns expected-behavior error",
			deleteErr:      nil,
			wantSentinel:   controller.ErrExpectedBehavior,
			wantSnapDelete: true,
		},
		{
			name: "delete returning IsNotFound is swallowed and still returns expected-behavior error",
			deleteErr: apierrors.NewNotFound(
				schema.GroupResource{Group: placementv1beta1.GroupVersion.Group, Resource: "clusterresourceoverridesnapshots"},
				"already-gone"),
			wantSentinel:   controller.ErrExpectedBehavior,
			wantSnapDelete: true,
		},
		{
			name:         "delete returning non-NotFound error returns API server error",
			deleteErr:    apierrors.NewInternalError(fmt.Errorf("simulated transient")),
			wantSentinel: controller.ErrAPIServerError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cro := &placementv1beta1.ClusterResourceOverride{
				TypeMeta: metav1.TypeMeta{
					APIVersion: placementv1beta1.GroupVersion.String(),
					Kind:       placementv1beta1.ClusterResourceOverrideKind,
				},
				ObjectMeta: metav1.ObjectMeta{Name: "ae-cro", UID: "fake-uid"},
				Spec: placementv1beta1.ClusterResourceOverrideSpec{
					ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
						{Group: "", Version: "v1", Kind: "Namespace", Name: "ae-ns"},
					},
				},
			}

			// Pre-existing snapshot 0 with an old hash and the tracking label so list returns it.
			snap0 := &placementv1beta1.ClusterResourceOverrideSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf(placementv1beta1.OverrideSnapshotNameFmt, cro.Name, 0),
					Labels: map[string]string{
						placementv1beta1.OverrideTrackingLabel: cro.Name,
						placementv1beta1.IsLatestSnapshotLabel: strconv.FormatBool(true),
						placementv1beta1.OverrideIndexLabel:    "0",
					},
				},
				Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
					OverrideSpec: cro.Spec,
					OverrideHash: []byte("stale-hash-not-matching-current-spec"),
				},
			}
			// Pre-existing snapshot 1 invisible to listSortedOverrideSnapshots (no tracking label),
			// with a hash that does NOT match the current spec — triggers the mismatch branch.
			snap1 := &placementv1beta1.ClusterResourceOverrideSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf(placementv1beta1.OverrideSnapshotNameFmt, cro.Name, 1),
					Labels: map[string]string{
						placementv1beta1.IsLatestSnapshotLabel: strconv.FormatBool(true),
						placementv1beta1.OverrideIndexLabel:    "1",
						// OverrideTrackingLabel deliberately omitted
					},
				},
				Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
					OverrideHash: []byte("different-hash-from-current-spec"),
				},
			}

			deleteCalls := 0
			interceptors := interceptor.Funcs{
				Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					if _, ok := obj.(*placementv1beta1.ClusterResourceOverrideSnapshot); ok && obj.GetName() == snap1.Name {
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

			r := &ClusterResourceReconciler{
				Reconciler: Reconciler{
					Client:         fakeClient,
					UncachedReader: fakeClient,
					recorder:       record.NewFakeRecorder(10),
				},
			}

			err := r.ensureClusterResourceOverrideSnapshot(context.Background(), cro, 10)
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
