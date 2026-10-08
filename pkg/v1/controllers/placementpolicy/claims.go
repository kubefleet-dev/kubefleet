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

package placementpolicy

import (
	"context"
	"fmt"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/naming"
)

const (
	// claimCleanupFinalizer marks policies with outstanding cluster claims; deleting such a
	// policy first withdraws its claims.
	claimCleanupFinalizer = "placement.kubefleet.dev/claim-cleanup"

	// claimNameBaseMaxLength bounds the policy-name prefix inside a generated claim name so
	// the full name stays well within the 253-character object name limit.
	claimNameBaseMaxLength = 200
)

// claimOwnershipLabels returns the labels that select the claims of a policy.
func claimOwnershipLabels(policy kfplacementv1alpha1.PlacementPolicyAccessor) client.MatchingLabels {
	return client.MatchingLabels{
		kfplacementv1alpha1.ClusterClaimPlacementPolicyNameLabel:      naming.LabelValue(policy.GetName()),
		kfplacementv1alpha1.ClusterClaimPlacementPolicyNamespaceLabel: policy.GetNamespace(),
	}
}

// claimName derives the deterministic name for the claim serving a policy's selector. The name
// embeds a hash of the policy's namespaced name because claims are cluster-scoped while
// PlacementPolicy names are only unique per namespace; two same-named policies in different
// namespaces must not collide. Determinism matters: creation is get-or-create, so a restarted
// reconciler converges on the same claim instead of issuing a duplicate.
func claimName(policy kfplacementv1alpha1.PlacementPolicyAccessor, selectorIndex int) string {
	base := naming.Truncate(policy.GetName(), claimNameBaseMaxLength)
	return fmt.Sprintf("%s-%d-%s", base, selectorIndex, naming.Hash(policy.GetNamespace()+"/"+policy.GetName()))
}

// desiredClaim describes a claim the policy currently wants outstanding.
type desiredClaim struct {
	name  string
	terms []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm
	// class is the ClusterProviderClass the claim is stamped with when issued; nil when the policy
	// resolves to none. A kept claim of another class no longer serves the selector and is replaced.
	class *kfplacementv1alpha1.ClusterProviderClass
	// blocked says why no claim can be issued for the selector right now -- no class, or terms
	// outside the class's vocabulary -- and is "" when one can. It gates issuing only: a claim
	// already outstanding for the selector is kept, so a class that vanishes for a moment or a
	// vocabulary that narrows never withdraws provisioning that is in flight.
	blocked string
	// outcome is the selector this claim serves. It is what lets the reconcile decide when a
	// completed claim has done its job and should be rotated to provision the next cluster.
	outcome *selectorOutcome
}

// desiredClaims returns the claims the policy should have outstanding given the selector
// outcomes: one claim per unfulfilled selector that opted into AddClusterClaim, in selector
// order. The concurrency limits apply when claims are issued, never to what is wanted: a claim
// that exists for a later selector stays wanted, and kept, even while an earlier selector waits
// for a slot, so a limit that tightens or a cluster that leaves never withdraws provisioning in
// flight.
//
// noClass is why the policy resolves to no class, or "" when class is set. The second value is
// the first reason, in selector order, that a wanted claim cannot be issued -- deterministic
// across reconciles, and empty when every wanted claim is issuable or nothing is wanted at all, so
// that a satisfied policy in a fleet without classes reports nothing.
func desiredClaims(policy kfplacementv1alpha1.PlacementPolicyAccessor, outcomes []selectorOutcome, class *kfplacementv1alpha1.ClusterProviderClass, noClass string) (wanted []desiredClaim, note string) {
	wanted = make([]desiredClaim, 0, len(outcomes))
	for i := range outcomes {
		o := &outcomes[i]
		if o.satisfiedInFull() || o.whenUnfulfilled != kfplacementv1alpha1.WhenUnfulfilledOptionAddClusterClaim {
			continue
		}
		w := desiredClaim{name: claimName(policy, i), terms: o.terms, class: class, outcome: o}
		switch {
		case class == nil:
			w.blocked = noClass
		default:
			if msg := vocabularyViolation(class, o.terms); msg != "" {
				w.blocked = fmt.Sprintf("no new cluster claim is issued for cluster selector %d: %s", i, msg)
			}
		}
		if w.blocked != "" && note == "" {
			note = w.blocked
		}
		wanted = append(wanted, w)
	}
	return wanted, note
}

