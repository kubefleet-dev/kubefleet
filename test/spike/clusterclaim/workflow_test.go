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
		claim := newClaim("eastus")
		Expect(k8sClient.Create(ctx, claim)).Should(Succeed())

		By("the provisioner creates a matching member cluster and the withdrawer deletes the claim")
		Eventually(func() bool {
			err := k8sClient.Get(ctx, clientKey(claim.Name), &placementv1alpha1.ClusterRequest{})
			return err != nil && client.IgnoreNotFound(err) == nil
		}, eventuallyTimeout, eventuallyInterval).Should(BeTrue(), "claim should be withdrawn (deleted)")

		By("the provisioned member cluster remains")
		mc := &clusterv1beta1.MemberCluster{}
		Expect(k8sClient.Get(ctx, clientKey("provisioned-"+claim.Name), mc)).Should(Succeed())
		Expect(mc.Labels).Should(HaveKeyWithValue("topology.kubernetes.io/region", "eastus"))
	})

	It("withdraw-by-other-cluster: an unrelated matching cluster joins while the provisioner never completes", func() {
		fakeProvisioner.SetPolicy(PolicyIgnore)
		claim := newClaim("westus")
		Expect(k8sClient.Create(ctx, claim)).Should(Succeed())

		By("the claim persists while unfulfilled and unprovisioned")
		Consistently(func() error {
			return k8sClient.Get(ctx, clientKey(claim.Name), &placementv1alpha1.ClusterRequest{})
		}, time.Second*2, eventuallyInterval).Should(Succeed())

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
		}, time.Second*2, eventuallyInterval).Should(Succeed())

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
		}, time.Second*2, eventuallyInterval).Should(Succeed())

		Expect(k8sClient.Delete(ctx, &placementv1alpha1.ClusterRequest{ObjectMeta: metav1.ObjectMeta{Name: claim.Name}})).Should(Succeed())
	})
})
