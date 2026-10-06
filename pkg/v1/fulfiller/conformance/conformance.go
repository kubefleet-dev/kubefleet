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

// Package conformance runs a cluster provider through the fulfillment contract. A provider's own
// tests call Run with a Subject; the suite starts an envtest API server with the KubeFleet CRDs,
// runs the fulfiller framework over the provider, plays the policy controller and the approver
// by hand, and asserts what the contract promises the policy controller and the fleet: a claim
// is never touched before approval, a fulfilled claim names a registered cluster under the
// derived name and labels, a failure is terminal, a provider never deprovisions a cluster that
// joined the fleet, and a re-issued claim gets a new cluster rather than the previous one. The
// framework's own tests prove the framework; this suite proves the pair.
//
// Some cases are the provider's to pass or fail -- fulfilling, failing permanently, naming,
// idempotent provisioning and deprovisioning, deprovisioning a never-joined cluster -- and the
// rest are the framework's gates, which a provider can only break by acting on its own
// initiative: ignoring an unapproved or stale claim, writing only its own fields, and keeping a
// cluster that joined or that expired as NotMatching. Three framework cases are left to the
// framework's own tests, since no provider can influence them: a join observed with a stale
// ObservedGeneration, a provider down at the moment of joining, and a withdrawal held past
// MaxWithdrawalHold.
package conformance

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onsi/gomega"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2/textlogger"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/v1/fulfiller"
)

// Behaviour is what the suite asks the provider to do with the next Provision call.
type Behaviour string

const (
	// Fulfill registers the cluster.
	Fulfill Behaviour = "Fulfill"
	// FailPermanent fails at once.
	FailPermanent Behaviour = "FailPermanent"
)

// Subject is the provider under test.
type Subject struct {
	// ProvisionerName is what the suite's classes name.
	ProvisionerName string
	// New builds the provider against the manager the suite runs; a provider that needs to run
	// something beside the framework, such as a heartbeat loop, adds it to the manager here.
	New func(mgr ctrl.Manager) (fulfiller.Provisioner, error)
	// Behave switches the provider's behaviour. The suite calls it before every case.
	Behave func(Behaviour)
	// Parameters are the class parameters the provider needs, such as an identity.
	Parameters map[string]string
	// CRDDirectoryPaths locate the KubeFleet CRDs for envtest.
	CRDDirectoryPaths []string
}

const (
	eventuallyTimeout    = 30 * time.Second
	consistentlyDuration = 3 * time.Second
	pollInterval         = 250 * time.Millisecond
	pollIntervalProvider = 250 * time.Millisecond
)

// Run runs the conformance suite against the subject as subtests of t.
func Run(t *testing.T, subject Subject) {
	t.Helper()
	s := start(t, subject)
	defer s.stop()

	t.Run("ignores a claim that is not approved", s.ignoresUnapproved)
	t.Run("fulfills an approved claim and leaves the freshness marker alone", s.fulfillsApproved)
	t.Run("waits on a stale claim until the marker advances, never failing it", s.waitsOnStaleClaim)
	t.Run("writes the fulfiller-owned fields only", s.writesOwnedFieldsOnly)
	t.Run("fails permanently and stays failed", s.failsPermanently)
	t.Run("deprovisions a never-joined cluster on a join timeout", s.deprovisionsNeverJoinedOnJoinTimeout)
	t.Run("keeps a joined cluster on a join timeout, eligible or dark", s.keepsJoinedOnJoinTimeout)
	t.Run("keeps a not-matching cluster", s.keepsNotMatching)
	t.Run("keeps a joined cluster on withdrawal", s.keepsJoinedOnWithdrawal)
	t.Run("deprovisions a never-joined cluster on withdrawal", s.deprovisionsNeverJoinedOnWithdrawal)
	t.Run("provisions a new cluster for a re-issued claim when the previous one is eligible", s.provisionsAnewWhenPreviousEligible)
	t.Run("provisions and deprovisions idempotently, as a restart requires", s.provisionsIdempotently)
	t.Run("derives a valid cluster name from a long claim name", s.derivesValidName)
}

