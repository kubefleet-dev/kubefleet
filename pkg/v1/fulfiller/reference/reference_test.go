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

package reference

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/v1/fulfiller"
)

func TestNewDefaults(t *testing.T) {
	p := New(nil, Options{})
	if p.currentBehaviour() != Fulfill || p.opts.JoinTarget != JoinMemberCluster || p.opts.HeartbeatInterval != defaultHeartbeatInterval {
		t.Errorf("New(Options{}) = behaviour %q, target %q, heartbeat %v; want %q, %q, %v", p.currentBehaviour(), p.opts.JoinTarget, p.opts.HeartbeatInterval, Fulfill, JoinMemberCluster, defaultHeartbeatInterval)
	}
	p.SetBehaviour(NeverJoin)
	if got := p.currentBehaviour(); got != NeverJoin {
		t.Errorf("after SetBehaviour(NeverJoin) the behaviour is %q", got)
	}
}

// TestSetAgentStatus pins that a heartbeat refresh keeps the join's transition time, so that the
// hub never mistakes a refresh for a re-join, and that a first stamp adds the entry.
func TestSetAgentStatus(t *testing.T) {
	earlier := metav1.NewTime(time.Date(2026, 10, 6, 11, 0, 0, 0, time.UTC))
	later := metav1.NewTime(earlier.Add(time.Minute))
	agentAt := func(at metav1.Time) clusterv1beta1.AgentStatus {
		return clusterv1beta1.AgentStatus{
			Type:                  clusterv1beta1.MemberAgent,
			LastReceivedHeartbeat: at,
			Conditions: []metav1.Condition{
				{Type: string(clusterv1beta1.AgentJoined), Status: metav1.ConditionTrue, Reason: joinedReason, LastTransitionTime: at},
				{Type: string(clusterv1beta1.AgentHealthy), Status: metav1.ConditionTrue, Reason: healthyReason, LastTransitionTime: at},
			},
		}
	}
	other := clusterv1beta1.AgentStatus{Type: clusterv1beta1.MultiClusterServiceAgent}

	testCases := []struct {
		name     string
		existing []clusterv1beta1.AgentStatus
		want     []clusterv1beta1.AgentStatus
	}{
		{name: "first stamp appends", existing: nil, want: []clusterv1beta1.AgentStatus{agentAt(later)}},
		{name: "refresh keeps the transition times and moves the heartbeat", existing: []clusterv1beta1.AgentStatus{other, agentAt(earlier)}, want: []clusterv1beta1.AgentStatus{other, {
			Type:                  clusterv1beta1.MemberAgent,
			LastReceivedHeartbeat: later,
			Conditions:            agentAt(earlier).Conditions,
		}}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := append([]clusterv1beta1.AgentStatus(nil), tc.existing...)
			setAgentStatus(&got, agentAt(later))
			if diff := cmp.Diff(got, tc.want); diff != "" {
				t.Errorf("setAgentStatus() mismatch (-got, +want):\n%s", diff)
			}
		})
	}
}

func TestLabelsFor(t *testing.T) {
	req := fulfiller.Request{
		ClaimName: "app-0-abc", ClaimUID: "uid-1",
		ClusterSelectorTerms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{
			{MatchLabels: map[string]string{"topology.kubernetes.io/region": "eastus", kfplacementv1alpha1.FulfilledClaimUIDLabel: "forged"}},
			{MatchLabels: map[string]string{"env": "prod"}},
		},
	}
	got := labelsFor(req, true)
	want := map[string]string{
		"topology.kubernetes.io/region": "eastus",
		"env":                           "prod",
		kfplacementv1alpha1.FulfilledClaimNameLabel: "app-0-abc",
		kfplacementv1alpha1.FulfilledClaimUIDLabel:  "uid-1",
		SimulatedJoinLabel:                          "true",
	}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Errorf("labelsFor() mismatch (-got, +want):\n%s", diff)
	}
	if _, simulated := labelsFor(req, false)[SimulatedJoinLabel]; simulated {
		t.Errorf("labelsFor(simulate=false) carries the simulated-join label")
	}
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clusterv1beta1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}

