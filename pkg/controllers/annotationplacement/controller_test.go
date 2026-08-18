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
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/controller"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/keys"
	testinformer "github.com/kubefleet-dev/kubefleet/test/utils/informer"
)

var (
	deploymentGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	namespaceGVR  = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}
)

const (
	testNamespace = "prod"
	testName      = "web"

	oneSelector  = "env=staging"
	twoSelectors = "env=staging,count=All;env=canary,region=eastus"
)

// newSource builds the annotated resource as the informer cache would hold it.
func newSource(gvk schema.GroupVersionKind, namespace, name string, annotations map[string]string) *unstructured.Unstructured {
	source := sourceObject(gvk, namespace, name)
	if annotations != nil {
		source.SetAnnotations(annotations)
	}
	return source
}

// keyFor builds the queue key the resource watcher would enqueue for a resource.
func keyFor(gvk schema.GroupVersionKind, namespace, name string) keys.ClusterWideKey {
	return keys.ClusterWideKey{
		ResourceIdentifier: placementv1beta1.ResourceIdentifier{
			Group:     gvk.Group,
			Version:   gvk.Version,
			Kind:      gvk.Kind,
			Namespace: namespace,
			Name:      name,
		},
	}
}

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := kfplacementv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() = %v, want no error", err)
	}
	return scheme
}

func newRESTMapper() meta.RESTMapper {
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{deploymentGVK.GroupVersion(), namespaceGVK.GroupVersion()})
	mapper.AddSpecific(deploymentGVK, deploymentGVR, deploymentGVR, meta.RESTScopeNamespace)
	mapper.AddSpecific(namespaceGVK, namespaceGVR, namespaceGVR, meta.RESTScopeRoot)
	return mapper
}

// newReconciler wires a reconciler whose informer cache holds the given resources and whose client
// holds the given policies.
func newReconciler(t *testing.T, sources map[schema.GroupVersionResource][]runtime.Object, clusterScoped []schema.GroupVersionKind, funcs interceptor.Funcs, policies ...client.Object) (*Reconciler, *record.FakeRecorder) {
	t.Helper()
	listers := make(map[schema.GroupVersionResource]*testinformer.FakeLister, len(sources))
	for gvr, objects := range sources {
		listers[gvr] = &testinformer.FakeLister{Objects: objects}
	}
	apiResources := make(map[schema.GroupVersionKind]bool, len(clusterScoped))
	for _, gvk := range clusterScoped {
		apiResources[gvk] = true
	}
	recorder := record.NewFakeRecorder(10)
	return &Reconciler{
		Client:     fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(policies...).WithInterceptorFuncs(funcs).Build(),
		RestMapper: newRESTMapper(),
		InformerManager: &testinformer.FakeManager{
			APIResources:            apiResources,
			IsClusterScopedResource: true,
			Listers:                 listers,
		},
		Recorder: recorder,
	}, recorder
}

// recordedReasons drains the recorder and returns the reason of every event it holds.
func recordedReasons(recorder *record.FakeRecorder) []string {
	reasons := make([]string, 0, len(recorder.Events))
	for {
		select {
		case event := <-recorder.Events:
			// A recorded event reads "Normal <Reason> <message>".
			fields := strings.SplitN(event, " ", 3)
			if len(fields) < 2 {
				reasons = append(reasons, event)
				continue
			}
			reasons = append(reasons, fields[1])
		default:
			return reasons
		}
	}
}

// policyFrom reads the generated policy for a resource back out of the client, or reports that none
// exists.
func policyFrom(ctx context.Context, t *testing.T, r *Reconciler, source *unstructured.Unstructured) (client.Object, bool) {
	t.Helper()
	namespace := source.GetNamespace()
	policy := emptyPolicyForScope(namespace)
	name := generatedPolicyName(source.GroupVersionKind(), namespace, source.GetName())
	err := r.Client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, policy)
	switch {
	case apierrors.IsNotFound(err):
		return nil, false
	case err != nil:
		t.Fatalf("Get(%s) = %v, want no error", name, err)
	}
	return policy, true
}