type suite struct {
	t           *testing.T
	subject     Subject
	provisioner fulfiller.Provisioner
	ctx         context.Context
	cancel      context.CancelFunc
	env         *envtest.Environment
	c           client.Client
	class       *kfplacementv1alpha1.ClusterProviderClass
	counter     int
}

func start(t *testing.T, subject Subject) *suite {
	t.Helper()
	g := gomega.NewWithT(t)
	scheme := runtime.NewScheme()
	g.Expect(clientgoscheme.AddToScheme(scheme)).To(gomega.Succeed())
	g.Expect(kfplacementv1alpha1.AddToScheme(scheme)).To(gomega.Succeed())
	g.Expect(clusterv1beta1.AddToScheme(scheme)).To(gomega.Succeed())

	s := &suite{t: t, subject: subject}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.env = &envtest.Environment{CRDDirectoryPaths: subject.CRDDirectoryPaths, ErrorIfCRDPathMissing: true}
	cfg, err := s.env.Start()
	g.Expect(err).NotTo(gomega.HaveOccurred())
	s.c, err = client.New(cfg, client.Options{Scheme: scheme})
	g.Expect(err).NotTo(gomega.HaveOccurred())

	logger := textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(2)))
	ctrl.SetLogger(logger)
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme,
		Metrics: server.Options{BindAddress: "0"},
		Logger:  logger,
		// Controller names are registered process-wide; a provider's own suite may already have
		// run the framework under the same name in this process.
		Controller: config.Controller{SkipNameValidation: ptr.To(true)},
	})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	provisioner, err := subject.New(mgr)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	s.provisioner = provisioner
	framework := fulfiller.New(mgr.GetClient(), fulfiller.Options{
		ProvisionerName: subject.ProvisionerName,
		Provisioner:     provisioner,
		APIReader:       mgr.GetAPIReader(),
		PollInterval:    pollIntervalProvider,
	})
	g.Expect(framework.SetupWithManager(mgr)).To(gomega.Succeed())
	go func() {
		if err := mgr.Start(s.ctx); err != nil {
			t.Errorf("the manager stopped with an error: %v", err)
		}
	}()
	g.Expect(mgr.GetCache().WaitForCacheSync(s.ctx)).To(gomega.BeTrue())

	s.class = &kfplacementv1alpha1.ClusterProviderClass{
		ObjectMeta: metav1.ObjectMeta{Name: "conformance"},
		Spec:       kfplacementv1alpha1.ClusterProviderClassSpec{ProvisionerName: subject.ProvisionerName, Parameters: subject.Parameters},
	}
	g.Expect(s.c.Create(s.ctx, s.class)).To(gomega.Succeed())
	return s
}

func (s *suite) stop() {
	s.cancel()
	if err := s.env.Stop(); err != nil {
		s.t.Errorf("stopping envtest: %v", err)
	}
}

func (s *suite) nextName(prefix string) string {
	s.counter++
	return fmt.Sprintf("%s-%d", prefix, s.counter)
}

