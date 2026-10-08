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

package e2e

import (
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/propertyprovider"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/naming"
	"github.com/kubefleet-dev/kubefleet/pkg/v1/fulfiller"
	"github.com/kubefleet-dev/kubefleet/pkg/v1/fulfiller/reference"
	e2eframework "github.com/kubefleet-dev/kubefleet/test/e2e/framework"
)

const (
	claimFulfillmentNS = "claim-fulfillment"
	claimRegionLabel   = "topology.kubernetes.io/region"
	// claimApprover is a user that holds the approve verb on cluster claims; claimTenant does not.
	claimApprover = "claim-approver"
	claimTenant   = "claim-tenant"
)

var (
	// referenceProvider is the reference cluster provider the suite runs in-process against the
	// hub, through the fulfiller framework, in place of a provider deployment: it registers a
	// MemberCluster for each claim and simulates the member agent's join on the
	// InternalMemberCluster, the way a provider would on a hub with a hub agent.
	referenceProvider *reference.Provisioner
)

// startReferenceFulfiller runs the framework and the reference provider in this process.
func startReferenceFulfiller() {
	restConfig, err := e2eframework.GetClientConfig(hubCluster).ClientConfig()
	Expect(err).Should(Succeed(), "Failed to build the hub rest config for the reference fulfiller")
	mgr, err := ctrl.NewManager(rest.CopyConfig(restConfig), ctrl.Options{
		Scheme:  scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	Expect(err).Should(Succeed(), "Failed to create the manager for the reference fulfiller")
	referenceProvider = reference.New(mgr.GetClient(), reference.Options{
		SimulateJoin:      true,
		JoinTarget:        reference.JoinInternalMemberCluster,
		HeartbeatInterval: 15 * time.Second,
	})
	Expect(mgr.Add(referenceProvider)).Should(Succeed())
	Expect(fulfiller.New(mgr.GetClient(), fulfiller.Options{
		ProvisionerName: reference.ProvisionerName,
		Provisioner:     referenceProvider,
		APIReader:       mgr.GetAPIReader(),
		PollInterval:    time.Second,
	}).SetupWithManager(mgr)).Should(Succeed())
	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(ctx)).Should(Succeed(), "The reference fulfiller stopped with an error")
	}()
	Expect(mgr.GetCache().WaitForCacheSync(ctx)).Should(BeTrue())
}