// policyIgnoreOpts drop the fields the API server owns, which no expectation here can predict.
var policyIgnoreOpts = cmp.Options{
	cmpopts.IgnoreFields(metav1.TypeMeta{}, "Kind", "APIVersion"),
	cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion", "Generation", "CreationTimestamp", "ManagedFields"),
}

func TestReconcile(t *testing.T) {
	// The desired policy is built with desiredPolicy, which policy_test.go covers on its own; what
	// is under test here is whether the reconciler puts that object in the cluster, takes it away
	// again, and says so.
	annotatedSource := newSource(deploymentGVK, testNamespace, testName, map[string]string{kfplacementv1alpha1.ClusterSelectorsAnnotation: oneSelector})
	bareSource := newSource(deploymentGVK, testNamespace, testName, nil)
	clusterScopedSource := newSource(namespaceGVK, "", testName, map[string]string{kfplacementv1alpha1.ClusterSelectorsAnnotation: oneSelector})

	testCases := []struct {
		name string
		// source is the resource the informer cache holds, and the resource the key points at.
		source *unstructured.Unstructured
		// existing is the annotation value a policy was already generated from, if any.
		existing    string
		gvr         schema.GroupVersionResource
		wantPolicy  bool
		wantValue   string
		wantReasons []string
	}{
		{
			name:        "an annotated resource with no policy yet gets one",
			source:      annotatedSource,
			gvr:         deploymentGVR,
			wantPolicy:  true,
			wantValue:   oneSelector,
			wantReasons: []string{EventReasonPolicyCreated},
		},
		{
			name:        "a policy that already matches the annotation is left alone",
			source:      annotatedSource,
			existing:    oneSelector,
			gvr:         deploymentGVR,
			wantPolicy:  true,
			wantValue:   oneSelector,
			wantReasons: nil,
		},
		{
			name:        "a changed annotation reaches the policy",
			source:      newSource(deploymentGVK, testNamespace, testName, map[string]string{kfplacementv1alpha1.ClusterSelectorsAnnotation: twoSelectors}),
			existing:    oneSelector,
			gvr:         deploymentGVR,
			wantPolicy:  true,
			wantValue:   twoSelectors,
			wantReasons: []string{EventReasonPolicyUpdated},
		},
		{
			name:        "removing the annotation deletes the policy",
			source:      bareSource,
			existing:    oneSelector,
			gvr:         deploymentGVR,
			wantPolicy:  false,
			wantReasons: []string{EventReasonPolicyDeleted},
		},
		{
			name:        "a resource that was never annotated is left alone",
			source:      bareSource,
			gvr:         deploymentGVR,
			wantPolicy:  false,
			wantReasons: nil,
		},
		{
			name:        "a cluster scoped resource generates a cluster scoped policy",
			source:      clusterScopedSource,
			gvr:         namespaceGVR,
			wantPolicy:  true,
			wantValue:   oneSelector,
			wantReasons: []string{EventReasonPolicyCreated},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			var policies []client.Object
			if tc.existing != "" {
				selectors, err := parseClusterSelectors(tc.existing)
				if err != nil {
					t.Fatalf("parseClusterSelectors(%q) = %v, want no error", tc.existing, err)
				}
				policies = append(policies, desiredPolicy(tc.source, selectors))
			}
			clusterScoped := []schema.GroupVersionKind{namespaceGVK}
			r, recorder := newReconciler(t, map[schema.GroupVersionResource][]runtime.Object{tc.gvr: {tc.source}}, clusterScoped, interceptor.Funcs{}, policies...)

			key := keyFor(tc.source.GroupVersionKind(), tc.source.GetNamespace(), tc.source.GetName())
			if _, err := r.Reconcile(ctx, key); err != nil {
				t.Fatalf("Reconcile(%v) = %v, want no error", key, err)
			}

			got, found := policyFrom(ctx, t, r, tc.source)
			if found != tc.wantPolicy {
				t.Fatalf("Reconcile(%v) left a generated policy = %v, want %v", key, found, tc.wantPolicy)
			}
			if tc.wantPolicy {
				selectors, err := parseClusterSelectors(tc.wantValue)
				if err != nil {
					t.Fatalf("parseClusterSelectors(%q) = %v, want no error", tc.wantValue, err)
				}
				want := desiredPolicy(tc.source, selectors)
				if diff := cmp.Diff(got, want, policyIgnoreOpts); diff != "" {
					t.Errorf("Reconcile(%v) generated policy mismatch (-got, +want):\n%s", key, diff)
				}
			}
			if diff := cmp.Diff(recordedReasons(recorder), tc.wantReasons, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Reconcile(%v) recorded events mismatch (-got, +want):\n%s", key, diff)
			}
		})
	}
}