// claimStillWanted reports whether an outstanding claim still serves the wanted claim of its
// name: it was issued for the same terms, and for the same class when one resolves now. A claim
// serves only its original terms and class; if either changed on the policy, the claim is
// withdrawn and a fresh one issued on a later pass. When no class resolves the stamp is not
// judged, since there is nothing to compare it with and the claim may be mid-provisioning.
func claimStillWanted(claim *kfplacementv1alpha1.ClusterClaim, w desiredClaim) bool {
	if !apiequality.Semantic.DeepEqual(claim.Spec.ClusterSelectorTerms, w.terms) {
		return false
	}
	return w.class == nil || claim.Spec.ClusterProviderClassName == w.class.Name
}

// claimReadyToRotate reports whether a claim has done its job -- the provisioner marked it completed
// and the cluster it provisioned is now eligible for the selector -- while the selector still needs
// more clusters. One claim yields one cluster (the claim carries no count, and its status names a
// single provisioned cluster), so a selector wanting several is filled one claim at a time: such a
// claim is withdrawn and reissued so the provisioner provisions the next cluster.
//
// The eligibility check is what makes this safe. The next claim is issued only once the current
// claim's cluster is counted toward the selector, so a provisioner is never handed a second claim
// while the first cluster is still joining -- which would ask it to provision two clusters at once
// for one selector.
func claimReadyToRotate(claim *kfplacementv1alpha1.ClusterClaim, outcome *selectorOutcome) bool {
	if outcome == nil || outcome.satisfiedInFull() {
		return false
	}
	if !meta.IsStatusConditionTrue(claim.Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeCompleted) {
		return false
	}
	provisioned := claim.Status.ProvisionedClusterName
	return provisioned != nil && slices.Contains(outcome.matched, *provisioned)
}

// claimRound is what one reconcile of a policy's claims works from.
type claimRound struct {
	policy kfplacementv1alpha1.PlacementPolicyAccessor
	wanted []desiredClaim
	// clusters indexes the fleet by name, for judging a completed claim's cluster.
	clusters map[string]*clusterv1beta1.MemberCluster
	// limit is the policy's effective concurrency limit, min(policy, fleet).
	limit                     int32
	mostRecentClusterCreation metav1.Time
	now                       time.Time
}

// claimReport is what one reconcile of a policy's claims found.
type claimReport struct {
	// outstanding counts the policy's claims, in whatever state, including ones being withdrawn.
	outstanding int32
	// states counts the outstanding claims by claimState, for the metric; it always sums to
	// outstanding, since both are only ever bumped together, through count.
	states map[string]int
	// held says, for the Scheduled message, why terminal claims are kept and why a wanted claim
	// waits on the fleet-wide limit.
	held []string
	// nextDeadline is the earliest moment a kept claim's timer fires -- an expiry, or a retry --
	// and zero when none is pending; the reconcile requeues for it.
	nextDeadline time.Time
}

// count records one outstanding claim in the given state.
func (rep *claimReport) count(state string) {
	rep.outstanding++
	rep.states[state]++
}

func (rep *claimReport) note(deadline time.Time) {
	if !deadline.IsZero() && (rep.nextDeadline.IsZero() || deadline.Before(rep.nextDeadline)) {
		rep.nextDeadline = deadline
	}
}