// TestProvisionRules pins the rules a Provision call applies before it registers anything: the
// behaviours, the identity parameter, and a name taken by another claim's cluster.
func TestProvisionRules(t *testing.T) {
	foreign := &clusterv1beta1.MemberCluster{ObjectMeta: metav1.ObjectMeta{Name: "taken", Labels: map[string]string{kfplacementv1alpha1.FulfilledClaimUIDLabel: "someone-else"}}}
	req := func(name string) fulfiller.Request {
		return fulfiller.Request{ClaimName: "c", ClaimUID: "uid-1", ClusterName: name, ClusterProviderClassName: "class", Parameters: map[string]string{IdentityParameter: "sa"}}
	}
	testCases := []struct {
		name          string
		behaviour     Behaviour
		req           fulfiller.Request
		wantErr       bool
		wantPermanent bool
		wantCluster   bool
	}{
		{name: "fulfill registers the cluster", behaviour: Fulfill, req: req("fresh"), wantCluster: true},
		{name: "transient failure", behaviour: FailTransient, req: req("fresh"), wantErr: true},
		{name: "ignore reports in progress", behaviour: Ignore, req: req("fresh"), wantErr: true},
		{name: "permanent failure", behaviour: FailPermanent, req: req("fresh"), wantErr: true, wantPermanent: true},
		{name: "never join registers without a join", behaviour: NeverJoin, req: req("fresh"), wantCluster: true},
		{name: "no identity parameter is permanent", behaviour: Fulfill, req: fulfiller.Request{ClaimName: "c", ClaimUID: "uid-1", ClusterName: "fresh"}, wantErr: true, wantPermanent: true},
		{name: "a name taken by another claim's cluster is permanent", behaviour: Fulfill, req: req("taken"), wantErr: true, wantPermanent: true},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(foreign).WithStatusSubresource(&clusterv1beta1.MemberCluster{}).Build()
			p := New(c, Options{Behaviour: tc.behaviour, SimulateJoin: true})
			err := p.Provision(context.Background(), tc.req)
			if (err != nil) != tc.wantErr || fulfiller.IsPermanent(err) != tc.wantPermanent {
				t.Errorf("Provision() = %v (permanent=%t), want error=%t permanent=%t", err, fulfiller.IsPermanent(err), tc.wantErr, tc.wantPermanent)
			}
			mc := &clusterv1beta1.MemberCluster{}
			exists := c.Get(context.Background(), client.ObjectKey{Name: "fresh"}, mc) == nil
			if exists != tc.wantCluster {
				t.Errorf("Provision() registered the cluster: %t, want %t", exists, tc.wantCluster)
			}
			if exists && (len(mc.Status.AgentStatus) > 0) != (tc.behaviour == Fulfill) {
				t.Errorf("Provision() wrote an agent status: %t, want %t for %s", len(mc.Status.AgentStatus) > 0, tc.behaviour == Fulfill, tc.behaviour)
			}
		})
	}
}

// TestDeprovisionAndRemediateOwnership pins that the provider never deletes a cluster that is not
// the claim's own, and repairs only clusters whose join it simulates.
func TestDeprovisionAndRemediateOwnership(t *testing.T) {
	own := &clusterv1beta1.MemberCluster{ObjectMeta: metav1.ObjectMeta{Name: "own", Labels: map[string]string{kfplacementv1alpha1.FulfilledClaimUIDLabel: "uid-1", SimulatedJoinLabel: "true"}}}
	foreign := &clusterv1beta1.MemberCluster{ObjectMeta: metav1.ObjectMeta{Name: "foreign", Labels: map[string]string{kfplacementv1alpha1.FulfilledClaimUIDLabel: "uid-2"}}}
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(own, foreign).WithStatusSubresource(&clusterv1beta1.MemberCluster{}).Build()
	p := New(c, Options{SimulateJoin: true})
	ctx := context.Background()

	if err := p.Deprovision(ctx, fulfiller.Request{ClaimUID: "uid-1", ClusterName: "foreign"}); err != nil {
		t.Errorf("Deprovision() of another claim's cluster = %v, want nil and the cluster kept", err)
	}
	if err := c.Get(ctx, client.ObjectKey{Name: "foreign"}, &clusterv1beta1.MemberCluster{}); err != nil {
		t.Errorf("Deprovision() removed another claim's cluster: %v", err)
	}
	if err := p.Deprovision(ctx, fulfiller.Request{ClaimUID: "uid-1", ClusterName: "own"}); err != nil {
		t.Errorf("Deprovision() of the own cluster = %v, want nil", err)
	}
	if err := c.Get(ctx, client.ObjectKey{Name: "own"}, &clusterv1beta1.MemberCluster{}); !apierrors.IsNotFound(err) {
		t.Errorf("Deprovision() left the own cluster behind: %v", err)
	}
	if err := p.Deprovision(ctx, fulfiller.Request{ClaimUID: "uid-1", ClusterName: "gone"}); err != nil {
		t.Errorf("Deprovision() of a cluster already gone = %v, want nil", err)
	}

	err := p.Remediate(ctx, fulfiller.Request{}, foreign)
	if !fulfiller.IsPermanent(err) {
		t.Errorf("Remediate() of a cluster without a simulated join = %v, want a permanent error", err)
	}
	simulated := &clusterv1beta1.MemberCluster{ObjectMeta: metav1.ObjectMeta{Name: "simulated", Labels: map[string]string{SimulatedJoinLabel: "true"}}}
	if err := c.Create(ctx, simulated); err != nil {
		t.Fatal(err)
	}
	if err := p.Remediate(ctx, fulfiller.Request{}, simulated); err != nil {
		t.Errorf("Remediate() of a simulated cluster = %v, want nil", err)
	}
	if err := c.Get(ctx, client.ObjectKey{Name: "simulated"}, simulated); err != nil || len(simulated.Status.AgentStatus) == 0 {
		t.Errorf("Remediate() did not re-stamp the agent status (err=%v)", err)
	}
	var permanent interface{ Unwrap() error }
	if errors.As(fulfiller.Permanent(errors.New("x")), &permanent); permanent == nil {
		t.Errorf("Permanent errors should unwrap")
	}
}
