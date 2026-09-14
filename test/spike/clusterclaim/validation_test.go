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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	placementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
)

// These specs pin the CEL/schema contract of the claim API (issue #791,
// coordinating with #793 for the broader CEL test scope).
var _ = Describe("ClusterClaim CEL and schema validation", func() {
	var counter int

	newClaim := func(terms []placementv1alpha1.ClusterLabelAndPropertySelectorTerm) *placementv1alpha1.ClusterClaim {
		counter++
		return &placementv1alpha1.ClusterClaim{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("cel-claim-%d", counter)},
			Spec: placementv1alpha1.ClusterClaimSpec{
				PlacementPolicyRef: &placementv1alpha1.ObjectReference{
					Name:       "app",
					Namespace:  "work",
					APIGroup:   "placement.kubefleet.dev",
					APIVersion: "v1alpha1",
					Kind:       "PlacementPolicy",
				},
				ClusterSelectorTerms: terms,
			},
		}
	}

	regionTerm := func(region string) placementv1alpha1.ClusterLabelAndPropertySelectorTerm {
		return placementv1alpha1.ClusterLabelAndPropertySelectorTerm{
			MatchLabels: map[string]string{"topology.kubernetes.io/region": region},
		}
	}

	It("rejects creation without placementPolicyRef", func() {
		claim := newClaim(nil)
		claim.Spec.PlacementPolicyRef = nil
		Expect(k8sClient.Create(ctx, claim)).ShouldNot(Succeed())
	})

	It("rejects mutation of placementPolicyRef (CEL immutability)", func() {
		claim := newClaim([]placementv1alpha1.ClusterLabelAndPropertySelectorTerm{regionTerm("eastus")})
		Expect(k8sClient.Create(ctx, claim)).Should(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, claim)).Should(Succeed()) })

		claim.Spec.PlacementPolicyRef.Name = "other"
		err := k8sClient.Update(ctx, claim)
		Expect(err).Should(HaveOccurred())
		Expect(err.Error()).Should(ContainSubstring("immutable"))
	})

	It("rejects mutation of clusterSelectorTerms, including reordering (CEL immutability)", func() {
		claim := newClaim([]placementv1alpha1.ClusterLabelAndPropertySelectorTerm{regionTerm("eastus"), regionTerm("westus")})
		Expect(k8sClient.Create(ctx, claim)).Should(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, claim)).Should(Succeed()) })

		By("appending a term")
		mutated := claim.DeepCopy()
		mutated.Spec.ClusterSelectorTerms = append(mutated.Spec.ClusterSelectorTerms, regionTerm("centralus"))
		Expect(k8sClient.Update(ctx, mutated)).ShouldNot(Succeed())

		By("reordering terms — CEL list equality is order-sensitive")
		mutated = claim.DeepCopy()
		mutated.Spec.ClusterSelectorTerms = []placementv1alpha1.ClusterLabelAndPropertySelectorTerm{regionTerm("westus"), regionTerm("eastus")}
		Expect(k8sClient.Update(ctx, mutated)).ShouldNot(Succeed())
	})

	// FINDING (spike, 2026-08-12): the field-level CEL rule `self == oldSelf`
	// on spec.clusterSelectorTerms does NOT fire when the field is removed —
	// Kubernetes skips field-scoped transition rules when the field is absent
	// from the new object. Unsetting the terms flips the claim's meaning to
	// "any cluster satisfies this claim", silently widening it. The guard must
	// be duplicated at the spec level, e.g.
	// `has(self.clusterSelectorTerms) == has(oldSelf.clusterSelectorTerms)`.
	// This spec pins the CURRENT (buggy) behavior so the suite stays green;
	// flip the assertion once the API is fixed (candidate: PR #803).
	It("KNOWN GAP: dropping clusterSelectorTerms entirely bypasses CEL immutability", func() {
		claim := newClaim([]placementv1alpha1.ClusterLabelAndPropertySelectorTerm{regionTerm("eastus")})
		Expect(k8sClient.Create(ctx, claim)).Should(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, claim)).Should(Succeed()) })

		claim.Spec.ClusterSelectorTerms = nil
		Expect(k8sClient.Update(ctx, claim)).Should(Succeed(), "currently accepted — should be rejected once the spec-level guard exists")
	})

	// FINDING (spike round 2): the removal bypass has a mirror image — a claim
	// created WITHOUT terms ("any cluster satisfies") can have terms ADDED
	// later, silently narrowing it. Same root cause as the removal case:
	// field-scoped transition rules are skipped when the field is absent on
	// either side of the update. Note `[]` serializes as absent via omitempty,
	// so an empty-list create also leaves the claim mutable.
	It("KNOWN GAP: adding clusterSelectorTerms after creation bypasses CEL immutability", func() {
		claim := newClaim(nil)
		Expect(k8sClient.Create(ctx, claim)).Should(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, claim)).Should(Succeed()) })

		claim.Spec.ClusterSelectorTerms = []placementv1alpha1.ClusterLabelAndPropertySelectorTerm{regionTerm("eastus")}
		Expect(k8sClient.Update(ctx, claim)).Should(Succeed(), "currently accepted — should be rejected once the spec-level guard exists")
	})

	// FINDING (spike round 2): nothing ties the Completed condition to its
	// evidence. A provisioner can report Completed=True with no
	// provisionedClusterName, and can later downgrade or repoint a completed
	// claim — the status subresource accepts all of it. Status atomicity and
	// terminal-state pinning need CEL rules (Completed=True requires
	// provisionedClusterName; no downgrade out of a terminal state;
	// provisionedClusterName immutable once set).
	It("KNOWN GAP: status accepts Completed=True without provisionedClusterName, and downgrades", func() {
		claim := newClaim([]placementv1alpha1.ClusterLabelAndPropertySelectorTerm{regionTerm("australiaeast")})
		Expect(k8sClient.Create(ctx, claim)).Should(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, claim)).Should(Succeed()) })

		By("Completed=True lands with no provisioned cluster recorded")
		claim.Status.Conditions = []metav1.Condition{{
			Type:               placementv1alpha1.ClusterClaimCondTypeCompleted,
			Status:             metav1.ConditionTrue,
			Reason:             "Provisioned",
			Message:            "spike: no provisionedClusterName set",
			LastTransitionTime: metav1.Now(),
		}}
		Expect(k8sClient.Status().Update(ctx, claim)).Should(Succeed(), "currently accepted — atomicity CEL would reject this")

		By("the terminal state downgrades back to in-progress")
		claim.Status.Conditions[0].Status = metav1.ConditionFalse
		claim.Status.Conditions[0].Reason = "Provisioning"
		Expect(k8sClient.Status().Update(ctx, claim)).Should(Succeed(), "currently accepted — terminal-state pinning would reject this")
	})

	It("accepts a claim with no selector terms (any cluster satisfies)", func() {
		claim := newClaim(nil)
		Expect(k8sClient.Create(ctx, claim)).Should(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, claim)).Should(Succeed()) })
	})

	It("enforces expression operator/values arity rules", func() {
		By("In with empty values is rejected")
		claim := newClaim([]placementv1alpha1.ClusterLabelAndPropertySelectorTerm{{
			MatchLabelExpressions: []placementv1alpha1.LabelClusterPropertyExpression{{
				Key:      "env",
				Operator: placementv1alpha1.LabelClusterPropertyExpressionOperatorIn,
			}},
		}})
		Expect(k8sClient.Create(ctx, claim)).ShouldNot(Succeed())

		By("Exists with values is rejected")
		claim = newClaim([]placementv1alpha1.ClusterLabelAndPropertySelectorTerm{{
			MatchLabelExpressions: []placementv1alpha1.LabelClusterPropertyExpression{{
				Key:      "env",
				Operator: placementv1alpha1.LabelClusterPropertyExpressionOperatorExists,
				Values:   []string{"staging"},
			}},
		}})
		Expect(k8sClient.Create(ctx, claim)).ShouldNot(Succeed())
	})

	It("keeps spec.generation at 1 for the object's whole lifetime (immutable spec invariant)", func() {
		claim := newClaim([]placementv1alpha1.ClusterLabelAndPropertySelectorTerm{regionTerm("eastus")})
		Expect(k8sClient.Create(ctx, claim)).Should(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, claim)).Should(Succeed()) })

		mutated := claim.DeepCopy()
		mutated.Spec.ClusterSelectorTerms = []placementv1alpha1.ClusterLabelAndPropertySelectorTerm{regionTerm("westus")}
		Expect(k8sClient.Update(ctx, mutated)).ShouldNot(Succeed())

		fetched := &placementv1alpha1.ClusterClaim{}
		Expect(k8sClient.Get(ctx, clientKey(claim.Name), fetched)).Should(Succeed())
		Expect(fetched.Generation).Should(Equal(int64(1)))
	})
})
