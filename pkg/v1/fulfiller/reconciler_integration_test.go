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

package fulfiller

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
)

// counter is shared by every spec; Ginkgo runs the specs of one process serially, so a plain int
// is safe here.
var counter int

// nextName returns a unique object name for a spec.
func nextName(prefix string) string {
	counter++
	return fmt.Sprintf("%s-%d", prefix, counter)
}

// createClass registers a class for the given provider and schedules its cleanup.
func createClass(provisioner string, mutate func(*kfplacementv1alpha1.ClusterProviderClass)) *kfplacementv1alpha1.ClusterProviderClass {
	class := &kfplacementv1alpha1.ClusterProviderClass{
		ObjectMeta: metav1.ObjectMeta{Name: nextName("class")},
		Spec:       kfplacementv1alpha1.ClusterProviderClassSpec{ProvisionerName: provisioner},
	}
	if mutate != nil {
		mutate(class)
	}
	Expect(k8sClient.Create(ctx, class)).Should(Succeed())
	DeferCleanup(func() {
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, class))).Should(Succeed())
	})
	return class
}

// labelsFor returns the ownership labels the framework expects on the cluster of a claim.
func labelsFor(claim *kfplacementv1alpha1.ClusterClaim) map[string]string {
	return Request{ClaimName: claim.Name, ClaimUID: claim.UID}.OwnershipLabels()
}

// createClaim issues a claim stamped with the class, as the policy controller would, and schedules
// its cleanup: the claim is deleted and the suite waits for the provider to release it, then
// removes whatever cluster was registered for it.
func createClaim(class *kfplacementv1alpha1.ClusterProviderClass, mutate func(*kfplacementv1alpha1.ClusterClaim)) *kfplacementv1alpha1.ClusterClaim {
	claim := &kfplacementv1alpha1.ClusterClaim{
		ObjectMeta: metav1.ObjectMeta{Name: nextName("claim")},
		Spec: kfplacementv1alpha1.ClusterClaimSpec{
			PlacementPolicyRef:       &kfplacementv1alpha1.ObjectReference{Name: "app", Namespace: "work", APIVersion: "v1alpha1", Kind: kfplacementv1alpha1.PlacementPolicyKind},
			ClusterProviderClassName: class.Name,
		},
	}
	if mutate != nil {
		mutate(claim)
	}
	Expect(k8sClient.Create(ctx, claim)).Should(Succeed())
	DeferCleanup(func() {
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, claim))).Should(Succeed())
		Eventually(func() bool { return notFound(claim) }, eventuallyTimeout, pollInterval).Should(BeTrue(), "the claim should be released and gone")
		// Only this claim's clusters (by name label, which same-name re-issues share); clusters a
		// spec created by hand register their own cleanup.
		Expect(k8sClient.DeleteAllOf(ctx, &clusterv1beta1.MemberCluster{}, client.MatchingLabels{
			kfplacementv1alpha1.FulfilledClaimNameLabel: labelsFor(claim)[kfplacementv1alpha1.FulfilledClaimNameLabel],
		})).Should(Succeed())
	})
	return claim
}

// setClaimCondition writes a condition as another actor (approver, policy controller) would.
func setClaimCondition(claim *kfplacementv1alpha1.ClusterClaim, cond metav1.Condition) {
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(claim), claim)).Should(Succeed())
		meta.SetStatusCondition(&claim.Status.Conditions, cond)
		g.Expect(k8sClient.Status().Update(ctx, claim)).Should(Succeed())
	}, eventuallyTimeout, pollInterval).Should(Succeed())
}

func approve(claim *kfplacementv1alpha1.ClusterClaim) {
	setClaimCondition(claim, metav1.Condition{Type: kfplacementv1alpha1.ClusterClaimCondTypeApproved, Status: metav1.ConditionTrue, Reason: kfplacementv1alpha1.ClusterClaimApprovedCondReasonApproved})
}

func expire(claim *kfplacementv1alpha1.ClusterClaim, reason string) {
	setClaimCondition(claim, metav1.Condition{Type: kfplacementv1alpha1.ClusterClaimCondTypeExpired, Status: metav1.ConditionTrue, Reason: reason})
}

// claimState fetches the claim and returns the bits the specs assert on.
type claimState struct {
	accepted, completed, failed, released bool
	clusterName                           string
	message                               string
}

