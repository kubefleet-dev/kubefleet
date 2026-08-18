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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
)

const (
	eventuallyTimeout  = "10s"
	eventuallyInterval = "250ms"
)

var configMapCount int

// annotate sets, changes, or (with an empty value) removes the annotation on an object, and waits for
// the informer cache the reconciler reads from to catch up.
func annotate(object client.Object, value string) {
	Expect(hubClient.Get(ctx, client.ObjectKeyFromObject(object), object)).Should(Succeed())
	annotations := object.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	if value == "" {
		delete(annotations, kfplacementv1alpha1.ClusterSelectorsAnnotation)
	} else {
		annotations[kfplacementv1alpha1.ClusterSelectorsAnnotation] = value
	}
	object.SetAnnotations(annotations)
	Expect(hubClient.Update(ctx, object)).Should(Succeed())

	// The reconciler reads the annotated resource from the informer cache rather than from the API
	// server, so a reconcile run before the cache catches up would act on the previous value.
	gvr := configMapGVR
	if object.GetNamespace() == "" {
		gvr = namespaceGVR
	}
	Eventually(func() (string, error) {
		cached, err := cachedObject(gvr, object)
		if err != nil {
			return "", err
		}
		return cached.GetAnnotations()[kfplacementv1alpha1.ClusterSelectorsAnnotation], nil
	}, eventuallyTimeout, eventuallyInterval).Should(Equal(value), "the informer cache never caught up with the annotation")
}

// waitForCache blocks until the informer cache the reconciler reads from has observed an object.
//
// Without it, a reconcile can run against a cache that has not caught up, where a resource that is
// merely not yet visible is indistinguishable from one that carries no annotation: both leave no
// generated policy behind, so an assertion that none exists would hold either way.
func waitForCache(gvr schema.GroupVersionResource, object client.Object) {
	Eventually(func() error {
		_, err := cachedObject(gvr, object)
		return err
	}, eventuallyTimeout, eventuallyInterval).Should(Succeed(), "the informer cache never observed the resource")
}

// cachedObject reads an object out of the informer cache the reconciler uses.
func cachedObject(gvr schema.GroupVersionResource, object client.Object) (client.Object, error) {
	lister := informerManager.Lister(gvr)
	if object.GetNamespace() == "" {
		cached, err := lister.Get(object.GetName())
		if err != nil {
			return nil, err
		}
		return cached.(client.Object), nil
	}
	cached, err := lister.ByNamespace(object.GetNamespace()).Get(object.GetName())
	if err != nil {
		return nil, err
	}
	return cached.(client.Object), nil
}

// reconcile runs one pass of the reconciler over an object, as the resource watcher would.
func reconcile(gvk schema.GroupVersionKind, object client.Object) error {
	_, err := reconciler.Reconcile(ctx, keyFor(gvk, object.GetNamespace(), object.GetName()))
	return err
}

// generatedPolicyFor reads back the policy generated for an object, whichever scope it has.
func generatedPolicyFor(gvk schema.GroupVersionKind, object client.Object) (client.Object, error) {
	namespace := object.GetNamespace()
	policy := emptyPolicyForScope(namespace)
	name := generatedPolicyName(gvk, namespace, object.GetName())
	err := hubClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, policy)
	return policy, err
}

// drainEvents returns the reasons of every event recorded so far, clearing the recorder.
func drainEvents() []string {
	reasons := []string{}
	for {
		select {
		case event := <-eventRecorder.Events:
			fields := strings.SplitN(event, " ", 3)
			if len(fields) >= 2 {
				reasons = append(reasons, fields[1])
			}
		default:
			return reasons
		}
	}
}

