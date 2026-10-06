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

package v1alpha1

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	placementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
)

// newClaim builds a minimal valid ClusterClaim; the caller registers cleanup.
func newClaim(name string) *placementv1alpha1.ClusterClaim {
	return &placementv1alpha1.ClusterClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: placementv1alpha1.ClusterClaimSpec{
			PlacementPolicyRef: &placementv1alpha1.ObjectReference{
				Name:       "app",
				Namespace:  "work",
				APIVersion: "v1alpha1",
				Kind:       placementv1alpha1.PlacementPolicyKind,
			},
		},
	}
}

func createClaim(claim *placementv1alpha1.ClusterClaim) {
	Expect(hubClient.Create(ctx, claim)).Should(Succeed())
	DeferCleanup(func() {
		Expect(client.IgnoreNotFound(hubClient.Delete(ctx, claim))).Should(Succeed())
	})
}

// rawObject builds an unstructured object of the given kind, which lets a test send a field value
// the typed client would drop on the wire (omitempty), such as an explicit empty string.
func rawObject(kind, name string, spec map[string]any) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{"spec": spec}}
	obj.SetGroupVersionKind(placementv1alpha1.GroupVersion.WithKind(kind))
	obj.SetName(name)
	return obj
}

// setCondition writes one condition onto the claim status and returns the update error.
func setCondition(claim *placementv1alpha1.ClusterClaim, cond metav1.Condition) error {
	meta.SetStatusCondition(&claim.Status.Conditions, cond)
	return hubClient.Status().Update(ctx, claim)
}

func completed(status metav1.ConditionStatus, reason, message string) metav1.Condition {
	return metav1.Condition{Type: placementv1alpha1.ClusterClaimCondTypeCompleted, Status: status, Reason: reason, Message: message}
}

func approved(status metav1.ConditionStatus, reason string) metav1.Condition {
	return metav1.Condition{Type: placementv1alpha1.ClusterClaimCondTypeApproved, Status: status, Reason: reason}
}