func stateOf(claim *kfplacementv1alpha1.ClusterClaim) func(Gomega) claimState {
	return func(g Gomega) claimState {
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(claim), claim)).Should(Succeed())
		s := claimState{
			accepted: controllerutil.ContainsFinalizer(claim, kfplacementv1alpha1.FulfillerFinalizer),
			released: !controllerutil.ContainsFinalizer(claim, kfplacementv1alpha1.FulfillerFinalizer),
		}
		if completed := meta.FindStatusCondition(claim.Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeCompleted); completed != nil {
			s.completed = completed.Status == metav1.ConditionTrue
			s.failed = completed.Status == metav1.ConditionFalse && completed.Reason == kfplacementv1alpha1.ClusterClaimCompletedCondReasonFailed
			s.message = completed.Message
		}
		if claim.Status.ProvisionedClusterName != nil {
			s.clusterName = *claim.Status.ProvisionedClusterName
		}
		return s
	}
}

func fulfilled(claim *kfplacementv1alpha1.ClusterClaim) func(Gomega) {
	return func(g Gomega) {
		s := stateOf(claim)(g)
		g.Expect(s.completed).Should(BeTrue(), "the claim should be completed")
		g.Expect(s.clusterName).Should(Equal(ClusterNameFor(claim)))
		g.Expect(s.accepted).Should(BeTrue(), "a fulfilled claim stays accepted until withdrawn")
	}
}

func clusterExists(name string) func() bool {
	return func() bool {
		return !notFound(&clusterv1beta1.MemberCluster{ObjectMeta: metav1.ObjectMeta{Name: name}})
	}
}