// newClaim creates a claim of the suite's class, as the policy controller would, and registers
// its removal together with every cluster registered under its name.
func (s *suite) newClaim(t *testing.T, name string, mutate func(*kfplacementv1alpha1.ClusterClaim)) *kfplacementv1alpha1.ClusterClaim {
	t.Helper()
	g := gomega.NewWithT(t)
	s.subject.Behave(Fulfill)
	claim := &kfplacementv1alpha1.ClusterClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: kfplacementv1alpha1.ClusterClaimSpec{
			PlacementPolicyRef:       &kfplacementv1alpha1.ObjectReference{Name: "app", Namespace: "work", APIVersion: kfplacementv1alpha1.GroupVersion.String(), Kind: kfplacementv1alpha1.PlacementPolicyKind},
			ClusterSelectorTerms:     []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{{MatchLabels: map[string]string{"topology.kubernetes.io/region": "eastus"}}},
			ClusterProviderClassName: s.class.Name,
		},
	}
	if mutate != nil {
		mutate(claim)
	}
	g.Expect(s.c.Create(s.ctx, claim)).To(gomega.Succeed())
	t.Cleanup(func() {
		g := gomega.NewWithT(t)
		g.Expect(client.IgnoreNotFound(s.c.Delete(s.ctx, claim))).To(gomega.Succeed())
		g.Eventually(func() bool { return s.gone(claim) }, eventuallyTimeout, pollInterval).Should(gomega.BeTrue(), "the claim should be released and gone")
		nameLabel := fulfiller.Request{ClaimName: claim.Name, ClaimUID: claim.UID}.OwnershipLabels()[kfplacementv1alpha1.FulfilledClaimNameLabel]
		g.Expect(s.c.DeleteAllOf(s.ctx, &clusterv1beta1.MemberCluster{}, client.MatchingLabels{kfplacementv1alpha1.FulfilledClaimNameLabel: nameLabel})).To(gomega.Succeed())
		g.Eventually(func() int {
			clusters := &clusterv1beta1.MemberClusterList{}
			if err := s.c.List(s.ctx, clusters, client.MatchingLabels{kfplacementv1alpha1.FulfilledClaimNameLabel: nameLabel}); err != nil {
				return -1
			}
			return len(clusters.Items)
		}, eventuallyTimeout, pollInterval).Should(gomega.BeZero())
	})
	return claim
}

func (s *suite) gone(obj client.Object) bool {
	return apierrors.IsNotFound(s.c.Get(s.ctx, client.ObjectKeyFromObject(obj), obj.DeepCopyObject().(client.Object)))
}

func (s *suite) setCondition(t *testing.T, claim *kfplacementv1alpha1.ClusterClaim, cond metav1.Condition) {
	t.Helper()
	gomega.NewWithT(t).Eventually(func(g gomega.Gomega) {
		g.Expect(s.c.Get(s.ctx, client.ObjectKeyFromObject(claim), claim)).To(gomega.Succeed())
		meta.SetStatusCondition(&claim.Status.Conditions, cond)
		g.Expect(s.c.Status().Update(s.ctx, claim)).To(gomega.Succeed())
	}, eventuallyTimeout, pollInterval).Should(gomega.Succeed())
}

func (s *suite) approve(t *testing.T, claim *kfplacementv1alpha1.ClusterClaim) {
	t.Helper()
	s.setCondition(t, claim, metav1.Condition{Type: kfplacementv1alpha1.ClusterClaimCondTypeApproved, Status: metav1.ConditionTrue, Reason: kfplacementv1alpha1.ClusterClaimApprovedCondReasonApproved})
}

func (s *suite) expire(t *testing.T, claim *kfplacementv1alpha1.ClusterClaim, reason string) {
	t.Helper()
	s.setCondition(t, claim, metav1.Condition{Type: kfplacementv1alpha1.ClusterClaimCondTypeExpired, Status: metav1.ConditionTrue, Reason: reason})
}

// state is what the suite asserts on a claim.
type state struct {
	held, completed, failed bool
	clusterName             string
}

func (s *suite) stateOf(claim *kfplacementv1alpha1.ClusterClaim) func(gomega.Gomega) state {
	return func(g gomega.Gomega) state {
		g.Expect(s.c.Get(s.ctx, client.ObjectKeyFromObject(claim), claim)).To(gomega.Succeed())
		st := state{held: controllerutil.ContainsFinalizer(claim, kfplacementv1alpha1.FulfillerFinalizer)}
		if completed := meta.FindStatusCondition(claim.Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeCompleted); completed != nil {
			st.completed = completed.Status == metav1.ConditionTrue
			st.failed = completed.Status == metav1.ConditionFalse && completed.Reason == kfplacementv1alpha1.ClusterClaimCompletedCondReasonFailed
		}
		if claim.Status.ProvisionedClusterName != nil {
			st.clusterName = *claim.Status.ProvisionedClusterName
		}
		return st
	}
}