// The API server itself enforces the count bounds on both forms of the int-or-string: the Pattern
// marker covers the string form, and the CEL rule covers the integer form, which a pattern alone
// leaves unbounded. The parser mirrors the same bounds for annotations; these specs pin the API
// side against a real API server, where a plain schema reading cannot.
var _ = Describe("the count bounds of a hand-authored policy", func() {
	newPolicy := func(count intstr.IntOrString) *kfplacementv1alpha1.PlacementPolicy {
		configMapCount++
		return &kfplacementv1alpha1.PlacementPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("hand-authored-%d", configMapCount),
				Namespace: "default",
			},
			Spec: kfplacementv1alpha1.PlacementPolicySpec{
				ClusterSelectors: []kfplacementv1alpha1.ClusterSelector{{Count: &count}},
				ResourceSelectors: []kfplacementv1alpha1.ResourceSelector{{
					APIVersion: "v1", Kind: "ConfigMap", Name: "app",
				}},
			},
		}
	}

	DescribeTable("integer and string forms share the same bounds",
		func(count intstr.IntOrString, wantAccepted bool) {
			policy := newPolicy(count)
			err := hubClient.Create(ctx, policy)
			if err == nil {
				// Whatever the verdict was meant to be, an object that made it in must not
				// outlive the spec; a regression that admits an out-of-range count would
				// otherwise leave its evidence lying around for the rest of the suite.
				DeferCleanup(func() {
					Expect(client.IgnoreNotFound(hubClient.Delete(ctx, policy))).Should(Succeed())
				})
			}
			if wantAccepted {
				Expect(err).Should(Succeed())
				return
			}
			Expect(apierrors.IsInvalid(err)).Should(BeTrue(), "got %v, want an invalid error", err)
		},
		Entry("count 1 as an integer is accepted", intstr.FromInt32(1), true),
		Entry("count 999 as an integer is accepted", intstr.FromInt32(999), true),
		Entry("count 999 as a string is accepted", intstr.FromString("999"), true),
		Entry("count All is accepted", intstr.FromString("All"), true),
		// The integer entries below are the reason the CEL rule exists: before it, they were
		// accepted while their quoted twins were rejected.
		Entry("count 1000 as an integer is rejected", intstr.FromInt32(1000), false),
		Entry("count 0 as an integer is rejected", intstr.FromInt32(0), false),
		Entry("count -1 as an integer is rejected", intstr.FromInt32(-1), false),
		Entry("count 1000 as a string is rejected", intstr.FromString("1000"), false),
		Entry("count 0 as a string is rejected", intstr.FromString("0"), false),
	)
})