var _ = Describe("fulfilling a cluster claim", func() {
	BeforeEach(func() {
		plain.setMode(modeFulfill)
		remediating.setMode(modeFulfill)
		DeferCleanup(plain.setMode, modeFulfill)
	})

	It("fulfills an approved claim in an empty fleet, with no freshness marker", func() {
		class := createClass(plainProvisioner, nil)
		claim := createClaim(class, nil)
		approve(claim)

		Eventually(fulfilled(claim), eventuallyTimeout, pollInterval).Should(Succeed())
		cluster := &clusterv1beta1.MemberCluster{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: ClusterNameFor(claim)}, cluster)).Should(Succeed())
		Expect(cluster.Labels).Should(Equal(labelsFor(claim)))
		Expect(claim.Annotations[kfplacementv1alpha1.ProvisionedClusterNameAnnotation]).Should(Equal(ClusterNameFor(claim)))
		Expect(meta.IsStatusConditionTrue(claim.Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeAccepted)).Should(BeTrue())
	})

	It("ignores a claim that is not approved", func() {
		class := createClass(plainProvisioner, nil)
		claim := createClaim(class, nil)
		Consistently(func(g Gomega) {
			g.Expect(stateOf(claim)(g).accepted).Should(BeFalse())
		}, consistentlyDuration, pollInterval).Should(Succeed())
	})

	It("ignores a claim of another provider's class", func() {
		class := createClass("someone-else.example.kubefleet.dev", nil)
		claim := createClaim(class, nil)
		approve(claim)
		Consistently(func(g Gomega) {
			g.Expect(stateOf(claim)(g).accepted).Should(BeFalse())
		}, consistentlyDuration, pollInterval).Should(Succeed())
	})

	It("waits, and never fails, while the claim's class is missing", func() {
		claim := createClaim(&kfplacementv1alpha1.ClusterProviderClass{ObjectMeta: metav1.ObjectMeta{Name: "not-yet"}}, nil)
		approve(claim)
		Consistently(func(g Gomega) {
			s := stateOf(claim)(g)
			g.Expect(s.accepted).Should(BeFalse())
			g.Expect(s.failed).Should(BeFalse())
		}, consistentlyDuration, pollInterval).Should(Succeed())
	})

	It("waits on a stale claim until KubeFleet advances the marker, then fulfills it", func() {
		class := createClass(plainProvisioner, nil)
		older := metav1.NewTime(time.Now().Add(-time.Hour))
		newcomer := &clusterv1beta1.MemberCluster{
			ObjectMeta: metav1.ObjectMeta{Name: nextName("newcomer")},
			Spec:       clusterv1beta1.MemberClusterSpec{Identity: rbacv1.Subject{Kind: "ServiceAccount", Name: "n", Namespace: "fleet-system"}},
		}
		Expect(k8sClient.Create(ctx, newcomer)).Should(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, newcomer))).Should(Succeed()) })

		claim := createClaim(class, nil)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(claim), claim)).Should(Succeed())
			claim.Status.LastObservedMostRecentClusterCreationTimestamp = &older
			meta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{Type: kfplacementv1alpha1.ClusterClaimCondTypeApproved, Status: metav1.ConditionTrue, Reason: kfplacementv1alpha1.ClusterClaimApprovedCondReasonApproved})
			g.Expect(k8sClient.Status().Update(ctx, claim)).Should(Succeed())
		}, eventuallyTimeout, pollInterval).Should(Succeed())

		Consistently(func(g Gomega) {
			g.Expect(stateOf(claim)(g).accepted).Should(BeFalse())
		}, consistentlyDuration, pollInterval).Should(Succeed())

		fresh := metav1.Now()
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(claim), claim)).Should(Succeed())
			claim.Status.LastObservedMostRecentClusterCreationTimestamp = &fresh
			g.Expect(k8sClient.Status().Update(ctx, claim)).Should(Succeed())
		}, eventuallyTimeout, pollInterval).Should(Succeed())
		Eventually(fulfilled(claim), eventuallyTimeout, pollInterval).Should(Succeed())
	})

	It("retries a transient failure and completes once provisioning finishes, even if the claim went stale meanwhile", func() {
		plain.setMode(modeTransient)
		class := createClass(plainProvisioner, nil)
		claim := createClaim(class, nil)
		approve(claim)

		Eventually(func(g Gomega) {
			g.Expect(stateOf(claim)(g).accepted).Should(BeTrue())
		}, eventuallyTimeout, pollInterval).Should(Succeed())
		Eventually(func() int { return plain.calls(plain.provisions, ClusterNameFor(claim)) }, eventuallyTimeout, pollInterval).Should(BeNumerically(">=", 2))

		// A cluster joins after the marker: the claim is stale, but a resuming provision ignores that.
		older := metav1.NewTime(time.Now().Add(-time.Hour))
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(claim), claim)).Should(Succeed())
			claim.Status.LastObservedMostRecentClusterCreationTimestamp = &older
			g.Expect(k8sClient.Status().Update(ctx, claim)).Should(Succeed())
		}, eventuallyTimeout, pollInterval).Should(Succeed())
		newcomer := &clusterv1beta1.MemberCluster{
			ObjectMeta: metav1.ObjectMeta{Name: nextName("newcomer")},
			Spec:       clusterv1beta1.MemberClusterSpec{Identity: rbacv1.Subject{Kind: "ServiceAccount", Name: "n", Namespace: "fleet-system"}},
		}
		Expect(k8sClient.Create(ctx, newcomer)).Should(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, newcomer))).Should(Succeed()) })

		plain.setMode(modeFulfill)
		Eventually(fulfilled(claim), eventuallyTimeout, pollInterval).Should(Succeed())
	})

	It("fails the claim on a permanent error, cleaning up its own partial infrastructure", func() {
		plain.setMode(modePermanent)
		class := createClass(plainProvisioner, nil)
		claim := createClaim(class, nil)
		approve(claim)

		Eventually(func(g Gomega) {
			s := stateOf(claim)(g)
			g.Expect(s.failed).Should(BeTrue())
			g.Expect(s.message).Should(ContainSubstring("quota exceeded"))
			g.Expect(s.accepted).Should(BeTrue(), "a failed claim stays held until withdrawn")
		}, eventuallyTimeout, pollInterval).Should(Succeed())
		Expect(plain.calls(plain.deprovision, ClusterNameFor(claim))).Should(Equal(1))
		Expect(clusterExists(ClusterNameFor(claim))()).Should(BeFalse())
	})

	It("fails the claim once maxProvisionDuration has elapsed since acceptance", func() {
		plain.setMode(modeTransient)
		class := createClass(plainProvisioner, func(c *kfplacementv1alpha1.ClusterProviderClass) {
			c.Spec.MaxProvisionDuration = &metav1.Duration{Duration: 2 * time.Second}
		})
		claim := createClaim(class, nil)
		approve(claim)

		Eventually(func(g Gomega) {
			s := stateOf(claim)(g)
			g.Expect(s.failed).Should(BeTrue())
			g.Expect(s.message).Should(ContainSubstring("did not complete within 2s"))
		}, eventuallyTimeout, pollInterval).Should(Succeed())
	})

	It("resumes its own cluster after a restart and never remediates it, even beside a dark sibling", func() {
		class := createClass(remediatingProvisioner, nil)
		// The claim as a previous process left it: held and accepted, the name recorded, the
		// cluster registered under this claim's UID but not yet eligible (still joining) -- and a
		// dark sibling of another UID standing under the same claim name.
		claim := createClaim(class, func(c *kfplacementv1alpha1.ClusterClaim) {
			controllerutil.AddFinalizer(c, kfplacementv1alpha1.FulfillerFinalizer)
		})
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(claim), claim)).Should(Succeed())
			claim.Annotations = map[string]string{kfplacementv1alpha1.ProvisionedClusterNameAnnotation: ClusterNameFor(claim)}
			g.Expect(k8sClient.Update(ctx, claim)).Should(Succeed())
		}, eventuallyTimeout, pollInterval).Should(Succeed())
		for name, uid := range map[string]string{ClusterNameFor(claim): string(claim.UID), nextName("sibling"): "previous-uid"} {
			labels := labelsFor(claim)
			labels[kfplacementv1alpha1.FulfilledClaimUIDLabel] = uid
			cluster := &clusterv1beta1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
				Spec:       clusterv1beta1.MemberClusterSpec{Identity: rbacv1.Subject{Kind: "ServiceAccount", Name: "r", Namespace: "fleet-system"}},
			}
			Expect(k8sClient.Create(ctx, cluster)).Should(Succeed())
		}
		setClaimCondition(claim, metav1.Condition{Type: kfplacementv1alpha1.ClusterClaimCondTypeAccepted, Status: metav1.ConditionTrue, Reason: kfplacementv1alpha1.ClusterClaimAcceptedCondReasonAccepted})
		approve(claim)

		Eventually(fulfilled(claim), eventuallyTimeout, pollInterval).Should(Succeed())
		Expect(remediating.calls(remediating.provisions, ClusterNameFor(claim))).Should(BeZero(), "the own cluster is resumed, not provisioned again")
		Expect(remediating.calls(remediating.remediated, ClusterNameFor(claim))).Should(BeZero(), "the own cluster is never remediated")
		Consistently(func() int {
			remediating.mu.Lock()
			defer remediating.mu.Unlock()
			return len(remediating.remediated)
		}, consistentlyDuration, pollInterval).Should(BeZero(), "nor is the dark sibling, once the claim is fulfilled")
	})

	It("repairs an acceptance a crash left half done, so the provision clock still runs", func() {
		plain.setMode(modeTransient)
		class := createClass(plainProvisioner, func(c *kfplacementv1alpha1.ClusterProviderClass) {
			c.Spec.MaxProvisionDuration = &metav1.Duration{Duration: 2 * time.Second}
		})
		// Finalizer and name present, Accepted absent: the first acceptance write landed, the
		// second did not.
		claim := createClaim(class, func(c *kfplacementv1alpha1.ClusterClaim) {
			controllerutil.AddFinalizer(c, kfplacementv1alpha1.FulfillerFinalizer)
			c.Annotations = map[string]string{kfplacementv1alpha1.ProvisionedClusterNameAnnotation: "preset"}
		})
		approve(claim)

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(claim), claim)).Should(Succeed())
			g.Expect(meta.IsStatusConditionTrue(claim.Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeAccepted)).Should(BeTrue())
		}, eventuallyTimeout, pollInterval).Should(Succeed())
		Eventually(func(g Gomega) {
			s := stateOf(claim)(g)
			g.Expect(s.failed).Should(BeTrue())
			g.Expect(s.message).Should(ContainSubstring("did not complete within 2s"))
		}, eventuallyTimeout, pollInterval).Should(Succeed())
		Expect(plain.calls(plain.provisions, "preset")).Should(BeNumerically(">=", 1), "the recorded name is what the provider is handed")
	})

	It("fails a re-issued claim whose previous cluster is dark when the provider cannot remediate", func() {
		class := createClass(plainProvisioner, nil)
		first := createClaim(class, nil)
		approve(first)
		Eventually(fulfilled(first), eventuallyTimeout, pollInterval).Should(Succeed())
		// The cluster registered but never joined (no agent status): not eligible, and the claim
		// is withdrawn as KubeFleet would on a selector change... except here it is re-issued
		// under the same name with the first cluster still dark.
		darkCluster := ClusterNameFor(first)
		Expect(k8sClient.Delete(ctx, first)).Should(Succeed())
		Eventually(func() bool { return notFound(first) }, eventuallyTimeout, pollInterval).Should(BeTrue())
		// Withdrawal of a never-joined, ineligible cluster deprovisions it; re-register it by hand to
		// stage the dark-cluster case.
		Eventually(func() bool { return !clusterExists(darkCluster)() }, eventuallyTimeout, pollInterval).Should(BeTrue())
		stale := &clusterv1beta1.MemberCluster{
			ObjectMeta: metav1.ObjectMeta{Name: darkCluster, Labels: labelsFor(first)},
			Spec:       clusterv1beta1.MemberClusterSpec{Identity: rbacv1.Subject{Kind: "ServiceAccount", Name: "d", Namespace: "fleet-system"}},
		}
		Expect(k8sClient.Create(ctx, stale)).Should(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, stale))).Should(Succeed()) })

		second := createClaim(class, func(c *kfplacementv1alpha1.ClusterClaim) { c.Name = first.Name })
		approve(second)
		Eventually(func(g Gomega) {
			s := stateOf(second)(g)
			g.Expect(s.failed).Should(BeTrue())
			g.Expect(s.message).Should(ContainSubstring(darkCluster))
			g.Expect(s.message).Should(ContainSubstring("cannot remediate"))
		}, eventuallyTimeout, pollInterval).Should(Succeed())
		Expect(plain.calls(plain.provisions, ClusterNameFor(second))).Should(BeZero(), "no new cluster while the dark one stands")
	})

	It("remediates a dark previous cluster when the provider can, then provisions the next one", func() {
		class := createClass(remediatingProvisioner, nil)
		first := createClaim(class, nil)
		approve(first)
		Eventually(fulfilled(first), eventuallyTimeout, pollInterval).Should(Succeed())
		darkCluster := ClusterNameFor(first)
		// Joined, then went dark: stays registered through the withdrawal.
		markJoined(darkCluster, false)
		Expect(k8sClient.Delete(ctx, first)).Should(Succeed())
		Eventually(func() bool { return notFound(first) }, eventuallyTimeout, pollInterval).Should(BeTrue())
		Expect(clusterExists(darkCluster)()).Should(BeTrue(), "a joined cluster is never deprovisioned")

		second := createClaim(class, func(c *kfplacementv1alpha1.ClusterClaim) { c.Name = first.Name })
		approve(second)
		Eventually(func() int { return remediating.calls(remediating.remediated, darkCluster) }, eventuallyTimeout, pollInterval).Should(Equal(1))
		// Remediation made it eligible, so the selector needs one more: a new cluster.
		Eventually(fulfilled(second), eventuallyTimeout, pollInterval).Should(Succeed())
		Expect(ClusterNameFor(second)).ShouldNot(Equal(darkCluster))
	})

	It("provisions a new cluster for a re-issued claim when the previous cluster is eligible", func() {
		class := createClass(plainProvisioner, nil)
		first := createClaim(class, nil)
		approve(first)
		Eventually(fulfilled(first), eventuallyTimeout, pollInterval).Should(Succeed())
		markJoined(ClusterNameFor(first), true)
		Expect(k8sClient.Delete(ctx, first)).Should(Succeed())
		Eventually(func() bool { return notFound(first) }, eventuallyTimeout, pollInterval).Should(BeTrue())
		Expect(clusterExists(ClusterNameFor(first))()).Should(BeTrue())

		second := createClaim(class, func(c *kfplacementv1alpha1.ClusterClaim) { c.Name = first.Name })
		approve(second)
		Eventually(fulfilled(second), eventuallyTimeout, pollInterval).Should(Succeed())
		Expect(clusterExists(ClusterNameFor(first))()).Should(BeTrue())
		Expect(clusterExists(ClusterNameFor(second))()).Should(BeTrue())
	})
})

