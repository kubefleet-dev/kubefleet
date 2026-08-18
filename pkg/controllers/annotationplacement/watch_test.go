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

package annotationplacement

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"

	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
)

// generatedPolicyObject builds a policy as the dynamic informer would deliver it, owned by the
// given references.
func generatedPolicyObject(namespace, name, resourceVersion string, owners ...metav1.OwnerReference) *unstructured.Unstructured {
	policy := &unstructured.Unstructured{}
	policy.SetGroupVersionKind(kfplacementv1alpha1.GroupVersion.WithKind("PlacementPolicy"))
	policy.SetNamespace(namespace)
	policy.SetName(name)
	policy.SetResourceVersion(resourceVersion)
	policy.SetOwnerReferences(owners)
	return policy
}

func ownerRef(apiVersion, kind, name string) metav1.OwnerReference {
	return metav1.OwnerReference{APIVersion: apiVersion, Kind: kind, Name: name, UID: "00000000-0000-0000-0000-000000000001"}
}

// enqueuedIdentity is what these tests compare: the full identity of an enqueued resource, since
// getting the namespace or the group wrong sends the reconciliation to the wrong object entirely.
type enqueuedIdentity struct {
	APIVersion, Kind, Namespace, Name string
}

func identityOf(obj interface{}) enqueuedIdentity {
	source := obj.(*unstructured.Unstructured)
	return enqueuedIdentity{
		APIVersion: source.GetAPIVersion(),
		Kind:       source.GetKind(),
		Namespace:  source.GetNamespace(),
		Name:       source.GetName(),
	}
}

func TestGeneratedPolicyEventHandler(t *testing.T) {
	deploymentOwner := ownerRef("apps/v1", "Deployment", "web")
	coreOwner := ownerRef("v1", "ConfigMap", "app")

	testCases := []struct {
		name string
		// event drives the handler under test.
		event func(cache.ResourceEventHandler)
		want  []enqueuedIdentity
	}{
		{
			name: "a policy created enqueues its owner",
			event: func(h cache.ResourceEventHandler) {
				h.OnAdd(generatedPolicyObject("prod", "deployment-web-abc", "1", deploymentOwner), false)
			},
			want: []enqueuedIdentity{{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "prod", Name: "web"}},
		},
		{
			name: "a core group owner keeps its empty group",
			event: func(h cache.ResourceEventHandler) {
				h.OnAdd(generatedPolicyObject("prod", "configmap-app-abc", "1", coreOwner), false)
			},
			want: []enqueuedIdentity{{APIVersion: "v1", Kind: "ConfigMap", Namespace: "prod", Name: "app"}},
		},
		{
			// The drift event this watch exists for: someone edits the generated policy.
			name: "a changed policy enqueues the owners of both sides",
			event: func(h cache.ResourceEventHandler) {
				h.OnUpdate(
					generatedPolicyObject("prod", "deployment-web-abc", "1", deploymentOwner),
					generatedPolicyObject("prod", "deployment-web-abc", "2", coreOwner),
				)
			},
			want: []enqueuedIdentity{
				{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "prod", Name: "web"},
				{APIVersion: "v1", Kind: "ConfigMap", Namespace: "prod", Name: "app"},
			},
		},
		{
			name: "a resync is not a change",
			event: func(h cache.ResourceEventHandler) {
				h.OnUpdate(
					generatedPolicyObject("prod", "deployment-web-abc", "1", deploymentOwner),
					generatedPolicyObject("prod", "deployment-web-abc", "1", deploymentOwner),
				)
			},
			want: nil,
		},
		{
			// The other drift event: someone deletes the generated policy from under a live resource.
			name: "a deleted policy enqueues its owner",
			event: func(h cache.ResourceEventHandler) {
				h.OnDelete(generatedPolicyObject("prod", "deployment-web-abc", "1", deploymentOwner))
			},
			want: []enqueuedIdentity{{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "prod", Name: "web"}},
		},
		{
			name: "a deleted policy arriving as a tombstone still enqueues its owner",
			event: func(h cache.ResourceEventHandler) {
				h.OnDelete(cache.DeletedFinalStateUnknown{
					Key: "prod/deployment-web-abc",
					Obj: generatedPolicyObject("prod", "deployment-web-abc", "1", deploymentOwner),
				})
			},
			want: []enqueuedIdentity{{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "prod", Name: "web"}},
		},
		{
			// A hand-authored policy carries no owner references, and must not cost anything.
			name: "a policy with no owners enqueues nothing",
			event: func(h cache.ResourceEventHandler) {
				h.OnAdd(generatedPolicyObject("prod", "hand-authored", "1"), false)
			},
			want: nil,
		},
		{
			name: "a cluster scoped policy enqueues a cluster scoped owner",
			event: func(h cache.ResourceEventHandler) {
				h.OnAdd(generatedPolicyObject("", "namespace-team-abc", "1", ownerRef("v1", "Namespace", "team")), false)
			},
			want: []enqueuedIdentity{{APIVersion: "v1", Kind: "Namespace", Namespace: "", Name: "team"}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var got []enqueuedIdentity
			handler := NewGeneratedPolicyEventHandler(func(obj interface{}) {
				got = append(got, identityOf(obj))
			})

			tc.event(handler)

			if diff := cmp.Diff(got, tc.want, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("enqueued resources mismatch (-got, +want):\n%s", diff)
			}
		})
	}
}