var _ = Describe("Test ClusterClaim fulfillment API validation", func() {
	Context("the provider class name", func() {
		It("may be left unset, set once, and then never changed", func() {
			claim := newClaim("class-once")
			createClaim(claim)

			claim.Spec.ClusterProviderClassName = "standard"
			Expect(hubClient.Update(ctx, claim)).Should(Succeed(), "setting the class on a claim without one is the policy controller's own write")

			claim.Spec.ClusterProviderClassName = "other"
			Expect(hubClient.Update(ctx, claim)).Should(MatchError(ContainSubstring("the clusterProviderClassName field is immutable once set")))

			claim.Spec.ClusterProviderClassName = ""
			Expect(hubClient.Update(ctx, claim)).Should(MatchError(ContainSubstring("the clusterProviderClassName field is immutable once set")))
		})

		It("rejects an explicit empty string", func() {
			raw := rawObject(placementv1alpha1.ClusterClaimKind, "class-empty", map[string]any{
				"placementPolicyRef":       map[string]any{"name": "app", "namespace": "work", "apiVersion": "v1alpha1", "kind": "PlacementPolicy"},
				"clusterProviderClassName": "",
			})
			Expect(hubClient.Create(ctx, raw)).Should(MatchError(ContainSubstring("should be at least 1 chars")))
		})
	})

	Context("the Completed condition", func() {
		It("requires a non-empty provisioned cluster name when True", func() {
			claim := newClaim("fulfilled-without-name")
			createClaim(claim)

			Expect(setCondition(claim, completed(metav1.ConditionTrue, placementv1alpha1.ClusterClaimCompletedCondReasonFulfilled, ""))).
				Should(MatchError(ContainSubstring("provisionedClusterName must be set when the Completed condition is True")))

			claim.Status.ProvisionedClusterName = ptr.To("")
			Expect(hubClient.Status().Update(ctx, claim)).Should(MatchError(ContainSubstring("provisionedClusterName must be set when the Completed condition is True")))

			claim.Status.ProvisionedClusterName = ptr.To("c1")
			Expect(hubClient.Status().Update(ctx, claim)).Should(Succeed())
		})

		It("keeps a fulfilled claim fulfilled", func() {
			claim := newClaim("fulfilled-stays")
			createClaim(claim)
			claim.Status.ProvisionedClusterName = ptr.To("c1")
			Expect(setCondition(claim, approved(metav1.ConditionTrue, placementv1alpha1.ClusterClaimApprovedCondReasonApproved))).Should(Succeed())
			Expect(setCondition(claim, completed(metav1.ConditionTrue, placementv1alpha1.ClusterClaimCompletedCondReasonFulfilled, ""))).Should(Succeed())

			Expect(setCondition(claim, completed(metav1.ConditionFalse, placementv1alpha1.ClusterClaimCompletedCondReasonFailed, "oops"))).
				Should(MatchError(ContainSubstring("a fulfilled cluster claim cannot be marked as not completed")))

			// Removing the Completed entry while the Approved entry stays is the same downgrade.
			meta.RemoveStatusCondition(&claim.Status.Conditions, placementv1alpha1.ClusterClaimCondTypeCompleted)
			Expect(hubClient.Status().Update(ctx, claim)).Should(MatchError(ContainSubstring("a fulfilled cluster claim cannot be marked as not completed")))

			claim.Status.Conditions = nil
			Expect(hubClient.Status().Update(ctx, claim)).Should(MatchError(ContainSubstring("a fulfilled cluster claim cannot be marked as not completed")))
		})

		It("keeps a failed claim failed, but lets its message change", func() {
			claim := newClaim("failed-stays")
			createClaim(claim)
			Expect(setCondition(claim, completed(metav1.ConditionFalse, placementv1alpha1.ClusterClaimCompletedCondReasonFailed, "quota"))).Should(Succeed())

			Expect(setCondition(claim, completed(metav1.ConditionFalse, placementv1alpha1.ClusterClaimCompletedCondReasonFailed, "quota, still"))).Should(Succeed())

			Expect(setCondition(claim, completed(metav1.ConditionFalse, "Retrying", "trying again"))).
				Should(MatchError(ContainSubstring("a failed cluster claim cannot be marked as anything else")))

			claim.Status.ProvisionedClusterName = ptr.To("c1")
			Expect(setCondition(claim, completed(metav1.ConditionTrue, placementv1alpha1.ClusterClaimCompletedCondReasonFulfilled, ""))).
				Should(MatchError(ContainSubstring("a failed cluster claim cannot be marked as anything else")))

			meta.RemoveStatusCondition(&claim.Status.Conditions, placementv1alpha1.ClusterClaimCondTypeCompleted)
			Expect(hubClient.Status().Update(ctx, claim)).Should(MatchError(ContainSubstring("a failed cluster claim cannot be marked as anything else")))
		})
	})

	Context("the Expired condition", func() {
		It("stays expired", func() {
			claim := newClaim("expired-stays")
			createClaim(claim)
			Expect(setCondition(claim, approved(metav1.ConditionTrue, placementv1alpha1.ClusterClaimApprovedCondReasonApproved))).Should(Succeed())
			expired := metav1.Condition{Type: placementv1alpha1.ClusterClaimCondTypeExpired, Status: metav1.ConditionTrue, Reason: placementv1alpha1.ClusterClaimExpiredCondReasonPendingTimeout}
			Expect(setCondition(claim, expired)).Should(Succeed())

			expired.Status = metav1.ConditionFalse
			Expect(setCondition(claim, expired)).Should(MatchError(ContainSubstring("an expired cluster claim cannot be marked as not expired")))

			meta.RemoveStatusCondition(&claim.Status.Conditions, placementv1alpha1.ClusterClaimCondTypeExpired)
			Expect(hubClient.Status().Update(ctx, claim)).Should(MatchError(ContainSubstring("an expired cluster claim cannot be marked as not expired")))

			claim.Status.Conditions = nil
			Expect(hubClient.Status().Update(ctx, claim)).Should(MatchError(ContainSubstring("an expired cluster claim cannot be marked as not expired")))
		})
	})

	Context("the Approved condition", func() {
		It("may be denied and then approved after all", func() {
			claim := newClaim("denied-then-approved")
			createClaim(claim)
			Expect(setCondition(claim, approved(metav1.ConditionFalse, placementv1alpha1.ClusterClaimApprovedCondReasonDenied))).Should(Succeed())
			Expect(setCondition(claim, approved(metav1.ConditionTrue, placementv1alpha1.ClusterClaimApprovedCondReasonApproved))).Should(Succeed())
		})

		It("accepts every reason the contract defines", func() {
			claim := newClaim("all-reasons")
			createClaim(claim)
			claim.Status.ProvisionedClusterName = ptr.To("c1")
			for _, cond := range []metav1.Condition{
				approved(metav1.ConditionTrue, placementv1alpha1.ClusterClaimApprovedCondReasonAutomaticallyApproved),
				{Type: placementv1alpha1.ClusterClaimCondTypeAccepted, Status: metav1.ConditionTrue, Reason: placementv1alpha1.ClusterClaimAcceptedCondReasonAccepted},
				completed(metav1.ConditionTrue, placementv1alpha1.ClusterClaimCompletedCondReasonFulfilled, ""),
				{Type: placementv1alpha1.ClusterClaimCondTypeExpired, Status: metav1.ConditionTrue, Reason: placementv1alpha1.ClusterClaimExpiredCondReasonJoinTimeout},
			} {
				Expect(setCondition(claim, cond)).Should(Succeed(), "condition %s/%s", cond.Type, cond.Reason)
			}
		})
	})

	Context("the conditions list", func() {
		It("holds up to sixteen conditions", func() {
			claim := newClaim("sixteen")
			createClaim(claim)
			for i := range 16 {
				claim.Status.Conditions = append(claim.Status.Conditions, metav1.Condition{
					Type: fmt.Sprintf("Provider%d", i), Status: metav1.ConditionTrue, Reason: "Set", LastTransitionTime: metav1.Now(),
				})
			}
			Expect(hubClient.Status().Update(ctx, claim)).Should(Succeed())

			claim.Status.Conditions = append(claim.Status.Conditions, metav1.Condition{
				Type: "Provider16", Status: metav1.ConditionTrue, Reason: "Set", LastTransitionTime: metav1.Now(),
			})
			Expect(hubClient.Status().Update(ctx, claim)).Should(MatchError(ContainSubstring("must have at most 16 items")))
		})
	})
})

