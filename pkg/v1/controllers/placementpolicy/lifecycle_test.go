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
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
)

var testNow = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func condAt(condType string, status metav1.ConditionStatus, reason string, at time.Time) metav1.Condition {
	return metav1.Condition{Type: condType, Status: status, Reason: reason, Message: "m", LastTransitionTime: metav1.NewTime(at)}
}

func claimWith(conditions ...metav1.Condition) *kfplacementv1alpha1.ClusterClaim {
	return &kfplacementv1alpha1.ClusterClaim{ObjectMeta: metav1.ObjectMeta{Name: "c"}, Status: kfplacementv1alpha1.ClusterClaimStatus{Conditions: conditions}}
}

var (
	approvedTrue   = condAt(kfplacementv1alpha1.ClusterClaimCondTypeApproved, metav1.ConditionTrue, kfplacementv1alpha1.ClusterClaimApprovedCondReasonApproved, testNow.Add(-time.Hour))
	denied         = condAt(kfplacementv1alpha1.ClusterClaimCondTypeApproved, metav1.ConditionFalse, kfplacementv1alpha1.ClusterClaimApprovedCondReasonDenied, testNow.Add(-time.Hour))
	acceptedTrue   = condAt(kfplacementv1alpha1.ClusterClaimCondTypeAccepted, metav1.ConditionTrue, kfplacementv1alpha1.ClusterClaimAcceptedCondReasonAccepted, testNow.Add(-time.Hour))
	completedTrue  = condAt(kfplacementv1alpha1.ClusterClaimCondTypeCompleted, metav1.ConditionTrue, kfplacementv1alpha1.ClusterClaimCompletedCondReasonFulfilled, testNow.Add(-time.Hour))
	completedFalse = condAt(kfplacementv1alpha1.ClusterClaimCondTypeCompleted, metav1.ConditionFalse, "Provisioning", testNow.Add(-time.Hour))
	failed         = condAt(kfplacementv1alpha1.ClusterClaimCondTypeCompleted, metav1.ConditionFalse, kfplacementv1alpha1.ClusterClaimCompletedCondReasonFailed, testNow.Add(-time.Hour))
	expired        = condAt(kfplacementv1alpha1.ClusterClaimCondTypeExpired, metav1.ConditionTrue, kfplacementv1alpha1.ClusterClaimExpiredCondReasonPendingTimeout, testNow.Add(-time.Hour))
)

func TestTerminalCondition(t *testing.T) {
	testCases := []struct {
		name  string
		claim *kfplacementv1alpha1.ClusterClaim
		want  string // the reason of the terminal condition, "" for none
	}{
		{name: "no conditions", claim: claimWith()},
		{name: "approved", claim: claimWith(approvedTrue)},
		{name: "denied", claim: claimWith(denied), want: kfplacementv1alpha1.ClusterClaimApprovedCondReasonDenied},
		{name: "in progress", claim: claimWith(approvedTrue, acceptedTrue, completedFalse)},
		{name: "fulfilled", claim: claimWith(approvedTrue, acceptedTrue, completedTrue)},
		{name: "failed", claim: claimWith(approvedTrue, acceptedTrue, failed), want: kfplacementv1alpha1.ClusterClaimCompletedCondReasonFailed},
		{name: "expired", claim: claimWith(approvedTrue, expired), want: kfplacementv1alpha1.ClusterClaimExpiredCondReasonPendingTimeout},
		{name: "expired wins over fulfilled", claim: claimWith(approvedTrue, completedTrue, expired), want: kfplacementv1alpha1.ClusterClaimExpiredCondReasonPendingTimeout},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := ""
			if cond := terminalCondition(tc.claim); cond != nil {
				got = cond.Reason
			}
			if got != tc.want {
				t.Errorf("terminalCondition(%v) reason = %q, want %q", tc.claim.Status.Conditions, got, tc.want)
			}
		})
	}
}