// fulfilled asserts the contract's success state: completed, naming the derived cluster, which is
// registered under the ownership labels, with the claim still held.
func (s *suite) fulfilled(claim *kfplacementv1alpha1.ClusterClaim) func(gomega.Gomega) {
	return func(g gomega.Gomega) {
		st := s.stateOf(claim)(g)
		g.Expect(st.completed).To(gomega.BeTrue(), "the claim should be completed; conditions: %v", claim.Status.Conditions)
		g.Expect(st.clusterName).To(gomega.Equal(fulfiller.ClusterNameFor(claim)), "a fulfilled claim names the derived cluster")
		g.Expect(st.held).To(gomega.BeTrue(), "a fulfilled claim stays held until withdrawn")
		cluster := &clusterv1beta1.MemberCluster{}
		g.Expect(s.c.Get(s.ctx, client.ObjectKey{Name: st.clusterName}, cluster)).To(gomega.Succeed(), "the named cluster is registered")
		for k, v := range (fulfiller.Request{ClaimName: claim.Name, ClaimUID: claim.UID}).OwnershipLabels() {
			g.Expect(cluster.Labels).To(gomega.HaveKeyWithValue(k, v), "the cluster carries the ownership labels")
		}
	}
}

func (s *suite) clusterExists(name string) func() bool {
	return func() bool {
		return !s.gone(&clusterv1beta1.MemberCluster{ObjectMeta: metav1.ObjectMeta{Name: name}})
	}
}

// markJoined stamps a member agent status as the member agent would, with a recent or a stale
// heartbeat; envtest has no hub agent to copy it from an InternalMemberCluster.
func (s *suite) markJoined(t *testing.T, name string, recent bool) {
	t.Helper()
	gomega.NewWithT(t).Eventually(func(g gomega.Gomega) {
		mc := &clusterv1beta1.MemberCluster{}
		g.Expect(s.c.Get(s.ctx, client.ObjectKey{Name: name}, mc)).To(gomega.Succeed())
		heartbeat := metav1.Now()
		if !recent {
			heartbeat = metav1.NewTime(time.Now().Add(-time.Hour))
		}
		mc.Status.AgentStatus = []clusterv1beta1.AgentStatus{{
			Type: clusterv1beta1.MemberAgent,
			Conditions: []metav1.Condition{
				{Type: string(clusterv1beta1.AgentJoined), Status: metav1.ConditionTrue, Reason: "Joined", Message: "conformance", LastTransitionTime: metav1.Now()},
				{Type: string(clusterv1beta1.AgentHealthy), Status: metav1.ConditionTrue, Reason: "Healthy", Message: "conformance", LastTransitionTime: metav1.Now()},
			},
			LastReceivedHeartbeat: heartbeat,
		}}
		g.Expect(s.c.Status().Update(s.ctx, mc)).To(gomega.Succeed())
	}, eventuallyTimeout, pollInterval).Should(gomega.Succeed())
}

func (s *suite) ignoresUnapproved(t *testing.T) {
	g := gomega.NewWithT(t)
	claim := s.newClaim(t, s.nextName("unapproved"), nil)
	g.Consistently(func(g gomega.Gomega) {
		st := s.stateOf(claim)(g)
		g.Expect(st.held).To(gomega.BeFalse(), "an unapproved claim is not accepted")
		g.Expect(meta.FindStatusCondition(claim.Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeAccepted)).To(gomega.BeNil())
		g.Expect(s.clusterExists(fulfiller.ClusterNameFor(claim))()).To(gomega.BeFalse(), "nothing is registered before approval")
	}, consistentlyDuration, pollInterval).Should(gomega.Succeed())
}

