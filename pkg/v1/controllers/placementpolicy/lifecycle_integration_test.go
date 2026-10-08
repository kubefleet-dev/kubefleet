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
	"time"

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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
)

// The specs here play the provider and the approver by hand on the claim status, and run their
// classes with second-long timers so that the controller's expiry and retry fire within a spec.
var _ = Describe("cluster claim lifecycle: expiry, terminal claims, retry, limits", Ordered, func() {
	var counter int
	nextName := func(prefix string) string {
		counter++
		return fmt.Sprintf("%s-life-%d", prefix, counter)
	}
	short := &metav1.Duration{Duration: time.Second}
	// createClass registers an Automatic class admitting the region label, with the given tweaks,
	// and schedules its removal.
	createClass := func(mutate func(*kfplacementv1alpha1.ClusterProviderClassSpec)) *kfplacementv1alpha1.ClusterProviderClass {
		class := &kfplacementv1alpha1.ClusterProviderClass{
			ObjectMeta: metav1.ObjectMeta{Name: nextName("class")},
			Spec: kfplacementv1alpha1.ClusterProviderClassSpec{
				ProvisionerName:    "test.kubefleet.dev",
				Approval:           kfplacementv1alpha1.ClusterClaimApprovalModeAutomatic,
				SelectorVocabulary: &kfplacementv1alpha1.SelectorVocabulary{LabelKeys: []kfplacementv1alpha1.LabelKeyRule{{Key: testRegionLabel}}},
			},
		}
		if mutate != nil {
			mutate(&class.Spec)
		}
		Expect(k8sClient.Create(ctx, class)).Should(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, class))).Should(Succeed()) })
		return class
	}
	createPolicy := func(class *kfplacementv1alpha1.ClusterProviderClass, selectors ...kfplacementv1alpha1.ClusterSelector) *kfplacementv1alpha1.PlacementPolicy {
		policy := newPolicy(nextName("pp"), selectors...)
		policy.Spec.ClusterProviderClassName = ptr.To(class.Name)
		Expect(k8sClient.Create(ctx, policy)).Should(Succeed())
		return policy
	}
	claimOf := func(policy *kfplacementv1alpha1.PlacementPolicy, selector int) func(Gomega) *kfplacementv1alpha1.ClusterClaim {
		return func(g Gomega) *kfplacementv1alpha1.ClusterClaim {
			claim := &kfplacementv1alpha1.ClusterClaim{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: claimName(policy, selector)}, claim)).Should(Succeed())
			return claim
		}
	}
	waitClaim := func(policy *kfplacementv1alpha1.PlacementPolicy) *kfplacementv1alpha1.ClusterClaim {
		var claim *kfplacementv1alpha1.ClusterClaim
		Eventually(func(g Gomega) { claim = claimOf(policy, 0)(g) }, eventuallyTimeout, pollInterval).Should(Succeed())
		return claim
	}
	// setStatus plays the provider or the approver: it re-reads the claim and applies the mutation
	// to its status, retrying on conflict.
	setStatus := func(claim *kfplacementv1alpha1.ClusterClaim, mutate func(*kfplacementv1alpha1.ClusterClaim)) {
		Eventually(func(g Gomega) {
			latest := &kfplacementv1alpha1.ClusterClaim{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(claim), latest)).Should(Succeed())
			mutate(latest)
			g.Expect(k8sClient.Status().Update(ctx, latest)).Should(Succeed())
		}, eventuallyTimeout, pollInterval).Should(Succeed())
	}
	cond := func(condType string, status metav1.ConditionStatus, reason string) metav1.Condition {
		return metav1.Condition{Type: condType, Status: status, Reason: reason, Message: "by test"}
	}
	complete := func(claim *kfplacementv1alpha1.ClusterClaim, clusterName string) {
		setStatus(claim, func(c *kfplacementv1alpha1.ClusterClaim) {
			c.Status.ProvisionedClusterName = ptr.To(clusterName)
			meta.SetStatusCondition(&c.Status.Conditions, cond(kfplacementv1alpha1.ClusterClaimCondTypeAccepted, metav1.ConditionTrue, kfplacementv1alpha1.ClusterClaimAcceptedCondReasonAccepted))
			meta.SetStatusCondition(&c.Status.Conditions, cond(kfplacementv1alpha1.ClusterClaimCondTypeCompleted, metav1.ConditionTrue, kfplacementv1alpha1.ClusterClaimCompletedCondReasonFulfilled))
		})
	}
	fail := func(claim *kfplacementv1alpha1.ClusterClaim) {
		setStatus(claim, func(c *kfplacementv1alpha1.ClusterClaim) {
			meta.SetStatusCondition(&c.Status.Conditions, cond(kfplacementv1alpha1.ClusterClaimCondTypeAccepted, metav1.ConditionTrue, kfplacementv1alpha1.ClusterClaimAcceptedCondReasonAccepted))
			meta.SetStatusCondition(&c.Status.Conditions, cond(kfplacementv1alpha1.ClusterClaimCondTypeCompleted, metav1.ConditionFalse, kfplacementv1alpha1.ClusterClaimCompletedCondReasonFailed))
		})
	}
	expiredAs := func(policy *kfplacementv1alpha1.PlacementPolicy, reason string) func(Gomega) {
		return func(g Gomega) {
			expired := meta.FindStatusCondition(claimOf(policy, 0)(g).Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeExpired)
			g.Expect(expired).NotTo(BeNil())
			g.Expect(expired.Status).Should(Equal(metav1.ConditionTrue))
			g.Expect(expired.Reason).Should(Equal(reason))
		}
	}
	notExpired := func(policy *kfplacementv1alpha1.PlacementPolicy) func(Gomega) {
		return func(g Gomega) {
			g.Expect(meta.FindStatusCondition(claimOf(policy, 0)(g).Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeExpired)).Should(BeNil())
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
	heldUID := func(policy *kfplacementv1alpha1.PlacementPolicy, uid types.UID, as string) {
		Eventually(scheduledMessage(policy), eventuallyTimeout, pollInterval).Should(ContainSubstring("is held as "+as), "the Scheduled message names the held claim")
		Consistently(func(g Gomega) {
			g.Expect(claimOf(policy, 0)(g).UID).Should(Equal(uid), "a terminal claim is the record and is neither withdrawn nor re-issued")
		}, consistentlyDuration, pollInterval).Should(Succeed())
	}
	joinedCluster := func(name string, labels map[string]string, taints ...clusterv1beta1.Taint) *clusterv1beta1.MemberCluster {
		mc := newMemberCluster(name, labels, taints...)
		Expect(k8sClient.Create(ctx, mc)).Should(Succeed())
		markJoined(mc)
		return mc
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
		// The specs play the provider by hand; a finalizer a failed spec left behind must not
		// hang the cleanup and cascade into the specs after it.
		claims := &kfplacementv1alpha1.ClusterClaimList{}
		Expect(k8sClient.List(ctx, claims)).Should(Succeed())
		for i := range claims.Items {
			if controllerutil.RemoveFinalizer(&claims.Items[i], kfplacementv1alpha1.FulfillerFinalizer) {
				Expect(client.IgnoreNotFound(k8sClient.Update(ctx, &claims.Items[i]))).Should(Succeed())
			}
		}
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
		memberClusters := &clusterv1beta1.MemberClusterList{}
		Expect(k8sClient.List(ctx, memberClusters)).Should(Succeed())
		for i := range memberClusters.Items {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &memberClusters.Items[i]))).Should(Succeed())
		}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.List(ctx, memberClusters)).Should(Succeed())
			g.Expect(memberClusters.Items).Should(BeEmpty())
		}, eventuallyTimeout, pollInterval).Should(Succeed())
		Expect(k8sClient.DeleteAllOf(ctx, &corev1.Event{}, client.InNamespace(testNamespace))).Should(Succeed())
	})

	It("expires an approved claim no provider accepted, keeps the record, and reports it", func() {
		class := createClass(func(spec *kfplacementv1alpha1.ClusterProviderClassSpec) { spec.PendingClaimTTL = short })
		policy := createPolicy(class, regionSelector("nowhere", ptr.To(intstr.FromInt32(1)), nil))
		claim := waitClaim(policy)

		Eventually(expiredAs(policy, kfplacementv1alpha1.ClusterClaimExpiredCondReasonPendingTimeout), eventuallyTimeout, pollInterval).Should(Succeed())
		heldUID(policy, claim.UID, "Expired/PendingTimeout")
		Eventually(warnings(policy), eventuallyTimeout, pollInterval).Should(ContainElement(EventReasonClaimExpired))
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), policy)).Should(Succeed())
			g.Expect(policy.Status.ActiveClusterClaims).Should(HaveValue(Equal(int32(1))), "a held claim still occupies its slot")
		}, eventuallyTimeout, pollInterval).Should(Succeed())
	})

	It("never expires an unapproved claim", func() {
		class := createClass(func(spec *kfplacementv1alpha1.ClusterProviderClassSpec) {
			spec.Approval = kfplacementv1alpha1.ClusterClaimApprovalModeManual
			spec.PendingClaimTTL = short
		})
		policy := createPolicy(class, regionSelector("nowhere", ptr.To(intstr.FromInt32(1)), nil))
		waitClaim(policy)
		Consistently(notExpired(policy), 3*time.Second, pollInterval).Should(Succeed())
	})

	It("does not expire an accepted claim, whether by condition or by the provider finalizer alone", func() {
		class := createClass(func(spec *kfplacementv1alpha1.ClusterProviderClassSpec) {
			spec.PendingClaimTTL = &metav1.Duration{Duration: 3 * time.Second}
		})
		byCondition := createPolicy(class, regionSelector("nowhere", ptr.To(intstr.FromInt32(1)), nil))
		setStatus(waitClaim(byCondition), func(c *kfplacementv1alpha1.ClusterClaim) {
			meta.SetStatusCondition(&c.Status.Conditions, cond(kfplacementv1alpha1.ClusterClaimCondTypeAccepted, metav1.ConditionTrue, kfplacementv1alpha1.ClusterClaimAcceptedCondReasonAccepted))
		})
		byFinalizer := createPolicy(class, regionSelector("elsewhere", ptr.To(intstr.FromInt32(1)), nil))
		Eventually(func(g Gomega) {
			claim := claimOf(byFinalizer, 0)(g)
			controllerutil.AddFinalizer(claim, kfplacementv1alpha1.FulfillerFinalizer)
			g.Expect(k8sClient.Update(ctx, claim)).Should(Succeed())
		}, eventuallyTimeout, pollInterval).Should(Succeed())

		Consistently(func(g Gomega) {
			notExpired(byCondition)(g)
			notExpired(byFinalizer)(g)
		}, 7*time.Second, pollInterval).Should(Succeed())
	})

	It("expires a fulfilled claim whose cluster never joined, and resumes rotation on a late join", func() {
		class := createClass(func(spec *kfplacementv1alpha1.ClusterProviderClassSpec) { spec.JoinTimeout = short })
		policy := createPolicy(class, regionSelector("australiaeast", ptr.To(intstr.FromInt32(2)), nil))
		claim := waitClaim(policy)
		late := nextName("mc-late")
		complete(claim, late)

		Eventually(expiredAs(policy, kfplacementv1alpha1.ClusterClaimExpiredCondReasonJoinTimeout), eventuallyTimeout, pollInterval).Should(Succeed())
		Eventually(scheduledMessage(policy), eventuallyTimeout, pollInterval).Should(ContainSubstring(fmt.Sprintf("the cluster %q provisioned for this claim has not joined", late)))
		heldUID(policy, claim.UID, "Expired/JoinTimeout")

		// The cluster joins after all, matching the selector: the record did its job and the
		// selector still wants one more, so the claim rotates to a fresh one.
		joinedCluster(late, map[string]string{testRegionLabel: "australiaeast"})
		Eventually(func(g Gomega) {
			g.Expect(claimOf(policy, 0)(g).UID).ShouldNot(Equal(claim.UID))
		}, eventuallyTimeout, pollInterval).Should(Succeed())
	})

	It("expires a fulfilled claim whose cluster joined but does not match, names why, and rotates once it does", func() {
		class := createClass(func(spec *kfplacementv1alpha1.ClusterProviderClassSpec) { spec.JoinTimeout = short })
		policy := createPolicy(class, regionSelector("australiaeast", ptr.To(intstr.FromInt32(2)), nil))
		claim := waitClaim(policy)
		mc := joinedCluster(nextName("mc-wrong"), map[string]string{testRegionLabel: "westus"})
		complete(claim, mc.Name)

		Eventually(expiredAs(policy, kfplacementv1alpha1.ClusterClaimExpiredCondReasonNotMatching), eventuallyTimeout, pollInterval).Should(Succeed())
		Eventually(scheduledMessage(policy), eventuallyTimeout, pollInterval).Should(ContainSubstring("it does not satisfy the selector's term"))
		heldUID(policy, claim.UID, "Expired/NotMatching")

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mc), mc)).Should(Succeed())
			mc.Labels[testRegionLabel] = "australiaeast"
			g.Expect(k8sClient.Update(ctx, mc)).Should(Succeed())
		}, eventuallyTimeout, pollInterval).Should(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(claimOf(policy, 0)(g).UID).ShouldNot(Equal(claim.UID))
		}, eventuallyTimeout, pollInterval).Should(Succeed())
	})

	It("expires a fulfilled claim whose cluster is tainted beyond the policy's tolerations as NotMatching", func() {
		class := createClass(func(spec *kfplacementv1alpha1.ClusterProviderClassSpec) { spec.JoinTimeout = short })
		policy := createPolicy(class, regionSelector("australiaeast", ptr.To(intstr.FromInt32(1)), nil))
		claim := waitClaim(policy)
		mc := joinedCluster(nextName("mc-tainted"), map[string]string{testRegionLabel: "australiaeast"}, clusterv1beta1.Taint{Key: "dedicated", Value: "gpu", Effect: corev1.TaintEffectNoSchedule})
		complete(claim, mc.Name)

		Eventually(expiredAs(policy, kfplacementv1alpha1.ClusterClaimExpiredCondReasonNotMatching), eventuallyTimeout, pollInterval).Should(Succeed())
		Eventually(scheduledMessage(policy), eventuallyTimeout, pollInterval).Should(ContainSubstring("its taints are not tolerated by the policy"))
	})

	It("holds a failed claim until the selector changes, then issues a fresh one", func() {
		class := createClass(nil)
		policy := createPolicy(class, regionSelector("nowhere", ptr.To(intstr.FromInt32(1)), nil))
		claim := waitClaim(policy)
		fail(claim)
		heldUID(policy, claim.UID, "Completed/Failed")
		Eventually(warnings(policy), eventuallyTimeout, pollInterval).Should(ContainElement(EventReasonClaimHeld))

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), policy)).Should(Succeed())
			policy.Spec.ClusterSelectors[0].Terms[0].MatchLabels[testRegionLabel] = "elsewhere"
			g.Expect(k8sClient.Update(ctx, policy)).Should(Succeed())
		}, eventuallyTimeout, pollInterval).Should(Succeed())
		Eventually(func(g Gomega) {
			fresh := claimOf(policy, 0)(g)
			g.Expect(fresh.UID).ShouldNot(Equal(claim.UID))
			g.Expect(terminalCondition(fresh)).Should(BeNil())
		}, eventuallyTimeout, pollInterval).Should(Succeed())
	})

	It("retries a failed claim after retryAfter when the class says so, but always holds a denial", func() {
		class := createClass(func(spec *kfplacementv1alpha1.ClusterProviderClassSpec) {
			spec.OnFailure = kfplacementv1alpha1.ClusterClaimFailureActionRetry
			spec.RetryAfter = short
		})
		retried := createPolicy(class, regionSelector("nowhere", ptr.To(intstr.FromInt32(1)), nil))
		claim := waitClaim(retried)
		fail(claim)
		Eventually(func(g Gomega) {
			fresh := claimOf(retried, 0)(g)
			g.Expect(fresh.UID).ShouldNot(Equal(claim.UID))
			g.Expect(terminalCondition(fresh)).Should(BeNil())
		}, eventuallyTimeout, pollInterval).Should(Succeed())

		held := createPolicy(class, regionSelector("elsewhere", ptr.To(intstr.FromInt32(1)), nil))
		deniedClaim := waitClaim(held)
		setStatus(deniedClaim, func(c *kfplacementv1alpha1.ClusterClaim) {
			meta.SetStatusCondition(&c.Status.Conditions, cond(kfplacementv1alpha1.ClusterClaimCondTypeApproved, metav1.ConditionFalse, kfplacementv1alpha1.ClusterClaimApprovedCondReasonDenied))
		})
		heldUID(held, deniedClaim.UID, "Approved/Denied")
		Consistently(func(g Gomega) {
			g.Expect(claimOf(held, 0)(g).UID).Should(Equal(deniedClaim.UID))
		}, 3*time.Second, pollInterval).Should(Succeed())
	})

	It("withdraws a held claim once other clusters satisfy the selector", func() {
		class := createClass(nil)
		policy := createPolicy(class, regionSelector("australiaeast", ptr.To(intstr.FromInt32(1)), nil))
		claim := waitClaim(policy)
		fail(claim)
		heldUID(policy, claim.UID, "Completed/Failed")

		joinedCluster(nextName("mc-aue"), map[string]string{testRegionLabel: "australiaeast"})
		Eventually(func() bool {
			return errors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: claimName(policy, 0)}, &kfplacementv1alpha1.ClusterClaim{}))
		}, eventuallyTimeout, pollInterval).Should(BeTrue())
		Eventually(scheduledMessage(policy), eventuallyTimeout, pollInterval).ShouldNot(ContainSubstring("is held"))
	})

	It("lets a policy run as many claims as its own limit, within the fleet's", func() {
		class := createClass(func(spec *kfplacementv1alpha1.ClusterProviderClassSpec) {
			spec.Approval = kfplacementv1alpha1.ClusterClaimApprovalModeManual
		})
		policy := newPolicy(nextName("pp"), regionSelector("nowhere", ptr.To(intstr.FromInt32(1)), nil), regionSelector("elsewhere", ptr.To(intstr.FromInt32(1)), nil), regionSelector("anywhere", ptr.To(intstr.FromInt32(1)), nil))
		policy.Spec.ClusterProviderClassName = ptr.To(class.Name)
		policy.Spec.MaxConcurrentClusterClaims = ptr.To[int32](suiteFleetClaimLimit + 1)
		Expect(k8sClient.Create(ctx, policy)).Should(Succeed())

		Eventually(func(g Gomega) {
			claimOf(policy, 0)(g)
			claimOf(policy, 1)(g)
		}, eventuallyTimeout, pollInterval).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(errors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: claimName(policy, 2)}, &kfplacementv1alpha1.ClusterClaim{}))).Should(BeTrue(), "the fleet-wide limit caps the policy's own")
		}, consistentlyDuration, pollInterval).Should(Succeed())
	})

	It("counts a terminal claim and a pending one toward the policy's slots alike", func() {
		class := createClass(func(spec *kfplacementv1alpha1.ClusterProviderClassSpec) {
			spec.Approval = kfplacementv1alpha1.ClusterClaimApprovalModeManual
		})
		policy := newPolicy(nextName("pp"), regionSelector("nowhere", ptr.To(intstr.FromInt32(1)), nil), regionSelector("elsewhere", ptr.To(intstr.FromInt32(1)), nil), regionSelector("anywhere", ptr.To(intstr.FromInt32(1)), nil))
		policy.Spec.ClusterProviderClassName = ptr.To(class.Name)
		policy.Spec.MaxConcurrentClusterClaims = ptr.To[int32](suiteFleetClaimLimit)
		Expect(k8sClient.Create(ctx, policy)).Should(Succeed())
		var first *kfplacementv1alpha1.ClusterClaim
		Eventually(func(g Gomega) { first = claimOf(policy, 0)(g); claimOf(policy, 1)(g) }, eventuallyTimeout, pollInterval).Should(Succeed())
		fail(first)

		heldUID(policy, first.UID, "Completed/Failed")
		Consistently(func(g Gomega) {
			claimOf(policy, 1)(g)
			g.Expect(errors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: claimName(policy, 2)}, &kfplacementv1alpha1.ClusterClaim{}))).Should(BeTrue(), "the record and the pending claim fill both slots")
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), policy)).Should(Succeed())
			g.Expect(policy.Status.ActiveClusterClaims).Should(HaveValue(Equal(int32(2))))
		}, consistentlyDuration, pollInterval).Should(Succeed())
	})

	It("keeps an in-flight claim for a later selector while an earlier one waits for a slot", func() {
		class := createClass(func(spec *kfplacementv1alpha1.ClusterProviderClassSpec) {
			spec.Approval = kfplacementv1alpha1.ClusterClaimApprovalModeManual
		})
		policy := newPolicy(nextName("pp"), regionSelector("australiaeast", ptr.To(intstr.FromInt32(1)), nil), regionSelector("elsewhere", ptr.To(intstr.FromInt32(1)), nil), regionSelector("anywhere", ptr.To(intstr.FromInt32(1)), nil))
		policy.Spec.ClusterProviderClassName = ptr.To(class.Name)
		policy.Spec.MaxConcurrentClusterClaims = ptr.To[int32](suiteFleetClaimLimit)
		Expect(k8sClient.Create(ctx, policy)).Should(Succeed())
		Eventually(func(g Gomega) { claimOf(policy, 0)(g); claimOf(policy, 1)(g) }, eventuallyTimeout, pollInterval).Should(Succeed())

		// The first selector is satisfied, which frees its slot for the third selector's claim.
		mc := joinedCluster(nextName("mc-aue"), map[string]string{testRegionLabel: "australiaeast"})
		var third types.UID
		Eventually(func(g Gomega) { third = claimOf(policy, 2)(g).UID }, eventuallyTimeout, pollInterval).Should(Succeed())

		// The cluster leaves: the first selector wants a claim again, but both slots are taken.
		// The third selector's claim is in flight and stays; the first waits.
		Expect(k8sClient.Delete(ctx, mc)).Should(Succeed())
		Eventually(wantScheduledState(client.ObjectKeyFromObject(policy), metav1.ConditionFalse, kfplacementv1alpha1.PlacementPolicyScheduledCondReasonFailedToFindSomeClusters, 3, 0), eventuallyTimeout, pollInterval).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(claimOf(policy, 2)(g).UID).Should(Equal(third), "the limit gates issuing, never keeping")
			g.Expect(errors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: claimName(policy, 0)}, &kfplacementv1alpha1.ClusterClaim{}))).Should(BeTrue())
		}, consistentlyDuration, pollInterval).Should(Succeed())
	})

	It("holds issuance across policies at the fleet-wide limit of active claims", func() {
		class := createClass(nil)
		policies := make([]*kfplacementv1alpha1.PlacementPolicy, 0, 3)
		for _, region := range []string{"one", "two", "three"} {
			policies = append(policies, createPolicy(class, regionSelector(region, ptr.To(intstr.FromInt32(1)), nil)))
		}
		activeClaims := func(g Gomega) int {
			claims := &kfplacementv1alpha1.ClusterClaimList{}
			g.Expect(k8sClient.List(ctx, claims)).Should(Succeed())
			return len(claims.Items)
		}
		Eventually(activeClaims, eventuallyTimeout, pollInterval).Should(Equal(suiteFleetClaimLimit))
		Consistently(activeClaims, consistentlyDuration, pollInterval).Should(Equal(suiteFleetClaimLimit))
		var waiting *kfplacementv1alpha1.PlacementPolicy
		for _, policy := range policies {
			if errors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: claimName(policy, 0)}, &kfplacementv1alpha1.ClusterClaim{})) {
				waiting = policy
			}
		}
		Expect(waiting).NotTo(BeNil())
		Eventually(scheduledMessage(waiting), eventuallyTimeout, pollInterval).Should(ContainSubstring("waits for the fleet-wide limit"))

		// Withdrawing one policy's claim frees a slot for the waiting one.
		for _, policy := range policies {
			if policy != waiting {
				Expect(k8sClient.Delete(ctx, policy)).Should(Succeed())
				break
			}
		}
		Eventually(func(g Gomega) { claimOf(waiting, 0)(g) }, eventuallyTimeout, pollInterval).Should(Succeed())
	})
})
