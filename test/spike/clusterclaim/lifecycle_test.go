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
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	placementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
)

const spikeProvisionerFinalizer = "spike.kubefleet.dev/provisioner"

// Lifecycle gaps in the claim contract: what happens around deletion,
// ownership, and naming is unspecified by FEP-0001. These specs demonstrate
// the current behavior empirically so the contract discussion has concrete
// anchors.
var _ = Describe("claim lifecycle gaps", Ordered, func() {
	var counter int

	newClaimFor := func(policyName, policyNamespace, region string) *placementv1alpha1.ClusterRequest {
		counter++
		return &placementv1alpha1.ClusterRequest{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("lc-claim-%d", counter)},
			Spec: placementv1alpha1.ClusterRequestSpec{
				PlacementPolicyRef: &placementv1alpha1.ObjectReference{
					Name:       policyName,
					Namespace:  policyNamespace,
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

	newPolicy := func(name, namespace string) *placementv1alpha1.PlacementPolicy {
		return &placementv1alpha1.PlacementPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: placementv1alpha1.PlacementPolicySpec{
				ResourceSelectors: []placementv1alpha1.ResourceSelector{
					{APIVersion: "v1", Kind: "ConfigMap", Name: "demo"},
				},
			},
		}
	}

	BeforeAll(func() {
		for _, ns := range []string{"tenant-a", "tenant-b"} {
			Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})).Should(Succeed())
		}
	})

	AfterEach(func() {
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

	// FINDING (spike round 2): nothing in the contract forbids a provisioner
	// finalizer, and with one held, "withdrawal" only marks the claim
	// Terminating. The FEP never says whether a Terminating claim still counts
	// toward the concurrency budget (starvation) or is replaced
	// (double-provisioning) — one of the two must be chosen by #786.
	It("withdrawal against a provisioner finalizer only marks the claim Terminating", func() {
		claim := newClaimFor("app", "tenant-a", "eastus")
		Expect(k8sClient.Create(ctx, claim)).Should(Succeed())

		By("the provisioner protects its in-flight work with a finalizer")
		Expect(k8sClient.Get(ctx, clientKey(claim.Name), claim)).Should(Succeed())
		claim.Finalizers = append(claim.Finalizers, spikeProvisionerFinalizer)
		Expect(k8sClient.Update(ctx, claim)).Should(Succeed())

		By("a matching cluster appears and the withdrawer deletes the claim")
		mc := &clusterv1beta1.MemberCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "manual-eastus", Labels: map[string]string{"topology.kubernetes.io/region": "eastus"}},
			Spec: clusterv1beta1.MemberClusterSpec{
				Identity: rbacv1.Subject{Kind: "ServiceAccount", Name: "manual-eastus", Namespace: "fleet-system"},
			},
		}
		Expect(k8sClient.Create(ctx, mc)).Should(Succeed())

		By("the claim lingers in Terminating instead of disappearing")
		Eventually(func() bool {
			fetched := &placementv1alpha1.ClusterRequest{}
			if err := k8sClient.Get(ctx, clientKey(claim.Name), fetched); err != nil {
				return false
			}
			return !fetched.DeletionTimestamp.IsZero()
		}, eventuallyTimeout, eventuallyInterval).Should(BeTrue())
		Consistently(func() error {
			return k8sClient.Get(ctx, clientKey(claim.Name), &placementv1alpha1.ClusterRequest{})
		}, time.Second*2, eventuallyInterval).Should(Succeed())

		By("releasing the finalizer completes the withdrawal")
		fetched := &placementv1alpha1.ClusterRequest{}
		Expect(k8sClient.Get(ctx, clientKey(claim.Name), fetched)).Should(Succeed())
		fetched.Finalizers = nil
		Expect(k8sClient.Update(ctx, fetched)).Should(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(ctx, clientKey(claim.Name), &placementv1alpha1.ClusterRequest{})
			return err != nil && client.IgnoreNotFound(err) == nil
		}, eventuallyTimeout, eventuallyInterval).Should(BeTrue())
	})

	// FINDING (spike round 2): deleting the owning PlacementPolicy leaves the
	// claim behind with nothing to ever clean it up — cross-scope
	// ownerReferences (namespaced owner, cluster-scoped dependent) are invalid
	// in Kubernetes GC, and the FEP specifies no controller-side cleanup.
	// placementPolicyRef also records no UID, so a recreated same-named policy
	// silently "inherits" the orphan.
	It("KNOWN GAP: deleting the referenced PlacementPolicy orphans the claim", func() {
		policy := newPolicy("app", "tenant-a")
		Expect(k8sClient.Create(ctx, policy)).Should(Succeed())
		claim := newClaimFor(policy.Name, policy.Namespace, "northeurope")
		Expect(k8sClient.Create(ctx, claim)).Should(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, claim))).Should(Succeed()) })

		By("the policy is deleted")
		Expect(k8sClient.Delete(ctx, policy)).Should(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(ctx, types.NamespacedName{Name: policy.Name, Namespace: policy.Namespace}, &placementv1alpha1.PlacementPolicy{})
			return err != nil && client.IgnoreNotFound(err) == nil
		}, eventuallyTimeout, eventuallyInterval).Should(BeTrue())

		By("the claim survives indefinitely — nothing reconciles it away")
		Consistently(func() error {
			return k8sClient.Get(ctx, clientKey(claim.Name), &placementv1alpha1.ClusterRequest{})
		}, time.Second*2, eventuallyInterval).Should(Succeed())
	})

	// The API server accepts a cross-scope ownerReference without complaint;
	// the invalidity only surfaces later, in the garbage collector (which
	// reports OwnerRefInvalidNamespace and skips the object — envtest runs no
	// GC, so that half is documented rather than asserted). The takeaway for
	// #786: ownerReferences are not a usable cleanup mechanism here, however
	// tempting the API server makes it look.
	It("cross-scope ownerReference to a namespaced policy is accepted by the API server (footgun)", func() {
		policy := newPolicy("owner-demo", "tenant-a")
		Expect(k8sClient.Create(ctx, policy)).Should(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, policy))).Should(Succeed()) })

		claim := newClaimFor(policy.Name, policy.Namespace, "uksouth")
		claim.OwnerReferences = []metav1.OwnerReference{{
			APIVersion: "placement.kubefleet.dev/v1alpha1",
			Kind:       "PlacementPolicy",
			Name:       policy.Name,
			UID:        policy.UID,
		}}
		Expect(k8sClient.Create(ctx, claim)).Should(Succeed(), "accepted at admission; invalid only at GC time")
		Expect(k8sClient.Delete(ctx, claim)).Should(Succeed())
	})

	// FINDING (spike round 2): claims are cluster-scoped, policies are
	// namespaced, and the FEP defines no naming scheme. Any deterministic
	// name derived from the policy name alone collides across namespaces.
	It("KNOWN GAP: deterministic claim names derived from policy names collide across namespaces", func() {
		policyA := newPolicy("app", "tenant-b")
		Expect(k8sClient.Create(ctx, policyA)).Should(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, policyA))).Should(Succeed()) })
		// tenant-a's policy "app" may already exist from earlier specs; ensure it does.
		policyB := newPolicy("app", "tenant-a")
		if err := k8sClient.Create(ctx, policyB); err != nil {
			Expect(errors.IsAlreadyExists(err)).Should(BeTrue())
		}

		claimName := "claim-for-app"
		claimA := newClaimFor("app", "tenant-b", "eastasia")
		claimA.Name = claimName
		Expect(k8sClient.Create(ctx, claimA)).Should(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, claimA))).Should(Succeed()) })

		claimB := newClaimFor("app", "tenant-a", "westeurope")
		claimB.Name = claimName
		err := k8sClient.Create(ctx, claimB)
		Expect(errors.IsAlreadyExists(err)).Should(BeTrue(), "same policy name in another namespace collides on the flat cluster-scoped claim namespace")
	})
})