// TestReconcileInvalidAnnotation covers the case the parser rejects: the user hears about it, the
// key is not retried, and a policy generated from an earlier valid annotation stays up.
func TestReconcileInvalidAnnotation(t *testing.T) {
	ctx := context.Background()
	valid := newSource(deploymentGVK, testNamespace, testName, map[string]string{kfplacementv1alpha1.ClusterSelectorsAnnotation: oneSelector})
	selectors, err := parseClusterSelectors(oneSelector)
	if err != nil {
		t.Fatalf("parseClusterSelectors(%q) = %v, want no error", oneSelector, err)
	}
	existing := desiredPolicy(valid, selectors)

	broken := newSource(deploymentGVK, testNamespace, testName, map[string]string{kfplacementv1alpha1.ClusterSelectorsAnnotation: "env"})
	r, recorder := newReconciler(t, map[schema.GroupVersionResource][]runtime.Object{deploymentGVR: {broken}}, nil, interceptor.Funcs{}, existing)

	key := keyFor(deploymentGVK, testNamespace, testName)
	if _, err := r.Reconcile(ctx, key); err != nil {
		t.Fatalf("Reconcile(%v) = %v, want no error, so that a malformed annotation is not retried", key, err)
	}

	got, found := policyFrom(ctx, t, r, broken)
	if !found {
		t.Fatalf("Reconcile(%v) removed the policy generated from the previous annotation, want it left in place", key)
	}
	if diff := cmp.Diff(got, existing, policyIgnoreOpts); diff != "" {
		t.Errorf("Reconcile(%v) changed the policy generated from the previous annotation (-got, +want):\n%s", key, diff)
	}
	if diff := cmp.Diff(recordedReasons(recorder), []string{EventReasonInvalidAnnotation}); diff != "" {
		t.Errorf("Reconcile(%v) recorded events mismatch (-got, +want):\n%s", key, diff)
	}
}

// TestReconcileDeletedResource covers a key for a resource that is already gone. The reconciler
// deletes the generated policy itself rather than deferring to garbage collection, which removes a
// dependent only once every owner reference on it is gone -- and the merge deliberately preserves
// owner references that other parties added, any live one of which would keep the policy standing.
// No event is recorded, since the resource an event would be recorded on no longer exists.
func TestReconcileDeletedResource(t *testing.T) {
	ctx := context.Background()
	source := newSource(deploymentGVK, testNamespace, testName, map[string]string{kfplacementv1alpha1.ClusterSelectorsAnnotation: oneSelector})
	selectors, err := parseClusterSelectors(oneSelector)
	if err != nil {
		t.Fatalf("parseClusterSelectors(%q) = %v, want no error", oneSelector, err)
	}
	existing := desiredPolicy(source, selectors)
	// The other party whose owner reference would hold the policy back from garbage collection.
	otherOwner := metav1.OwnerReference{APIVersion: "example.com/v1", Kind: "Widget", Name: "unrelated", UID: "00000000-0000-0000-0000-0000000000ff"}
	existing.SetOwnerReferences(append(existing.GetOwnerReferences(), otherOwner))

	r, recorder := newReconciler(t, map[schema.GroupVersionResource][]runtime.Object{deploymentGVR: {}}, nil, interceptor.Funcs{}, existing)

	key := keyFor(deploymentGVK, testNamespace, testName)
	if _, err := r.Reconcile(ctx, key); err != nil {
		t.Fatalf("Reconcile(%v) = %v, want no error", key, err)
	}
	if _, found := policyFrom(ctx, t, r, source); found {
		t.Errorf("Reconcile(%v) left the generated policy standing, want it deleted", key)
	}
	if got := recordedReasons(recorder); len(got) != 0 {
		t.Errorf("Reconcile(%v) recorded events = %v, want none", key, got)
	}
}