// reconcileClaims drives the policy's cluster claims toward the desired set: it withdraws
// claims whose selector is fulfilled or gone, refreshes the freshness marker on claims that
// are still wanted, expires kept claims that ran out of time under their class, holds or retries
// terminal claims, and issues new claims within the concurrency budgets.
//
// A claim held in Terminating by a provisioner finalizer still counts toward the concurrency
// budget (its deterministic name also blocks re-creation), so a slow provisioner teardown can
// never cause double-provisioning for the same selector. A terminal claim counts the same way:
// it is the record of what happened, and the selector is not re-claimed while the record stands.
func (r *Reconciler) reconcileClaims(ctx context.Context, round claimRound) (claimReport, error) {
	policy := round.policy
	rep := claimReport{states: make(map[string]int, len(claimStates))}
	existing, err := r.listClaims(ctx, policy)
	if err != nil {
		return rep, err
	}
	if len(existing) > 0 {
		// Re-assert the cleanup finalizer while any claim exists, so an out-of-band finalizer
		// removal self-heals instead of leaving claims orphanable.
		if err := r.ensureFinalizer(ctx, policy); err != nil {
			return rep, err
		}
	}

	wantedByName := make(map[string]desiredClaim, len(round.wanted))
	for _, w := range round.wanted {
		wantedByName[w.name] = w
	}

	listed := make(map[string]bool, len(existing))
	for i := range existing {
		claim := &existing[i]
		listed[claim.Name] = true
		if !claim.DeletionTimestamp.IsZero() {
			// Already being withdrawn: the claim still occupies its name and budget slot
			// regardless of whether its terms happen to match the currently wanted set (a
			// fulfillment flap can re-want identical terms mid-teardown), and an object on
			// its way out receives no further status writes.
			rep.count(claimStateTerminating)
			delete(wantedByName, claim.Name)
			continue
		}
		w, stillWanted := wantedByName[claim.Name]
		keep := stillWanted && claimStillWanted(claim, w)
		if keep && claimReadyToRotate(claim, w.outcome) {
			// The provisioner has provisioned an eligible cluster for this claim, but the selector
			// still needs more. The completed claim is not kept: it falls through to the withdrawal
			// below and, its entry left in wantedByName, is reissued on a later pass so the provisioner
			// gets a fresh claim for the next cluster. Rotation is gated on the provisioned cluster
			// being eligible, so the next claim is never issued before this one's cluster is confirmed.
			// This also resumes a claim expired as JoinTimeout whose cluster joined late: the record
			// is withdrawn because it did its job after all.
			keep = false
		}
		if keep {
			delete(wantedByName, claim.Name)
			if err := r.syncKeptClaim(ctx, claim, &w, round, &rep); err != nil {
				return rep, err
			}
			rep.count(claimState(claim))
			continue
		}
		klog.V(2).InfoS("Withdrawing a cluster claim", "clusterClaim", claim.Name, "placementPolicy", klog.KObj(policy))
		gone, err := r.withdrawClaim(ctx, claim)
		if err != nil {
			return rep, err
		}
		if !gone {
			// A claim withdrawn this pass still occupies its budget slot: a provisioner finalizer
			// can hold it in Terminating past this reconcile, and a differently-named claim created
			// below would otherwise stand beside it, exceeding the concurrency budget. The claim
			// watch re-queues the policy once the object is truly gone, and the slot frees then.
			rep.count(claimStateTerminating)
		}
	}

	// A blocked entry exists only to keep a claim that is already outstanding; once the existing
	// claims are matched it has no further role, and must not count as something to create -- or
	// the cleanup finalizer would land on, and never leave, a claim-free policy in a fleet with no
	// resolvable class.
	for name, w := range wantedByName {
		if w.blocked != "" {
			delete(wantedByName, name)
		}
	}

	if len(wantedByName) == 0 {
		// Nothing more to create. If nothing is outstanding either, the policy is claim-free, so the
		// cleanup finalizer -- whose only job is to withdraw claims before the policy is deleted --
		// has nothing left to guard and is released. Without this a policy that once had a claim would
		// carry the finalizer forever, and disabling the feature and then deleting such a claim-free
		// policy would hang its deletion with no controller left to clear it, outside the documented
		// outstanding-claims caveat.
		if rep.outstanding == 0 {
			if err := r.releaseFinalizerIfNoClaims(ctx, policy); err != nil {
				return rep, err
			}
		}
		return rep, nil
	}

	// The cleanup finalizer lands on the policy before any claim is created, so a crash
	// between the two writes cannot orphan a claim.
	if err := r.ensureFinalizer(ctx, policy); err != nil {
		return rep, err
	}
	for _, w := range round.wanted {
		if _, still := wantedByName[w.name]; !still {
			continue
		}
		if rep.outstanding >= round.limit {
			// Every slot is taken, by kept claims or by ones withdrawn moments ago that may
			// still be terminating; the create is retried when a watch frees a slot.
			break
		}
		switch outcome, err := r.issueClaim(ctx, policy, &w, round); {
		case err != nil:
			return rep, err
		case outcome == issueLimited:
			rep.held = append(rep.held, fmt.Sprintf("cluster claim %q waits for the fleet-wide limit of %d concurrent cluster claims", w.name, r.maxConcurrentClaims))
			return rep, nil
		case outcome == issued:
			rep.count(claimStatePending)
		case outcome == issueOccupied && !listed[w.name]:
			// The name is taken by a claim the cache has yet to show -- the previous round's
			// own create, most likely -- which no slot counts yet. It is counted now, or a
			// policy of several selectors could be issued past its limit under cache lag.
			rep.count(claimStatePending)
		}
	}
	return rep, nil
}

