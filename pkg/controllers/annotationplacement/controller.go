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
	"fmt"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/controller"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/informer"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/keys"
)

// The reasons of the events this controller records on the annotated resource.
//
// The events are recorded on the resource the user annotated rather than on the policy generated
// from it, because the resource is where a user who has just run kubectl annotate is looking, and
// because a rejected annotation generates no policy for an event to be attached to.
const (
	// EventReasonPolicyCreated is recorded when a generated policy is created for a resource.
	EventReasonPolicyCreated = "PlacementPolicyCreated"
	// EventReasonPolicyUpdated is recorded when an annotation change reaches its generated policy.
	EventReasonPolicyUpdated = "PlacementPolicyUpdated"
	// EventReasonPolicyDeleted is recorded when the policy generated for a resource is deleted --
	// because the annotation was removed, or because the resource stopped being eligible for
	// placement. The event's message names which.
	EventReasonPolicyDeleted = "PlacementPolicyDeleted"
	// EventReasonInvalidAnnotation is recorded when an annotation cannot be parsed. It is a warning
	// rather than an error on the queue: no amount of retrying makes a malformed annotation parse,
	// so the event is the only way the user learns that the placement they asked for is not running.
	EventReasonInvalidAnnotation = "InvalidClusterSelectorsAnnotation"
)

// Reconciler keeps the placement policy generated from a resource's cluster-selectors annotation in
// sync with that annotation.
//
// It is driven by the same dynamic informers as the resource change controller, so its queue holds
// keys for resources of any kind the hub agent watches, not for the generated policies themselves.
type Reconciler struct {
	// Client reads and writes the generated placement policies. Its reads are expected to be served
	// from a cache: the reconciler reads a policy on every pass, including for the overwhelming
	// majority of resources that carry no annotation and generate none.
	Client client.Client

	// RestMapper converts the group kind of a queued key into the resource that the informer
	// manager knows it by.
	RestMapper meta.RESTMapper

	// InformerManager holds the informers the annotated resources are read from.
	InformerManager informer.Manager

	// Recorder records the outcome of a reconciliation on the annotated resource.
	Recorder record.EventRecorder

	// ShouldPlace reports whether a resource is one KubeFleet places at all, mirroring the filter
	// the resource watcher applies to its events. The reconciler applies it again because it can be
	// reached for a resource the watcher would filter out: the watcher reports a resource that
	// stops passing its filter as a deletion, and the generated policy watch enqueues whatever a
	// policy names as its owner. A resource that fails the check has its generated policy deleted.
	//
	// Left nil, every resource is eligible.
	ShouldPlace func(source *unstructured.Unstructured) (bool, error)
}