var _ = Describe("annotation based placement", func() {
	Context("a namespaced resource", Ordered, func() {
		var configMap *corev1.ConfigMap

		BeforeAll(func() {
			configMapCount++
			configMap = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("app-%d", configMapCount),
					Namespace: "default",
				},
			}
			Expect(hubClient.Create(ctx, configMap)).Should(Succeed())
			waitForCache(configMapGVR, configMap)
			drainEvents()
		})

		AfterAll(func() {
			Expect(client.IgnoreNotFound(hubClient.Delete(ctx, configMap))).Should(Succeed())
		})

		It("should generate no policy while the resource carries no annotation", func() {
			Expect(reconcile(configMapGVK, configMap)).Should(Succeed())
			_, err := generatedPolicyFor(configMapGVK, configMap)
			Expect(apierrors.IsNotFound(err)).Should(BeTrue(), "got %v, want a not found error", err)
			Expect(drainEvents()).Should(BeEmpty())
		})

		It("should generate a placement policy when the annotation is set", func() {
			annotate(configMap, "env=staging,count=All")
			Expect(reconcile(configMapGVK, configMap)).Should(Succeed())

			policy, err := generatedPolicyFor(configMapGVK, configMap)
			Expect(err).Should(Succeed())

			spec := policySpec(policy)
			Expect(spec.ClusterSelectors).Should(HaveLen(1))
			Expect(spec.ClusterSelectors[0].Count).Should(Equal(ptrIntOrString(countAll)))
			Expect(spec.ClusterSelectors[0].Terms[0].MatchLabels).Should(Equal(map[string]string{"env": "staging"}))
			Expect(spec.ResourceSelectors).Should(Equal([]kfplacementv1alpha1.ResourceSelector{{
				APIGroup:   "",
				APIVersion: "v1",
				Kind:       "ConfigMap",
				Name:       configMap.Name,
			}}))

			Expect(policy.GetNamespace()).Should(Equal(configMap.Namespace), "a namespaced resource must generate a policy in its own namespace")
			Expect(policy.GetLabels()).Should(HaveKeyWithValue(kfplacementv1alpha1.ParentKindLabel, "ConfigMap"))
			Expect(policy.GetLabels()).Should(HaveKeyWithValue(kfplacementv1alpha1.ParentNameLabel, configMap.Name))
			Expect(policy.GetLabels()).Should(HaveKeyWithValue(kfplacementv1alpha1.ParentAPIGroupLabel, ""))

			Expect(policy.GetOwnerReferences()).Should(HaveLen(1))
			owner := policy.GetOwnerReferences()[0]
			Expect(owner.UID).Should(Equal(configMap.UID))
			Expect(owner.Controller).Should(BeNil(), "a generated policy must not claim controller ownership")
			Expect(owner.BlockOwnerDeletion).Should(BeNil(), "a generated policy must not block deletion of the resource it came from")

			Expect(drainEvents()).Should(Equal([]string{EventReasonPolicyCreated}))
		})

		It("should not touch the policy when nothing changed", func() {
			before, err := generatedPolicyFor(configMapGVK, configMap)
			Expect(err).Should(Succeed())

			Expect(reconcile(configMapGVK, configMap)).Should(Succeed())

			after, err := generatedPolicyFor(configMapGVK, configMap)
			Expect(err).Should(Succeed())
			// A resource version that moved means the reconciler wrote to the API server without
			// anything having changed, which every pass would then repeat.
			Expect(after.GetResourceVersion()).Should(Equal(before.GetResourceVersion()))
			Expect(drainEvents()).Should(BeEmpty())
		})

		It("should update the policy when the annotation changes", func() {
			annotate(configMap, "env=canary,region=eastus,count=3")
			Expect(reconcile(configMapGVK, configMap)).Should(Succeed())

			policy, err := generatedPolicyFor(configMapGVK, configMap)
			Expect(err).Should(Succeed())
			spec := policySpec(policy)
			Expect(spec.ClusterSelectors).Should(HaveLen(1))
			Expect(spec.ClusterSelectors[0].Count).Should(Equal(ptrIntOrString("3")))
			Expect(spec.ClusterSelectors[0].Terms[0].MatchLabels).Should(Equal(map[string]string{
				"env":                      "canary",
				corev1.LabelTopologyRegion: "eastus",
			}))
			Expect(drainEvents()).Should(Equal([]string{EventReasonPolicyUpdated}))
		})

		It("should restore the policy when someone edits it", func() {
			policy, err := generatedPolicyFor(configMapGVK, configMap)
			Expect(err).Should(Succeed())
			policySpec(policy).ClusterSelectors = nil
			Expect(hubClient.Update(ctx, policy)).Should(Succeed())

			// In the running agent this reconcile is triggered by the watch on the generated
			// policies themselves; an edit produces no event on the ConfigMap.
			Expect(reconcile(configMapGVK, configMap)).Should(Succeed())

			restored, err := generatedPolicyFor(configMapGVK, configMap)
			Expect(err).Should(Succeed())
			Expect(policySpec(restored).ClusterSelectors).Should(HaveLen(1))
			Expect(drainEvents()).Should(Equal([]string{EventReasonPolicyUpdated}))
		})

		It("should recreate the policy when someone deletes it", func() {
			policy, err := generatedPolicyFor(configMapGVK, configMap)
			Expect(err).Should(Succeed())
			Expect(hubClient.Delete(ctx, policy)).Should(Succeed())

			Expect(reconcile(configMapGVK, configMap)).Should(Succeed())

			_, err = generatedPolicyFor(configMapGVK, configMap)
			Expect(err).Should(Succeed(), "the deleted policy must be generated again")
			Expect(drainEvents()).Should(Equal([]string{EventReasonPolicyCreated}))
		})

		It("should keep the policy and warn when the annotation becomes invalid", func() {
			annotate(configMap, "env")
			Expect(reconcile(configMapGVK, configMap)).Should(Succeed(), "a malformed annotation must not be retried")

			_, err := generatedPolicyFor(configMapGVK, configMap)
			Expect(err).Should(Succeed(), "the policy from the last valid annotation must be left in place")
			Expect(drainEvents()).Should(Equal([]string{EventReasonInvalidAnnotation}))
		})

		It("should delete the policy when the annotation is removed", func() {
			annotate(configMap, "")
			Expect(reconcile(configMapGVK, configMap)).Should(Succeed())

			_, err := generatedPolicyFor(configMapGVK, configMap)
			Expect(apierrors.IsNotFound(err)).Should(BeTrue(), "got %v, want a not found error", err)
			Expect(drainEvents()).Should(Equal([]string{EventReasonPolicyDeleted}))
		})

		It("should delete the policy once the resource itself is gone", func() {
			// Deleting explicitly, rather than leaving the policy to garbage collection, is what
			// keeps an owner reference some other party added from holding the policy up forever.
			// It also means envtest, which runs no garbage collector, can observe the cleanup.
			annotate(configMap, "env=staging")
			Expect(reconcile(configMapGVK, configMap)).Should(Succeed())
			_, err := generatedPolicyFor(configMapGVK, configMap)
			Expect(err).Should(Succeed())
			drainEvents()

			Expect(hubClient.Delete(ctx, configMap)).Should(Succeed())
			Eventually(func() error {
				_, err := cachedObject(configMapGVR, configMap)
				return err
			}, eventuallyTimeout, eventuallyInterval).ShouldNot(Succeed(), "the informer cache never observed the deletion")

			Expect(reconcile(configMapGVK, configMap)).Should(Succeed())
			_, err = generatedPolicyFor(configMapGVK, configMap)
			Expect(apierrors.IsNotFound(err)).Should(BeTrue(), "got %v, want a not found error", err)
			// No event: the resource an event would be recorded on no longer exists.
			Expect(drainEvents()).Should(BeEmpty())
		})
	})

	Context("a cluster scoped resource", Ordered, func() {
		var namespace *corev1.Namespace

		BeforeAll(func() {
			configMapCount++
			namespace = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("team-%d", configMapCount)},
			}
			Expect(hubClient.Create(ctx, namespace)).Should(Succeed())
			waitForCache(namespaceGVR, namespace)
			drainEvents()
		})

		AfterAll(func() {
			Expect(client.IgnoreNotFound(hubClient.Delete(ctx, namespace))).Should(Succeed())
		})

		It("should generate a cluster scoped policy", func() {
			annotate(namespace, "env=staging")
			Expect(reconcile(namespaceGVK, namespace)).Should(Succeed())

			policy, err := generatedPolicyFor(namespaceGVK, namespace)
			Expect(err).Should(Succeed())
			Expect(policy).Should(BeAssignableToTypeOf(&kfplacementv1alpha1.ClusterPlacementPolicy{}),
				"a cluster scoped resource must generate a ClusterPlacementPolicy, since a namespaced policy owned by it would never be collected")
			Expect(policy.GetNamespace()).Should(BeEmpty())
			Expect(policySpec(policy).ClusterSelectors).Should(HaveLen(1))
			Expect(drainEvents()).Should(Equal([]string{EventReasonPolicyCreated}))
		})

		It("should delete the cluster scoped policy when the annotation is removed", func() {
			annotate(namespace, "")
			Expect(reconcile(namespaceGVK, namespace)).Should(Succeed())

			_, err := generatedPolicyFor(namespaceGVK, namespace)
			Expect(apierrors.IsNotFound(err)).Should(BeTrue(), "got %v, want a not found error", err)
			Expect(drainEvents()).Should(Equal([]string{EventReasonPolicyDeleted}))
		})
	})
})

func ptrIntOrString(value string) *intstr.IntOrString {
	parsed := intstr.Parse(value)
	return &parsed
}
