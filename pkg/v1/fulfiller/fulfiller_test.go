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
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/naming"
)

func claimNamed(name string, uid types.UID) *kfplacementv1alpha1.ClusterClaim {
	return &kfplacementv1alpha1.ClusterClaim{ObjectMeta: metav1.ObjectMeta{Name: name, UID: uid}}
}

func TestIsPermanent(t *testing.T) {
	base := errors.New("quota exceeded")
	testCases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "plain error is transient", err: base, want: false},
		{name: "marked error is permanent", err: Permanent(base), want: true},
		{name: "wrapped marked error is permanent", err: fmt.Errorf("provision: %w", Permanent(base)), want: true},
		{name: "nil is not permanent", err: nil, want: false},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsPermanent(tc.err); got != tc.want {
				t.Errorf("IsPermanent(%v) = %t, want %t", tc.err, got, tc.want)
			}
		})
	}
}

// TestClusterNameFor pins the shape of the derived cluster name: a DNS label short enough for the
// fleet-member-<name> namespace the hub creates, stable for one claim, and distinct for two claims
// that KubeFleet issued under the same name. The cases check properties of the name rather than an
// exact value, since the exact value is a hash.
func TestClusterNameFor(t *testing.T) {
	long := strings.Repeat("a", 60) + "." + strings.Repeat("b", 60) + "-0-" + strings.Repeat("c", 80)
	testCases := []struct {
		name  string
		claim *kfplacementv1alpha1.ClusterClaim
	}{
		{name: "short claim name", claim: claimNamed("app-0-abcdef0123456789", "uid-1")},
		{name: "long dotted claim name", claim: claimNamed(long, "uid-2")},
		{name: "separator at the truncation point", claim: claimNamed(strings.Repeat("a", 32)+"."+strings.Repeat("b", 50), "uid-3")},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClusterNameFor(tc.claim)
			if errs := validation.IsDNS1123Label(got); len(errs) > 0 {
				t.Errorf("ClusterNameFor() = %q, want a DNS label, got errors %v", got, errs)
			}
			if suffix := got[strings.LastIndex(got, "-")+1:]; len(suffix) != naming.HashLength {
				t.Errorf("ClusterNameFor() = %q ends in a hash of %d characters, want the full %d", got, len(suffix), naming.HashLength)
			}
			if len(got) > 50 {
				t.Errorf("ClusterNameFor() = %q has length %d, want at most 50 so that fleet-member-%s fits a DNS label", got, len(got), got)
			}
			if again := ClusterNameFor(tc.claim); again != got {
				t.Errorf("ClusterNameFor() = %q on the second call, want the deterministic %q", again, got)
			}
		})
	}

	first, second := claimNamed("app-0-abc", "uid-1"), claimNamed("app-0-abc", "uid-2")
	if a, b := ClusterNameFor(first), ClusterNameFor(second); a == b {
		t.Errorf("ClusterNameFor() = %q for two claims of the same name and different UIDs, want distinct names", a)
	}
}

func TestStale(t *testing.T) {
	now := metav1.Now()
	earlier := metav1.NewTime(now.Add(-time.Hour))
	clusterCreatedAt := func(at metav1.Time) clusterv1beta1.MemberCluster {
		return clusterv1beta1.MemberCluster{ObjectMeta: metav1.ObjectMeta{Name: "c", CreationTimestamp: at}}
	}
	claimObservedAt := func(at *metav1.Time) *kfplacementv1alpha1.ClusterClaim {
		claim := claimNamed("c", "u")
		claim.Status.LastObservedMostRecentClusterCreationTimestamp = at
		return claim
	}
	testCases := []struct {
		name     string
		claim    *kfplacementv1alpha1.ClusterClaim
		clusters []clusterv1beta1.MemberCluster
		want     bool
	}{
		{name: "unset marker in an empty fleet is fresh", claim: claimObservedAt(nil), want: false},
		{name: "unset marker with clusters is fresh", claim: claimObservedAt(nil), clusters: []clusterv1beta1.MemberCluster{clusterCreatedAt(now)}, want: false},
		{name: "marker newer than every cluster is fresh", claim: claimObservedAt(&now), clusters: []clusterv1beta1.MemberCluster{clusterCreatedAt(earlier)}, want: false},
		{name: "a cluster newer than the marker is stale", claim: claimObservedAt(&earlier), clusters: []clusterv1beta1.MemberCluster{clusterCreatedAt(now)}, want: true},
		{name: "a cluster created at the marker's own second is fresh", claim: claimObservedAt(&now), clusters: []clusterv1beta1.MemberCluster{clusterCreatedAt(now)}, want: false},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stale(tc.claim, tc.clusters); got != tc.want {
				t.Errorf("stale(marker=%v, clusters=%v) = %t, want %t", tc.claim.Status.LastObservedMostRecentClusterCreationTimestamp, tc.clusters, got, tc.want)
			}
		})
	}
}