// syncKeptClaim brings a kept claim up to date: a terminal one is held as the record, and
// withdrawn to retry when its class says so and the wait has passed; a live one has its labels,
// its freshness marker, and its automatic approval re-asserted, and its timers judged.
func (r *Reconciler) syncKeptClaim(ctx context.Context, claim *kfplacementv1alpha1.ClusterClaim, w *desiredClaim, round claimRound, rep *claimReport) error {
	if terminal := terminalCondition(claim); terminal != nil {
		// The record of a failure, an expiry, or a denial. It is held, counted, and never
		// written to; a class that retries withdraws it once retryAfter has passed, and the
		// name is re-issued on a later pass like any withdrawn claim.
		if w.class != nil && w.blocked == "" {
			if deadline, retry := retryDeadline(terminal, &w.class.Spec); retry {
				if !round.now.Before(deadline) {
					klog.V(2).InfoS("Withdrawing a terminal cluster claim to retry", "clusterClaim", claim.Name, "placementPolicy", klog.KObj(round.policy), "reason", terminal.Reason)
					_, err := r.withdrawClaim(ctx, claim)
					return err
				}
				rep.note(deadline)
			}
		}
		note := heldNote(claim, terminal)
		rep.held = append(rep.held, note)
		// Raised on every pass that holds the record; the events API folds the repeats into one
		// series with a count, which is the record's age in reconciles.
		if r.recorder != nil {
			r.recorder.Eventf(round.policy, claim, corev1.EventTypeWarning, EventReasonClaimHeld, "HoldClaim", "%s", eventNote(note))
		}
		return nil
	}

	if err := r.reconcileClaimLabels(ctx, claim, round.policy); err != nil {
		return err
	}
	// A claim held through a vocabulary that narrowed under it is not approved on the
	// class's behalf: the class no longer admits what the claim asks for, so approval stays
	// with an approver, as it would for a claim the class cannot issue. Its timers are not
	// judged either, nor are those of a claim whose class is gone: the class is where the
	// timers live.
	class := w.class
	if w.blocked != "" {
		class = nil
	}
	if err := r.syncClaimStatus(ctx, claim, class, round.mostRecentClusterCreation, round.now); err != nil {
		return err
	}
	if class == nil {
		return nil
	}
	deadline, err := r.expireIfDue(ctx, claim, class, w, round)
	if err != nil {
		return err
	}
	rep.note(deadline)
	return nil
}

// withdrawClaim deletes a claim pinned to the UID the cache showed: claim names are deterministic,
// so a cache that has yet to see an earlier withdrawal can still list the predecessor after its
// successor was created under the same name, and an unpinned delete would withdraw the successor.
// A UID mismatch fails with a conflict, and the requeue retries on a fresher view. It reports
// whether the claim was already fully gone, in which case it occupies nothing.
func (r *Reconciler) withdrawClaim(ctx context.Context, claim *kfplacementv1alpha1.ClusterClaim) (gone bool, err error) {
	if err := r.Delete(ctx, claim, client.Preconditions{UID: &claim.UID}); err != nil {
		if errors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

// issueOutcome is what issueClaim did.
type issueOutcome int

const (
	// issued means the claim was created and occupies a slot.
	issued issueOutcome = iota
	// issueOccupied means the name is still taken by a claim that is not where the list showed
	// it: this round's own withdrawal, held in Terminating by a provisioner finalizer and already
	// counted, or a claim the cache has yet to show at all, which the caller counts. The claim
	// watch re-queues the policy either way.
	issueOccupied
	// issueLimited means the fleet-wide limit is reached; nothing was created.
	issueLimited
)

// issueClaim creates a wanted claim and stamps its status, under the fleet-wide limit: the fleet's
// active claims are counted from the API server inside a critical section the two controllers of
// this Reconciler share, and the create and the automatic approval both happen inside it, so that
// in one leader-elected hub agent the limit is exact for Automatic classes. For Manual classes it
// bounds issuance only -- an approver may approve several outstanding claims at once, and the
// provider's own concurrency limit is the backstop. It reports false when the limit is reached.
func (r *Reconciler) issueClaim(ctx context.Context, policy kfplacementv1alpha1.PlacementPolicyAccessor, w *desiredClaim, round claimRound) (issueOutcome, error) {
	r.claimMu.Lock()
	defer r.claimMu.Unlock()

	active, err := r.countFleetActiveClaims(ctx)
	if err != nil {
		return issueLimited, err
	}
	if active >= r.maxConcurrentClaims {
		klog.V(2).InfoS("Holding a cluster claim at the fleet-wide limit", "clusterClaim", w.name, "placementPolicy", klog.KObj(policy), "active", active, "limit", r.maxConcurrentClaims)
		return issueLimited, nil
	}

	claim := &kfplacementv1alpha1.ClusterClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: w.name,
			// The labels select a policy's claims; spec.placementPolicyRef below carries
			// the policy's authoritative identity, which a label value cannot always hold.
			Labels: claimOwnershipLabels(policy),
		},
		Spec: kfplacementv1alpha1.ClusterClaimSpec{
			PlacementPolicyRef:       policyReference(policy),
			ClusterSelectorTerms:     w.terms,
			ClusterProviderClassName: w.class.Name,
		},
	}
	klog.V(2).InfoS("Adding a cluster claim", "clusterClaim", claim.Name, "placementPolicy", klog.KObj(policy))
	if err := r.Create(ctx, claim); err != nil {
		if errors.IsAlreadyExists(err) {
			return issueOccupied, nil
		}
		return issueLimited, err
	}
	// Stamp the freshness marker and the automatic approval right after creation, per the
	// FEP: the claim carries the latest observed cluster creation timestamp from the moment
	// provisioners can see it.
	return issued, r.syncClaimStatus(ctx, claim, w.class, round.mostRecentClusterCreation, round.now)
}

