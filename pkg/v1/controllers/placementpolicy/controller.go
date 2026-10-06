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

// Package placementpolicy implements the FEP-0001 placement policy controller, which reconciles
// the PlacementPolicy and ClusterPlacementPolicy API objects (placement.kubefleet.dev API group):
// it resolves cluster selectors against the current member cluster inventory and reports
// scheduling status on the policy objects.
package placementpolicy

import (
	"context"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"
	runtime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/scheduler/clustereligibilitychecker"
	kferrors "github.com/kubefleet-dev/kubefleet/pkg/utils/errors"
)

const (
	// unfulfilledRequeueAfter is the wait before re-evaluating a policy that has not reached
	// all of its desired cluster counts; it backstops cluster eligibility transitions that do
	// not surface as watch events (e.g., a member agent heartbeat going stale).
	unfulfilledRequeueAfter = 2 * time.Minute
	// fulfilledRequeueAfter is the wait before re-evaluating a fulfilled policy, for the same
	// backstop reason: a cluster that silently stops heartbeating emits no event, yet it must
	// eventually stop counting toward fulfillment. Set to half the eligibility checker's
	// 5-minute heartbeat timeout so the worst-case detection latency stays near the timeout
	// itself (timeout + one requeue period, ~7.5 minutes) instead of doubling it.
	fulfilledRequeueAfter = 150 * time.Second
	// bindingManagerRetryAfter is the wait before retrying a binding change that found the
	// policy's binding manager role held by another process (e.g., a rollout in progress). The
	// FEP has contenders back off and retry rather than wait on the role.
	bindingManagerRetryAfter = 10 * time.Second

	// EventReasonClaimNotIssued is recorded, as a warning, when a selector wants a cluster claim
	// that cannot be issued: the policy resolves to no cluster provider class, or the selector's
	// terms fall outside the class's vocabulary. The note says which.
	EventReasonClaimNotIssued = "ClusterClaimNotIssued"
	// EventReasonClaimExpired is recorded, as a warning, when the controller expires one of the
	// policy's cluster claims; the note names the claim and the reason.
	EventReasonClaimExpired = "ClusterClaimExpired"
	// EventReasonClaimHeld is recorded, as a warning, on every reconcile that finds one of the
	// policy's cluster claims terminal -- failed, expired, or denied -- and held as the record;
	// the note names the claim and why.
	EventReasonClaimHeld = "ClusterClaimHeld"

	// defaultMaxConcurrentClaims is the fleet-wide number of cluster claims that may hold a
	// provider's attention at once, per the FEP's default of one; WithMaxConcurrentClusterClaims
	// raises it.
	defaultMaxConcurrentClaims = 1
	// defaultMaxConcurrentClaimsPerPolicy applies to a policy that sets no limit of its own.
	defaultMaxConcurrentClaimsPerPolicy = 1

	// maxEventNoteLength is the API server's limit on an event's note; a longer one is rejected.
	maxEventNoteLength = 1024
)

// eventNote fits a status message into an event note. The Scheduled message carries the full
// text; the event only needs to point at it.
func eventNote(message string) string {
	if len(message) <= maxEventNoteLength {
		return message
	}
	return message[:maxEventNoteLength-3] + "..."
}

// Reconciler reconciles PlacementPolicy and ClusterPlacementPolicy objects.
//
// One Reconciler instance serves both APIs: requests for cluster-scoped ClusterPlacementPolicy
// objects carry no namespace, which is how the two are told apart.
type Reconciler struct {
	client.Client

	// uncachedReader reads directly from the API server; it gates the claim cleanup
	// finalizer release, where a stale cache read could orphan a just-created claim.
	uncachedReader client.Reader

	eligibility eligibilityChecker

	// snapshots provides the resource snapshot a new binding rolls out.
	snapshots snapshotter

	// recorder reports, on the policy, why a selector gets no cluster claim.
	recorder events.EventRecorder

	// clock is the time source for judging claim expiry and retry deadlines. The transition
	// times those deadlines count from are stamped with the wall clock by whoever writes the
	// condition, so a test that swaps this clock must keep it near wall-clock time.
	clock clock.Clock

	// maxConcurrentClaims is the fleet-wide limit on active cluster claims; claimMu serializes
	// the count-then-create under it across the two controllers this Reconciler serves.
	maxConcurrentClaims int32
	claimMu             sync.Mutex
}

