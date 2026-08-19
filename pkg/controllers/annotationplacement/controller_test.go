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
		{
			// The source was deleted and recreated under the same name, so its reference carries the
			// old UID. It must be updated in place, not left dangling while a second one is appended --
			// otherwise the list grows by one on every such cycle.
			name: "an owner reference left by a recreated source is replaced, not appended",
			mutate: func(policy client.Object) {
				stale := parentOwnerReference(source)
				stale.UID = "00000000-0000-0000-0000-00000000dead"
				policy.SetOwnerReferences([]metav1.OwnerReference{stale})
			},
			wantChanged: true,
			check: func(t *testing.T, policy client.Object) {
				want := []metav1.OwnerReference{parentOwnerReference(source)}
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

// TestEventMessagesNameTheGeneratedKind pins that an event's message names the kind actually
// generated: the two scopes generate different kinds, and a message claiming a PlacementPolicy for
// what is really a ClusterPlacementPolicy sends the user's kubectl to a resource that is not there.
func TestEventMessagesNameTheGeneratedKind(t *testing.T) {
	testCases := []struct {
		name     string
		source   *unstructured.Unstructured
		gvr      schema.GroupVersionResource
		wantKind string
	}{
		{
			name:     "a namespaced source names PlacementPolicy",
			source:   newSource(deploymentGVK, testNamespace, testName, map[string]string{kfplacementv1alpha1.ClusterSelectorsAnnotation: oneSelector}),
			gvr:      deploymentGVR,
			wantKind: "PlacementPolicy",
		},
		{
			name:     "a cluster scoped source names ClusterPlacementPolicy",
			source:   newSource(namespaceGVK, "", testName, map[string]string{kfplacementv1alpha1.ClusterSelectorsAnnotation: oneSelector}),
			gvr:      namespaceGVR,
			wantKind: "ClusterPlacementPolicy",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r, recorder := newReconciler(t, map[schema.GroupVersionResource][]runtime.Object{tc.gvr: {tc.source}}, []schema.GroupVersionKind{namespaceGVK}, interceptor.Funcs{})

			key := keyFor(tc.source.GroupVersionKind(), tc.source.GetNamespace(), tc.source.GetName())
			if _, err := r.Reconcile(context.Background(), key); err != nil {
				t.Fatalf("Reconcile(%v) = %v, want no error", key, err)
			}

			select {
			case event := <-recorder.Events:
				// The kind is matched with its surrounding spaces: "ClusterPlacementPolicy"
				// contains "PlacementPolicy" as a bare substring, so an unanchored check would
				// pass the namespaced case even if the wrong kind were named.
				if !strings.Contains(event, " the "+tc.wantKind+" ") {
					t.Errorf("Reconcile(%v) recorded event %q, want it to name the kind %q", key, event, tc.wantKind)
				}
			default:
				t.Fatalf("Reconcile(%v) recorded no event, want one naming the kind %q", key, tc.wantKind)
			}
		})
	}
}

// TestReconcileUnknownKind covers a key whose kind the API server does not know, which the
// generated policy watch can produce by enqueuing whatever a policy names as its owner. The kind
// being gone means the source's own CRD was removed, so any policy generated from it is stale and is
// deleted; when none exists the key is simply dropped, since no retry can make the kind exist and an
// error would keep it backing off forever.
func TestReconcileUnknownKind(t *testing.T) {
	widgetGVK := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}
	// The resource whose kind is gone. It is only used to derive the generated policy the reconciler
	// must find and delete; the informer and the REST mapper deliberately do not know its kind.
	widget := newSource(widgetGVK, testNamespace, testName, map[string]string{kfplacementv1alpha1.ClusterSelectorsAnnotation: oneSelector})
	selectors, err := parseClusterSelectors(oneSelector)
	if err != nil {
		t.Fatalf("parseClusterSelectors(%q) = %v, want no error", oneSelector, err)
	}
	stale := desiredPolicy(widget, selectors)

	testCases := []struct {
		name       string
		existing   []client.Object
		wantPolicy bool
	}{
		{name: "no policy to clean up, key is dropped", existing: nil, wantPolicy: false},
		{name: "a stale policy left by the gone kind is deleted", existing: []client.Object{stale}, wantPolicy: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r, recorder := newReconciler(t, map[schema.GroupVersionResource][]runtime.Object{}, nil, interceptor.Funcs{}, tc.existing...)
			key := keyFor(widgetGVK, testNamespace, testName)
			if _, err := r.Reconcile(context.Background(), key); err != nil {
				t.Errorf("Reconcile(%v) = %v, want no error, so that a kind that does not exist is not retried", key, err)
			}
			if _, found := policyFrom(context.Background(), t, r, widget); found != tc.wantPolicy {
				t.Errorf("Reconcile(%v) left a generated policy = %v, want %v", key, found, tc.wantPolicy)
			}
			// No event is recorded either way: the resource an event would attach to is gone.
			if got := recordedReasons(recorder); len(got) != 0 {
				t.Errorf("Reconcile(%v) recorded events = %v, want none", key, got)
			}
		})
	}
}