func (s *suite) fulfillsApproved(t *testing.T) {
	g := gomega.NewWithT(t)
	claim := s.newClaim(t, s.nextName("approved"), nil)
	s.approve(t, claim)
	g.Eventually(s.fulfilled(claim), eventuallyTimeout, pollInterval).Should(gomega.Succeed())
	g.Expect(claim.Status.LastObservedMostRecentClusterCreationTimestamp).To(gomega.BeNil(), "a nil marker is fresh and is not the provider's to set")
}

func (s *suite) waitsOnStaleClaim(t *testing.T) {
	g := gomega.NewWithT(t)
	// A cluster joined after the claim was last evaluated: the marker is older than it.
	existing := &clusterv1beta1.MemberCluster{
		ObjectMeta: metav1.ObjectMeta{Name: s.nextName("existing")},
		Spec:       clusterv1beta1.MemberClusterSpec{Identity: rbacv1.Subject{Kind: rbacv1.ServiceAccountKind, Name: "sa", Namespace: "fleet-system"}},
	}
	g.Expect(s.c.Create(s.ctx, existing)).To(gomega.Succeed())
	t.Cleanup(func() { _ = s.c.Delete(s.ctx, existing) })
	g.Expect(s.c.Get(s.ctx, client.ObjectKeyFromObject(existing), existing)).To(gomega.Succeed())
	stale := metav1.NewTime(existing.CreationTimestamp.Add(-time.Hour))

	claim := s.newClaim(t, s.nextName("stale"), nil)
	g.Eventually(func(g gomega.Gomega) {
		g.Expect(s.c.Get(s.ctx, client.ObjectKeyFromObject(claim), claim)).To(gomega.Succeed())
		claim.Status.LastObservedMostRecentClusterCreationTimestamp = &stale
		meta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{Type: kfplacementv1alpha1.ClusterClaimCondTypeApproved, Status: metav1.ConditionTrue, Reason: kfplacementv1alpha1.ClusterClaimApprovedCondReasonApproved})
		g.Expect(s.c.Status().Update(s.ctx, claim)).To(gomega.Succeed())
	}, eventuallyTimeout, pollInterval).Should(gomega.Succeed())
	g.Consistently(func(g gomega.Gomega) {
		st := s.stateOf(claim)(g)
		g.Expect(st.held).To(gomega.BeFalse(), "a stale claim is not started")
		g.Expect(st.failed).To(gomega.BeFalse(), "a stale claim is never failed")
	}, consistentlyDuration, pollInterval).Should(gomega.Succeed())

	fresh := metav1.NewTime(existing.CreationTimestamp.Add(time.Hour))
	g.Eventually(func(g gomega.Gomega) {
		g.Expect(s.c.Get(s.ctx, client.ObjectKeyFromObject(claim), claim)).To(gomega.Succeed())
		claim.Status.LastObservedMostRecentClusterCreationTimestamp = &fresh
		g.Expect(s.c.Status().Update(s.ctx, claim)).To(gomega.Succeed())
	}, eventuallyTimeout, pollInterval).Should(gomega.Succeed())
	g.Eventually(s.fulfilled(claim), eventuallyTimeout, pollInterval).Should(gomega.Succeed())
}