// Reconcile brings the generated policy for one resource in line with that resource's annotation.
func (r *Reconciler) Reconcile(ctx context.Context, key controller.QueueKey) (ctrl.Result, error) {
	startTime := time.Now()
	clusterWideKey, ok := key.(keys.ClusterWideKey)
	if !ok {
		err := fmt.Errorf("got a resource key %+v not of type cluster wide key", key)
		klog.ErrorS(err, "We have encountered a fatal error that can't be retried", "key", key)
		return ctrl.Result{}, controller.NewUnexpectedBehaviorError(err)
	}
	klog.V(2).InfoS("Reconciling annotation-based placement", "obj", clusterWideKey)
	defer func() {
		klog.V(2).InfoS("Annotation-based placement reconciliation loop ends", "obj", clusterWideKey, "latency", time.Since(startTime).Milliseconds())
	}()

	source, err := r.sourceObject(clusterWideKey)
	switch {
	case apierrors.IsNotFound(err):
		// The resource is gone, and the delete is issued from here rather than left to garbage
		// collection. The generated policy does carry an owner reference back to the resource, but
		// the collector removes a dependent only once every owner is gone, and the merge
		// deliberately preserves owner references that other parties added -- any live one of which
		// would keep the policy standing indefinitely. Deleting explicitly is idempotent, so at
		// worst it beats the collector to an object that was doomed anyway.
		deleted, err := r.deleteGeneratedPolicy(ctx, clusterWideKey.GroupVersionKind(), clusterWideKey.Namespace, clusterWideKey.Name)
		switch {
		case err != nil:
			klog.ErrorS(err, "Failed to delete the policy generated for a resource that is gone", "obj", clusterWideKey)
		case deleted:
			klog.V(2).InfoS("Deleted the policy generated for a resource that is gone", "obj", clusterWideKey)
		}
		return ctrl.Result{}, err
	case meta.IsNoMatchError(err):
		// The kind itself is unknown to the API server. Keys of such kinds come from the generated
		// policy watch, which enqueues whatever a policy names as its owner; retrying cannot make
		// the kind exist, so the key is dropped rather than kept on the queue backing off forever.
		klog.V(2).InfoS("The owner of a generated policy is of a kind the API server does not know; dropping the key", "obj", clusterWideKey, "err", err)
		return ctrl.Result{}, nil
	case err != nil:
		klog.ErrorS(err, "Failed to get the annotated resource", "obj", clusterWideKey)
		return ctrl.Result{}, err
	}

	if r.ShouldPlace != nil {
		eligible, err := r.ShouldPlace(source)
		if err != nil {
			klog.ErrorS(err, "Failed to decide whether the resource is eligible for placement", "obj", clusterWideKey)
			return ctrl.Result{}, err
		}
		if !eligible {
			// A resource KubeFleet does not place cannot keep a generated policy either; without
			// this, a resource that stops being eligible (for instance a ReplicaSet adopted by a
			// Deployment) would leave its policy behind, invisible to the watcher from then on.
			return ctrl.Result{}, r.deletePolicy(ctx, source, "the resource is not eligible for placement")
		}
	}

	value, annotated := source.GetAnnotations()[kfplacementv1alpha1.ClusterSelectorsAnnotation]
	if !annotated {
		return ctrl.Result{}, r.deletePolicy(ctx, source, "the "+kfplacementv1alpha1.ClusterSelectorsAnnotation+" annotation was removed")
	}

	selectors, err := parseClusterSelectors(value)
	if err != nil {
		// The annotation is the user's to fix, so the failure is reported to the user and the key is
		// dropped. Any policy generated from an earlier, valid annotation is deliberately left
		// standing: the desired state is now unknown, and tearing down a running placement is a
		// worse answer to a typo than leaving the last one the user did express.
		klog.V(2).InfoS("The annotation cannot be parsed", "obj", clusterWideKey, "err", err)
		r.Recorder.Eventf(source, corev1.EventTypeWarning, EventReasonInvalidAnnotation,
			"The %s annotation is not valid and no placement policy was generated from it: %s", kfplacementv1alpha1.ClusterSelectorsAnnotation, err)
		return ctrl.Result{}, nil
	}
	return ctrl.Result{}, r.syncPolicy(ctx, source, selectors)
}

// sourceObject reads the annotated resource a queued key refers to.
func (r *Reconciler) sourceObject(key keys.ClusterWideKey) (*unstructured.Unstructured, error) {
	restMapping, err := r.RestMapper.RESTMapping(key.GroupKind(), key.Version)
	if err != nil {
		if meta.IsNoMatchError(err) {
			// Returned unwrapped: the caller distinguishes a kind that does not exist, which no
			// retry can fix, and the wrapping below would flatten the error to a string.
			return nil, err
		}
		return nil, controller.NewUnexpectedBehaviorError(fmt.Errorf("failed to get the resource of object %+v: %w", key, err))
	}
	gvr := restMapping.Resource
	if !r.InformerManager.IsInformerSynced(gvr) {
		return nil, controller.NewExpectedBehaviorError(fmt.Errorf("informer cache for %+v is not synced yet", gvr))
	}

	var object runtime.Object
	if r.InformerManager.IsClusterScopedResources(key.GroupVersionKind()) {
		object, err = r.InformerManager.Lister(gvr).Get(key.Name)
	} else {
		object, err = r.InformerManager.Lister(gvr).ByNamespace(key.Namespace).Get(key.Name)
	}
	if err != nil {
		// Wrapped rather than replaced: the caller distinguishes a resource that is gone from a
		// cache that could not answer, and only the original error carries that.
		return nil, fmt.Errorf("failed to get the object %+v: %w", key, err)
	}

	source, ok := object.(*unstructured.Unstructured)
	if !ok {
		return nil, controller.NewUnexpectedBehaviorError(fmt.Errorf("object %+v read from the informer cache is not unstructured", key))
	}
	return source, nil
}