var _ = Describe("withdrawal and expiry", func() {
	BeforeEach(func() {
		plain.setMode(modeFulfill)
		DeferCleanup(plain.setMode, modeFulfill)
	})

	It("deprovisions a never-joined cluster on a join timeout and releases the claim", func() {
		class := createClass(plainProvisioner, nil)
		claim := createClaim(class, nil)
		approve(claim)
		Eventually(fulfilled(claim), eventuallyTimeout, pollInterval).Should(Succeed())

		expire(claim, kfplacementv1alpha1.ClusterClaimExpiredCondReasonJoinTimeout)
		Eventually(func(g Gomega) {
			g.Expect(stateOf(claim)(g).released).Should(BeTrue())
			g.Expect(clusterExists(ClusterNameFor(claim))()).Should(BeFalse())
		}, eventuallyTimeout, pollInterval).Should(Succeed())
		Expect(plain.calls(plain.deprovision, ClusterNameFor(claim))).Should(Equal(1))
	})

	It("keeps a cluster that joined late, or joined and went dark, on a join timeout", func() {
		class := createClass(plainProvisioner, nil)
		for _, recent := range []bool{true, false} {
			claim := createClaim(class, nil)
			approve(claim)
			Eventually(fulfilled(claim), eventuallyTimeout, pollInterval).Should(Succeed())
			markJoined(ClusterNameFor(claim), recent)

			expire(claim, kfplacementv1alpha1.ClusterClaimExpiredCondReasonJoinTimeout)
			Eventually(func(g Gomega) {
				g.Expect(stateOf(claim)(g).released).Should(BeTrue())
			}, eventuallyTimeout, pollInterval).Should(Succeed())
			Expect(clusterExists(ClusterNameFor(claim))()).Should(BeTrue(), "recent=%t", recent)
			Expect(plain.calls(plain.deprovision, ClusterNameFor(claim))).Should(BeZero(), "recent=%t", recent)
		}
	})

	It("releases a not-matching claim without touching the cluster", func() {
		class := createClass(plainProvisioner, nil)
		claim := createClaim(class, nil)
		approve(claim)
		Eventually(fulfilled(claim), eventuallyTimeout, pollInterval).Should(Succeed())

		expire(claim, kfplacementv1alpha1.ClusterClaimExpiredCondReasonNotMatching)
		Eventually(func(g Gomega) {
			g.Expect(stateOf(claim)(g).released).Should(BeTrue())
		}, eventuallyTimeout, pollInterval).Should(Succeed())
		Expect(clusterExists(ClusterNameFor(claim))()).Should(BeTrue())
		Expect(plain.calls(plain.deprovision, ClusterNameFor(claim))).Should(BeZero())
	})

	It("cleans up and releases a pending-timeout claim it somehow still holds", func() {
		class := createClass(plainProvisioner, nil)
		claim := createClaim(class, func(c *kfplacementv1alpha1.ClusterClaim) {
			// As a crash between the two acceptance writes could leave it: finalizer and
			// pre-recorded name present, no Accepted condition.
			controllerutil.AddFinalizer(c, kfplacementv1alpha1.FulfillerFinalizer)
		})
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(claim), claim)).Should(Succeed())
			if claim.Annotations == nil {
				claim.Annotations = map[string]string{}
			}
			claim.Annotations[kfplacementv1alpha1.ProvisionedClusterNameAnnotation] = ClusterNameFor(claim)
			g.Expect(k8sClient.Update(ctx, claim)).Should(Succeed())
		}, eventuallyTimeout, pollInterval).Should(Succeed())
		approve(claim)
		expire(claim, kfplacementv1alpha1.ClusterClaimExpiredCondReasonPendingTimeout)

		Eventually(func(g Gomega) {
			g.Expect(stateOf(claim)(g).released).Should(BeTrue())
		}, eventuallyTimeout, pollInterval).Should(Succeed())
		Expect(plain.calls(plain.deprovision, ClusterNameFor(claim))).Should(Equal(1))
	})

	It("cancels an in-flight provision on withdrawal", func() {
		plain.setMode(modeTransient)
		class := createClass(plainProvisioner, nil)
		claim := createClaim(class, nil)
		approve(claim)
		Eventually(func(g Gomega) {
			g.Expect(stateOf(claim)(g).accepted).Should(BeTrue())
		}, eventuallyTimeout, pollInterval).Should(Succeed())

		Expect(k8sClient.Delete(ctx, claim)).Should(Succeed())
		Eventually(func() bool { return notFound(claim) }, eventuallyTimeout, pollInterval).Should(BeTrue())
		Expect(plain.calls(plain.deprovision, ClusterNameFor(claim))).Should(Equal(1))
	})

	It("cleans up a failed claim on withdrawal", func() {
		plain.setMode(modePermanent)
		class := createClass(plainProvisioner, nil)
		claim := createClaim(class, nil)
		approve(claim)
		Eventually(func(g Gomega) {
			g.Expect(stateOf(claim)(g).failed).Should(BeTrue())
		}, eventuallyTimeout, pollInterval).Should(Succeed())

		Expect(k8sClient.Delete(ctx, claim)).Should(Succeed())
		Eventually(func() bool { return notFound(claim) }, eventuallyTimeout, pollInterval).Should(BeTrue())
		Expect(plain.calls(plain.deprovision, ClusterNameFor(claim))).Should(BeNumerically(">=", 1))
		Expect(clusterExists(ClusterNameFor(claim))()).Should(BeFalse())
	})

	It("releases a fulfilled claim on withdrawal and keeps a cluster that joined, eligible or momentarily dark", func() {
		class := createClass(plainProvisioner, nil)
		for _, recent := range []bool{true, false} {
			claim := createClaim(class, nil)
			approve(claim)
			Eventually(fulfilled(claim), eventuallyTimeout, pollInterval).Should(Succeed())
			markJoined(ClusterNameFor(claim), recent)

			Expect(k8sClient.Delete(ctx, claim)).Should(Succeed())
			Eventually(func() bool { return notFound(claim) }, eventuallyTimeout, pollInterval).Should(BeTrue())
			Expect(clusterExists(ClusterNameFor(claim))()).Should(BeTrue(), "recent=%t", recent)
			Expect(plain.calls(plain.deprovision, ClusterNameFor(claim))).Should(BeZero(), "recent=%t", recent)
		}
	})

	It("deprovisions a never-joined cluster when a fulfilled claim is withdrawn inside the join window", func() {
		class := createClass(plainProvisioner, nil)
		claim := createClaim(class, nil)
		approve(claim)
		Eventually(fulfilled(claim), eventuallyTimeout, pollInterval).Should(Succeed())

		Expect(k8sClient.Delete(ctx, claim)).Should(Succeed())
		Eventually(func() bool { return notFound(claim) }, eventuallyTimeout, pollInterval).Should(BeTrue())
		Expect(clusterExists(ClusterNameFor(claim))()).Should(BeFalse())
		Expect(plain.calls(plain.deprovision, ClusterNameFor(claim))).Should(Equal(1))
	})

	It("has nothing to do for a withdrawn claim it never accepted", func() {
		class := createClass(plainProvisioner, nil)
		claim := createClaim(class, nil)
		Expect(k8sClient.Delete(ctx, claim)).Should(Succeed())
		Eventually(func() bool { return notFound(claim) }, eventuallyTimeout, pollInterval).Should(BeTrue())
		Expect(plain.calls(plain.deprovision, ClusterNameFor(claim))).Should(BeZero())
	})
})