func (s *suite) writesOwnedFieldsOnly(t *testing.T) {
	g := gomega.NewWithT(t)
	claim := s.newClaim(t, s.nextName("owned"), func(c *kfplacementv1alpha1.ClusterClaim) {
		c.Labels = map[string]string{"placement.kubefleet.dev/placement-policy-name": "app"}
	})
	marker := metav1.Now()
	g.Eventually(func(g gomega.Gomega) {
		g.Expect(s.c.Get(s.ctx, client.ObjectKeyFromObject(claim), claim)).To(gomega.Succeed())
		claim.Status.LastObservedMostRecentClusterCreationTimestamp = &marker
		meta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{Type: kfplacementv1alpha1.ClusterClaimCondTypeApproved, Status: metav1.ConditionTrue, Reason: kfplacementv1alpha1.ClusterClaimApprovedCondReasonApproved, Message: "by the approver"})
		g.Expect(s.c.Status().Update(s.ctx, claim)).To(gomega.Succeed())
	}, eventuallyTimeout, pollInterval).Should(gomega.Succeed())
	spec := claim.Spec.DeepCopy()
	g.Eventually(s.fulfilled(claim), eventuallyTimeout, pollInterval).Should(gomega.Succeed())

	g.Expect(claim.Spec).To(gomega.Equal(*spec), "the spec is never written")
	g.Expect(claim.Labels).To(gomega.HaveKeyWithValue("placement.kubefleet.dev/placement-policy-name", "app"), "labels are never written")
	g.Expect(claim.Status.LastObservedMostRecentClusterCreationTimestamp.Time).To(gomega.BeTemporally("~", marker.Time, time.Second), "the marker is KubeFleet's")
	approved := meta.FindStatusCondition(claim.Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeApproved)
	g.Expect(approved.Message).To(gomega.Equal("by the approver"), "the approval is the approver's")
	for _, cond := range claim.Status.Conditions {
		g.Expect(cond.Type).To(gomega.BeElementOf(kfplacementv1alpha1.ClusterClaimCondTypeApproved, kfplacementv1alpha1.ClusterClaimCondTypeAccepted, kfplacementv1alpha1.ClusterClaimCondTypeCompleted), "only Accepted and Completed are the provider's")
	}
	for _, finalizer := range claim.Finalizers {
		g.Expect(finalizer).To(gomega.Equal(kfplacementv1alpha1.FulfillerFinalizer), "only the fulfiller finalizer is added")
	}
	for k := range claim.Annotations {
		g.Expect(k).To(gomega.Equal(kfplacementv1alpha1.ProvisionedClusterNameAnnotation), "only the pre-record annotation is added")
	}
}

func (s *suite) failsPermanently(t *testing.T) {
	g := gomega.NewWithT(t)
	claim := s.newClaim(t, s.nextName("failing"), nil)
	s.subject.Behave(FailPermanent)
	s.approve(t, claim)
	g.Eventually(func(g gomega.Gomega) {
		g.Expect(s.stateOf(claim)(g).failed).To(gomega.BeTrue(), "conditions: %v", claim.Status.Conditions)
	}, eventuallyTimeout, pollInterval).Should(gomega.Succeed())
	s.subject.Behave(Fulfill)
	g.Consistently(func(g gomega.Gomega) {
		st := s.stateOf(claim)(g)
		g.Expect(st.failed).To(gomega.BeTrue(), "a failure is terminal")
		g.Expect(st.completed).To(gomega.BeFalse())
		g.Expect(s.clusterExists(fulfiller.ClusterNameFor(claim))()).To(gomega.BeFalse(), "a failed claim leaves no cluster behind")
	}, consistentlyDuration, pollInterval).Should(gomega.Succeed())
}

func (s *suite) deprovisionsNeverJoinedOnJoinTimeout(t *testing.T) {
	g := gomega.NewWithT(t)
	claim := s.newClaim(t, s.nextName("join-timeout"), nil)
	s.approve(t, claim)
	g.Eventually(s.fulfilled(claim), eventuallyTimeout, pollInterval).Should(gomega.Succeed())
	s.expire(t, claim, kfplacementv1alpha1.ClusterClaimExpiredCondReasonJoinTimeout)
	g.Eventually(func(g gomega.Gomega) {
		g.Expect(s.stateOf(claim)(g).held).To(gomega.BeFalse(), "an expired claim is released")
		g.Expect(s.clusterExists(fulfiller.ClusterNameFor(claim))()).To(gomega.BeFalse(), "a cluster that never joined is deprovisioned")
	}, eventuallyTimeout, pollInterval).Should(gomega.Succeed())
}