// Option configures a Reconciler.
type Option func(*Reconciler)

// WithMaxConcurrentClusterClaims sets the fleet-wide number of cluster claims that may be active
// at once: approved, or of an Automatic class. The limit is exact for Automatic classes; for
// Manual classes it bounds issuance only, since an approver may approve several outstanding
// claims at once, and the provider's own concurrency limit is the backstop. A policy's own
// spec.maxConcurrentClusterClaims never exceeds it. Values below one are ignored.
func WithMaxConcurrentClusterClaims(n int32) Option {
	return func(r *Reconciler) {
		if n >= 1 {
			r.maxConcurrentClaims = n
		}
	}
}

// NewReconciler returns a Reconciler that judges cluster fulfillment with the scheduler's
// standard cluster eligibility gate, takes resource snapshots through the given manager, and
// records events through the given recorder, which may be nil.
func NewReconciler(c client.Client, uncachedReader client.Reader, snapshots snapshotter, recorder events.EventRecorder, opts ...Option) *Reconciler {
	r := &Reconciler{
		Client:              c,
		uncachedReader:      uncachedReader,
		eligibility:         clustereligibilitychecker.New(),
		snapshots:           snapshots,
		recorder:            recorder,
		clock:               clock.RealClock{},
		maxConcurrentClaims: defaultMaxConcurrentClaims,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// claimLimit returns the policy's effective concurrency limit: its own, else the default of one,
// never above the fleet-wide limit.
func (r *Reconciler) claimLimit(policy kfplacementv1alpha1.PlacementPolicyAccessor) int32 {
	limit := int32(defaultMaxConcurrentClaimsPerPolicy)
	if own := policy.GetSpec().MaxConcurrentClusterClaims; own != nil {
		limit = *own
	}
	return min(limit, r.maxConcurrentClaims)
}

// Reconcile runs a single reconciliation round for a PlacementPolicy or ClusterPlacementPolicy object.
func (r *Reconciler) Reconcile(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	startTime := time.Now()
	klog.V(2).InfoS("Placement policy reconciliation starts", "placementPolicy", req.NamespacedName)
	defer func() {
		latency := time.Since(startTime).Milliseconds()
		klog.V(2).InfoS("Placement policy reconciliation ends", "placementPolicy", req.NamespacedName, "latency", latency)
	}()

	policy, err := r.fetchPolicy(ctx, req)
	if err != nil {
		if errors.IsNotFound(err) {
			klog.V(4).InfoS("Ignoring not-found placement policy", "placementPolicy", req.NamespacedName)
			// The policy is gone for good; stop reporting metrics for it.
			forgetPolicyMetrics(req.Namespace, req.Name)
			return runtime.Result{}, nil
		}
		klog.ErrorS(err, "Failed to get the placement policy object", "placementPolicy", req.NamespacedName)
		return runtime.Result{}, err
	}
	if !policy.GetDeletionTimestamp().IsZero() {
		// Cluster claims cannot be owner-referenced by the policy (cross-scope), so cleanup
		// is finalizer-driven.
		return runtime.Result{}, r.cleanupClaims(ctx, policy)
	}

	memberClusters := &clusterv1beta1.MemberClusterList{}
	if err := r.List(ctx, memberClusters); err != nil {
		klog.ErrorS(err, "Failed to list member clusters", "placementPolicy", req.NamespacedName)
		return runtime.Result{}, err
	}
	var mostRecentClusterCreation metav1.Time
	clustersByName := make(map[string]*clusterv1beta1.MemberCluster, len(memberClusters.Items))
	for i := range memberClusters.Items {
		cluster := &memberClusters.Items[i]
		clustersByName[cluster.Name] = cluster
		if ts := cluster.CreationTimestamp; ts.After(mostRecentClusterCreation.Time) {
			mostRecentClusterCreation = ts
		}
	}

	outcomes, err := evaluateSelectors(policy.GetSpec(), memberClusters.Items, r.eligibility)
	if err != nil {
		// Selector evaluation fails only on invalid selector contents (e.g., an operator
		// applied to a key it does not support); retrying cannot help until the spec changes,
		// so the error surfaces on the status instead of the reconcile loop. Outstanding
		// claims are deliberately left untouched: their specs reflect the last valid
		// selectors.
		klog.ErrorS(err, "Failed to evaluate the cluster selectors", "placementPolicy", req.NamespacedName)
		return runtime.Result{}, r.updateStatus(ctx, policy, nil, nil, invalidSelectorsCondition(policy.GetGeneration(), err))
	}

	existingBindings, err := r.listBindings(ctx, policy)
	if err != nil {
		klog.ErrorS(err, "Failed to list the placement bindings", "placementPolicy", req.NamespacedName)
		return runtime.Result{}, err
	}
	bound := make(map[string]metav1.Time, len(existingBindings))
	for _, binding := range existingBindings {
		bound[binding.GetSpec().ClusterName] = binding.GetCreationTimestamp()
	}
	desiredBindings := chooseClusters(outcomes, bound, clustersByName)
	roleHeld, bindingErr := r.reconcileBindings(ctx, policy, outcomes, desiredBindings, existingBindings)

	class, noClass, err := r.resolveClass(ctx, policy)
	if err != nil {
		klog.ErrorS(err, "Failed to resolve the cluster provider class", "placementPolicy", req.NamespacedName)
		return runtime.Result{}, err
	}
	wanted, claimNote := desiredClaims(policy, outcomes, class, noClass)
	if claimNote != "" && r.recorder != nil {
		r.recorder.Eventf(policy, nil, corev1.EventTypeWarning, EventReasonClaimNotIssued, "IssueClaim", "%s", eventNote(claimNote))
	}

	now := r.clock.Now()
	report, claimErr := r.reconcileClaims(ctx, claimRound{
		policy:                    policy,
		wanted:                    wanted,
		clusters:                  clustersByName,
		limit:                     r.claimLimit(policy),
		mostRecentClusterCreation: mostRecentClusterCreation,
		now:                       now,
	})
	// The scheduling status is written even when claim reconciliation failed partway, but the
	// claim count is published only from a completed round: a failed round returns whatever it
	// had counted when it stopped, which would misreport the claims that the rest of the round
	// never reached. A nil report leaves the last completed round's value standing; the retry
	// corrects it.
	reported := &report
	if claimErr != nil {
		reported = nil
	}
	notes := append([]string{}, report.held...)
	if claimNote != "" {
		notes = append([]string{claimNote}, notes...)
	}
	if err := r.updateStatus(ctx, policy, outcomes, reported, scheduledCondition(policy.GetGeneration(), outcomes, strings.Join(notes, "; "))); err != nil {
		return runtime.Result{}, err
	}
	if bindingErr != nil {
		klog.ErrorS(bindingErr, "Failed to reconcile the placement bindings", "placementPolicy", req.NamespacedName)
		return runtime.Result{}, bindingErr
	}
	if claimErr != nil {
		klog.ErrorS(claimErr, "Failed to reconcile the cluster claims", "placementPolicy", req.NamespacedName)
		return runtime.Result{}, claimErr
	}
	if roleHeld {
		klog.V(2).InfoS("The binding manager role is held by another process; will retry the binding changes", "placementPolicy", req.NamespacedName)
		return runtime.Result{RequeueAfter: bindingManagerRetryAfter}, nil
	}

	requeueAfter := fulfilledRequeueAfter
	for i := range outcomes {
		if !outcomes[i].satisfiedInFull() {
			requeueAfter = unfulfilledRequeueAfter
			break
		}
	}
	// A claim timer that fires sooner than the periodic re-evaluation is waited for exactly;
	// one that already fired is acted on this pass, so a deadline in the past is not a wait.
	if until := report.nextDeadline.Sub(now); !report.nextDeadline.IsZero() && until > 0 && until < requeueAfter {
		requeueAfter = until
	}
	return runtime.Result{RequeueAfter: requeueAfter}, nil
}

// updateStatus writes the scheduling outcome onto the policy status, skipping the API call when
// nothing has changed. A nil outcome list clears the cluster counts (used when the selectors
// cannot be evaluated at all); a nil activeClaims leaves the current claim count untouched.
func (r *Reconciler) updateStatus(ctx context.Context, policy kfplacementv1alpha1.PlacementPolicyAccessor, outcomes []selectorOutcome, claims *claimReport, scheduledCond metav1.Condition) error {
	status := policy.GetStatus()
	observedStatus := status.DeepCopy()

	if outcomes == nil {
		status.DesiredClusters = nil
		status.ScheduledClusters = nil
	} else {
		desired, scheduled := aggregateCounts(outcomes)
		status.DesiredClusters = &desired
		status.ScheduledClusters = &scheduled
	}
	if claims != nil {
		status.ActiveClusterClaims = &claims.outstanding
	}
	meta.SetStatusCondition(&status.Conditions, scheduledCond)

	reportPolicyMetrics(policy, claims, scheduledCond)

	if apiequality.Semantic.DeepEqual(observedStatus, status) {
		return nil
	}
	if err := r.Status().Update(ctx, policy); err != nil {
		klog.ErrorS(err, "Failed to update the placement policy status", "placementPolicy", client.ObjectKeyFromObject(policy))
		return err
	}
	return nil
}

// fetchPolicy retrieves the policy object for the given request; requests without a namespace
// concern the cluster-scoped ClusterPlacementPolicy API.
//
// The read is categorized as an API server error, which stays recognizable to errors.IsNotFound:
// it resolves through the wrap, so the caller can still tell a deleted policy from a failed read.
func (r *Reconciler) fetchPolicy(ctx context.Context, req runtime.Request) (kfplacementv1alpha1.PlacementPolicyAccessor, error) {
	var policy kfplacementv1alpha1.PlacementPolicyAccessor = &kfplacementv1alpha1.PlacementPolicy{}
	if req.Namespace == "" {
		policy = &kfplacementv1alpha1.ClusterPlacementPolicy{}
	}
	if err := r.Get(ctx, req.NamespacedName, policy); err != nil {
		return nil, kferrors.NewAPIServerError(err, "failed to get the placement policy", true, "placementPolicy", req.NamespacedName)
	}
	return policy, nil
}

// SetupWithManagerForPlacementPolicy registers the reconciler with the manager for the
// namespaced PlacementPolicy API.
func (r *Reconciler) SetupWithManagerForPlacementPolicy(mgr runtime.Manager) error {
	return runtime.NewControllerManagedBy(mgr).
		Named(controllerName).
		For(&kfplacementv1alpha1.PlacementPolicy{}).
		Owns(&kfplacementv1alpha1.PlacementBinding{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(
			&clusterv1beta1.MemberCluster{},
			handler.EnqueueRequestsFromMapFunc(r.mapMemberClusterToPlacementPolicies),
			builder.WithPredicates(memberClusterSchedulingRelevantChanges()),
		).
		Watches(
			&kfplacementv1alpha1.ClusterClaim{},
			handler.EnqueueRequestsFromMapFunc(r.mapClaimToPlacementPolicy),
		).
		Watches(
			&kfplacementv1alpha1.ClusterClaim{},
			handler.EnqueueRequestsFromMapFunc(r.mapToAllPlacementPolicies),
			builder.WithPredicates(claimFreesFleetSlot()),
		).
		Watches(
			&kfplacementv1alpha1.ClusterProviderClass{},
			handler.EnqueueRequestsFromMapFunc(r.mapToAllPlacementPolicies),
		).
		Complete(r)
}

// SetupWithManagerForClusterPlacementPolicy registers the reconciler with the manager for the
// cluster-scoped ClusterPlacementPolicy API.
func (r *Reconciler) SetupWithManagerForClusterPlacementPolicy(mgr runtime.Manager) error {
	return runtime.NewControllerManagedBy(mgr).
		Named("cluster-placement-policy-controller").
		For(&kfplacementv1alpha1.ClusterPlacementPolicy{}).
		Owns(&kfplacementv1alpha1.ClusterPlacementBinding{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(
			&clusterv1beta1.MemberCluster{},
			handler.EnqueueRequestsFromMapFunc(r.mapMemberClusterToClusterPlacementPolicies),
			builder.WithPredicates(memberClusterSchedulingRelevantChanges()),
		).
		Watches(
			&kfplacementv1alpha1.ClusterClaim{},
			handler.EnqueueRequestsFromMapFunc(r.mapClaimToClusterPlacementPolicy),
		).
		Watches(
			&kfplacementv1alpha1.ClusterClaim{},
			handler.EnqueueRequestsFromMapFunc(r.mapToAllClusterPlacementPolicies),
			builder.WithPredicates(claimFreesFleetSlot()),
		).
		Watches(
			&kfplacementv1alpha1.ClusterProviderClass{},
			handler.EnqueueRequestsFromMapFunc(r.mapToAllClusterPlacementPolicies),
		).
		Complete(r)
}
