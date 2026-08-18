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

// enqueueGeneratingResources enqueues every resource a policy names as its owner.
//
// The owner references are used rather than the parent labels, because the labels are lossy (a long
// name is shortened to a prefix and a hash) and, being labels, can be stripped -- which is itself
// drift this path exists to repair. Owners that never generated a policy cost one reconciliation
// that reads the annotation, finds none, looks up a policy under the generated name, and stops.
func enqueueGeneratingResources(obj interface{}, enqueue func(obj interface{})) {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	accessor, err := meta.Accessor(obj)
	if err != nil {
		klog.ErrorS(fmt.Errorf("object %+v is not a policy: %w", obj, err), "Skipped a generated policy event")
		return
	}
	for _, owner := range accessor.GetOwnerReferences() {
		gv, err := schema.ParseGroupVersion(owner.APIVersion)
		if err != nil {
			klog.ErrorS(err, "Skipped an owner with an unparsable API version", "policy", klog.KObj(accessor), "apiVersion", owner.APIVersion)
			continue
		}
		// The owner's own namespace is the policy's: a generated policy always lives in the
		// namespace of the resource it came from, and a cluster-scoped one has none, matching a
		// cluster-scoped owner.
		source := &unstructured.Unstructured{}
		source.SetGroupVersionKind(gv.WithKind(owner.Kind))
		source.SetNamespace(accessor.GetNamespace())
		source.SetName(owner.Name)
		enqueue(source)
	}
}