// countFleetActiveClaims counts, from the API server, the claims across the fleet that hold a
// provider's attention or are about to: approved, or of an Automatic class whether or not the
// approval stamp has landed yet; terminal claims and claims being withdrawn are records, not work.
func (r *Reconciler) countFleetActiveClaims(ctx context.Context) (int32, error) {
	claims := &kfplacementv1alpha1.ClusterClaimList{}
	if err := r.uncachedReader.List(ctx, claims); err != nil {
		return 0, err
	}
	classes := &kfplacementv1alpha1.ClusterProviderClassList{}
	if err := r.List(ctx, classes); err != nil {
		return 0, err
	}
	automatic := make(map[string]bool, len(classes.Items))
	for i := range classes.Items {
		automatic[classes.Items[i].Name] = classes.Items[i].Spec.Approval == kfplacementv1alpha1.ClusterClaimApprovalModeAutomatic
	}
	var active int32
	for i := range claims.Items {
		claim := &claims.Items[i]
		if !claim.DeletionTimestamp.IsZero() || terminalCondition(claim) != nil {
			continue
		}
		if meta.IsStatusConditionTrue(claim.Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeApproved) || automatic[claim.Spec.ClusterProviderClassName] {
			active++
		}
	}
	return active, nil
}

// expireIfDue stamps Expired on a kept claim that has run out of time under its class, and returns
// the deadline of the timer that is still running otherwise. The write is a resourceVersion-conditional
// update: a provider's acceptance landing first makes it conflict, and the requeue re-judges on the
// fresher object, so an accepted claim can never be expired out from under its provider.
func (r *Reconciler) expireIfDue(ctx context.Context, claim *kfplacementv1alpha1.ClusterClaim, class *kfplacementv1alpha1.ClusterProviderClass, w *desiredClaim, round claimRound) (time.Time, error) {
	var cluster *clusterv1beta1.MemberCluster
	if claim.Status.ProvisionedClusterName != nil {
		cluster = round.clusters[*claim.Status.ProvisionedClusterName]
	}
	eligible := func(mc *clusterv1beta1.MemberCluster) bool {
		ok, _ := r.eligibility.IsEligible(mc)
		return ok
	}
	unmatched := func(mc *clusterv1beta1.MemberCluster) string {
		return whyUnmatched(mc, w.terms, round.policy.GetSpec().Tolerations)
	}
	verdict, deadline := judgeExpiry(claim, &class.Spec, round.now, cluster, eligible, unmatched)
	if verdict == nil {
		return deadline, nil
	}
	meta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{
		Type:               kfplacementv1alpha1.ClusterClaimCondTypeExpired,
		Status:             metav1.ConditionTrue,
		Reason:             verdict.reason,
		Message:            verdict.message,
		ObservedGeneration: claim.Generation,
		LastTransitionTime: metav1.NewTime(round.now),
	})
	klog.V(2).InfoS("Expiring a cluster claim", "clusterClaim", claim.Name, "placementPolicy", klog.KObj(round.policy), "reason", verdict.reason)
	if err := r.Status().Update(ctx, claim); err != nil {
		if errors.IsNotFound(err) || errors.IsConflict(err) {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	reportClaimExpiry(round.policy, verdict.reason)
	if r.recorder != nil {
		r.recorder.Eventf(round.policy, claim, corev1.EventTypeWarning, EventReasonClaimExpired, "ExpireClaim", "%s", eventNote(fmt.Sprintf("cluster claim %s expired as %s: %s", claim.Name, verdict.reason, verdict.message)))
	}
	return time.Time{}, nil
}

// approveAutomatically stamps Approved on a claim whose class approves automatically, reporting
// whether it changed the claim. It runs at creation and again on every reconcile of a kept claim
// that has no Approved entry: approval is a second write after the create, so a controller stopped
// between the two would otherwise leave a claim no provider may act on and that, being unapproved,
// never expires. A claim that already carries an Approved entry -- an approver's, a denial, or this
// controller's own -- is left alone, as is a claim whose policy resolves to no class right now.
func approveAutomatically(claim *kfplacementv1alpha1.ClusterClaim, class *kfplacementv1alpha1.ClusterProviderClass, now time.Time) bool {
	if class == nil || class.Spec.Approval != kfplacementv1alpha1.ClusterClaimApprovalModeAutomatic ||
		meta.FindStatusCondition(claim.Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeApproved) != nil {
		return false
	}
	meta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{
		Type:               kfplacementv1alpha1.ClusterClaimCondTypeApproved,
		Status:             metav1.ConditionTrue,
		Reason:             kfplacementv1alpha1.ClusterClaimApprovedCondReasonAutomaticallyApproved,
		Message:            fmt.Sprintf("Approved automatically per the cluster provider class %s", class.Name),
		ObservedGeneration: claim.Generation,
		LastTransitionTime: metav1.NewTime(now),
	})
	return true
}