// TestReconcileIneligibleResource covers a resource that exists but fails the placement eligibility
// test -- for instance one in a skipped namespace, or a ReplicaSet that a Deployment has adopted.
// Its generated policy is deleted: the resource watcher reports such a resource as deleted and then
// never again, so this reconciliation is the last chance to clean up.
func TestReconcileIneligibleResource(t *testing.T) {
	ctx := context.Background()
	source := newSource(deploymentGVK, testNamespace, testName, map[string]string{kfplacementv1alpha1.ClusterSelectorsAnnotation: oneSelector})
	selectors, err := parseClusterSelectors(oneSelector)
	if err != nil {
		t.Fatalf("parseClusterSelectors(%q) = %v, want no error", oneSelector, err)
	}
	existing := desiredPolicy(source, selectors)

	r, recorder := newReconciler(t, map[schema.GroupVersionResource][]runtime.Object{deploymentGVR: {source}}, nil, interceptor.Funcs{}, existing)
	r.ShouldPlace = func(*unstructured.Unstructured) (bool, error) { return false, nil }

	key := keyFor(deploymentGVK, testNamespace, testName)
	if _, err := r.Reconcile(ctx, key); err != nil {
		t.Fatalf("Reconcile(%v) = %v, want no error", key, err)
	}
	if _, found := policyFrom(ctx, t, r, source); found {
		t.Errorf("Reconcile(%v) left the generated policy standing, want it deleted", key)
	}
	if diff := cmp.Diff(recordedReasons(recorder), []string{EventReasonPolicyDeleted}); diff != "" {
		t.Errorf("Reconcile(%v) recorded events mismatch (-got, +want):\n%s", key, diff)
	}

	// A second pass over a resource that never generated anything must stay silent.
	if _, err := r.Reconcile(ctx, key); err != nil {
		t.Fatalf("Reconcile(%v) = %v, want no error", key, err)
	}
	if got := recordedReasons(recorder); len(got) != 0 {
		t.Errorf("Reconcile(%v) recorded events = %v, want none on the second pass", key, got)
	}
}

// TestReconcileEligibilityError covers the eligibility test itself failing, which must surface as a
// retryable error rather than as either verdict.
func TestReconcileEligibilityError(t *testing.T) {
	source := newSource(deploymentGVK, testNamespace, testName, map[string]string{kfplacementv1alpha1.ClusterSelectorsAnnotation: oneSelector})
	r, _ := newReconciler(t, map[schema.GroupVersionResource][]runtime.Object{deploymentGVR: {source}}, nil, interceptor.Funcs{})
	wantErr := errors.New("the eligibility test is unwell")
	r.ShouldPlace = func(*unstructured.Unstructured) (bool, error) { return false, wantErr }

	key := keyFor(deploymentGVK, testNamespace, testName)
	if _, err := r.Reconcile(context.Background(), key); !errors.Is(err, wantErr) {
		t.Errorf("Reconcile(%v) = %v, want an error wrapping %v", key, err, wantErr)
	}
	if _, found := policyFrom(context.Background(), t, r, source); found {
		t.Errorf("Reconcile(%v) acted on the policy despite the eligibility error", key)
	}
}

func TestReconcileErrors(t *testing.T) {
	source := newSource(deploymentGVK, testNamespace, testName, map[string]string{kfplacementv1alpha1.ClusterSelectorsAnnotation: oneSelector})

	testCases := []struct {
		name     string
		key      controller.QueueKey
		synced   *bool
		wantErr  error
		wantKind string
	}{
		{
			name:    "a key of the wrong type cannot be retried",
			key:     "apps/v1/Deployment/prod/web",
			wantErr: controller.ErrUnexpectedBehavior,
		},

		{
			name:    "an unsynced informer is retried",
			key:     keyFor(deploymentGVK, testNamespace, testName),
			synced:  ptr.To(false),
			wantErr: controller.ErrExpectedBehavior,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newReconciler(t, map[schema.GroupVersionResource][]runtime.Object{deploymentGVR: {source}}, nil, interceptor.Funcs{})
			if tc.synced != nil {
				r.InformerManager.(*testinformer.FakeManager).InformerSynced = tc.synced
			}
			_, err := r.Reconcile(context.Background(), tc.key)
			if err == nil {
				t.Fatalf("Reconcile(%v) = nil, want an error", tc.key)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("Reconcile(%v) = %v, want an error of kind %v", tc.key, err, tc.wantErr)
			}
		})
	}
}

