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

package admissionpolicymanager

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils"
)

const (
	hubAgentUsername  = "system:serviceaccount:fleet-system:hub-agent-sa"
	approverUsername  = "approver"
	approverUserGroup = "claim-approvers"
	tenantUsername    = "tenant"
	fulfillerUsername = "system:serviceaccount:fleet-system:reference-fulfiller"

	claimWriterRole   = "clusterclaim-status-writer"
	claimApproverRole = "clusterclaim-approver"

	policyDeniedMessage = "setting or changing the Approved condition of a ClusterClaim requires the approve verb on it"
)

// The policy guards the Approved condition of a ClusterClaim; who may write it is the whole
// point, so every request here comes from an impersonated identity. Only system:masters may
// impersonate, which the envtest admin credentials are. Every identity may write claim status,
// so RBAC alone never denies a request here; the approve verb is what separates approvers.
var _ = Describe("ClusterClaim approval policy effects", Ordered, func() {
	var (
		asApprover, asGroupApprover, asHubAgent, asTenant, asFulfiller client.Client
		claim                                                          *kfplacementv1alpha1.ClusterClaim
	)

	impersonate := func(username string, groups ...string) client.Client {
		impersonated := rest.CopyConfig(cfg)
		impersonated.Impersonate = rest.ImpersonationConfig{UserName: username, Groups: groups}
		c, err := client.New(impersonated, client.Options{Scheme: scheme.Scheme})
		Expect(err).ToNot(HaveOccurred())
		return c
	}
	approved := func(status metav1.ConditionStatus, reason string) *metav1.Condition {
		return &metav1.Condition{Type: kfplacementv1alpha1.ClusterClaimCondTypeApproved, Status: status, Reason: reason, Message: "by test"}
	}
	// latestClaim re-reads the claim as the admin, so that each attempt is judged on the condition
	// change alone and not on a stale resourceVersion.
	latestClaim := func() *kfplacementv1alpha1.ClusterClaim {
		latest := &kfplacementv1alpha1.ClusterClaim{}
		Expect(hubUncachedClient.Get(ctx, client.ObjectKeyFromObject(claim), latest)).To(Succeed())
		return latest
	}
	// setCondition writes the claim status as the given identity, setting one condition and/or
	// removing one.
	setCondition := func(as client.Client, cond *metav1.Condition, remove string, opts ...client.SubResourceUpdateOption) error {
		latest := latestClaim()
		if cond != nil {
			meta.SetStatusCondition(&latest.Status.Conditions, *cond)
		}
		if remove != "" {
			meta.RemoveStatusCondition(&latest.Status.Conditions, remove)
		}
		return as.Status().Update(ctx, latest, opts...)
	}
	// expectPolicyDenied tells the policy's denial apart from an RBAC one: both are Forbidden, so the
	// message is what proves the policy ran.
	expectPolicyDenied := func(err error, what string) {
		GinkgoHelper()
		Expect(errors.IsForbidden(err)).To(BeTrue(), "%s: got %v", what, err)
		Expect(err.Error()).To(ContainSubstring(policyDeniedMessage), what)
	}
	approvedReasonIs := func(reason string) {
		GinkgoHelper()
		cond := meta.FindStatusCondition(latestClaim().Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeApproved)
		if reason == "" {
			Expect(cond).To(BeNil())
			return
		}
		Expect(cond).ToNot(BeNil())
		Expect(cond.Reason).To(Equal(reason))
	}

	BeforeAll(func() {
		By("Granting every identity the RBAC to write claim status, and approvers the approve verb")
		subject := func(kind, name string) rbacv1.Subject {
			return rbacv1.Subject{Kind: kind, APIGroup: rbacv1.GroupName, Name: name}
		}
		roles := []struct {
			name     string
			verbs    []string
			subjects []rbacv1.Subject
		}{
			{
				name:  claimWriterRole,
				verbs: []string{"get", "update", "patch"},
				subjects: []rbacv1.Subject{
					subject(rbacv1.UserKind, approverUsername), subject(rbacv1.GroupKind, approverUserGroup),
					subject(rbacv1.UserKind, hubAgentUsername), subject(rbacv1.UserKind, tenantUsername),
					subject(rbacv1.UserKind, fulfillerUsername),
				},
			},
			{
				name:     claimApproverRole,
				verbs:    []string{approveVerb},
				subjects: []rbacv1.Subject{subject(rbacv1.UserKind, approverUsername), subject(rbacv1.GroupKind, approverUserGroup), subject(rbacv1.UserKind, hubAgentUsername)},
			},
		}
		for _, r := range roles {
			role := &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{Name: r.name},
				Rules: []rbacv1.PolicyRule{{
					APIGroups: []string{kfplacementv1alpha1.GroupVersion.Group},
					Resources: []string{"clusterclaims", "clusterclaims/status"},
					Verbs:     r.verbs,
				}},
			}
			Expect(hubUncachedClient.Create(ctx, role)).To(Succeed())
			binding := &rbacv1.ClusterRoleBinding{
				ObjectMeta: metav1.ObjectMeta{Name: r.name},
				RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: r.name},
				Subjects:   r.subjects,
			}
			Expect(hubUncachedClient.Create(ctx, binding)).To(Succeed())
			DeferCleanup(func() {
				Expect(hubUncachedClient.Delete(ctx, binding)).To(Succeed())
				Expect(hubUncachedClient.Delete(ctx, role)).To(Succeed())
			})
		}

		asApprover = impersonate(approverUsername)
		asGroupApprover = impersonate("someone-else", approverUserGroup)
		asHubAgent = impersonate(hubAgentUsername)
		asTenant = impersonate(tenantUsername)
		asFulfiller = impersonate(fulfillerUsername)

		claim = &kfplacementv1alpha1.ClusterClaim{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("claim-%s", utils.RandStr())},
			Spec: kfplacementv1alpha1.ClusterClaimSpec{
				PlacementPolicyRef: &kfplacementv1alpha1.ObjectReference{APIVersion: kfplacementv1alpha1.GroupVersion.String(), Kind: "PlacementPolicy", Name: "app", Namespace: "work"},
			},
		}
		Expect(hubUncachedClient.Create(ctx, claim)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(hubUncachedClient.Delete(ctx, claim))).To(Succeed()) })

		By("Waiting for the policy and the RBAC to take effect, without persisting anything")
		Eventually(func() error {
			return setCondition(asTenant, approved(metav1.ConditionTrue, "Probe"), "", client.DryRunAll)
		}, eventuallyDuration, eventuallyInterval).Should(MatchError(ContainSubstring(policyDeniedMessage)), "the policy should deny a tenant approval once bound")
		Eventually(func() error {
			return setCondition(asApprover, approved(metav1.ConditionTrue, "Probe"), "", client.DryRunAll)
		}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "the approve grant should be live before the specs rely on it")
		approvedReasonIs("")
	})

	It("lets a provider accept and complete the claim without touching Approved", func() {
		Expect(setCondition(asFulfiller, &metav1.Condition{Type: kfplacementv1alpha1.ClusterClaimCondTypeAccepted, Status: metav1.ConditionTrue, Reason: kfplacementv1alpha1.ClusterClaimAcceptedCondReasonAccepted}, "")).To(Succeed())
		approvedReasonIs("")
	})

	It("denies a tenant setting Approved, by update and by patch", func() {
		expectPolicyDenied(setCondition(asTenant, approved(metav1.ConditionTrue, kfplacementv1alpha1.ClusterClaimApprovedCondReasonApproved), ""), "update")

		latest := latestClaim()
		patched := latest.DeepCopy()
		meta.SetStatusCondition(&patched.Status.Conditions, *approved(metav1.ConditionTrue, kfplacementv1alpha1.ClusterClaimApprovedCondReasonApproved))
		expectPolicyDenied(asTenant.Status().Patch(ctx, patched, client.MergeFrom(latest)), "patch")
		approvedReasonIs("")
	})

	It("ignores Approved on a write to the main resource, which cannot reach the status", func() {
		latest := latestClaim()
		meta.SetStatusCondition(&latest.Status.Conditions, *approved(metav1.ConditionTrue, kfplacementv1alpha1.ClusterClaimApprovedCondReasonApproved))
		Expect(asTenant.Update(ctx, latest)).To(Succeed(), "the policy matches the status subresource only; the main resource drops the status")
		approvedReasonIs("")
	})

	It("lets the hub agent stamp an automatic approval", func() {
		Expect(setCondition(asHubAgent, approved(metav1.ConditionTrue, kfplacementv1alpha1.ClusterClaimApprovedCondReasonAutomaticallyApproved), "")).To(Succeed())
		approvedReasonIs(kfplacementv1alpha1.ClusterClaimApprovedCondReasonAutomaticallyApproved)
	})

	It("denies a tenant changing or removing an existing Approved entry, but not other writes", func() {
		expectPolicyDenied(setCondition(asTenant, approved(metav1.ConditionFalse, kfplacementv1alpha1.ClusterClaimApprovedCondReasonDenied), ""), "changing")
		expectPolicyDenied(setCondition(asTenant, nil, kfplacementv1alpha1.ClusterClaimCondTypeApproved), "removing")
		approvedReasonIs(kfplacementv1alpha1.ClusterClaimApprovedCondReasonAutomaticallyApproved)

		Expect(setCondition(asTenant, &metav1.Condition{Type: kfplacementv1alpha1.ClusterClaimCondTypeCompleted, Status: metav1.ConditionFalse, Reason: kfplacementv1alpha1.ClusterClaimCompletedCondReasonFailed}, "")).To(Succeed(), "a write that leaves Approved as it found it passes")
	})

	It("lets an approver, by name or by group, overrule the approval", func() {
		Expect(setCondition(asApprover, approved(metav1.ConditionFalse, kfplacementv1alpha1.ClusterClaimApprovedCondReasonDenied), "")).To(Succeed())
		approvedReasonIs(kfplacementv1alpha1.ClusterClaimApprovedCondReasonDenied)
		Expect(setCondition(asGroupApprover, approved(metav1.ConditionTrue, kfplacementv1alpha1.ClusterClaimApprovedCondReasonApproved), "")).To(Succeed())
		approvedReasonIs(kfplacementv1alpha1.ClusterClaimApprovedCondReasonApproved)
		Expect(setCondition(asApprover, nil, kfplacementv1alpha1.ClusterClaimCondTypeApproved)).To(Succeed())
		approvedReasonIs("")
	})

	It("garbage-collects the policy when the generator is no longer configured", func() {
		// The manager is started once per hub agent run; a later run without the generator must
		// take the policy down with it, since nothing else owns it. The suite's own manager is
		// restarted afterwards so that the other container finds the policy it expects, whichever
		// order the containers run in.
		DeferCleanup(func() {
			manager, err := New(hubUncachedClient, suiteConfigs())
			Expect(err).ToNot(HaveOccurred())
			Expect(manager.Start(ctx)).To(Succeed())
		})
		manager, err := New(hubUncachedClient, DefaultPolicyGeneratorConfigs)
		Expect(err).ToNot(HaveOccurred())
		Expect(manager.Start(ctx)).To(Succeed())
		Eventually(func() bool {
			policyErr := hubUncachedClient.Get(ctx, client.ObjectKey{Name: clusterClaimApprovalVAPPolicyName}, &admissionregistrationv1.ValidatingAdmissionPolicy{})
			bindingErr := hubUncachedClient.Get(ctx, client.ObjectKey{Name: clusterClaimApprovalVAPPolicyBindingName}, &admissionregistrationv1.ValidatingAdmissionPolicyBinding{})
			return errors.IsNotFound(policyErr) && errors.IsNotFound(bindingErr)
		}, eventuallyDuration, eventuallyInterval).Should(BeTrue())
		Eventually(func() error {
			return setCondition(asTenant, approved(metav1.ConditionTrue, "Unguarded"), "", client.DryRunAll)
		}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "with the policy gone, RBAC alone decides")
	})
})