var _ = Describe("status writes", func() {
	It("writes the fulfiller-owned fields only", func() {
		plain.setMode(modeFulfill)
		class := createClass(plainProvisioner, nil)
		marker := metav1.Now()
		claim := createClaim(class, nil)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(claim), claim)).Should(Succeed())
			claim.Status.LastObservedMostRecentClusterCreationTimestamp = &marker
			meta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{Type: kfplacementv1alpha1.ClusterClaimCondTypeApproved, Status: metav1.ConditionTrue, Reason: kfplacementv1alpha1.ClusterClaimApprovedCondReasonApproved})
			g.Expect(k8sClient.Status().Update(ctx, claim)).Should(Succeed())
		}, eventuallyTimeout, pollInterval).Should(Succeed())
		Eventually(fulfilled(claim), eventuallyTimeout, pollInterval).Should(Succeed())

		Expect(claim.Status.LastObservedMostRecentClusterCreationTimestamp.Time).Should(BeTemporally("~", marker.Time, time.Second))
		Expect(meta.FindStatusCondition(claim.Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeApproved).Reason).Should(Equal(kfplacementv1alpha1.ClusterClaimApprovedCondReasonApproved))
		Expect(claim.Status.ProvisionedClusterName).Should(Equal(ptr.To(ClusterNameFor(claim))))
	})
})
