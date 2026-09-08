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

package placementpolicy

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
)

func claimWithRef(ref *kfplacementv1alpha1.ObjectReference) *kfplacementv1alpha1.ClusterClaim {
	return &kfplacementv1alpha1.ClusterClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim"},
		Spec:       kfplacementv1alpha1.ClusterClaimSpec{PlacementPolicyRef: ref},
	}
}

// TestMapClaimToPolicies pins the claim-to-policy mapping to spec.placementPolicyRef, which is
// the only identity that survives policy names longer than a label value can hold.
func TestMapClaimToPolicies(t *testing.T) {
	longName := strings.Repeat("x", 250)

	testCases := []struct {
		name              string
		obj               client.Object
		wantNamespaced    []reconcile.Request
		wantClusterScoped []reconcile.Request
	}{
		{
			name:           "namespaced policy reference",
			obj:            claimWithRef(&kfplacementv1alpha1.ObjectReference{Name: "app", Namespace: "tenant-a"}),
			wantNamespaced: []reconcile.Request{{NamespacedName: types.NamespacedName{Name: "app", Namespace: "tenant-a"}}},
		},
		{
			name:              "cluster-scoped policy reference",
			obj:               claimWithRef(&kfplacementv1alpha1.ObjectReference{Name: "app"}),
			wantClusterScoped: []reconcile.Request{{NamespacedName: types.NamespacedName{Name: "app"}}},
		},
		{
			name:           "name longer than a label value still maps",
			obj:            claimWithRef(&kfplacementv1alpha1.ObjectReference{Name: longName, Namespace: "tenant-a"}),
			wantNamespaced: []reconcile.Request{{NamespacedName: types.NamespacedName{Name: longName, Namespace: "tenant-a"}}},
		},
		{
			name: "claim without a policy reference maps to nothing",
			obj:  claimWithRef(nil),
		},
		{
			name: "reference without a name maps to nothing",
			obj:  claimWithRef(&kfplacementv1alpha1.ObjectReference{Namespace: "tenant-a"}),
		},
		{
			name: "object of another kind maps to nothing",
			obj:  &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm", Namespace: "tenant-a"}},
		},
	}

	r := &Reconciler{}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotNamespaced := r.mapClaimToPlacementPolicy(context.Background(), tc.obj)
			if diff := cmp.Diff(gotNamespaced, tc.wantNamespaced); diff != "" {
				t.Errorf("mapClaimToPlacementPolicy() mismatch (-got, +want):\n%s", diff)
			}
			gotClusterScoped := r.mapClaimToClusterPlacementPolicy(context.Background(), tc.obj)
			if diff := cmp.Diff(gotClusterScoped, tc.wantClusterScoped); diff != "" {
				t.Errorf("mapClaimToClusterPlacementPolicy() mismatch (-got, +want):\n%s", diff)
			}
		})
	}
}