// syncPolicy creates or updates the policy generated from a resource's annotation.
func (r *Reconciler) syncPolicy(ctx context.Context, source *unstructured.Unstructured, selectors []kfplacementv1alpha1.ClusterSelector) error {
	desired := desiredPolicy(source, selectors)
	actual := emptyPolicyForScope(source.GetNamespace())

	err := r.Client.Get(ctx, client.ObjectKeyFromObject(desired), actual)
	switch {
	case apierrors.IsNotFound(err):
		if err := r.Client.Create(ctx, desired); err != nil {
			klog.ErrorS(err, "Failed to create the generated placement policy", "obj", klog.KObj(source), "policy", klog.KObj(desired))
			return controller.NewAPIServerError(false, err)
		}
		klog.V(2).InfoS("Created the generated placement policy", "obj", klog.KObj(source), "policy", klog.KObj(desired))
		r.Recorder.Eventf(source, corev1.EventTypeNormal, EventReasonPolicyCreated,
			"Created the %s %s from the %s annotation", generatedPolicyKind(source.GetNamespace()), desired.GetName(), kfplacementv1alpha1.ClusterSelectorsAnnotation)
		return nil
	case err != nil:
		klog.ErrorS(err, "Failed to get the generated placement policy", "obj", klog.KObj(source), "policy", klog.KObj(desired))
		return controller.NewAPIServerError(true, err)
	}

	if !applyDesiredPolicy(actual, desired) {
		klog.V(3).InfoS("The generated placement policy is already up to date", "obj", klog.KObj(source), "policy", klog.KObj(actual))
		return nil
	}
	if err := r.Client.Update(ctx, actual); err != nil {
		klog.ErrorS(err, "Failed to update the generated placement policy", "obj", klog.KObj(source), "policy", klog.KObj(actual))
		return controller.NewAPIServerError(false, err)
	}
	klog.V(2).InfoS("Updated the generated placement policy", "obj", klog.KObj(source), "policy", klog.KObj(actual))
	r.Recorder.Eventf(source, corev1.EventTypeNormal, EventReasonPolicyUpdated,
		"Updated the %s %s from the %s annotation", generatedPolicyKind(source.GetNamespace()), actual.GetName(), kfplacementv1alpha1.ClusterSelectorsAnnotation)
	return nil
}

// deletePolicy removes the policy generated for a resource that should not have one -- because the
// annotation was removed, or because the resource is not eligible for placement -- and tells the
// user which through an event carrying the given cause.
//
// The cause is a plain string, never a format: keeping the only format string in the Eventf call
// below constant is what lets go vet check it.
func (r *Reconciler) deletePolicy(ctx context.Context, source *unstructured.Unstructured, cause string) error {
	name := generatedPolicyName(source.GroupVersionKind(), source.GetNamespace(), source.GetName())
	deleted, err := r.deleteGeneratedPolicy(ctx, source.GroupVersionKind(), source.GetNamespace(), source.GetName())
	if err != nil {
		klog.ErrorS(err, "Failed to delete the generated placement policy", "obj", klog.KObj(source), "policy", klog.KRef(source.GetNamespace(), name))
		return err
	}
	if !deleted {
		// The common case by far: a resource nobody annotated.
		return nil
	}
	klog.V(2).InfoS("Deleted the generated placement policy", "obj", klog.KObj(source), "policy", klog.KRef(source.GetNamespace(), name))
	r.Recorder.Eventf(source, corev1.EventTypeNormal, EventReasonPolicyDeleted, "Deleted the %s %s because %s", generatedPolicyKind(source.GetNamespace()), name, cause)
	return nil
}

