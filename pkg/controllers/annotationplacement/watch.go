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
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/informer"
)

// SourceNamespace returns the namespace whose skip-status governs whether a source resource is
// placed. For an ordinary namespaced resource that is its metadata.namespace; a core Namespace
// object carries none and is itself the namespace, so its own name is returned. The skip check
// would otherwise read an empty namespace for a Namespace source and place one KubeFleet excludes.
func SourceNamespace(source *unstructured.Unstructured) string {
	if gvk := source.GroupVersionKind(); gvk.Group == "" && gvk.Kind == "Namespace" {
		return source.GetName()
	}
	return source.GetNamespace()
}

// GeneratedPolicyResources returns the resources that hold the policies this controller generates,
// for the resource watcher to add informers for.
//
// Watching the generated policies, and not only the resources they are generated from, is what
// makes drift repairable: without it, deleting a generated policy or editing its spec produces no
// event on the resource it came from, and for a resource that never changes again the policy would
// stay missing or wrong forever. A resync does not step in either, since the resource watcher
// drops updates whose resource version did not move.
func GeneratedPolicyResources() []informer.APIResourceMeta {
	return []informer.APIResourceMeta{
		{
			GroupVersionKind:     kfplacementv1alpha1.GroupVersion.WithKind("PlacementPolicy"),
			GroupVersionResource: kfplacementv1alpha1.GroupVersion.WithResource("placementpolicies"),
		},
		{
			GroupVersionKind:     kfplacementv1alpha1.GroupVersion.WithKind("ClusterPlacementPolicy"),
			GroupVersionResource: kfplacementv1alpha1.GroupVersion.WithResource("clusterplacementpolicies"),
			IsClusterScoped:      true,
		},
	}
}

// NewGeneratedPolicyEventHandler returns the event handler for the generated policy informers. For
// every policy event it enqueues the resources the policy was generated from, so that the next
// reconciliation of those resources restores whatever the event changed.
func NewGeneratedPolicyEventHandler(enqueue func(obj interface{})) cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			enqueueGeneratingResources(obj, enqueue)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldMeta, err := meta.Accessor(oldObj)
			if err != nil {
				klog.ErrorS(err, "Failed to handle a generated policy update event", "oldObj", oldObj)
				return
			}
			newMeta, err := meta.Accessor(newObj)
			if err != nil {
				klog.ErrorS(err, "Failed to handle a generated policy update event", "newObj", newObj)
				return
			}
			if oldMeta.GetResourceVersion() == newMeta.GetResourceVersion() {
				// A resync, not a change.
				return
			}
			// Both sides are enqueued: an owner reference present only on the old side belongs to a
			// resource whose policy this update just took away from it.
			enqueueGeneratingResources(oldObj, enqueue)
			enqueueGeneratingResources(newObj, enqueue)
		},
		DeleteFunc: func(obj interface{}) {
			enqueueGeneratingResources(obj, enqueue)
		},
	}
}

// enqueueGeneratingResources enqueues the resource that generated a policy, identified from the
// policy's owner references.
//
// Only the owner whose identity reproduces this policy's own generated name is enqueued. A policy
// may carry more than one owner reference -- a foreign one that applyDesiredPolicy deliberately
// preserves, or any owner on a hand-authored policy that happens to share these informers -- and
// following those would enqueue a key for a kind the resource watcher does not track, which
// sourceObject would answer by lazily creating an informer outside the resource configuration.
// Matching the generated name is exact, so only the true source passes.
//
// The owner reference is used rather than the parent labels because the labels are lossy (a long
// name is shortened to a prefix and a hash) and, being labels, can be stripped -- which is itself
// drift this path exists to repair.
func enqueueGeneratingResources(obj interface{}, enqueue func(obj interface{})) {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	accessor, err := meta.Accessor(obj)
	if err != nil {
		klog.ErrorS(fmt.Errorf("object %+v is not a policy: %w", obj, err), "Skipped a generated policy event")
		return
	}
	// A generated policy always lives in the namespace of the resource it came from, and a
	// cluster-scoped one has none, matching a cluster-scoped owner.
	policyName, policyNamespace := accessor.GetName(), accessor.GetNamespace()
	for _, owner := range accessor.GetOwnerReferences() {
		gv, err := schema.ParseGroupVersion(owner.APIVersion)
		if err != nil {
			klog.ErrorS(err, "Skipped an owner with an unparsable API version", "policy", klog.KObj(accessor), "apiVersion", owner.APIVersion)
			continue
		}
		gvk := gv.WithKind(owner.Kind)
		if generatedPolicyName(gvk, policyNamespace, owner.Name) != policyName {
			// Not the owner this policy was generated from; following it would reach a resource
			// the watcher never selected.
			continue
		}
		source := &unstructured.Unstructured{}
		source.SetGroupVersionKind(gvk)
		source.SetNamespace(policyNamespace)
		source.SetName(owner.Name)
		enqueue(source)
	}
}
