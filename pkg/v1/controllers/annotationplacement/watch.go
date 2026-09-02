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
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
)

const controllerName = "annotation-placement-controller"

// SetupWithManager registers the controller with the manager.
//
// The annotated resources are of any kind the hub agent watches, which the manager's cache does not
// know ahead of time, so their events arrive on the given channel -- from the resource watcher, in
// the hub agent -- rather than from a watch of their own. The generated policies are watched through
// the manager, and an event on one enqueues the resource it was generated from: without that, an
// edit or a deletion of a generated policy produces no event on its resource, and for a resource
// that never changes again the policy would stay missing or wrong forever.
//
// The caller owns what the controller cannot decide for itself: the placement.kubefleet.dev types
// must be registered in the manager's scheme, the hub agent must hold RBAC to read, create, update,
// and delete the two policy kinds, and the channel's sender is expected to buffer it to the event
// rate it produces -- the channel source blocks its dispatcher on a full channel.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager, sourceEvents <-chan event.TypedGenericEvent[client.Object]) error {
	return builder.TypedControllerManagedBy[Request](mgr).
		Named(controllerName).
		WatchesRawSource(source.TypedChannel(sourceEvents, handler.TypedEnqueueRequestsFromMapFunc(mapSourceToRequest))).
		Watches(
			&kfplacementv1alpha1.PlacementPolicy{},
			handler.TypedEnqueueRequestsFromMapFunc(mapGeneratedPolicyToSource),
			builder.WithPredicates(predicate.ResourceVersionChangedPredicate{}),
		).
		Watches(
			&kfplacementv1alpha1.ClusterPlacementPolicy{},
			handler.TypedEnqueueRequestsFromMapFunc(mapGeneratedPolicyToSource),
			builder.WithPredicates(predicate.ResourceVersionChangedPredicate{}),
		).
		Complete(r)
}

// mapSourceToRequest enqueues an annotated resource the resource watcher reported.
func mapSourceToRequest(_ context.Context, source client.Object) []Request {
	req := RequestFor(source)
	if req.Kind == "" {
		klog.ErrorS(nil, "Skipped a resource event that names no kind", "obj", klog.KObj(source))
		return nil
	}
	return []Request{req}
}

// mapGeneratedPolicyToSource enqueues the resource that generated a policy, identified from the
// policy's owner references.
//
// Only the owner whose identity reproduces this policy's own generated name is enqueued. A policy
// may carry more than one owner reference -- a foreign one that applyDesiredPolicy deliberately
// preserves, or any owner on a hand-authored policy that shares these watches -- and following
// those would enqueue a request for a kind the resource watcher does not track. Matching the
// generated name is exact, so only the true source passes.
//
// The owner reference is used rather than the parent labels because the labels are lossy (a long
// name is shortened to a prefix and a hash) and, being labels, can be stripped -- which is itself
// drift this watch exists to repair. On an update both the old and the new object are mapped, so an
// owner reference present only on the old side still enqueues the resource whose policy the update
// just took away from it.
func mapGeneratedPolicyToSource(_ context.Context, policy client.Object) []Request {
	// A generated policy always lives in the namespace of the resource it came from, and a
	// cluster-scoped one has none, matching a cluster-scoped owner.
	policyName, policyNamespace := policy.GetName(), policy.GetNamespace()
	for _, owner := range policy.GetOwnerReferences() {
		gv, err := schema.ParseGroupVersion(owner.APIVersion)
		if err != nil {
			klog.ErrorS(err, "Skipped an owner with an unparsable API version", "policy", klog.KObj(policy), "apiVersion", owner.APIVersion)
			continue
		}
		gvk := gv.WithKind(owner.Kind)
		if generatedPolicyName(gvk, policyNamespace, owner.Name) != policyName {
			// Not the owner this policy was generated from; following it would reach a resource
			// the watcher never selected.
			continue
		}
		return []Request{{
			GroupVersionKind: gvk,
			NamespacedName:   client.ObjectKey{Namespace: policyNamespace, Name: owner.Name},
		}}
	}
	return nil
}

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
