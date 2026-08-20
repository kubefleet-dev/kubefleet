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
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"

	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
)

var configMapSourceGVK = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}

// generatedPolicyOwnedBy builds a policy as the informer would deliver it: correctly named for the
// resource that generated it, owned by that resource, plus any extra owner references a caller
// wants to plant. A policy whose name does not derive from an owner is that owner's, which is what
// lets the handler tell the true source from a foreign owner or a hand-authored policy.
func generatedPolicyOwnedBy(source *unstructured.Unstructured, resourceVersion string, extraOwners ...metav1.OwnerReference) *unstructured.Unstructured {
	namespace := source.GetNamespace()
	policy := &unstructured.Unstructured{}
	if namespace == "" {
		policy.SetGroupVersionKind(kfplacementv1alpha1.GroupVersion.WithKind("ClusterPlacementPolicy"))
	} else {
		policy.SetGroupVersionKind(kfplacementv1alpha1.GroupVersion.WithKind("PlacementPolicy"))
	}
	policy.SetNamespace(namespace)
	policy.SetName(generatedPolicyName(source.GroupVersionKind(), namespace, source.GetName()))
	policy.SetResourceVersion(resourceVersion)
	policy.SetOwnerReferences(append([]metav1.OwnerReference{parentOwnerReference(source)}, extraOwners...))
	return policy
}

// namedPolicy builds a policy with an explicit name (not derived from any owner), for the cases a
// generating source is not supposed to be found -- a hand-authored policy, or one whose only owners
// are foreign.
func namedPolicy(namespace, name, resourceVersion string, owners ...metav1.OwnerReference) *unstructured.Unstructured {
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
	deploymentSource := sourceObject(deploymentGVK, "prod", "web")
	configMapSource := sourceObject(configMapSourceGVK, "prod", "app")
	namespaceSource := sourceObject(namespaceGVK, "", "team")

	deploymentIdentity := enqueuedIdentity{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "prod", Name: "web"}

	testCases := []struct {
		name string
		// event drives the handler under test.
		event func(cache.ResourceEventHandler)
		want  []enqueuedIdentity
	}{
		{
			name: "a policy created enqueues the resource that generated it",
			event: func(h cache.ResourceEventHandler) {
				h.OnAdd(generatedPolicyOwnedBy(deploymentSource, "1"), false)
			},
			want: []enqueuedIdentity{deploymentIdentity},
		},
		{
			name: "a core group generating resource keeps its empty group",
			event: func(h cache.ResourceEventHandler) {
				h.OnAdd(generatedPolicyOwnedBy(configMapSource, "1"), false)
			},
			want: []enqueuedIdentity{{APIVersion: "v1", Kind: "ConfigMap", Namespace: "prod", Name: "app"}},
		},
		{
			// The finding this filter fixes: a foreign owner reference -- one applyDesiredPolicy
			// preserves, or any owner on a policy that merely shares these informers -- must not be
			// followed, or the watcher would enqueue a key for a kind it never selected.
			name: "a foreign owner reference is not followed",
			event: func(h cache.ResourceEventHandler) {
				h.OnAdd(generatedPolicyOwnedBy(deploymentSource, "1", ownerRef("v1", "Pod", "some-pod"), ownerRef("apps/v1", "Deployment", "unrelated")), false)
			},
			want: []enqueuedIdentity{deploymentIdentity},
		},
		{
			// The drift event this watch exists for: someone strips the generating owner reference.
			// The old side still names the source whose policy just lost its reference.
			name: "an owner reference removed on update is still enqueued from the old side",
			event: func(h cache.ResourceEventHandler) {
				withOwner := generatedPolicyOwnedBy(deploymentSource, "1")
				stripped := generatedPolicyOwnedBy(deploymentSource, "2")
				stripped.SetOwnerReferences(nil)
				h.OnUpdate(withOwner, stripped)
			},
			want: []enqueuedIdentity{deploymentIdentity},
		},
		{
			name: "a resync is not a change",
			event: func(h cache.ResourceEventHandler) {
				h.OnUpdate(generatedPolicyOwnedBy(deploymentSource, "1"), generatedPolicyOwnedBy(deploymentSource, "1"))
			},
			want: nil,
		},
		{
			name: "a deleted policy enqueues the resource that generated it",
			event: func(h cache.ResourceEventHandler) {
				h.OnDelete(generatedPolicyOwnedBy(deploymentSource, "1"))
			},
			want: []enqueuedIdentity{deploymentIdentity},
		},
		{
			name: "a deleted policy arriving as a tombstone still enqueues its source",
			event: func(h cache.ResourceEventHandler) {
				h.OnDelete(cache.DeletedFinalStateUnknown{Key: "prod/x", Obj: generatedPolicyOwnedBy(deploymentSource, "1")})
			},
			want: []enqueuedIdentity{deploymentIdentity},
		},
		{
			// A hand-authored policy is owned by nothing this controller generated: its name does
			// not derive from its owner, so no owner is followed.
			name: "a policy whose name does not derive from its owner enqueues nothing",
			event: func(h cache.ResourceEventHandler) {
				h.OnAdd(namedPolicy("prod", "hand-authored", "1", ownerRef("apps/v1", "Deployment", "web")), false)
			},
			want: nil,
		},
		{
			name: "a policy with no owners enqueues nothing",
			event: func(h cache.ResourceEventHandler) {
				h.OnAdd(namedPolicy("prod", "hand-authored", "1"), false)
			},
			want: nil,
		},
		{
			name: "a cluster scoped policy enqueues a cluster scoped source",
			event: func(h cache.ResourceEventHandler) {
				h.OnAdd(generatedPolicyOwnedBy(namespaceSource, "1"), false)
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

// TestSourceNamespace covers the skip-check namespace: a namespaced resource uses its own
// namespace, but a Namespace object -- which carries none -- is itself the namespace.
func TestSourceNamespace(t *testing.T) {
	testCases := []struct {
		name   string
		source *unstructured.Unstructured
		want   string
	}{
		{
			name:   "a namespaced resource uses its metadata namespace",
			source: sourceObject(deploymentGVK, "prod", "web"),
			want:   "prod",
		},
		{
			name:   "a namespace object uses its own name",
			source: sourceObject(namespaceGVK, "", "kube-system"),
			want:   "kube-system",
		},
		{
			name:   "a cluster scoped non-namespace resource has no namespace",
			source: sourceObject(schema.GroupVersionKind{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRole"}, "", "admin"),
			want:   "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SourceNamespace(tc.source); got != tc.want {
				t.Errorf("SourceNamespace(%s) = %q, want %q", tc.source.GetName(), got, tc.want)
			}
		})
	}
}