func TestClaimState(t *testing.T) {
	terminating := claimWith(approvedTrue)
	terminating.DeletionTimestamp = ptr.To(metav1.NewTime(testNow))
	finalized := claimWith(approvedTrue)
	finalized.Finalizers = []string{kfplacementv1alpha1.FulfillerFinalizer}
	testCases := []struct {
		name  string
		claim *kfplacementv1alpha1.ClusterClaim
		want  string
	}{
		{name: "pending", claim: claimWith(), want: claimStatePending},
		{name: "approved", claim: claimWith(approvedTrue), want: claimStateApproved},
		{name: "accepted by condition", claim: claimWith(approvedTrue, acceptedTrue), want: claimStateAccepted},
		{name: "accepted by finalizer alone", claim: finalized, want: claimStateAccepted},
		{name: "completed", claim: claimWith(approvedTrue, acceptedTrue, completedTrue), want: claimStateCompleted},
		{name: "terminal", claim: claimWith(approvedTrue, acceptedTrue, failed), want: claimStateTerminal},
		{name: "terminating wins", claim: terminating, want: claimStateTerminating},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := claimState(tc.claim); got != tc.want {
				t.Errorf("claimState() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestJudgeExpiry(t *testing.T) {
	cluster := &clusterv1beta1.MemberCluster{ObjectMeta: metav1.ObjectMeta{Name: "mc"}}
	always := func(*clusterv1beta1.MemberCluster) bool { return true }
	never := func(*clusterv1beta1.MemberCluster) bool { return false }
	unmatched := func(*clusterv1beta1.MemberCluster) string { return "it does not satisfy the selector's term" }
	withCluster := func(claim *kfplacementv1alpha1.ClusterClaim) *kfplacementv1alpha1.ClusterClaim {
		claim.Status.ProvisionedClusterName = ptr.To("mc")
		return claim
	}
	finalized := claimWith(approvedTrue)
	finalized.Finalizers = []string{kfplacementv1alpha1.FulfillerFinalizer}
	tenMinutes := &metav1.Duration{Duration: 10 * time.Minute}

	testCases := []struct {
		name         string
		claim        *kfplacementv1alpha1.ClusterClaim
		class        kfplacementv1alpha1.ClusterProviderClassSpec
		cluster      *clusterv1beta1.MemberCluster
		eligible     func(*clusterv1beta1.MemberCluster) bool
		wantReason   string
		wantMessage  string
		wantDeadline time.Time
	}{
		{name: "unapproved never expires", claim: claimWith()},
		{name: "denied never expires", claim: claimWith(denied)},
		{name: "approved within the default TTL", claim: claimWith(condAt(kfplacementv1alpha1.ClusterClaimCondTypeApproved, metav1.ConditionTrue, "Approved", testNow.Add(-time.Minute))), wantDeadline: testNow.Add(29 * time.Minute)},
		{name: "approved past the default TTL", claim: claimWith(approvedTrue), wantReason: kfplacementv1alpha1.ClusterClaimExpiredCondReasonPendingTimeout, wantMessage: "no provider accepted this claim within 30m0s of its approval"},
		{name: "approved past a class TTL", claim: claimWith(condAt(kfplacementv1alpha1.ClusterClaimCondTypeApproved, metav1.ConditionTrue, "Approved", testNow.Add(-11*time.Minute))), class: kfplacementv1alpha1.ClusterProviderClassSpec{PendingClaimTTL: tenMinutes}, wantReason: kfplacementv1alpha1.ClusterClaimExpiredCondReasonPendingTimeout, wantMessage: "within 10m0s"},
		{name: "approved exactly at the TTL expires", claim: claimWith(condAt(kfplacementv1alpha1.ClusterClaimCondTypeApproved, metav1.ConditionTrue, "Approved", testNow.Add(-defaultPendingClaimTTL))), wantReason: kfplacementv1alpha1.ClusterClaimExpiredCondReasonPendingTimeout},
		{name: "completed exactly at the join timeout expires", claim: withCluster(claimWith(approvedTrue, acceptedTrue, condAt(kfplacementv1alpha1.ClusterClaimCondTypeCompleted, metav1.ConditionTrue, "Fulfilled", testNow.Add(-defaultJoinTimeout)))), wantReason: kfplacementv1alpha1.ClusterClaimExpiredCondReasonJoinTimeout},
		{name: "accepted is never pending-expired", claim: claimWith(approvedTrue, acceptedTrue)},
		{name: "the provider finalizer alone counts as accepted", claim: finalized},
		{name: "completed within the join timeout", claim: withCluster(claimWith(approvedTrue, acceptedTrue, condAt(kfplacementv1alpha1.ClusterClaimCondTypeCompleted, metav1.ConditionTrue, "Fulfilled", testNow.Add(-time.Minute)))), cluster: cluster, eligible: never, wantDeadline: testNow.Add(29 * time.Minute)},
		{name: "completed, cluster never registered", claim: withCluster(claimWith(approvedTrue, acceptedTrue, completedTrue)), wantReason: kfplacementv1alpha1.ClusterClaimExpiredCondReasonJoinTimeout, wantMessage: `the cluster "mc" provisioned for this claim has not joined the fleet within 30m0s`},
		{name: "completed, cluster registered but not eligible", claim: withCluster(claimWith(approvedTrue, acceptedTrue, completedTrue)), cluster: cluster, eligible: never, wantReason: kfplacementv1alpha1.ClusterClaimExpiredCondReasonJoinTimeout},
		{name: "completed, cluster eligible but not counted", claim: withCluster(claimWith(approvedTrue, acceptedTrue, completedTrue)), cluster: cluster, eligible: always, wantReason: kfplacementv1alpha1.ClusterClaimExpiredCondReasonNotMatching, wantMessage: "it does not satisfy the selector's term"},
		{name: "completed past a class join timeout", claim: withCluster(claimWith(approvedTrue, acceptedTrue, condAt(kfplacementv1alpha1.ClusterClaimCondTypeCompleted, metav1.ConditionTrue, "Fulfilled", testNow.Add(-11*time.Minute)))), class: kfplacementv1alpha1.ClusterProviderClassSpec{JoinTimeout: tenMinutes}, wantReason: kfplacementv1alpha1.ClusterClaimExpiredCondReasonJoinTimeout},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			eligible := tc.eligible
			if eligible == nil {
				eligible = never
			}
			verdict, deadline := judgeExpiry(tc.claim, &tc.class, testNow, tc.cluster, eligible, unmatched)
			gotReason, gotMessage := "", ""
			if verdict != nil {
				gotReason, gotMessage = verdict.reason, verdict.message
			}
			if gotReason != tc.wantReason || !strings.Contains(gotMessage, tc.wantMessage) {
				t.Errorf("judgeExpiry() = %q/%q, want %q with message containing %q", gotReason, gotMessage, tc.wantReason, tc.wantMessage)
			}
			if !deadline.Equal(tc.wantDeadline) {
				t.Errorf("judgeExpiry() deadline = %v, want %v", deadline, tc.wantDeadline)
			}
		})
	}
}

func TestRetryDeadline(t *testing.T) {
	retry := kfplacementv1alpha1.ClusterProviderClassSpec{OnFailure: kfplacementv1alpha1.ClusterClaimFailureActionRetry}
	retrySoon := kfplacementv1alpha1.ClusterProviderClassSpec{OnFailure: kfplacementv1alpha1.ClusterClaimFailureActionRetry, RetryAfter: &metav1.Duration{Duration: time.Minute}}
	testCases := []struct {
		name         string
		terminal     metav1.Condition
		class        kfplacementv1alpha1.ClusterProviderClassSpec
		wantRetry    bool
		wantDeadline time.Time
	}{
		{name: "hold by default", terminal: failed},
		{name: "hold explicitly", terminal: failed, class: kfplacementv1alpha1.ClusterProviderClassSpec{OnFailure: kfplacementv1alpha1.ClusterClaimFailureActionHold}},
		{name: "retry a failure after the default", terminal: failed, class: retry, wantRetry: true, wantDeadline: failed.LastTransitionTime.Add(defaultRetryAfter)},
		{name: "retry an expiry after the class's wait", terminal: expired, class: retrySoon, wantRetry: true, wantDeadline: expired.LastTransitionTime.Add(time.Minute)},
		{name: "a denial is always held", terminal: denied, class: retry},
		{name: "a NotMatching expiry is held: its cluster is a member nobody deprovisions", terminal: condAt(kfplacementv1alpha1.ClusterClaimCondTypeExpired, metav1.ConditionTrue, kfplacementv1alpha1.ClusterClaimExpiredCondReasonNotMatching, testNow), class: retry},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			deadline, retry := retryDeadline(&tc.terminal, &tc.class)
			if retry != tc.wantRetry || !deadline.Equal(tc.wantDeadline) {
				t.Errorf("retryDeadline(%s) = %v, %t, want %v, %t", tc.terminal.Reason, deadline, retry, tc.wantDeadline, tc.wantRetry)
			}
		})
	}
}