// TestReconcileResolvesRemovedVersion covers a key whose recorded version is no longer served while
// the kind lives on under another. The generated policy watch can enqueue such a key, and treating
// the removed version as a gone kind would wrongly delete a policy that is still wanted; the
// reconciler falls back to the served version and keeps the policy in sync instead.
func TestReconcileResolvesRemovedVersion(t *testing.T) {
	ctx := context.Background()
	// The source is served under apps/v1, the version the REST mapper knows; the key arrives naming
	// apps/v2, a version that has since been removed.
	source := newSource(deploymentGVK, testNamespace, testName, map[string]string{kfplacementv1alpha1.ClusterSelectorsAnnotation: oneSelector})
	r, recorder := newReconciler(t, map[schema.GroupVersionResource][]runtime.Object{deploymentGVR: {source}}, nil, interceptor.Funcs{})

	key := keyFor(schema.GroupVersionKind{Group: "apps", Version: "v2", Kind: "Deployment"}, testNamespace, testName)
	if _, err := r.Reconcile(ctx, key); err != nil {
		t.Fatalf("Reconcile(%v) = %v, want no error", key, err)
	}

	if _, found := policyFrom(ctx, t, r, source); !found {
		t.Errorf("Reconcile(%v) generated no policy, want the removed version resolved to the served one", key)
	}
	if diff := cmp.Diff(recordedReasons(recorder), []string{EventReasonPolicyCreated}); diff != "" {
		t.Errorf("Reconcile(%v) recorded events mismatch (-got, +want):\n%s", key, diff)
	}
}

// TestReconcileForeignPolicyAtGeneratedName covers a policy that already occupies a resource's
// generated name but was authored by someone else -- it carries none of this controller's provenance
// labels. The controller must neither overwrite it when the annotation asks for a policy nor delete
// it when the annotation is removed; a bare name match would do both.
func TestReconcileForeignPolicyAtGeneratedName(t *testing.T) {
	// A policy sitting at the generated name, distinguishable by a spec this controller would never
	// produce and by the absence of the provenance labels.
	foreignSpec := func() kfplacementv1alpha1.PlacementPolicySpec {
		return kfplacementv1alpha1.PlacementPolicySpec{
			ResourceSelectors: []kfplacementv1alpha1.ResourceSelector{{APIGroup: "example.com", APIVersion: "v1", Kind: "Widget", Name: "hand-authored"}},
		}
	}
	newForeign := func() *kfplacementv1alpha1.PlacementPolicy {
		return &kfplacementv1alpha1.PlacementPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      generatedPolicyName(deploymentGVK, testNamespace, testName),
				Namespace: testNamespace,
			},
			Spec: foreignSpec(),
		}
	}

	testCases := []struct {
		name        string
		source      *unstructured.Unstructured
		wantReasons []string
	}{
		{
			name:   "the annotation asks for a policy, the foreign one is not overwritten",
			source: newSource(deploymentGVK, testNamespace, testName, map[string]string{kfplacementv1alpha1.ClusterSelectorsAnnotation: oneSelector}),
			// The user hears why the placement they asked for is not running.
			wantReasons: []string{EventReasonPolicyConflict},
		},
		{
			name: "the annotation is absent, the foreign one is not deleted",
			// Nothing asked for a policy here, so declining to delete a policy that was never this
			// controller's is silent -- the same as a resource that never generated anything.
			source:      newSource(deploymentGVK, testNamespace, testName, nil),
			wantReasons: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			r, recorder := newReconciler(t, map[schema.GroupVersionResource][]runtime.Object{deploymentGVR: {tc.source}}, nil, interceptor.Funcs{}, newForeign())

			key := keyFor(deploymentGVK, testNamespace, testName)
			if _, err := r.Reconcile(ctx, key); err != nil {
				t.Fatalf("Reconcile(%v) = %v, want no error", key, err)
			}

			got, found := policyFrom(ctx, t, r, tc.source)
			if !found {
				t.Fatalf("Reconcile(%v) removed the foreign policy, want it left in place", key)
			}
			gotSpec := got.(*kfplacementv1alpha1.PlacementPolicy).Spec
			if diff := cmp.Diff(gotSpec, foreignSpec()); diff != "" {
				t.Errorf("Reconcile(%v) changed the foreign policy's spec (-got, +want):\n%s", key, diff)
			}
			if labels := got.GetLabels(); len(labels) != 0 {
				t.Errorf("Reconcile(%v) added labels %v to the foreign policy, want it left untouched", key, labels)
			}
			if diff := cmp.Diff(recordedReasons(recorder), tc.wantReasons, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Reconcile(%v) recorded events mismatch (-got, +want):\n%s", key, diff)
			}
		})
	}
}