func TestJoined(t *testing.T) {
	agentStatus := func(joined metav1.ConditionStatus, generation int64) []clusterv1beta1.AgentStatus {
		return []clusterv1beta1.AgentStatus{{
			Type:       clusterv1beta1.MemberAgent,
			Conditions: []metav1.Condition{{Type: string(clusterv1beta1.AgentJoined), Status: joined, ObservedGeneration: generation}},
		}}
	}
	testCases := []struct {
		name    string
		cluster clusterv1beta1.MemberCluster
		want    bool
	}{
		{name: "no agent status", want: false},
		{name: "joined", cluster: clusterv1beta1.MemberCluster{Status: clusterv1beta1.MemberClusterStatus{AgentStatus: agentStatus(metav1.ConditionTrue, 1)}}, want: true},
		{name: "joined with a stale observed generation", cluster: clusterv1beta1.MemberCluster{ObjectMeta: metav1.ObjectMeta{Generation: 7}, Status: clusterv1beta1.MemberClusterStatus{AgentStatus: agentStatus(metav1.ConditionTrue, 1)}}, want: true},
		{name: "left", cluster: clusterv1beta1.MemberCluster{Status: clusterv1beta1.MemberClusterStatus{AgentStatus: agentStatus(metav1.ConditionFalse, 1)}}, want: false},
		{name: "join unknown", cluster: clusterv1beta1.MemberCluster{Status: clusterv1beta1.MemberClusterStatus{AgentStatus: agentStatus(metav1.ConditionUnknown, 1)}}, want: false},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := joined(&tc.cluster); got != tc.want {
				t.Errorf("joined(%v) = %t, want %t", tc.cluster.Status.AgentStatus, got, tc.want)
			}
		})
	}
}

func TestIsTerminal(t *testing.T) {
	cond := func(condType string, status metav1.ConditionStatus, reason string) metav1.Condition {
		return metav1.Condition{Type: condType, Status: status, Reason: reason}
	}
	testCases := []struct {
		name       string
		conditions []metav1.Condition
		want       bool
	}{
		{name: "no conditions", want: false},
		{name: "failed", conditions: []metav1.Condition{cond(kfplacementv1alpha1.ClusterClaimCondTypeCompleted, metav1.ConditionFalse, kfplacementv1alpha1.ClusterClaimCompletedCondReasonFailed)}, want: true},
		{name: "fulfilled", conditions: []metav1.Condition{cond(kfplacementv1alpha1.ClusterClaimCondTypeCompleted, metav1.ConditionTrue, kfplacementv1alpha1.ClusterClaimCompletedCondReasonFulfilled)}, want: false},
		{name: "denied", conditions: []metav1.Condition{cond(kfplacementv1alpha1.ClusterClaimCondTypeApproved, metav1.ConditionFalse, kfplacementv1alpha1.ClusterClaimApprovedCondReasonDenied)}, want: true},
		{name: "approved", conditions: []metav1.Condition{cond(kfplacementv1alpha1.ClusterClaimCondTypeApproved, metav1.ConditionTrue, kfplacementv1alpha1.ClusterClaimApprovedCondReasonApproved)}, want: false},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			claim := claimNamed("c", "u")
			claim.Status.Conditions = tc.conditions
			if got := isTerminal(claim); got != tc.want {
				t.Errorf("isTerminal(%v) = %t, want %t", tc.conditions, got, tc.want)
			}
		})
	}
}

// TestControllerName checks that two provisioner names that sanitize alike still get distinct
// controller names, since controller-runtime refuses to start two controllers of one name.
func TestControllerName(t *testing.T) {
	a, b := controllerName("infra.example.dev"), controllerName("infra-example.dev")
	if a == b {
		t.Errorf("controllerName() = %q for both infra.example.dev and infra-example.dev, want distinct names", a)
	}
	if again := controllerName("infra.example.dev"); again != a {
		t.Errorf("controllerName(infra.example.dev) = %q on the second call, want the deterministic %q", again, a)
	}
}