// TestReconcileAPIServerErrors covers the paths where the write itself fails. Each has to come back
// as a retryable error rather than as a silent success, because the annotation and the cluster have
// disagreed at that point and only another pass can settle it.
func TestReconcileAPIServerErrors(t *testing.T) {
	annotated := newSource(deploymentGVK, testNamespace, testName, map[string]string{kfplacementv1alpha1.ClusterSelectorsAnnotation: oneSelector})
	bare := newSource(deploymentGVK, testNamespace, testName, nil)
	selectors, err := parseClusterSelectors(oneSelector)
	if err != nil {
		t.Fatalf("parseClusterSelectors(%q) = %v, want no error", oneSelector, err)
	}
	existing := desiredPolicy(annotated, selectors)
	failure := apierrors.NewInternalError(errors.New("the api server is unwell"))

	testCases := []struct {
		name        string
		source      *unstructured.Unstructured
		existing    []client.Object
		interceptor interceptor.Funcs
	}{
		{
			name:   "the policy cannot be read",
			source: annotated,
			interceptor: interceptor.Funcs{
				Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
					return failure
				},
			},
		},
		{
			name:   "the policy cannot be created",
			source: annotated,
			interceptor: interceptor.Funcs{
				Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
					return failure
				},
			},
		},
		{
			name:     "the policy cannot be updated",
			source:   newSource(deploymentGVK, testNamespace, testName, map[string]string{kfplacementv1alpha1.ClusterSelectorsAnnotation: twoSelectors}),
			existing: []client.Object{existing},
			interceptor: interceptor.Funcs{
				Update: func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error {
					return failure
				},
			},
		},
		{
			name:     "the policy cannot be deleted",
			source:   bare,
			existing: []client.Object{existing},
			interceptor: interceptor.Funcs{
				Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
					return failure
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r, recorder := newReconciler(t, map[schema.GroupVersionResource][]runtime.Object{deploymentGVR: {tc.source}}, nil, tc.interceptor, tc.existing...)

			key := keyFor(deploymentGVK, testNamespace, testName)
			_, err := r.Reconcile(context.Background(), key)
			if err == nil {
				t.Fatalf("Reconcile(%v) = nil, want an error", key)
			}
			if !errors.Is(err, controller.ErrAPIServerError) {
				t.Errorf("Reconcile(%v) = %v, want an error of kind %v", key, err, controller.ErrAPIServerError)
			}
			// A write that failed must not be announced as though it had happened.
			if got := recordedReasons(recorder); len(got) != 0 {
				t.Errorf("Reconcile(%v) recorded events = %v, want none", key, got)
			}
		})
	}
}