func (s *suite) keepsJoinedOnJoinTimeout(t *testing.T) {
	for _, recent := range []bool{true, false} {
		g := gomega.NewWithT(t)
		claim := s.newClaim(t, s.nextName("joined-late"), nil)
		s.approve(t, claim)
		g.Eventually(s.fulfilled(claim), eventuallyTimeout, pollInterval).Should(gomega.Succeed())
		s.markJoined(t, fulfiller.ClusterNameFor(claim), recent)
		s.expire(t, claim, kfplacementv1alpha1.ClusterClaimExpiredCondReasonJoinTimeout)
		g.Eventually(func(g gomega.Gomega) {
			g.Expect(s.stateOf(claim)(g).held).To(gomega.BeFalse())
		}, eventuallyTimeout, pollInterval).Should(gomega.Succeed())
		g.Consistently(s.clusterExists(fulfiller.ClusterNameFor(claim)), consistentlyDuration, pollInterval).Should(gomega.BeTrue(), "a cluster that joined is never deprovisioned (recent heartbeat: %t)", recent)
	}
}

func (s *suite) keepsNotMatching(t *testing.T) {
	g := gomega.NewWithT(t)
	claim := s.newClaim(t, s.nextName("not-matching"), nil)
	s.approve(t, claim)
	g.Eventually(s.fulfilled(claim), eventuallyTimeout, pollInterval).Should(gomega.Succeed())
	s.expire(t, claim, kfplacementv1alpha1.ClusterClaimExpiredCondReasonNotMatching)
	g.Eventually(func(g gomega.Gomega) {
		g.Expect(s.stateOf(claim)(g).held).To(gomega.BeFalse())
	}, eventuallyTimeout, pollInterval).Should(gomega.Succeed())
	g.Consistently(s.clusterExists(fulfiller.ClusterNameFor(claim)), consistentlyDuration, pollInterval).Should(gomega.BeTrue(), "a not-matching cluster is a member in its own right")
}

func (s *suite) keepsJoinedOnWithdrawal(t *testing.T) {
	for _, recent := range []bool{true, false} {
		g := gomega.NewWithT(t)
		claim := s.newClaim(t, s.nextName("withdrawn-joined"), nil)
		s.approve(t, claim)
		g.Eventually(s.fulfilled(claim), eventuallyTimeout, pollInterval).Should(gomega.Succeed())
		s.markJoined(t, fulfiller.ClusterNameFor(claim), recent)
		g.Expect(s.c.Delete(s.ctx, claim)).To(gomega.Succeed())
		g.Eventually(func() bool { return s.gone(claim) }, eventuallyTimeout, pollInterval).Should(gomega.BeTrue())
		g.Consistently(s.clusterExists(fulfiller.ClusterNameFor(claim)), consistentlyDuration, pollInterval).Should(gomega.BeTrue(), "withdrawal never deprovisions a cluster that joined (recent heartbeat: %t)", recent)
	}
}

func (s *suite) deprovisionsNeverJoinedOnWithdrawal(t *testing.T) {
	g := gomega.NewWithT(t)
	claim := s.newClaim(t, s.nextName("withdrawn-unjoined"), nil)
	s.approve(t, claim)
	g.Eventually(s.fulfilled(claim), eventuallyTimeout, pollInterval).Should(gomega.Succeed())
	g.Expect(s.c.Delete(s.ctx, claim)).To(gomega.Succeed())
	g.Eventually(func(g gomega.Gomega) {
		g.Expect(s.gone(claim)).To(gomega.BeTrue())
		g.Expect(s.clusterExists(fulfiller.ClusterNameFor(claim))()).To(gomega.BeFalse(), "a cluster withdrawn inside its join window is deprovisioned")
	}, eventuallyTimeout, pollInterval).Should(gomega.Succeed())
}

