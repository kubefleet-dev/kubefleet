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

package clusterclaim

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	placementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
)

const (
	eventuallyTimeout  = time.Second * 10
	eventuallyInterval = time.Millisecond * 250
	// consistentlyDuration is how long a negative assertion watches for the thing that must not
	// happen; long enough for several reconcile rounds, short enough not to dominate the suite.
	consistentlyDuration = time.Second * 2
)

func clientKey(name string) types.NamespacedName {
	return types.NamespacedName{Name: name}
}

// The behavioral contract of the claim workflow, per FEP-0001 "Cluster
// requests". Each spec here corresponds to a named scenario in the #791
// verification matrix.
var _ = Describe("cluster claim workflow", Ordered, func() {
	var counter int

	newClaim := func(region string) *placementv1alpha1.ClusterRequest {
		counter++
		return &placementv1alpha1.ClusterRequest{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("wf-claim-%d", counter)},
			Spec: placementv1alpha1.ClusterRequestSpec{
				PlacementPolicyRef: &placementv1alpha1.ObjectReference{
					Name:       "app",
					Namespace:  "work",
					APIGroup:   "placement.kubefleet.dev",
					APIVersion: "v1alpha1",
					Kind:       "PlacementPolicy",
				},
				ClusterSelectorTerms: []placementv1alpha1.ClusterLabelAndPropertySelectorTerm{
					{MatchLabels: map[string]string{"topology.kubernetes.io/region": region}},
				},
			},
		}
	}

	newMemberCluster := func(name string, labels map[string]string) *clusterv1beta1.MemberCluster {
		return &clusterv1beta1.MemberCluster{
			ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
			Spec: clusterv1beta1.MemberClusterSpec{
				Identity: rbacv1.Subject{
					Kind:      "ServiceAccount",
					Name:      name,
					Namespace: "fleet-system",
				},
			},
		}
	}

	AfterEach(func() {
		// The gate is spike-process state, not cluster state: reset it whatever the spec did, or
		// a failure between set and reset would silently change every later spec's semantics.
		withdrawer.SetEligibilityGate(false)
		claimList := &placementv1alpha1.ClusterRequestList{}
		Expect(k8sClient.List(ctx, claimList)).Should(Succeed())
		for i := range claimList.Items {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &claimList.Items[i]))).Should(Succeed())
		}
		fakeProvisioner.SetPolicy(PolicyIgnore)
		// Scrub member clusters between scenarios so fulfillment state does not leak.
		mcList := &clusterv1beta1.MemberClusterList{}
		Expect(k8sClient.List(ctx, mcList)).Should(Succeed())
		for i := range mcList.Items {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &mcList.Items[i]))).Should(Succeed())
		}
		Eventually(func() (int, error) {
			if err := k8sClient.List(ctx, mcList); err != nil {
				return -1, err
			}
			return len(mcList.Items), nil
		}, eventuallyTimeout, eventuallyInterval).Should(Equal(0))
	})

	It("happy path: provisioner fulfills the claim, then the claim is withdrawn", func() {
		fakeProvisioner.SetPolicy(PolicyFulfill)
		// The eligibility gate holds withdrawal open: the provisioned cluster has no agent
		// heartbeat, so a gated withdrawer does not yet count it. Without the hold, withdrawal
		// can race the provisioner's status update, and the test would pass without ever
		// proving the provisioner's half of the contract was honored.
		withdrawer.SetEligibilityGate(true)
		claim := newClaim("eastus")
		Expect(k8sClient.Create(ctx, claim)).Should(Succeed())

		By("the provisioner completes the claim while withdrawal is held")
		Eventually(func(g Gomega) {
			fulfilled := &placementv1alpha1.ClusterRequest{}
			g.Expect(k8sClient.Get(ctx, clientKey(claim.Name), fulfilled)).Should(Succeed())
			g.Expect(fulfilled.Status.ProvisionedClusterName).ShouldNot(BeNil())
			g.Expect(*fulfilled.Status.ProvisionedClusterName).Should(Equal("provisioned-" + claim.Name))
			completed := meta.FindStatusCondition(fulfilled.Status.Conditions, placementv1alpha1.ClusterRequestCondTypeCompleted)
			g.Expect(completed).ShouldNot(BeNil())
			g.Expect(completed.Status).Should(Equal(metav1.ConditionTrue))
		}, eventuallyTimeout, eventuallyInterval).Should(Succeed(), "the provisioner must report completion before withdrawal")

		By("the provisioned cluster's member agent reporting in is what withdraws the claim")
		// The gate stays on: withdrawal happens because the cluster genuinely becomes eligible,
		// the same transition eligibility_test drives, not because the test flips the harness's
		// bypass switch. The status update is itself the member cluster event that re-runs the
		// withdrawer's evaluation.
		mc := &clusterv1beta1.MemberCluster{}
		Expect(k8sClient.Get(ctx, clientKey("provisioned-"+claim.Name), mc)).Should(Succeed())
		now := metav1.Now()
		mc.Status.AgentStatus = []clusterv1beta1.AgentStatus{{
			Type: clusterv1beta1.MemberAgent,
			Conditions: []metav1.Condition{
				{Type: string(clusterv1beta1.AgentJoined), Status: metav1.ConditionTrue, Reason: "AgentJoined", Message: "spike: simulated join", LastTransitionTime: now},
				{Type: string(clusterv1beta1.AgentHealthy), Status: metav1.ConditionTrue, Reason: "AgentHealthy", Message: "spike: simulated health", LastTransitionTime: now},
			},
			LastReceivedHeartbeat: now,
		}}
		Expect(k8sClient.Status().Update(ctx, mc)).Should(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(ctx, clientKey(claim.Name), &placementv1alpha1.ClusterRequest{})
			return err != nil && client.IgnoreNotFound(err) == nil
		}, eventuallyTimeout, eventuallyInterval).Should(BeTrue(), "claim should be withdrawn (deleted)")

		By("the provisioned member cluster remains")
		Expect(k8sClient.Get(ctx, clientKey("provisioned-"+claim.Name), mc)).Should(Succeed())
		Expect(mc.Labels).Should(HaveKeyWithValue("topology.kubernetes.io/region", "eastus"))
	})

	It("a name collision with a cluster that does not satisfy the claim is not reported as completion", func() {
		fakeProvisioner.SetPolicy(PolicyFulfill)
		claim := newClaim("northeurope")
		// Squat the deterministic name with a cluster whose labels satisfy nothing.
		squatter := &clusterv1beta1.MemberCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "provisioned-" + claim.Name,
				Labels: map[string]string{"unrelated": "squatter"},
			},
			Spec: clusterv1beta1.MemberClusterSpec{
				Identity: rbacv1.Subject{Kind: "ServiceAccount", Name: "squatter", Namespace: "fleet-system"},
			},
		}
		Expect(k8sClient.Create(ctx, squatter)).Should(Succeed())
		Expect(k8sClient.Create(ctx, claim)).Should(Succeed())

		By("the claim stays uncompleted rather than claiming the squatter as fulfillment")
		Consistently(func() bool {
			current := &placementv1alpha1.ClusterRequest{}
			if err := k8sClient.Get(ctx, clientKey(claim.Name), current); err != nil {
				return false
			}
			return meta.FindStatusCondition(current.Status.Conditions, placementv1alpha1.ClusterRequestCondTypeCompleted) == nil
		}, consistentlyDuration, eventuallyInterval).Should(BeTrue(), "a squatted name must not become a completion report")
	})

	It("withdraw-by-other-cluster: an unrelated matching cluster joins while the provisioner never completes", func() {
		fakeProvisioner.SetPolicy(PolicyIgnore)
		claim := newClaim("westus")
		Expect(k8sClient.Create(ctx, claim)).Should(Succeed())

		By("the claim persists while unfulfilled and unprovisioned")
		Consistently(func() error {
			return k8sClient.Get(ctx, clientKey(claim.Name), &placementv1alpha1.ClusterRequest{})
		}, consistentlyDuration, eventuallyInterval).Should(Succeed())

		By("a manually joined cluster satisfies the selector")
		Expect(k8sClient.Create(ctx, newMemberCluster("manual-westus", map[string]string{"topology.kubernetes.io/region": "westus"}))).Should(Succeed())

		By("the claim is withdrawn even though Completed was never set")
		Eventually(func() bool {
			err := k8sClient.Get(ctx, clientKey(claim.Name), &placementv1alpha1.ClusterRequest{})
			return err != nil && client.IgnoreNotFound(err) == nil
		}, eventuallyTimeout, eventuallyInterval).Should(BeTrue())
	})

	It("failure path: Completed=False/Failed leaves the claim outstanding until a matching cluster appears", func() {
		fakeProvisioner.SetPolicy(PolicyFail)
		claim := newClaim("centralus")
		Expect(k8sClient.Create(ctx, claim)).Should(Succeed())

		By("the provisioner reports terminal failure")
		Eventually(func() bool {
			fetched := &placementv1alpha1.ClusterRequest{}
			if err := k8sClient.Get(ctx, clientKey(claim.Name), fetched); err != nil {
				return false
			}
			cond := meta.FindStatusCondition(fetched.Status.Conditions, placementv1alpha1.ClusterRequestCondTypeCompleted)
			return cond != nil && cond.Status == metav1.ConditionFalse && cond.Reason == "Failed"
		}, eventuallyTimeout, eventuallyInterval).Should(BeTrue())

		By("the failed claim is NOT withdrawn — the selector is still unfulfilled (open design question: retry policy)")
		Consistently(func() error {
			return k8sClient.Get(ctx, clientKey(claim.Name), &placementv1alpha1.ClusterRequest{})
		}, consistentlyDuration, eventuallyInterval).Should(Succeed())

		By("fulfillment by any cluster still withdraws the failed claim")
		Expect(k8sClient.Create(ctx, newMemberCluster("late-centralus", map[string]string{"topology.kubernetes.io/region": "centralus"}))).Should(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(ctx, clientKey(claim.Name), &placementv1alpha1.ClusterRequest{})
			return err != nil && client.IgnoreNotFound(err) == nil
		}, eventuallyTimeout, eventuallyInterval).Should(BeTrue())
	})

	It("staleness: a non-matching cluster join refreshes the freshness marker instead of withdrawing", func() {
		fakeProvisioner.SetPolicy(PolicyIgnore)
		claim := newClaim("northeurope")
		Expect(k8sClient.Create(ctx, claim)).Should(Succeed())

		By("a cluster joins that does not satisfy the selector")
		Expect(k8sClient.Create(ctx, newMemberCluster("wrong-region", map[string]string{"topology.kubernetes.io/region": "eastus"}))).Should(Succeed())

		By("the claim survives and lastObservedMostRecentClusterCreationTimestamp is refreshed")
		Eventually(func() bool {
			fetched := &placementv1alpha1.ClusterRequest{}
			if err := k8sClient.Get(ctx, clientKey(claim.Name), fetched); err != nil {
				return false
			}
			return fetched.Status.LastObservedMostRecentClusterCreationTimestamp != nil
		}, eventuallyTimeout, eventuallyInterval).Should(BeTrue())

		Consistently(func() error {
			return k8sClient.Get(ctx, clientKey(claim.Name), &placementv1alpha1.ClusterRequest{})
		}, consistentlyDuration, eventuallyInterval).Should(Succeed())

		Expect(k8sClient.Delete(ctx, &placementv1alpha1.ClusterRequest{ObjectMeta: metav1.ObjectMeta{Name: claim.Name}})).Should(Succeed())
	})
})
