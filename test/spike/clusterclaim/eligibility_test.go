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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	placementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/scheduler/clustereligibilitychecker"
)

// The join window: FEP-0001 withdraws a claim once a matching cluster "is
// joined to the fleet", but never says whether "joined" means the
// MemberCluster object exists or the cluster actually passes the scheduler's
// eligibility gate (member agent online, recent heartbeat, Joined=True,
// healthy — pkg/scheduler/clustereligibilitychecker). A provisioner-created
// MemberCluster matches its selector at object-creation time, minutes before
// the member agent comes up. These specs pin the difference between the two
// readings.
var _ = Describe("join-window eligibility gap", Ordered, func() {
	var counter int

	newClaim := func(region string) *placementv1alpha1.ClusterRequest {
		counter++
		return &placementv1alpha1.ClusterRequest{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("elig-claim-%d", counter)},
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
		withdrawer.SetEligibilityGate(false)
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

	// FINDING (spike round 2): under the FEP-as-written reading, the claim —
	// the only visible "a cluster is still needed" signal, and the thing
	// budgeted against the concurrency limit — is withdrawn while the matching
	// cluster is still unusable to the scheduler. The placement stays
	// unscheduled with no outstanding claim to explain why.
	It("KNOWN GAP: raw-match withdrawal fires for a cluster the scheduler cannot use yet", func() {
		claim := newClaim("eastus2")
		Expect(k8sClient.Create(ctx, claim)).Should(Succeed())

		By("a provisioned cluster registers with matching labels but its member agent has not reported yet")
		mc := newMemberCluster("registered-not-joined", map[string]string{"topology.kubernetes.io/region": "eastus2"})
		Expect(k8sClient.Create(ctx, mc)).Should(Succeed())

		By("the scheduler's own eligibility gate would reject this cluster right now")
		eligible, reason := clustereligibilitychecker.New().IsEligible(mc)
		Expect(eligible).Should(BeFalse())
		Expect(reason).Should(ContainSubstring("not online"))

		By("yet the claim is withdrawn — the 'still needed' signal is gone while nothing is schedulable")
		Eventually(func() bool {
			err := k8sClient.Get(ctx, clientKey(claim.Name), &placementv1alpha1.ClusterRequest{})
			return err != nil && client.IgnoreNotFound(err) == nil
		}, eventuallyTimeout, eventuallyInterval).Should(BeTrue())
	})

	It("recommended reading: eligibility-gated withdrawal holds the claim through the join window", func() {
		withdrawer.SetEligibilityGate(true)
		claim := newClaim("westus2")
		Expect(k8sClient.Create(ctx, claim)).Should(Succeed())

		By("a matching but not-yet-joined cluster does not withdraw the claim")
		mc := newMemberCluster("joining-westus2", map[string]string{"topology.kubernetes.io/region": "westus2"})
		Expect(k8sClient.Create(ctx, mc)).Should(Succeed())
		Consistently(func() error {
			return k8sClient.Get(ctx, clientKey(claim.Name), &placementv1alpha1.ClusterRequest{})
		}, time.Second*2, eventuallyInterval).Should(Succeed())

		By("once the member agent reports joined + healthy + heartbeating, the claim is withdrawn")
		Expect(k8sClient.Get(ctx, clientKey(mc.Name), mc)).Should(Succeed())
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
		}, eventuallyTimeout, eventuallyInterval).Should(BeTrue())
	})
})