func (s *suite) provisionsAnewWhenPreviousEligible(t *testing.T) {
	g := gomega.NewWithT(t)
	name := s.nextName("reissued")
	first := s.newClaim(t, name, nil)
	s.approve(t, first)
	g.Eventually(s.fulfilled(first), eventuallyTimeout, pollInterval).Should(gomega.Succeed())
	firstCluster := fulfiller.ClusterNameFor(first)
	s.markJoined(t, firstCluster, true)
	// KubeFleet counts the cluster and rotates the claim: withdrawn, and re-issued under the
	// same name for one more cluster.
	g.Expect(s.c.Delete(s.ctx, first)).To(gomega.Succeed())
	g.Eventually(func() bool { return s.gone(first) }, eventuallyTimeout, pollInterval).Should(gomega.BeTrue())

	second := s.newClaim(t, name, nil)
	s.approve(t, second)
	g.Eventually(s.fulfilled(second), eventuallyTimeout, pollInterval).Should(gomega.Succeed())
	g.Expect(fulfiller.ClusterNameFor(second)).NotTo(gomega.Equal(firstCluster), "a re-issued claim gets a cluster of its own")
	g.Expect(s.clusterExists(firstCluster)()).To(gomega.BeTrue(), "the previous cluster is left alone")
}

// provisionsIdempotently drives the provider directly, as the framework does across a restart:
// Provision called again for the same request must find its own cluster rather than make a
// second one, and Deprovision called again must be a no-op. The claim stays unapproved so the
// framework keeps out of the way.
func (s *suite) provisionsIdempotently(t *testing.T) {
	g := gomega.NewWithT(t)
	claim := s.newClaim(t, s.nextName("idempotent"), nil)
	req := fulfiller.Request{
		ClaimName: claim.Name, ClaimUID: claim.UID, ClusterName: fulfiller.ClusterNameFor(claim),
		ClusterSelectorTerms: claim.Spec.ClusterSelectorTerms, ClusterProviderClassName: s.class.Name, Parameters: s.class.Spec.Parameters,
	}
	ownClusters := func() int {
		clusters := &clusterv1beta1.MemberClusterList{}
		g.Expect(s.c.List(s.ctx, clusters, client.MatchingLabels{kfplacementv1alpha1.FulfilledClaimUIDLabel: string(claim.UID)})).To(gomega.Succeed())
		return len(clusters.Items)
	}
	for i := range 2 {
		g.Eventually(func() error { return s.provisioner.Provision(s.ctx, req) }, eventuallyTimeout, pollInterval).Should(gomega.Succeed(), "Provision call %d", i+1)
		g.Expect(ownClusters()).To(gomega.Equal(1), "exactly one cluster after Provision call %d", i+1)
		g.Expect(s.clusterExists(req.ClusterName)()).To(gomega.BeTrue(), "the cluster is registered under the request's name")
	}
	for i := range 2 {
		g.Eventually(func() error { return s.provisioner.Deprovision(s.ctx, req) }, eventuallyTimeout, pollInterval).Should(gomega.Succeed(), "Deprovision call %d", i+1)
		g.Expect(ownClusters()).To(gomega.BeZero(), "no cluster after Deprovision call %d", i+1)
	}
}

func (s *suite) derivesValidName(t *testing.T) {
	g := gomega.NewWithT(t)
	long := strings.Repeat("a", 60) + "." + strings.Repeat("b", 60) + "-0-" + strings.Repeat("c", 80)
	claim := s.newClaim(t, long, nil)
	s.approve(t, claim)
	g.Eventually(s.fulfilled(claim), eventuallyTimeout, pollInterval).Should(gomega.Succeed())
	name := *claim.Status.ProvisionedClusterName
	g.Expect(validation.IsDNS1123Label(name)).To(gomega.BeEmpty(), "the cluster name %q is a DNS label", name)
	g.Expect(len(name)).To(gomega.BeNumerically("<=", 49), "the cluster name fits fleet-member-<name>")
}