// reconcileClaimLabels restores the ownership labels on a kept claim. The controller itself finds
// a policy's claims through the immutable spec.placementPolicyRef, so a stripped or rewritten label
// never confuses the reconcile; the labels exist for external consumers -- a provisioner watching a
// policy's claims by label -- and a provisioner or user that mutated one would hide the claim from
// them. The labels are re-asserted here for the same reason the cleanup finalizer is re-asserted
// while claims exist: out-of-band drift on an object the controller owns should self-heal.
func (r *Reconciler) reconcileClaimLabels(ctx context.Context, claim *kfplacementv1alpha1.ClusterClaim, policy kfplacementv1alpha1.PlacementPolicyAccessor) error {
	want := claimOwnershipLabels(policy)
	drifted := false
	for k, v := range want {
		if claim.Labels[k] != v {
			drifted = true
			break
		}
	}
	if !drifted {
		return nil
	}
	if claim.Labels == nil {
		claim.Labels = make(map[string]string, len(want))
	}
	for k, v := range want {
		claim.Labels[k] = v
	}
	klog.V(2).InfoS("Restoring ownership labels on a cluster claim", "clusterClaim", claim.Name, "placementPolicy", klog.KObj(policy))
	// A conflict means another writer touched the claim; the claim watch re-queues the policy and
	// the repair retries then. A NotFound means the claim was withdrawn out from under us, which a
	// later pass reconciles.
	if err := r.Update(ctx, claim); err != nil && !errors.IsNotFound(err) && !errors.IsConflict(err) {
		return err
	}
	return nil
}

// refreshClaimFreshness advances the claim's freshness marker when clusters joined after the
// last observation, reporting whether it did; provisioners use the marker to tell that the claim
// has been re-evaluated and is still wanted.
func refreshClaimFreshness(claim *kfplacementv1alpha1.ClusterClaim, mostRecent metav1.Time) bool {
	if mostRecent.IsZero() {
		return false
	}
	observed := claim.Status.LastObservedMostRecentClusterCreationTimestamp
	if observed != nil && !mostRecent.After(observed.Time) {
		return false
	}
	claim.Status.LastObservedMostRecentClusterCreationTimestamp = &mostRecent
	return true
}