// TestReconcileRepairsDriftedProvenance covers a policy this controller generated whose provenance
// labels were later edited away. Its owner reference still identifies it as ours, so it must be
// repaired while the annotation stands and deleted once the annotation is removed -- never stranded
// as though it were foreign, which would leave its placement running with nothing able to reconcile
// or remove it.
func TestReconcileRepairsDriftedProvenance(t *testing.T) {
	annotated := newSource(deploymentGVK, testNamespace, testName, map[string]string{kfplacementv1alpha1.ClusterSelectorsAnnotation: oneSelector})
	selectors, err := parseClusterSelectors(oneSelector)
	if err != nil {
		t.Fatalf("parseClusterSelectors(%q) = %v, want no error", oneSelector, err)
	}
	// A generated policy whose provenance labels have drifted -- one label is gone -- while the owner
	// reference this controller set is untouched, so the policy is still recognizable as ours.
	drifted := func() client.Object {
		policy := desiredPolicy(annotated, selectors)
		labels := policy.GetLabels()
		delete(labels, kfplacementv1alpha1.ParentKindLabel)
		policy.SetLabels(labels)
		return policy
	}

	testCases := []struct {
		name       string
		source     *unstructured.Unstructured
		wantPolicy bool
		wantReason string
	}{
		{
			name:       "the annotation stands, so the drifted labels are repaired",
			source:     annotated,
			wantPolicy: true,
			wantReason: EventReasonPolicyUpdated,
		},
		{
			name:       "the annotation is gone, so the policy is deleted",
			source:     newSource(deploymentGVK, testNamespace, testName, nil),
			wantPolicy: false,
			wantReason: EventReasonPolicyDeleted,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			r, recorder := newReconciler(t, map[schema.GroupVersionResource][]runtime.Object{deploymentGVR: {tc.source}}, nil, interceptor.Funcs{}, drifted())

			key := keyFor(deploymentGVK, testNamespace, testName)
			if _, err := r.Reconcile(ctx, key); err != nil {
				t.Fatalf("Reconcile(%v) = %v, want no error", key, err)
			}

			got, found := policyFrom(ctx, t, r, annotated)
			if found != tc.wantPolicy {
				t.Fatalf("Reconcile(%v) left a generated policy = %v, want %v", key, found, tc.wantPolicy)
			}
			if tc.wantPolicy {
				if diff := cmp.Diff(got.GetLabels(), desiredPolicy(annotated, selectors).GetLabels()); diff != "" {
					t.Errorf("Reconcile(%v) did not restore the drifted provenance labels (-got, +want):\n%s", key, diff)
				}
			}
			if diff := cmp.Diff(recordedReasons(recorder), []string{tc.wantReason}); diff != "" {
				t.Errorf("Reconcile(%v) recorded events mismatch (-got, +want):\n%s", key, diff)
			}
		})
	}
}

// TestReconcileResolvesRemovedVersionForClusterScoped covers a cluster-scoped source reached through
// a key whose version is no longer served -- what the generated policy watch produces from an owner
// reference written under an old version. Scope must come from the resolved mapping: read from the
// stale queued version, which the informer manager no longer indexes as cluster-scoped, it would look
// the object up in a namespace it does not live in and take it for deleted.
func TestReconcileResolvesRemovedVersionForClusterScoped(t *testing.T) {
	ctx := context.Background()
	source := newSource(namespaceGVK, "", testName, map[string]string{kfplacementv1alpha1.ClusterSelectorsAnnotation: oneSelector})

	// The informer holds the source as a cluster-scoped resource: it lives under no namespace, so a
	// namespaced lookup finds nothing. That is what makes this a real regression pin -- determining
	// scope from the stale queued GVK would misclassify the source as namespaced and read it through
	// ByNamespace, which now misses it, taking the source for deleted; determining scope from the
	// resolved mapping reads it through the cluster-scoped lister and finds it.
	recorder := record.NewFakeRecorder(10)
	r := &Reconciler{
		Client:     fake.NewClientBuilder().WithScheme(newScheme(t)).Build(),
		RestMapper: newRESTMapper(),
		InformerManager: &testinformer.FakeManager{
			// The manager knows the served version's scope but not the retired one the key names.
			APIResources:            map[schema.GroupVersionKind]bool{namespaceGVK: true},
			IsClusterScopedResource: true,
			Listers: map[schema.GroupVersionResource]*testinformer.FakeLister{
				namespaceGVR: {Objects: []runtime.Object{source}, ClusterScoped: true},
			},
		},
		Recorder: recorder,
	}

	// The key names Namespace under a retired version; only the served v1 is registered in the mapper.
	key := keyFor(schema.GroupVersionKind{Group: "", Version: "v2", Kind: "Namespace"}, "", testName)
	if _, err := r.Reconcile(ctx, key); err != nil {
		t.Fatalf("Reconcile(%v) = %v, want no error", key, err)
	}
	if _, found := policyFrom(ctx, t, r, source); !found {
		t.Errorf("Reconcile(%v) generated no policy, want the cluster-scoped source resolved through the served version", key)
	}
	if diff := cmp.Diff(recordedReasons(recorder), []string{EventReasonPolicyCreated}); diff != "" {
		t.Errorf("Reconcile(%v) recorded events mismatch (-got, +want):\n%s", key, diff)
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