var _ = Describe("Test PlacementPolicy fulfillment API validation", func() {
	resourceSelectors := []any{map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "name": "demo"}}

	DescribeTable("the class name and the claim limit, on both policy kinds",
		func(kind string, namespaced bool) {
			create := func(name string, spec map[string]any) error {
				spec["resourceSelectors"] = resourceSelectors
				raw := rawObject(kind, name, spec)
				if namespaced {
					raw.SetNamespace("default")
				}
				err := hubClient.Create(ctx, raw)
				if err == nil {
					DeferCleanup(func() {
						Expect(client.IgnoreNotFound(hubClient.Delete(ctx, raw))).Should(Succeed())
					})
				}
				return err
			}
			Expect(create("fulfillment-ok", map[string]any{"clusterProviderClassName": "standard", "maxConcurrentClusterClaims": 1})).Should(Succeed())
			Expect(create("fulfillment-max", map[string]any{"maxConcurrentClusterClaims": 10})).Should(Succeed())
			Expect(create("fulfillment-empty-class", map[string]any{"clusterProviderClassName": ""})).Should(MatchError(ContainSubstring("should be at least 1 chars")))
			Expect(create("fulfillment-zero", map[string]any{"maxConcurrentClusterClaims": 0})).Should(MatchError(ContainSubstring("should be greater than or equal to 1")))
			Expect(create("fulfillment-eleven", map[string]any{"maxConcurrentClusterClaims": 11})).Should(MatchError(ContainSubstring("should be less than or equal to 10")))
		},
		Entry("PlacementPolicy", placementv1alpha1.PlacementPolicyKind, true),
		Entry("ClusterPlacementPolicy", placementv1alpha1.ClusterPlacementPolicyKind, false),
	)
})
