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
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
)

// The class's lifecycle knobs default per the API doc when unset.
const (
	defaultPendingClaimTTL = 30 * time.Minute
	defaultJoinTimeout     = 30 * time.Minute
	defaultRetryAfter      = 15 * time.Minute
)

// Claim states, as the active-claims metric reports them. A claim is in the first state of this
// list that describes it.
const (
	claimStateTerminating = "terminating"
	claimStateTerminal    = "terminal"
	claimStateCompleted   = "completed"
	claimStateAccepted    = "accepted"
	claimStateApproved    = "approved"
	claimStatePending     = "pending"
)

var claimStates = []string{claimStateTerminating, claimStateTerminal, claimStateCompleted, claimStateAccepted, claimStateApproved, claimStatePending}

func durationOrDefault(d *metav1.Duration, fallback time.Duration) time.Duration {
	if d == nil {
		return fallback
	}
	return d.Duration
}

// terminalCondition returns the condition that made a claim terminal -- Completed=False/Failed,
// Expired=True, or Approved=False/Denied -- or nil for a claim that is still in flight. A terminal
// claim is the record of what happened: it keeps its name and its per-policy slot, so that the
// same selector is not re-claimed while the record stands.
func terminalCondition(claim *kfplacementv1alpha1.ClusterClaim) *metav1.Condition {
	conditions := claim.Status.Conditions
	if cond := meta.FindStatusCondition(conditions, kfplacementv1alpha1.ClusterClaimCondTypeExpired); cond != nil && cond.Status == metav1.ConditionTrue {
		return cond
	}
	if cond := meta.FindStatusCondition(conditions, kfplacementv1alpha1.ClusterClaimCondTypeCompleted); cond != nil && cond.Status == metav1.ConditionFalse && cond.Reason == kfplacementv1alpha1.ClusterClaimCompletedCondReasonFailed {
		return cond
	}
	if cond := meta.FindStatusCondition(conditions, kfplacementv1alpha1.ClusterClaimCondTypeApproved); cond != nil && cond.Status == metav1.ConditionFalse && cond.Reason == kfplacementv1alpha1.ClusterClaimApprovedCondReasonDenied {
		return cond
	}
	return nil
}

// accepted reports whether a provider has taken the claim: the Accepted condition, or the
// provider's finalizer alone, which lands first and counts on its own so that a provider stopped
// between its two acceptance writes never sees its claim expire under it.
func accepted(claim *kfplacementv1alpha1.ClusterClaim) bool {
	return meta.IsStatusConditionTrue(claim.Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeAccepted) ||
		controllerutil.ContainsFinalizer(claim, kfplacementv1alpha1.FulfillerFinalizer)
}

// claimState names the state of a claim for the active-claims metric.
func claimState(claim *kfplacementv1alpha1.ClusterClaim) string {
	switch {
	case !claim.DeletionTimestamp.IsZero():
		return claimStateTerminating
	case terminalCondition(claim) != nil:
		return claimStateTerminal
	case meta.IsStatusConditionTrue(claim.Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeCompleted):
		return claimStateCompleted
	case accepted(claim):
		return claimStateAccepted
	case meta.IsStatusConditionTrue(claim.Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeApproved):
		return claimStateApproved
	default:
		return claimStatePending
	}
}

// expiryVerdict is an Expired condition the policy controller is about to stamp.
type expiryVerdict struct {
	reason, message string
}

