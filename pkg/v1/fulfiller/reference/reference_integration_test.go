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

package reference

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/propertyprovider"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/naming"
)

const regionLabel = "topology.kubernetes.io/region"

// These specs drive the whole fulfillment loop: a policy wants clusters, the policy controller
// claims them, the framework hands the claims to the reference provider, the provider registers
// member clusters and simulates their join, and the policy binds them. Nothing here touches a
// claim's status by hand; every transition is a controller's.
var _ = Describe("fulfillment loop with the reference provider", Ordered, func() {
	var counter int
	nextName := func(prefix string) string {
		counter++
		return fmt.Sprintf("%s-%d", prefix, counter)
	}
	short := &metav1.Duration{Duration: time.Second}

	createClass := func(mutate func(*kfplacementv1alpha1.ClusterProviderClassSpec)) *kfplacementv1alpha1.ClusterProviderClass {
		class := &kfplacementv1alpha1.ClusterProviderClass{
			ObjectMeta: metav1.ObjectMeta{Name: nextName("ref")},
			Spec: kfplacementv1alpha1.ClusterProviderClassSpec{
				ProvisionerName:    ProvisionerName,
				Approval:           kfplacementv1alpha1.ClusterClaimApprovalModeAutomatic,
				Parameters:         map[string]string{IdentityParameter: "member-agent-sa"},
				SelectorVocabulary: &kfplacementv1alpha1.SelectorVocabulary{LabelKeys: []kfplacementv1alpha1.LabelKeyRule{{Key: regionLabel}}},
			},
		}
		if mutate != nil {
			mutate(&class.Spec)
		}
		Expect(k8sClient.Create(ctx, class)).Should(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, class))).Should(Succeed()) })
		return class
	}
	regionSelector := func(region string, count int32) kfplacementv1alpha1.ClusterSelector {
		return kfplacementv1alpha1.ClusterSelector{
			Terms:           []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{{MatchLabels: map[string]string{regionLabel: region}}},
			Count:           ptr.To(intstr.FromInt32(count)),
			WhenUnfulfilled: kfplacementv1alpha1.WhenUnfulfilledOptionAddClusterClaim,
		}
	}
	createPolicy := func(class *kfplacementv1alpha1.ClusterProviderClass, selectors ...kfplacementv1alpha1.ClusterSelector) *kfplacementv1alpha1.PlacementPolicy {
		policy := &kfplacementv1alpha1.PlacementPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: nextName("pp"), Namespace: testNamespace},
			Spec: kfplacementv1alpha1.PlacementPolicySpec{
				ResourceSelectors:        []kfplacementv1alpha1.ResourceSelector{{APIVersion: "v1", Kind: "ConfigMap", Name: "app"}},
				ClusterSelectors:         selectors,
				ClusterProviderClassName: ptr.To(class.Name),
			},
		}
		Expect(k8sClient.Create(ctx, policy)).Should(Succeed())
		return policy
	}
	claimsOf := func(policy *kfplacementv1alpha1.PlacementPolicy) []kfplacementv1alpha1.ClusterClaim {
		claims := &kfplacementv1alpha1.ClusterClaimList{}
		Expect(k8sClient.List(ctx, claims, client.MatchingLabels{kfplacementv1alpha1.ClusterClaimPlacementPolicyNameLabel: naming.LabelValue(policy.Name)})).Should(Succeed())
		return claims.Items
	}
	theClaim := func(policy *kfplacementv1alpha1.PlacementPolicy) func(Gomega) *kfplacementv1alpha1.ClusterClaim {
		return func(g Gomega) *kfplacementv1alpha1.ClusterClaim {
			claims := claimsOf(policy)
			latest := &kfplacementv1alpha1.PlacementPolicy{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), latest)).Should(Succeed())
			g.Expect(claims).Should(HaveLen(1), "policy status: %+v", latest.Status)
			return &claims[0]
		}
	}
	conditionIs := func(claim *kfplacementv1alpha1.ClusterClaim, condType string, status metav1.ConditionStatus, reason string) bool {
		cond := meta.FindStatusCondition(claim.Status.Conditions, condType)
		return cond != nil && cond.Status == status && cond.Reason == reason
	}
	bindingsOf := func(policy *kfplacementv1alpha1.PlacementPolicy) func(Gomega) []string {
		return func(g Gomega) []string {
			bindings := &kfplacementv1alpha1.PlacementBindingList{}
			g.Expect(k8sClient.List(ctx, bindings, client.InNamespace(testNamespace))).Should(Succeed())
			var clusters []string
			for i := range bindings.Items {
				if metav1.IsControlledBy(&bindings.Items[i], policy) {
					clusters = append(clusters, bindings.Items[i].Spec.ClusterName)
				}
			}
			return clusters
		}
	}
	memberClusters := func(g Gomega) []clusterv1beta1.MemberCluster {
		list := &clusterv1beta1.MemberClusterList{}
		g.Expect(k8sClient.List(ctx, list)).Should(Succeed())
		return list.Items
	}
	clusterExists := func(name string) func(Gomega) bool {
		return func(g Gomega) bool {
			err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, &clusterv1beta1.MemberCluster{})
			g.Expect(client.IgnoreNotFound(err)).Should(Succeed())
			return err == nil
		}
	}
	// neverJoined reports a registered cluster that carries no agent status at all -- not even a
	// Joined=False -- which is how a cluster whose member agent never came up looks.
	neverJoined := func(name string) func(Gomega) bool {
		return func(g Gomega) bool {
			mc := &clusterv1beta1.MemberCluster{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, mc)).Should(Succeed())
			g.Expect(mc.Status.AgentStatus).Should(BeEmpty(), "NeverJoin writes no agent status")
			return true
		}
	}
	// completedFor waits for the provider to fulfill the policy's claim and returns the claim as
	// it then stands, with the cluster it names.
	completedFor := func(policy *kfplacementv1alpha1.PlacementPolicy) (*kfplacementv1alpha1.ClusterClaim, string) {
		var claim *kfplacementv1alpha1.ClusterClaim
		Eventually(func(g Gomega) {
			claim = theClaim(policy)(g)
			g.Expect(conditionIs(claim, kfplacementv1alpha1.ClusterClaimCondTypeCompleted, metav1.ConditionTrue, kfplacementv1alpha1.ClusterClaimCompletedCondReasonFulfilled)).Should(BeTrue(), "conditions: %v", claim.Status.Conditions)
			g.Expect(claim.Status.ProvisionedClusterName).NotTo(BeNil())
		}, eventuallyTimeout, pollInterval).Should(Succeed())
		return claim, *claim.Status.ProvisionedClusterName
	}
	heldAs := func(policy *kfplacementv1alpha1.PlacementPolicy, uid types.UID, condType, reason string) {
		GinkgoHelper()
		Eventually(func(g Gomega) {
			claim := theClaim(policy)(g)
			g.Expect(claim.UID).Should(Equal(uid))
			g.Expect(conditionIs(claim, condType, func() metav1.ConditionStatus {
				if condType == kfplacementv1alpha1.ClusterClaimCondTypeCompleted {
					return metav1.ConditionFalse
				}
				return metav1.ConditionTrue
			}(), reason)).Should(BeTrue(), "conditions: %v", claim.Status.Conditions)
			// An expired claim is released by the provider; a failed one keeps the finalizer until
			// it is withdrawn, so the deprovision backstop runs then.
			g.Expect(controllerutil.ContainsFinalizer(claim, kfplacementv1alpha1.FulfillerFinalizer)).Should(Equal(condType != kfplacementv1alpha1.ClusterClaimCondTypeExpired), "finalizers: %v", claim.Finalizers)
		}, eventuallyTimeout, pollInterval).Should(Succeed())
		Consistently(func(g Gomega) { g.Expect(theClaim(policy)(g).UID).Should(Equal(uid)) }, consistentlyDuration, pollInterval).Should(Succeed())
	}

	AfterEach(func() {
		provider.SetBehaviour(Fulfill)
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
		for _, mc := range memberClusters(Default) {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &mc))).Should(Succeed())
		}
		Eventually(func(g Gomega) { g.Expect(memberClusters(g)).Should(BeEmpty()) }, eventuallyTimeout, pollInterval).Should(Succeed())
	})

	It("claims, provisions, joins, binds, and rotates until a count-2 selector is fulfilled", func() {
		class := createClass(nil)
		// The provider is held back first, so the claim can be inspected before the loop -- which
		// runs within a second here -- rotates it away.
		provider.SetBehaviour(FailTransient)
		policy := createPolicy(class, regionSelector("eastus", 2))

		By("the first claim is issued into an empty fleet, approved automatically, and accepted")
		var firstUID types.UID
		Eventually(func(g Gomega) {
			claim := theClaim(policy)(g)
			g.Expect(claim.Status.LastObservedMostRecentClusterCreationTimestamp).Should(BeNil(), "nothing to be stale against in an empty fleet")
			g.Expect(conditionIs(claim, kfplacementv1alpha1.ClusterClaimCondTypeApproved, metav1.ConditionTrue, kfplacementv1alpha1.ClusterClaimApprovedCondReasonAutomaticallyApproved)).Should(BeTrue())
			g.Expect(conditionIs(claim, kfplacementv1alpha1.ClusterClaimCondTypeAccepted, metav1.ConditionTrue, kfplacementv1alpha1.ClusterClaimAcceptedCondReasonAccepted)).Should(BeTrue())
			g.Expect(controllerutil.ContainsFinalizer(claim, kfplacementv1alpha1.FulfillerFinalizer)).Should(BeTrue())
			firstUID = claim.UID
		}, eventuallyTimeout, pollInterval).Should(Succeed())

		By("the provider registers a cluster carrying the selector's labels, it joins, the policy binds it and rotates the claim")
		provider.SetBehaviour(Fulfill)
		Eventually(func(g Gomega) {
			g.Expect(bindingsOf(policy)(g)).Should(HaveLen(2))
			g.Expect(claimsOf(policy)).Should(BeEmpty())
			clusters := memberClusters(g)
			g.Expect(clusters).Should(HaveLen(2))
			uids := map[string]bool{}
			for _, mc := range clusters {
				g.Expect(mc.Labels).Should(HaveKeyWithValue(regionLabel, "eastus"))
				g.Expect(mc.Spec.Identity.Name).Should(Equal("member-agent-sa"))
				g.Expect(mc.Status.AgentStatus).ShouldNot(BeEmpty(), "the simulated member agent joined")
				uids[mc.Labels[kfplacementv1alpha1.FulfilledClaimUIDLabel]] = true
			}
			g.Expect(uids).Should(HaveLen(2), "each cluster came from its own claim")
			g.Expect(uids).Should(HaveKey(string(firstUID)), "the first cluster came from the first claim")
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), policy)).Should(Succeed())
			g.Expect(policy.Finalizers).ShouldNot(ContainElement(ContainSubstring("claim-cleanup")))
		}, eventuallyTimeout, pollInterval).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(memberClusters(g)).Should(HaveLen(2), "withdrawing a fulfilled claim never deprovisions its joined cluster")
			g.Expect(claimsOf(policy)).Should(BeEmpty())
		}, consistentlyDuration, pollInterval).Should(Succeed())
	})

	It("retries a permanently failed claim after retryAfter when the class says so", func() {
		class := createClass(func(spec *kfplacementv1alpha1.ClusterProviderClassSpec) {
			spec.OnFailure = kfplacementv1alpha1.ClusterClaimFailureActionRetry
			spec.RetryAfter = short
		})
		provider.SetBehaviour(FailPermanent)
		policy := createPolicy(class, regionSelector("eastus", 1))
		var failedUID types.UID
		Eventually(func(g Gomega) {
			claim := theClaim(policy)(g)
			g.Expect(conditionIs(claim, kfplacementv1alpha1.ClusterClaimCondTypeCompleted, metav1.ConditionFalse, kfplacementv1alpha1.ClusterClaimCompletedCondReasonFailed)).Should(BeTrue())
			failedUID = claim.UID
		}, eventuallyTimeout, pollInterval).Should(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(theClaim(policy)(g).UID).ShouldNot(Equal(failedUID), "the record is withdrawn and a fresh claim issued")
		}, eventuallyTimeout, pollInterval).Should(Succeed())

		// Once the provider recovers, the fresh claim (or its successor) is fulfilled.
		provider.SetBehaviour(Fulfill)
		Eventually(bindingsOf(policy), eventuallyTimeout, pollInterval).Should(HaveLen(1))
	})

	It("holds a permanently failed claim until the selector changes", func() {
		class := createClass(nil)
		provider.SetBehaviour(FailPermanent)
		policy := createPolicy(class, regionSelector("eastus", 1))
		var failedUID types.UID
		Eventually(func(g Gomega) { failedUID = theClaim(policy)(g).UID }, eventuallyTimeout, pollInterval).Should(Succeed())
		heldAs(policy, failedUID, kfplacementv1alpha1.ClusterClaimCondTypeCompleted, kfplacementv1alpha1.ClusterClaimCompletedCondReasonFailed)

		provider.SetBehaviour(Fulfill)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), policy)).Should(Succeed())
			policy.Spec.ClusterSelectors[0].Terms[0].MatchLabels[regionLabel] = "westus"
			g.Expect(k8sClient.Update(ctx, policy)).Should(Succeed())
		}, eventuallyTimeout, pollInterval).Should(Succeed())
		Eventually(bindingsOf(policy), eventuallyTimeout, pollInterval).Should(HaveLen(1))
	})

	It("expires a cluster that never joins, deprovisions it, and holds the record", func() {
		class := createClass(func(spec *kfplacementv1alpha1.ClusterProviderClassSpec) {
			spec.JoinTimeout = &metav1.Duration{Duration: 4 * time.Second}
		})
		provider.SetBehaviour(NeverJoin)
		policy := createPolicy(class, regionSelector("eastus", 1))
		claim, name := completedFor(policy)
		Eventually(neverJoined(name), eventuallyTimeout, pollInterval).Should(BeTrue())

		heldAs(policy, claim.UID, kfplacementv1alpha1.ClusterClaimCondTypeExpired, kfplacementv1alpha1.ClusterClaimExpiredCondReasonJoinTimeout)
		Eventually(clusterExists(name), eventuallyTimeout, pollInterval).Should(BeFalse(), "a cluster that never joined is deprovisioned on JoinTimeout")
	})

	It("deprovisions a never-joined cluster when its policy is deleted inside the join window", func() {
		class := createClass(nil)
		provider.SetBehaviour(NeverJoin)
		policy := createPolicy(class, regionSelector("eastus", 1))
		_, name := completedFor(policy)
		Eventually(neverJoined(name), eventuallyTimeout, pollInterval).Should(BeTrue())

		Expect(k8sClient.Delete(ctx, policy)).Should(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(claimsOf(policy)).Should(BeEmpty())
			g.Expect(clusterExists(name)(g)).Should(BeFalse())
		}, eventuallyTimeout, pollInterval).Should(Succeed())
	})

	It("expires a joined cluster the selector does not count as NotMatching and keeps it", func() {
		class := createClass(func(spec *kfplacementv1alpha1.ClusterProviderClassSpec) {
			spec.JoinTimeout = &metav1.Duration{Duration: 2 * time.Second}
			spec.SelectorVocabulary.PropertyKeys = []kfplacementv1alpha1.PropertyKeyRule{{Key: propertyprovider.NodeCountProperty}}
		})
		// The provider can carry a label, but not deliver a property: the cluster joins and
		// stays short of the selector.
		selector := regionSelector("eastus", 1)
		selector.Terms[0].MatchClusterPropertyExpressions = []kfplacementv1alpha1.LabelClusterPropertyExpression{{
			Key: propertyprovider.NodeCountProperty, Operator: kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorGe, Values: []string{"1"},
		}}
		policy := createPolicy(class, selector)
		claim, name := completedFor(policy)

		heldAs(policy, claim.UID, kfplacementv1alpha1.ClusterClaimCondTypeExpired, kfplacementv1alpha1.ClusterClaimExpiredCondReasonNotMatching)
		Consistently(func(g Gomega) {
			mc := &clusterv1beta1.MemberCluster{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, mc)).Should(Succeed(), "a NotMatching cluster is a member in its own right and is never deprovisioned")
			g.Expect(mc.Status.AgentStatus).ShouldNot(BeEmpty(), "it joined")
		}, consistentlyDuration, pollInterval).Should(Succeed())
	})

	It("stamps the InternalMemberCluster where a hub agent would copy it from", func() {
		// The hub agent creates the fleet-member namespace and the InternalMemberCluster once it
		// sees a MemberCluster; here the spec plays that part, since envtest runs no hub agent.
		name := nextName("mc-imc")
		namespace := fmt.Sprintf("fleet-member-%s", name)
		Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})).Should(Succeed())
		imc := &clusterv1beta1.InternalMemberCluster{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}, Spec: clusterv1beta1.InternalMemberClusterSpec{State: clusterv1beta1.ClusterStateJoin}}
		Expect(k8sClient.Create(ctx, imc)).Should(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, imc))).Should(Succeed()) })

		viaHub := New(k8sClient, Options{SimulateJoin: true, JoinTarget: JoinInternalMemberCluster})
		Expect(viaHub.stampJoined(ctx, name, false)).Should(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(imc), imc)).Should(Succeed())
		Expect(imc.Status.AgentStatus).Should(HaveLen(1))
		Expect(imc.Status.AgentStatus[0].Type).Should(Equal(clusterv1beta1.MemberAgent))
		Expect(meta.IsStatusConditionTrue(imc.Status.AgentStatus[0].Conditions, string(clusterv1beta1.AgentJoined))).Should(BeTrue())

		// Before the hub agent has created the object, a provision round retries; a heartbeat
		// tick has nothing to refresh.
		Expect(viaHub.stampJoined(ctx, nextName("not-yet"), false)).Should(HaveOccurred())
		Expect(viaHub.stampJoined(ctx, nextName("not-yet"), true)).Should(Succeed())
	})
})