// TestDeleteRacesAnotherDeletion covers a cached read handing the reconciler a policy that is
// already gone by the time it deletes -- typically its own deletion re-entered through the
// generated policy watch. The pass must not claim, in logs or events, a deletion it did not do.
func TestDeleteRacesAnotherDeletion(t *testing.T) {
	ctx := context.Background()
	source := newSource(deploymentGVK, testNamespace, testName, nil)
	annotated := newSource(deploymentGVK, testNamespace, testName, map[string]string{kfplacementv1alpha1.ClusterSelectorsAnnotation: oneSelector})
	selectors, err := parseClusterSelectors(oneSelector)
	if err != nil {
		t.Fatalf("parseClusterSelectors(%q) = %v, want no error", oneSelector, err)
	}
	existing := desiredPolicy(annotated, selectors)

	// The interceptor stands in for the stale cache: the Get finds the policy, the Delete
	// discovers someone else got there first.
	raced := interceptor.Funcs{
		Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
			return apierrors.NewNotFound(schema.GroupResource{Group: kfplacementv1alpha1.GroupVersion.Group, Resource: "placementpolicies"}, existing.GetName())
		},
	}
	r, recorder := newReconciler(t, map[schema.GroupVersionResource][]runtime.Object{deploymentGVR: {source}}, nil, raced, existing)

	key := keyFor(deploymentGVK, testNamespace, testName)
	if _, err := r.Reconcile(ctx, key); err != nil {
		t.Fatalf("Reconcile(%v) = %v, want no error", key, err)
	}
	if got := recordedReasons(recorder); len(got) != 0 {
		t.Errorf("Reconcile(%v) recorded events = %v, want none for a deletion this pass did not perform", key, got)
	}
}

func TestApplyDesiredPolicy(t *testing.T) {
	source := newSource(deploymentGVK, testNamespace, testName, map[string]string{kfplacementv1alpha1.ClusterSelectorsAnnotation: oneSelector})
	selectors, err := parseClusterSelectors(oneSelector)
	if err != nil {
		t.Fatalf("parseClusterSelectors(%q) = %v, want no error", oneSelector, err)
	}
	desired := desiredPolicy(source, selectors)

	otherOwner := metav1.OwnerReference{
		APIVersion: "example.com/v1",
		Kind:       "Widget",
		Name:       "unrelated",
		UID:        "00000000-0000-0000-0000-0000000000ff",
	}

	testCases := []struct {
		name string
		// mutate turns the desired policy into the live one the reconciler would read back.
		mutate      func(client.Object)
		wantChanged bool
		// check asserts on whatever the merge is supposed to preserve.
		check func(*testing.T, client.Object)
	}{
		{
			name:        "an untouched policy needs no update",
			mutate:      func(client.Object) {},
			wantChanged: false,
		},
		{
			name: "a drifted spec is restored",
			mutate: func(policy client.Object) {
				policySpec(policy).ClusterSelectors = nil
			},
			wantChanged: true,
			check: func(t *testing.T, policy client.Object) {
				if diff := cmp.Diff(policySpec(policy), policySpec(desired)); diff != "" {
					t.Errorf("applyDesiredPolicy() spec mismatch (-got, +want):\n%s", diff)
				}
			},
		},
		{
			name: "a missing provenance label is restored",
			mutate: func(policy client.Object) {
				labels := policy.GetLabels()
				delete(labels, kfplacementv1alpha1.ParentKindLabel)
				policy.SetLabels(labels)
			},
			wantChanged: true,
			check: func(t *testing.T, policy client.Object) {
				if diff := cmp.Diff(policy.GetLabels(), desired.GetLabels()); diff != "" {
					t.Errorf("applyDesiredPolicy() labels mismatch (-got, +want):\n%s", diff)
				}
			},
		},
		{
			name: "a policy stripped of every label gets the provenance labels back",
			mutate: func(policy client.Object) {
				policy.SetLabels(nil)
			},
			wantChanged: true,
			check: func(t *testing.T, policy client.Object) {
				if diff := cmp.Diff(policy.GetLabels(), desired.GetLabels()); diff != "" {
					t.Errorf("applyDesiredPolicy() labels mismatch (-got, +want):\n%s", diff)
				}
			},
		},
		{
			name: "labels added by something else are kept",
			mutate: func(policy client.Object) {
				labels := policy.GetLabels()
				labels["example.com/managed-by"] = "gitops"
				delete(labels, kfplacementv1alpha1.ParentKindLabel)
				policy.SetLabels(labels)
			},
			wantChanged: true,
			check: func(t *testing.T, policy client.Object) {
				if got := policy.GetLabels()["example.com/managed-by"]; got != "gitops" {
					t.Errorf("applyDesiredPolicy() label example.com/managed-by = %q, want %q", got, "gitops")
				}
			},
		},
		{
			name: "an owner reference that drifted is corrected in place",
			mutate: func(policy client.Object) {
				drifted := parentOwnerReference(source)
				drifted.APIVersion = "apps/v1beta1"
				policy.SetOwnerReferences([]metav1.OwnerReference{drifted})
			},
			wantChanged: true,
			check: func(t *testing.T, policy client.Object) {
				want := []metav1.OwnerReference{parentOwnerReference(source)}
				if diff := cmp.Diff(policy.GetOwnerReferences(), want); diff != "" {
					t.Errorf("applyDesiredPolicy() owner references mismatch (-got, +want):\n%s", diff)
				}
			},
		},
		{
			name: "a missing owner reference is restored alongside any other",
			mutate: func(policy client.Object) {
				policy.SetOwnerReferences([]metav1.OwnerReference{otherOwner})
			},
			wantChanged: true,
			check: func(t *testing.T, policy client.Object) {
				want := []metav1.OwnerReference{otherOwner, parentOwnerReference(source)}
				if diff := cmp.Diff(policy.GetOwnerReferences(), want); diff != "" {
					t.Errorf("applyDesiredPolicy() owner references mismatch (-got, +want):\n%s", diff)
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := desired.DeepCopyObject().(client.Object)
			tc.mutate(actual)

			if got := applyDesiredPolicy(actual, desired); got != tc.wantChanged {
				t.Errorf("applyDesiredPolicy() = %v, want %v", got, tc.wantChanged)
			}
			if tc.check != nil {
				tc.check(t, actual)
			}
			// Whatever the merge did, a second pass over its own output must find nothing left to
			// do; otherwise the reconciler would issue an update on every single pass.
			if got := applyDesiredPolicy(actual, desired); got {
				t.Errorf("applyDesiredPolicy() = true on the second pass, want false")
			}
		})
	}
}

// TestReconcileUnknownKind covers a key whose kind the API server does not know, which the
// generated policy watch can produce by enqueuing whatever a policy names as its owner. The key is
// dropped: no retry can make the kind exist, and an error would keep it backing off forever.
func TestReconcileUnknownKind(t *testing.T) {
	r, recorder := newReconciler(t, map[schema.GroupVersionResource][]runtime.Object{}, nil, interceptor.Funcs{})
	key := keyFor(schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}, testNamespace, testName)
	if _, err := r.Reconcile(context.Background(), key); err != nil {
		t.Errorf("Reconcile(%v) = %v, want no error, so that a kind that does not exist is not retried", key, err)
	}
	if got := recordedReasons(recorder); len(got) != 0 {
		t.Errorf("Reconcile(%v) recorded events = %v, want none", key, got)
	}
}

