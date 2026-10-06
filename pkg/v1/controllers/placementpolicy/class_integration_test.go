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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
)

var _ = Describe("cluster provider class resolution", Ordered, func() {
	var counter int
	nextName := func(prefix string) string {
		counter++
		return fmt.Sprintf("%s-class-%d", prefix, counter)
	}

	// createClass registers a class admitting the region label (any value unless restricted) and
	// schedules its removal.
	createClass := func(name string, mutate func(*kfplacementv1alpha1.ClusterProviderClass)) *kfplacementv1alpha1.ClusterProviderClass {
		class := &kfplacementv1alpha1.ClusterProviderClass{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: kfplacementv1alpha1.ClusterProviderClassSpec{
				ProvisionerName:    "test.kubefleet.dev",
				SelectorVocabulary: &kfplacementv1alpha1.SelectorVocabulary{LabelKeys: []kfplacementv1alpha1.LabelKeyRule{{Key: testRegionLabel}}},
			},
		}
		if mutate != nil {
			mutate(class)
		}
		Expect(k8sClient.Create(ctx, class)).Should(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, class))).Should(Succeed()) })
		return class
	}

	claimOf := func(policy *kfplacementv1alpha1.PlacementPolicy) func(Gomega) *kfplacementv1alpha1.ClusterClaim {
		return func(g Gomega) *kfplacementv1alpha1.ClusterClaim {
			claim := &kfplacementv1alpha1.ClusterClaim{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: claimName(policy, 0)}, claim)).Should(Succeed())
			return claim
		}
	}
	noClaim := func(policy *kfplacementv1alpha1.PlacementPolicy) func() bool {
		return func() bool {
			return errors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: claimName(policy, 0)}, &kfplacementv1alpha1.ClusterClaim{}))
		}
	}
	scheduledMessage := func(policy *kfplacementv1alpha1.PlacementPolicy) func(Gomega) string {
		return func(g Gomega) string {
			cond, _, _, err := scheduledConditionOf(client.ObjectKeyFromObject(policy))()
			g.Expect(err).Should(Succeed())
			g.Expect(cond).NotTo(BeNil())
			return cond.Message
		}
	}
	warnings := func(policy *kfplacementv1alpha1.PlacementPolicy) func(Gomega) []string {
		return func(g Gomega) []string {
			events := &corev1.EventList{}
			g.Expect(k8sClient.List(ctx, events, client.InNamespace(testNamespace))).Should(Succeed())
			var reasons []string
			for _, event := range events.Items {
				if event.InvolvedObject.Name == policy.Name && event.Type == corev1.EventTypeWarning {
					reasons = append(reasons, event.Reason)
				}
			}
			return reasons
		}
	}

	AfterEach(func() {
		policies := &kfplacementv1alpha1.PlacementPolicyList{}
		Expect(k8sClient.List(ctx, policies, client.InNamespace(testNamespace))).Should(Succeed())
		for i := range policies.Items {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &policies.Items[i]))).Should(Succeed())
		}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.List(ctx, policies, client.InNamespace(testNamespace))).Should(Succeed())
			g.Expect(policies.Items).Should(BeEmpty())
			claims := &kfplacementv1alpha1.ClusterClaimList{}
			g.Expect(k8sClient.List(ctx, claims)).Should(Succeed())
			g.Expect(claims.Items).Should(BeEmpty())
		}, eventuallyTimeout, pollInterval).Should(Succeed())
		Expect(k8sClient.DeleteAllOf(ctx, &corev1.Event{}, client.InNamespace(testNamespace))).Should(Succeed())
	})

	It("stamps the class the policy names on the claim, and replaces the claim when the class changes", func() {
		first := createClass(nextName("first"), nil)
		second := createClass(nextName("second"), nil)
		policy := newPolicy(nextName("pp"), regionSelector("nowhere", ptr.To(intstr.FromInt32(1)), nil))
		policy.Spec.ClusterProviderClassName = ptr.To(first.Name)
		Expect(k8sClient.Create(ctx, policy)).Should(Succeed())

		var firstUID types.UID
		Eventually(func(g Gomega) {
			claim := claimOf(policy)(g)
			g.Expect(claim.Spec.ClusterProviderClassName).Should(Equal(first.Name))
			firstUID = claim.UID
		}, eventuallyTimeout, pollInterval).Should(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), policy)).Should(Succeed())
			policy.Spec.ClusterProviderClassName = ptr.To(second.Name)
			g.Expect(k8sClient.Update(ctx, policy)).Should(Succeed())
		}, eventuallyTimeout, pollInterval).Should(Succeed())
		Eventually(func(g Gomega) {
			claim := claimOf(policy)(g)
			g.Expect(claim.Spec.ClusterProviderClassName).Should(Equal(second.Name))
			g.Expect(claim.UID).ShouldNot(Equal(firstUID), "the claim of the old class is withdrawn and a fresh one issued")
		}, eventuallyTimeout, pollInterval).Should(Succeed())
	})

	It("uses the fleet's default class when the policy names none", func() {
		policy := newPolicy(nextName("pp"), regionSelector("nowhere", ptr.To(intstr.FromInt32(1)), nil))
		Expect(k8sClient.Create(ctx, policy)).Should(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(claimOf(policy)(g).Spec.ClusterProviderClassName).Should(Equal(defaultClassName))
		}, eventuallyTimeout, pollInterval).Should(Succeed())
	})

	It("issues no claim for a class that does not exist, and says why", func() {
		policy := newPolicy(nextName("pp"), regionSelector("nowhere", ptr.To(intstr.FromInt32(1)), nil))
		policy.Spec.ClusterProviderClassName = ptr.To("missing")
		Expect(k8sClient.Create(ctx, policy)).Should(Succeed())

		Eventually(scheduledMessage(policy), eventuallyTimeout, pollInterval).Should(ContainSubstring(`the cluster provider class "missing" does not exist`))
		Eventually(wantScheduledState(client.ObjectKeyFromObject(policy), metav1.ConditionFalse, kfplacementv1alpha1.PlacementPolicyScheduledCondReasonFailedToFindSomeClusters, 1, 0), eventuallyTimeout, pollInterval).Should(Succeed())
		Eventually(warnings(policy), eventuallyTimeout, pollInterval).Should(ContainElement(EventReasonClaimNotIssued))
		Consistently(noClaim(policy), consistentlyDuration, pollInterval).Should(BeTrue())
	})

	It("issues no claim while the fleet has no default class, and recovers when one appears", func() {
		Expect(k8sClient.Delete(ctx, defaultClass())).Should(Succeed())
		DeferCleanup(func() {
			Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, defaultClass()))).Should(Succeed())
		})
		policy := newPolicy(nextName("pp"), regionSelector("nowhere", ptr.To(intstr.FromInt32(1)), nil))
		Expect(k8sClient.Create(ctx, policy)).Should(Succeed())

		Eventually(scheduledMessage(policy), eventuallyTimeout, pollInterval).Should(ContainSubstring("the fleet has no default class"))
		Consistently(func(g Gomega) {
			g.Expect(noClaim(policy)()).Should(BeTrue())
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), policy)).Should(Succeed())
			g.Expect(policy.Finalizers).ShouldNot(ContainElement(claimCleanupFinalizer), "a policy with nothing to issue carries no cleanup finalizer")
		}, consistentlyDuration, pollInterval).Should(Succeed())

		Expect(k8sClient.Create(ctx, defaultClass())).Should(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(claimOf(policy)(g).Spec.ClusterProviderClassName).Should(Equal(defaultClassName))
		}, eventuallyTimeout, pollInterval).Should(Succeed())
		Eventually(scheduledMessage(policy), eventuallyTimeout, pollInterval).ShouldNot(ContainSubstring("no new cluster claims are issued"))
	})

	It("keeps an outstanding claim while its class is gone, and reports why no new one is issued", func() {
		class := createClass(nextName("transient"), nil)
		policy := newPolicy(nextName("pp"), regionSelector("nowhere", ptr.To(intstr.FromInt32(1)), nil))
		policy.Spec.ClusterProviderClassName = ptr.To(class.Name)
		Expect(k8sClient.Create(ctx, policy)).Should(Succeed())
		var uid types.UID
		Eventually(func(g Gomega) { uid = claimOf(policy)(g).UID }, eventuallyTimeout, pollInterval).Should(Succeed())

		// An admin deleting and recreating the class, or moving the default annotation between
		// classes, must not tear down provisioning that is in flight.
		Expect(k8sClient.Delete(ctx, class)).Should(Succeed())
		Eventually(scheduledMessage(policy), eventuallyTimeout, pollInterval).Should(ContainSubstring(fmt.Sprintf("the cluster provider class %q does not exist", class.Name)))
		Consistently(func(g Gomega) {
			g.Expect(claimOf(policy)(g).UID).Should(Equal(uid), "the claim outlives its class")
		}, consistentlyDuration, pollInterval).Should(Succeed())

		createClass(class.Name, nil)
		Eventually(scheduledMessage(policy), eventuallyTimeout, pollInterval).ShouldNot(ContainSubstring("no new cluster claims are issued"))
		Expect(claimOf(policy)(Default).UID).Should(Equal(uid), "the same claim serves the recreated class")
	})

	It("reports nothing about classes for a policy that wants no claim", func() {
		Expect(k8sClient.Delete(ctx, defaultClass())).Should(Succeed())
		DeferCleanup(func() {
			Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, defaultClass()))).Should(Succeed())
		})
		selector := regionSelector("nowhere", ptr.To(intstr.FromInt32(1)), nil)
		selector.WhenUnfulfilled = kfplacementv1alpha1.WhenUnfulfilledOptionKeepSearching
		policy := newPolicy(nextName("pp"), selector)
		Expect(k8sClient.Create(ctx, policy)).Should(Succeed())

		Eventually(wantScheduledState(client.ObjectKeyFromObject(policy), metav1.ConditionFalse, kfplacementv1alpha1.PlacementPolicyScheduledCondReasonFailedToFindSomeClusters, 1, 0), eventuallyTimeout, pollInterval).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(scheduledMessage(policy)(g)).ShouldNot(ContainSubstring("no new cluster claims are issued"))
			g.Expect(warnings(policy)(g)).Should(BeEmpty())
		}, consistentlyDuration, pollInterval).Should(Succeed())
	})

	It("issues no claim for a selector outside the class's vocabulary, and names the term", func() {
		class := createClass(nextName("east-only"), func(c *kfplacementv1alpha1.ClusterProviderClass) {
			c.Spec.SelectorVocabulary.LabelKeys[0].Values = []string{"eastus"}
		})
		policy := newPolicy(nextName("pp"), regionSelector("antarctica", ptr.To(intstr.FromInt32(1)), nil))
		policy.Spec.ClusterProviderClassName = ptr.To(class.Name)
		Expect(k8sClient.Create(ctx, policy)).Should(Succeed())

		Eventually(scheduledMessage(policy), eventuallyTimeout, pollInterval).Should(ContainSubstring(`the value "antarctica" for label key "topology.kubernetes.io/region" is not in the selector vocabulary`))
		Eventually(warnings(policy), eventuallyTimeout, pollInterval).Should(ContainElement(EventReasonClaimNotIssued))
		Consistently(noClaim(policy), consistentlyDuration, pollInterval).Should(BeTrue())

		// Widening the vocabulary issues the claim; the class watch re-evaluates the policy.
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(class), class)).Should(Succeed())
			class.Spec.SelectorVocabulary.LabelKeys[0].Values = nil
			g.Expect(k8sClient.Update(ctx, class)).Should(Succeed())
		}, eventuallyTimeout, pollInterval).Should(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(claimOf(policy)(g).Spec.ClusterProviderClassName).Should(Equal(class.Name))
		}, eventuallyTimeout, pollInterval).Should(Succeed())
	})

	It("approves automatically per the class, and re-asserts an approval that went missing", func() {
		class := createClass(nextName("auto"), func(c *kfplacementv1alpha1.ClusterProviderClass) {
			c.Spec.Approval = kfplacementv1alpha1.ClusterClaimApprovalModeAutomatic
		})
		policy := newPolicy(nextName("pp"), regionSelector("nowhere", ptr.To(intstr.FromInt32(1)), nil))
		policy.Spec.ClusterProviderClassName = ptr.To(class.Name)
		Expect(k8sClient.Create(ctx, policy)).Should(Succeed())

		approved := func(g Gomega) {
			claim := claimOf(policy)(g)
			cond := meta.FindStatusCondition(claim.Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeApproved)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).Should(Equal(metav1.ConditionTrue))
			g.Expect(cond.Reason).Should(Equal(kfplacementv1alpha1.ClusterClaimApprovedCondReasonAutomaticallyApproved))
		}
		Eventually(approved, eventuallyTimeout, pollInterval).Should(Succeed())

		// As a controller stopped between the create and the stamp would have left it.
		Eventually(func(g Gomega) {
			claim := claimOf(policy)(g)
			meta.RemoveStatusCondition(&claim.Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeApproved)
			g.Expect(k8sClient.Status().Update(ctx, claim)).Should(Succeed())
		}, eventuallyTimeout, pollInterval).Should(Succeed())
		Eventually(approved, eventuallyTimeout, pollInterval).Should(Succeed())

		// A denial by an approver is not overwritten.
		Eventually(func(g Gomega) {
			claim := claimOf(policy)(g)
			meta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{Type: kfplacementv1alpha1.ClusterClaimCondTypeApproved, Status: metav1.ConditionFalse, Reason: kfplacementv1alpha1.ClusterClaimApprovedCondReasonDenied})
			g.Expect(k8sClient.Status().Update(ctx, claim)).Should(Succeed())
		}, eventuallyTimeout, pollInterval).Should(Succeed())
		Consistently(func(g Gomega) {
			cond := meta.FindStatusCondition(claimOf(policy)(g).Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeApproved)
			g.Expect(cond.Reason).Should(Equal(kfplacementv1alpha1.ClusterClaimApprovedCondReasonDenied))
		}, consistentlyDuration, pollInterval).Should(Succeed())
	})

	It("leaves approval to an approver when the class is manual", func() {
		policy := newPolicy(nextName("pp"), regionSelector("nowhere", ptr.To(intstr.FromInt32(1)), nil))
		Expect(k8sClient.Create(ctx, policy)).Should(Succeed())
		Eventually(func(g Gomega) { claimOf(policy)(g) }, eventuallyTimeout, pollInterval).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(meta.FindStatusCondition(claimOf(policy)(g).Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeApproved)).Should(BeNil())
		}, consistentlyDuration, pollInterval).Should(Succeed())
	})
})