func TestHeldNoteBounded(t *testing.T) {
	long := &metav1.Condition{Type: kfplacementv1alpha1.ClusterClaimCondTypeCompleted, Reason: kfplacementv1alpha1.ClusterClaimCompletedCondReasonFailed, Message: strings.Repeat("x", 4096)}
	got := heldNote(claimWith(), long)
	if len(got) > maxHeldMessageLength+128 || !strings.HasSuffix(got, "...") {
		t.Errorf("heldNote() = %d chars ending %q, want the message truncated to %d with an ellipsis", len(got), got[len(got)-8:], maxHeldMessageLength)
	}
}

func TestWhyUnmatched(t *testing.T) {
	terms := []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{
		{MatchLabels: map[string]string{"env": "prod"}},
		{MatchLabels: map[string]string{regionLabel: "eastus"}},
	}
	taint := clusterv1beta1.Taint{Key: "dedicated", Value: "x", Effect: "NoSchedule"}
	testCases := []struct {
		name        string
		cluster     *clusterv1beta1.MemberCluster
		tolerations []kfplacementv1alpha1.Toleration
		want        string
	}{
		{name: "taint not tolerated", cluster: &clusterv1beta1.MemberCluster{Spec: clusterv1beta1.MemberClusterSpec{Taints: []clusterv1beta1.Taint{taint}}}, want: "its taints are not tolerated by the policy"},
		{name: "no term satisfied", cluster: &clusterv1beta1.MemberCluster{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"env": "dev", regionLabel: "westus"}}}, want: "it satisfies none of the selector's 2 terms"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := whyUnmatched(tc.cluster, terms, tc.tolerations); got != tc.want {
				t.Errorf("whyUnmatched() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClaimLimit(t *testing.T) {
	testCases := []struct {
		name   string
		policy *int32
		fleet  int32
		want   int32
	}{
		{name: "defaults", fleet: 1, want: 1},
		{name: "policy below the fleet", policy: ptr.To[int32](2), fleet: 5, want: 2},
		{name: "policy above the fleet", policy: ptr.To[int32](5), fleet: 2, want: 2},
		{name: "no policy limit, larger fleet", fleet: 3, want: 1},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewReconciler(nil, nil, snapshotStub{}, nil, WithMaxConcurrentClusterClaims(tc.fleet))
			policy := &kfplacementv1alpha1.PlacementPolicy{Spec: kfplacementv1alpha1.PlacementPolicySpec{MaxConcurrentClusterClaims: tc.policy}}
			if got := r.claimLimit(policy); got != tc.want {
				t.Errorf("claimLimit(policy=%v, fleet=%d) = %d, want %d", tc.policy, tc.fleet, got, tc.want)
			}
		})
	}
	if r := NewReconciler(nil, nil, snapshotStub{}, nil, WithMaxConcurrentClusterClaims(0)); r.maxConcurrentClaims != defaultMaxConcurrentClaims {
		t.Errorf("WithMaxConcurrentClusterClaims(0) set the fleet limit to %d, want the default %d kept", r.maxConcurrentClaims, defaultMaxConcurrentClaims)
	}
}