// syncClaimStatus writes the freshness marker and the automatic approval in one status update,
// so that a conflict on one can never strand the other: the two are the only status fields this
// controller owns on a claim, and both are no-ops once in place, which is what terminates the
// claim-watch self-loop -- a write fires one echo reconcile, which then changes nothing here.
//
// Conflicts are expected steady-state once a provisioner co-writes the claim status; the
// provisioner's own write re-enqueues the policy through the claim watch, so the sync simply
// retries then. A NotFound means the claim was withdrawn out from under us, which a later pass
// reconciles.
func (r *Reconciler) syncClaimStatus(ctx context.Context, claim *kfplacementv1alpha1.ClusterClaim, class *kfplacementv1alpha1.ClusterProviderClass, mostRecent metav1.Time, now time.Time) error {
	refreshed := refreshClaimFreshness(claim, mostRecent)
	approved := approveAutomatically(claim, class, now)
	if !refreshed && !approved {
		return nil
	}
	if err := r.Status().Update(ctx, claim); err != nil && !errors.IsNotFound(err) && !errors.IsConflict(err) {
		return err
	}
	return nil
}

// cleanupClaims withdraws every claim belonging to a policy that is being deleted, releasing
// the cleanup finalizer once none remain. The claim count that gates the finalizer release is
// read from the API server directly, not the informer cache: a claim created moments before
// the policy deletion might not have reached the cache yet, and releasing the finalizer on a
// stale zero would orphan it permanently (nothing else ever looks at claims of a gone policy).
func (r *Reconciler) cleanupClaims(ctx context.Context, policy kfplacementv1alpha1.PlacementPolicyAccessor) error {
	if !controllerutil.ContainsFinalizer(policy, claimCleanupFinalizer) {
		return nil
	}

	// The release decision cannot rest on the ownership labels: labels are mutable, and a claim
	// whose labels were stripped would vanish from a label-selected list, releasing the
	// finalizer over a claim that still exists -- permanently orphaned, since nothing else ever
	// looks at claims of a gone policy. The claim's spec.placementPolicyRef is immutable, so
	// every claim is listed and ownership is decided by the reference.
	claims := &kfplacementv1alpha1.ClusterClaimList{}
	if err := r.uncachedReader.List(ctx, claims); err != nil {
		klog.ErrorS(err, "Failed to list cluster claims for the deleted policy", "placementPolicy", klog.KObj(policy))
		return err
	}
	ownRef := policyReference(policy)
	remaining := 0
	for i := range claims.Items {
		claim := &claims.Items[i]
		if !claimBelongsTo(claim, ownRef) {
			continue
		}
		if !claim.DeletionTimestamp.IsZero() {
			remaining++
			continue
		}
		klog.V(2).InfoS("Withdrawing a cluster claim of a deleted policy", "clusterClaim", claim.Name, "placementPolicy", klog.KObj(policy))
		if err := r.Delete(ctx, claim, client.Preconditions{UID: &claim.UID}); err != nil {
			if errors.IsNotFound(err) {
				// Already fully removed between the list and the delete; nothing remains for
				// this claim.
				continue
			}
			return err
		}
		remaining++
	}
	if remaining > 0 {
		// Claims may be held in Terminating by provisioner finalizers; the watch on claims
		// re-queues the policy as they go away.
		return nil
	}

	controllerutil.RemoveFinalizer(policy, claimCleanupFinalizer)
	// Once the finalizer is gone the policy is deleted, and the reconcile that observes its
	// absence drops the metric series.
	return r.Update(ctx, policy)
}

// ensureFinalizer adds the claim cleanup finalizer to the policy if not present yet.
func (r *Reconciler) ensureFinalizer(ctx context.Context, policy kfplacementv1alpha1.PlacementPolicyAccessor) error {
	if controllerutil.ContainsFinalizer(policy, claimCleanupFinalizer) {
		return nil
	}
	controllerutil.AddFinalizer(policy, claimCleanupFinalizer)
	return r.Update(ctx, policy)
}