// deleteGeneratedPolicy deletes the policy generated for the given resource identity, reporting
// whether this pass performed the deletion.
//
// The policy is read before it is deleted, which for the vast majority of resources — those that
// were never annotated at all — is a single cached read and no request to the API server.
func (r *Reconciler) deleteGeneratedPolicy(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string) (bool, error) {
	actual := emptyPolicyForScope(namespace)
	policyName := generatedPolicyName(gvk, namespace, name)

	err := r.Client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: policyName}, actual)
	switch {
	case apierrors.IsNotFound(err):
		return false, nil
	case err != nil:
		return false, controller.NewAPIServerError(true, err)
	}

	if err := r.Client.Delete(ctx, actual); err != nil {
		if apierrors.IsNotFound(err) {
			// The cached read raced a deletion that already happened -- typically this controller's
			// own, re-entered through the generated policy watch moments later. Nothing was deleted
			// here, and reporting otherwise would log, and on some paths announce to the user, a
			// deletion that this pass did not perform.
			return false, nil
		}
		return false, controller.NewAPIServerError(false, err)
	}
	return true, nil
}

// applyDesiredPolicy brings a live generated policy in line with the desired one, reporting whether
// anything changed.
//
// Only what this controller generates is overwritten: the spec, the provenance labels, and the owner
// reference to the annotated resource. Labels and annotations that something else added are left
// alone, so that a generated policy can be labelled by an operator or a GitOps tool without this
// controller and that tool taking turns undoing each other.
func applyDesiredPolicy(actual, desired client.Object) bool {
	changed := false

	actualSpec, desiredSpec := policySpec(actual), policySpec(desired)
	if actualSpec == nil || desiredSpec == nil {
		// Unreachable for the objects this package builds; treated as no change rather than a panic
		// so that a future scope cannot take the reconcile loop down with it.
		klog.ErrorS(fmt.Errorf("object of type %T is not a generated placement policy", actual), "Skipped updating an object that is not a placement policy")
		return false
	}
	if !equality.Semantic.DeepEqual(actualSpec, desiredSpec) {
		desiredSpec.DeepCopyInto(actualSpec)
		changed = true
	}

	labels := actual.GetLabels()
	for key, value := range desired.GetLabels() {
		if existing, found := labels[key]; found && existing == value {
			continue
		}
		if labels == nil {
			labels = make(map[string]string, len(desired.GetLabels()))
		}
		labels[key] = value
		changed = true
	}
	if changed {
		actual.SetLabels(labels)
	}

	for _, owner := range desired.GetOwnerReferences() {
		if ensureOwnerReference(actual, owner) {
			changed = true
		}
	}
	return changed
}

// ensureOwnerReference adds the owner reference to the annotated resource if it is missing or has
// drifted, leaving any other owner reference in place, and reports whether it changed anything.
func ensureOwnerReference(policy client.Object, want metav1.OwnerReference) bool {
	owners := policy.GetOwnerReferences()
	for i := range owners {
		if owners[i].UID != want.UID {
			continue
		}
		if equality.Semantic.DeepEqual(owners[i], want) {
			return false
		}
		// Deep copied rather than assigned: an owner reference carries two pointer fields, and the
		// live policy must not come away sharing them with the object the desired one was built from.
		owners[i] = *want.DeepCopy()
		policy.SetOwnerReferences(owners)
		return true
	}
	policy.SetOwnerReferences(append(slices.Clone(owners), *want.DeepCopy()))
	return true
}

// HasClusterSelectorsAnnotation reports whether an object carries the annotation this controller
// acts on. It is what keeps every unrelated resource in the cluster out of this controller's queue,
// and so is called by the resource watcher on every event for every watched resource.
func HasClusterSelectorsAnnotation(object metav1.Object) bool {
	_, found := object.GetAnnotations()[kfplacementv1alpha1.ClusterSelectorsAnnotation]
	return found
}