// judgeExpiry decides whether a kept, non-terminal claim has run out of time under its class,
// and when it next could. The deadline is zero when no timer applies to the claim's state.
//
// A completed claim is given joinTimeout from completion for its cluster to be counted for the
// selector (a counted cluster rotates the claim before this is reached): a cluster that is not
// eligible by then -- never registered, or registered and not joined -- expires as JoinTimeout,
// which tells the provider to deprovision it if it never joined; an eligible cluster that the
// selector still does not count expires as NotMatching, which tells the provider to leave the
// cluster alone since it is a member in its own right. An approved claim is given pendingClaimTTL
// from approval for a provider to accept it. An unapproved claim never expires: approval is the
// brake, and an unapproved claim is inert.
func judgeExpiry(claim *kfplacementv1alpha1.ClusterClaim, class *kfplacementv1alpha1.ClusterProviderClassSpec, now time.Time, cluster *clusterv1beta1.MemberCluster, eligible func(*clusterv1beta1.MemberCluster) bool, unmatched func(*clusterv1beta1.MemberCluster) string) (*expiryVerdict, time.Time) {
	conditions := claim.Status.Conditions
	if completed := meta.FindStatusCondition(conditions, kfplacementv1alpha1.ClusterClaimCondTypeCompleted); completed != nil && completed.Status == metav1.ConditionTrue {
		timeout := durationOrDefault(class.JoinTimeout, defaultJoinTimeout)
		deadline := completed.LastTransitionTime.Add(timeout)
		if now.Before(deadline) {
			return nil, deadline
		}
		name := ""
		if claim.Status.ProvisionedClusterName != nil {
			name = *claim.Status.ProvisionedClusterName
		}
		if cluster == nil || !eligible(cluster) {
			return &expiryVerdict{
				reason:  kfplacementv1alpha1.ClusterClaimExpiredCondReasonJoinTimeout,
				message: fmt.Sprintf("the cluster %q provisioned for this claim has not joined the fleet within %s of completion", name, timeout),
			}, time.Time{}
		}
		return &expiryVerdict{
			reason:  kfplacementv1alpha1.ClusterClaimExpiredCondReasonNotMatching,
			message: fmt.Sprintf("the cluster %q provisioned for this claim joined the fleet but was not counted for the selector within %s of completion: %s", name, timeout, unmatched(cluster)),
		}, time.Time{}
	}
	approved := meta.FindStatusCondition(conditions, kfplacementv1alpha1.ClusterClaimCondTypeApproved)
	if approved == nil || approved.Status != metav1.ConditionTrue || accepted(claim) {
		return nil, time.Time{}
	}
	ttl := durationOrDefault(class.PendingClaimTTL, defaultPendingClaimTTL)
	deadline := approved.LastTransitionTime.Add(ttl)
	if now.Before(deadline) {
		return nil, deadline
	}
	return &expiryVerdict{
		reason:  kfplacementv1alpha1.ClusterClaimExpiredCondReasonPendingTimeout,
		message: fmt.Sprintf("no provider accepted this claim within %s of its approval", ttl),
	}, time.Time{}
}

// retryDeadline returns when a terminal claim of the class is withdrawn so that a fresh claim can
// be issued, and false when it is held instead. A class that holds on failure holds everything.
// A class that retries still holds two records: a denial, since a human said no, and a
// NotMatching expiry, since its cluster is a member in its own right that nobody deprovisions --
// retrying would provision another cluster every retryAfter for as long as the mismatch lasts.
// Only a selector or class change, an admin deleting the record, or (for NotMatching) relabelling
// the cluster onto the selector moves those on.
func retryDeadline(terminal *metav1.Condition, class *kfplacementv1alpha1.ClusterProviderClassSpec) (time.Time, bool) {
	if class.OnFailure != kfplacementv1alpha1.ClusterClaimFailureActionRetry ||
		terminal.Reason == kfplacementv1alpha1.ClusterClaimApprovedCondReasonDenied ||
		terminal.Reason == kfplacementv1alpha1.ClusterClaimExpiredCondReasonNotMatching {
		return time.Time{}, false
	}
	return terminal.LastTransitionTime.Add(durationOrDefault(class.RetryAfter, defaultRetryAfter)), true
}

// maxHeldMessageLength bounds the provider-written message a held note embeds, so that a policy
// holding several records never overflows its Scheduled condition's message.
const maxHeldMessageLength = 256

// heldNote says, for the policy's Scheduled message, why a terminal claim is being held.
func heldNote(claim *kfplacementv1alpha1.ClusterClaim, terminal *metav1.Condition) string {
	message := terminal.Message
	if len(message) > maxHeldMessageLength {
		message = message[:maxHeldMessageLength-3] + "..."
	}
	return fmt.Sprintf("cluster claim %q is held as %s/%s: %s", claim.Name, terminal.Type, terminal.Reason, message)
}

// whyUnmatched explains, for a NotMatching expiry, why an eligible cluster is not counted for a
// selector: its taints are not tolerated, or -- the terms being ORed -- it satisfies none of them.
func whyUnmatched(cluster *clusterv1beta1.MemberCluster, terms []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm, tolerations []kfplacementv1alpha1.Toleration) string {
	if !taintsTolerated(cluster.Spec.Taints, tolerations) {
		return "its taints are not tolerated by the policy"
	}
	if len(terms) == 1 {
		return "it does not satisfy the selector's term"
	}
	return fmt.Sprintf("it satisfies none of the selector's %d terms", len(terms))
}