// TestApplyDesiredPolicyRejectsForeignObject covers the branch that exists only so that a scope this
// package does not know about cannot take the reconcile loop down with it.
func TestApplyDesiredPolicyRejectsForeignObject(t *testing.T) {
	if got := applyDesiredPolicy(&unstructured.Unstructured{}, &kfplacementv1alpha1.PlacementPolicy{}); got {
		t.Errorf("applyDesiredPolicy(%T) = true, want false", &unstructured.Unstructured{})
	}
}

func TestHasClusterSelectorsAnnotation(t *testing.T) {
	testCases := []struct {
		name        string
		annotations map[string]string
		want        bool
	}{
		{name: "no annotations at all", annotations: nil, want: false},
		{name: "other annotations only", annotations: map[string]string{"example.com/other": "value"}, want: false},
		{name: "the annotation is present", annotations: map[string]string{kfplacementv1alpha1.ClusterSelectorsAnnotation: oneSelector}, want: true},
		// An empty value is still a request for annotation-based placement; the parser is what
		// rejects it, and it can only do that if the resource reaches the queue at all.
		{name: "the annotation is present but empty", annotations: map[string]string{kfplacementv1alpha1.ClusterSelectorsAnnotation: ""}, want: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			source := newSource(deploymentGVK, testNamespace, testName, tc.annotations)
			if got := HasClusterSelectorsAnnotation(source); got != tc.want {
				t.Errorf("HasClusterSelectorsAnnotation(%v) = %v, want %v", tc.annotations, got, tc.want)
			}
		})
	}
}