// claimBelongsTo reports whether a claim's immutable back-reference names the given policy.
// The API enforces the reference's immutability, which is what makes it, unlike the ownership
// labels, fit to gate the cleanup finalizer's release.
//
// The comparison deliberately ignores the reference's version: an API promotion keeps the group,
// kind, namespace, and name, and a version-strict comparison would orphan every outstanding claim
// the moment the policy API moved to a new version. The group is part of the identity: a claim
// naming another group's PlacementPolicy is not this policy's.
func claimBelongsTo(claim *kfplacementv1alpha1.ClusterClaim, ref *kfplacementv1alpha1.ObjectReference) bool {
	got := claim.Spec.PlacementPolicyRef
	if got == nil || ref == nil {
		return false
	}
	return got.APIGroup == ref.APIGroup && got.Kind == ref.Kind && got.Name == ref.Name && got.Namespace == ref.Namespace
}

// listClaims returns the cluster claims belonging to a policy, matched on the immutable
// spec.placementPolicyRef rather than the ownership labels.
//
// The labels are mutable: a provisioner or a user that removes or rewrites one would make the claim
// vanish from a label-selected list while spec.placementPolicyRef still names the policy. The live
// reconcile would then stop tracking a claim it still owns -- never withdrawing it once the selector
// is fulfilled, changed, or switched to KeepSearching -- and a provisioner could keep acting on it.
// Matching on the immutable reference, as the cleanup path already does, keeps that from happening.
//
// The list is served from the informer cache; a claim the cache has yet to observe is picked up on
// the re-queue the claim watch fires, which is why the cache is acceptable here while the finalizer
// release, whose mistake would be permanent, reads through the uncached reader instead.
func (r *Reconciler) listClaims(ctx context.Context, policy kfplacementv1alpha1.PlacementPolicyAccessor) ([]kfplacementv1alpha1.ClusterClaim, error) {
	all := &kfplacementv1alpha1.ClusterClaimList{}
	if err := r.List(ctx, all); err != nil {
		klog.ErrorS(err, "Failed to list cluster claims for the policy", "placementPolicy", klog.KObj(policy))
		return nil, err
	}
	return claimsForPolicy(all.Items, policy), nil
}

// claimsForPolicy returns the claims whose immutable reference names the policy.
func claimsForPolicy(all []kfplacementv1alpha1.ClusterClaim, policy kfplacementv1alpha1.PlacementPolicyAccessor) []kfplacementv1alpha1.ClusterClaim {
	ref := policyReference(policy)
	owned := make([]kfplacementv1alpha1.ClusterClaim, 0, len(all))
	for i := range all {
		if claimBelongsTo(&all[i], ref) {
			owned = append(owned, all[i])
		}
	}
	return owned
}

// releaseFinalizerIfNoClaims removes the cleanup finalizer from a live policy once no claim of it
// remains, confirmed against the API server. It mirrors the release in cleanupClaims: the finalizer
// exists only to withdraw claims before the policy is deleted, so once none remain it is safe to
// drop -- and dropping it is what keeps a claim-free policy deletable even after the feature is
// turned off. The count is read uncached because releasing the finalizer over a claim a stale cache
// has yet to show would orphan it, nothing ever looking at the claims of a gone policy.
func (r *Reconciler) releaseFinalizerIfNoClaims(ctx context.Context, policy kfplacementv1alpha1.PlacementPolicyAccessor) error {
	if !controllerutil.ContainsFinalizer(policy, claimCleanupFinalizer) {
		return nil
	}
	claims := &kfplacementv1alpha1.ClusterClaimList{}
	if err := r.uncachedReader.List(ctx, claims); err != nil {
		klog.ErrorS(err, "Failed to list cluster claims to release the cleanup finalizer", "placementPolicy", klog.KObj(policy))
		return err
	}
	if len(claimsForPolicy(claims.Items, policy)) > 0 {
		return nil
	}
	controllerutil.RemoveFinalizer(policy, claimCleanupFinalizer)
	return r.Update(ctx, policy)
}

// policyReference builds the claim's back-reference to its policy.
func policyReference(policy kfplacementv1alpha1.PlacementPolicyAccessor) *kfplacementv1alpha1.ObjectReference {
	kind := kfplacementv1alpha1.PlacementPolicyKind
	if policy.GetNamespace() == "" {
		kind = kfplacementv1alpha1.ClusterPlacementPolicyKind
	}
	return &kfplacementv1alpha1.ObjectReference{
		APIGroup:   kfplacementv1alpha1.GroupVersion.Group,
		APIVersion: kfplacementv1alpha1.GroupVersion.Version,
		Kind:       kind,
		Name:       policy.GetName(),
		Namespace:  policy.GetNamespace(),
	}
}