var _ = Describe("cluster claim fulfillment", Label("custom"), Ordered, Serial, func() {
	var counter int
	nextName := func(prefix string) string {
		counter++
		return fmt.Sprintf("%s-%d", prefix, counter)
	}
	createClass := func(mutate func(*kfplacementv1alpha1.ClusterProviderClassSpec)) *kfplacementv1alpha1.ClusterProviderClass {
		class := &kfplacementv1alpha1.ClusterProviderClass{
			ObjectMeta: metav1.ObjectMeta{Name: nextName("ref")},
			Spec: kfplacementv1alpha1.ClusterProviderClassSpec{
				ProvisionerName:    reference.ProvisionerName,
				Approval:           kfplacementv1alpha1.ClusterClaimApprovalModeAutomatic,
				Parameters:         map[string]string{reference.IdentityParameter: memberCluster1EastProdSAName},
				SelectorVocabulary: &kfplacementv1alpha1.SelectorVocabulary{LabelKeys: []kfplacementv1alpha1.LabelKeyRule{{Key: claimRegionLabel}}},
			},
		}
		if mutate != nil {
			mutate(&class.Spec)
		}
		Expect(hubClient.Create(ctx, class)).Should(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(hubClient.Delete(ctx, class))).Should(Succeed()) })
		return class
	}
	regionSelector := func(region string, count int32) kfplacementv1alpha1.ClusterSelector {
		return kfplacementv1alpha1.ClusterSelector{
			Terms:           []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{{MatchLabels: map[string]string{claimRegionLabel: region}}},
			Count:           ptr.To(intstr.FromInt32(count)),
			WhenUnfulfilled: kfplacementv1alpha1.WhenUnfulfilledOptionAddClusterClaim,
		}
	}
	createPolicy := func(class *kfplacementv1alpha1.ClusterProviderClass, selectors ...kfplacementv1alpha1.ClusterSelector) *kfplacementv1alpha1.PlacementPolicy {
		policy := &kfplacementv1alpha1.PlacementPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: nextName("pp"), Namespace: claimFulfillmentNS},
			Spec: kfplacementv1alpha1.PlacementPolicySpec{
				ResourceSelectors:        []kfplacementv1alpha1.ResourceSelector{{APIVersion: "v1", Kind: "ConfigMap", Name: "app"}},
				ClusterSelectors:         selectors,
				ClusterProviderClassName: ptr.To(class.Name),
			},
		}
		Expect(hubClient.Create(ctx, policy)).Should(Succeed())
		return policy
	}
	claimsOf := func(policy *kfplacementv1alpha1.PlacementPolicy) []kfplacementv1alpha1.ClusterClaim {
		claims := &kfplacementv1alpha1.ClusterClaimList{}
		Expect(hubClient.List(ctx, claims, client.MatchingLabels{kfplacementv1alpha1.ClusterClaimPlacementPolicyNameLabel: naming.LabelValue(policy.Name)})).Should(Succeed())
		return claims.Items
	}
	theClaim := func(policy *kfplacementv1alpha1.PlacementPolicy) func(Gomega) *kfplacementv1alpha1.ClusterClaim {
		return func(g Gomega) *kfplacementv1alpha1.ClusterClaim {
			claims := claimsOf(policy)
			g.Expect(claims).Should(HaveLen(1))
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
			g.Expect(hubClient.List(ctx, bindings, client.InNamespace(claimFulfillmentNS))).Should(Succeed())
			var clusters []string
			for i := range bindings.Items {
				if metav1.IsControlledBy(&bindings.Items[i], policy) {
					clusters = append(clusters, bindings.Items[i].Spec.ClusterName)
				}
			}
			return clusters
		}
	}
	provisionedClusters := func(g Gomega) []clusterv1beta1.MemberCluster {
		clusters := &clusterv1beta1.MemberClusterList{}
		g.Expect(hubClient.List(ctx, clusters, client.HasLabels{kfplacementv1alpha1.FulfilledClaimUIDLabel})).Should(Succeed())
		return clusters.Items
	}
	completedFor := func(policy *kfplacementv1alpha1.PlacementPolicy) (*kfplacementv1alpha1.ClusterClaim, string) {
		var claim *kfplacementv1alpha1.ClusterClaim
		Eventually(func(g Gomega) {
			claim = theClaim(policy)(g)
			g.Expect(conditionIs(claim, kfplacementv1alpha1.ClusterClaimCondTypeCompleted, metav1.ConditionTrue, kfplacementv1alpha1.ClusterClaimCompletedCondReasonFulfilled)).Should(BeTrue(), "conditions: %v", claim.Status.Conditions)
			g.Expect(claim.Status.ProvisionedClusterName).NotTo(BeNil())
		}, longEventuallyDuration, eventuallyInterval).Should(Succeed())
		return claim, *claim.Status.ProvisionedClusterName
	}
	heldAs := func(policy *kfplacementv1alpha1.PlacementPolicy, uid types.UID, condType string, status metav1.ConditionStatus, reason string) {
		GinkgoHelper()
		Eventually(func(g Gomega) {
			claim := theClaim(policy)(g)
			g.Expect(claim.UID).Should(Equal(uid))
			g.Expect(conditionIs(claim, condType, status, reason)).Should(BeTrue(), "conditions: %v", claim.Status.Conditions)
		}, longEventuallyDuration, eventuallyInterval).Should(Succeed())
		Consistently(func(g Gomega) { g.Expect(theClaim(policy)(g).UID).Should(Equal(uid)) }, consistentlyDuration, eventuallyInterval).Should(Succeed())
	}
	// asUser impersonates a hub user who may write claim status; whether the user may approve is
	// RBAC's and the admission policy's to decide.
	asUser := func(name string) client.Client {
		cfg, err := e2eframework.GetClientConfig(hubCluster).ClientConfig()
		Expect(err).Should(Succeed())
		cfg.Impersonate = rest.ImpersonationConfig{UserName: name}
		c, err := client.New(cfg, client.Options{Scheme: scheme})
		Expect(err).Should(Succeed())
		return c
	}

	BeforeAll(func() {
		Expect(client.IgnoreAlreadyExists(hubClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: claimFulfillmentNS}}))).Should(Succeed())
		// The policies select this ConfigMap; without it the resource snapshot, and so every
		// binding, fails.
		Expect(client.IgnoreAlreadyExists(hubClient.Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: claimFulfillmentNS}}))).Should(Succeed())
		By("granting the approver and the tenant the RBAC to write claim status, and only the approver the approve verb")
		roles := []struct {
			name     string
			verbs    []string
			subjects []string
		}{
			{name: "e2e-clusterclaim-status-writer", verbs: []string{"get", "list", "update", "patch"}, subjects: []string{claimApprover, claimTenant}},
			{name: "e2e-clusterclaim-approver", verbs: []string{"approve"}, subjects: []string{claimApprover}},
		}
		for _, r := range roles {
			role := &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{Name: r.name},
				Rules:      []rbacv1.PolicyRule{{APIGroups: []string{kfplacementv1alpha1.GroupVersion.Group}, Resources: []string{"clusterclaims", "clusterclaims/status"}, Verbs: r.verbs}},
			}
			Expect(client.IgnoreAlreadyExists(hubClient.Create(ctx, role))).Should(Succeed())
			binding := &rbacv1.ClusterRoleBinding{
				ObjectMeta: metav1.ObjectMeta{Name: r.name},
				RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: r.name},
			}
			for _, subject := range r.subjects {
				binding.Subjects = append(binding.Subjects, rbacv1.Subject{Kind: rbacv1.UserKind, APIGroup: rbacv1.GroupName, Name: subject})
			}
			Expect(client.IgnoreAlreadyExists(hubClient.Create(ctx, binding))).Should(Succeed())
			DeferCleanup(func() {
				Expect(client.IgnoreNotFound(hubClient.Delete(ctx, binding))).Should(Succeed())
				Expect(client.IgnoreNotFound(hubClient.Delete(ctx, role))).Should(Succeed())
			})
		}
	})

	// dumpState attaches the policies, claims, provisioned clusters, and bindings to the report of a
	// failed spec, since the hub agent's leader log is not among the artifacts CI collects.
	dumpState := func() {
		if !CurrentSpecReport().Failed() {
			return
		}
		for _, list := range []client.ObjectList{
			&kfplacementv1alpha1.PlacementPolicyList{}, &kfplacementv1alpha1.ClusterClaimList{},
			&kfplacementv1alpha1.PlacementBindingList{}, &clusterv1beta1.MemberClusterList{},
			&kfplacementv1alpha1.ClusterProviderClassList{},
		} {
			if err := hubClient.List(ctx, list); err != nil {
				AddReportEntry(fmt.Sprintf("%T", list), err.Error())
				continue
			}
			raw, _ := json.MarshalIndent(list, "", "  ")
			AddReportEntry(fmt.Sprintf("%T", list), string(raw))
		}
	}

	AfterEach(func() {
		dumpState()
		referenceProvider.SetBehaviour(reference.Fulfill)
		policies := &kfplacementv1alpha1.PlacementPolicyList{}
		Expect(hubClient.List(ctx, policies, client.InNamespace(claimFulfillmentNS))).Should(Succeed())
		for i := range policies.Items {
			Expect(client.IgnoreNotFound(hubClient.Delete(ctx, &policies.Items[i]))).Should(Succeed())
		}
		Eventually(func(g Gomega) {
			g.Expect(hubClient.List(ctx, policies, client.InNamespace(claimFulfillmentNS))).Should(Succeed())
			g.Expect(policies.Items).Should(BeEmpty())
			claims := &kfplacementv1alpha1.ClusterClaimList{}
			g.Expect(hubClient.List(ctx, claims)).Should(Succeed())
			g.Expect(claims.Items).Should(BeEmpty())
		}, longEventuallyDuration, eventuallyInterval).Should(Succeed())
		// Provisioned clusters are the test's to remove; the suite's real members stay.
		for _, mc := range provisionedClusters(Default) {
			ensureMemberClusterAndRelatedResourcesDeletion(mc.Name)
		}
	})

	It("claims, provisions, joins, and binds two clusters for a count-2 selector with automatic approval", func() {
		class := createClass(nil)
		policy := createPolicy(class, regionSelector("antarctica", 2))
		Eventually(func(g Gomega) {
			g.Expect(bindingsOf(policy)(g)).Should(HaveLen(2))
			g.Expect(claimsOf(policy)).Should(BeEmpty())
			clusters := provisionedClusters(g)
			g.Expect(clusters).Should(HaveLen(2))
			for _, mc := range clusters {
				g.Expect(mc.Labels).Should(HaveKeyWithValue(claimRegionLabel, "antarctica"))
				g.Expect(meta.IsStatusConditionTrue(mc.Status.Conditions, string(clusterv1beta1.ConditionTypeMemberClusterJoined))).Should(BeTrue(), "the hub copied the simulated join: %v", mc.Status.Conditions)
			}
		}, longEventuallyDuration, eventuallyInterval).Should(Succeed())
	})

	It("gates manual approval on the approve verb: a tenant is refused, an approver lets the claim through", func() {
		class := createClass(func(spec *kfplacementv1alpha1.ClusterProviderClassSpec) {
			spec.Approval = kfplacementv1alpha1.ClusterClaimApprovalModeManual
		})
		policy := createPolicy(class, regionSelector("antarctica", 1))
		var claim *kfplacementv1alpha1.ClusterClaim
		Eventually(func(g Gomega) { claim = theClaim(policy)(g) }, longEventuallyDuration, eventuallyInterval).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(meta.FindStatusCondition(theClaim(policy)(g).Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeAccepted)).Should(BeNil(), "nothing happens before approval")
		}, consistentlyDuration, eventuallyInterval).Should(Succeed())

		approve := func(as client.Client) error {
			latest := &kfplacementv1alpha1.ClusterClaim{}
			if err := hubClient.Get(ctx, client.ObjectKeyFromObject(claim), latest); err != nil {
				return err
			}
			meta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{Type: kfplacementv1alpha1.ClusterClaimCondTypeApproved, Status: metav1.ConditionTrue, Reason: kfplacementv1alpha1.ClusterClaimApprovedCondReasonApproved})
			return as.Status().Update(ctx, latest)
		}
		Eventually(func(g Gomega) {
			err := approve(asUser(claimTenant))
			g.Expect(apierrors.IsForbidden(err)).Should(BeTrue(), "a tenant without the approve verb is refused: %v", err)
			g.Expect(err.Error()).Should(ContainSubstring("requires the approve verb"))
		}, longEventuallyDuration, eventuallyInterval).Should(Succeed())
		Eventually(func() error { return approve(asUser(claimApprover)) }, longEventuallyDuration, eventuallyInterval).Should(Succeed())
		Eventually(bindingsOf(policy), longEventuallyDuration, eventuallyInterval).Should(HaveLen(1))
	})

	It("retries a permanently failed claim after retryAfter when the class says so", func() {
		class := createClass(func(spec *kfplacementv1alpha1.ClusterProviderClassSpec) {
			spec.OnFailure = kfplacementv1alpha1.ClusterClaimFailureActionRetry
			spec.RetryAfter = &metav1.Duration{Duration: 5 * time.Second}
		})
		referenceProvider.SetBehaviour(reference.FailPermanent)
		policy := createPolicy(class, regionSelector("antarctica", 1))
		var failedUID types.UID
		Eventually(func(g Gomega) {
			claim := theClaim(policy)(g)
			g.Expect(conditionIs(claim, kfplacementv1alpha1.ClusterClaimCondTypeCompleted, metav1.ConditionFalse, kfplacementv1alpha1.ClusterClaimCompletedCondReasonFailed)).Should(BeTrue(), "conditions: %v", claim.Status.Conditions)
			failedUID = claim.UID
		}, longEventuallyDuration, eventuallyInterval).Should(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(theClaim(policy)(g).UID).ShouldNot(Equal(failedUID), "the record is withdrawn and a fresh claim issued")
		}, longEventuallyDuration, eventuallyInterval).Should(Succeed())
		referenceProvider.SetBehaviour(reference.Fulfill)
		Eventually(bindingsOf(policy), longEventuallyDuration, eventuallyInterval).Should(HaveLen(1))
	})

	It("expires a cluster that never joins as JoinTimeout, deprovisions it, and holds the record", func() {
		class := createClass(func(spec *kfplacementv1alpha1.ClusterProviderClassSpec) {
			spec.JoinTimeout = &metav1.Duration{Duration: 10 * time.Second}
		})
		referenceProvider.SetBehaviour(reference.NeverJoin)
		policy := createPolicy(class, regionSelector("antarctica", 1))
		claim, name := completedFor(policy)
		heldAs(policy, claim.UID, kfplacementv1alpha1.ClusterClaimCondTypeExpired, metav1.ConditionTrue, kfplacementv1alpha1.ClusterClaimExpiredCondReasonJoinTimeout)
		// The provider deletes the MemberCluster at once, but the hub lets a member whose agent
		// never reported in go only after forceDeleteWaitTime (1m in setup.sh), which this wait
		// has to cover.
		Eventually(func(g Gomega) {
			g.Expect(apierrors.IsNotFound(hubClient.Get(ctx, types.NamespacedName{Name: name}, &clusterv1beta1.MemberCluster{}))).Should(BeTrue(), "a cluster that never joined is deprovisioned")
		}, longEventuallyDuration, eventuallyInterval).Should(Succeed())
	})

	It("expires a joined cluster the selector does not count as NotMatching and keeps it", func() {
		class := createClass(func(spec *kfplacementv1alpha1.ClusterProviderClassSpec) {
			// Long enough for the simulated join to reach the MemberCluster through the hub's
			// InternalMemberCluster copy, so the verdict is NotMatching rather than JoinTimeout.
			spec.JoinTimeout = &metav1.Duration{Duration: 30 * time.Second}
			spec.SelectorVocabulary.PropertyKeys = []kfplacementv1alpha1.PropertyKeyRule{{Key: propertyprovider.NodeCountProperty}}
		})
		// The provider can carry a label, but cannot deliver the node count the selector asks for.
		selector := regionSelector("antarctica", 1)
		selector.Terms[0].MatchClusterPropertyExpressions = []kfplacementv1alpha1.LabelClusterPropertyExpression{{
			Key: propertyprovider.NodeCountProperty, Operator: kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorGe, Values: []string{"1000"},
		}}
		policy := createPolicy(class, selector)
		claim, name := completedFor(policy)
		heldAs(policy, claim.UID, kfplacementv1alpha1.ClusterClaimCondTypeExpired, metav1.ConditionTrue, kfplacementv1alpha1.ClusterClaimExpiredCondReasonNotMatching)
		Consistently(func(g Gomega) {
			g.Expect(hubClient.Get(ctx, types.NamespacedName{Name: name}, &clusterv1beta1.MemberCluster{})).Should(Succeed(), "a NotMatching cluster is a member in its own right")
		}, consistentlyDuration, eventuallyInterval).Should(Succeed())
	})
})
