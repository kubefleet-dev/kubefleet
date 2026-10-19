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
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
)

func claimWithRef(ref *kfplacementv1alpha1.ObjectReference) *kfplacementv1alpha1.ClusterClaim {
	return &kfplacementv1alpha1.ClusterClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim"},
		Spec:       kfplacementv1alpha1.ClusterClaimSpec{PlacementPolicyRef: ref},
	}
}

// TestMapClaimToPolicies pins the claim-to-policy mapping to spec.placementPolicyRef, which is
// the only identity that survives policy names longer than a label value can hold.
func TestMapClaimToPolicies(t *testing.T) {
	longName := strings.Repeat("x", 250)

	testCases := []struct {
		name              string
		obj               client.Object
		wantNamespaced    []reconcile.Request
		wantClusterScoped []reconcile.Request
	}{
		{
			name:           "namespaced policy reference",
			obj:            claimWithRef(&kfplacementv1alpha1.ObjectReference{Name: "app", Namespace: "tenant-a"}),
			wantNamespaced: []reconcile.Request{{NamespacedName: types.NamespacedName{Name: "app", Namespace: "tenant-a"}}},
		},
		{
			name:              "cluster-scoped policy reference",
			obj:               claimWithRef(&kfplacementv1alpha1.ObjectReference{Name: "app"}),
			wantClusterScoped: []reconcile.Request{{NamespacedName: types.NamespacedName{Name: "app"}}},
		},
		{
			name:           "name longer than a label value still maps",
			obj:            claimWithRef(&kfplacementv1alpha1.ObjectReference{Name: longName, Namespace: "tenant-a"}),
			wantNamespaced: []reconcile.Request{{NamespacedName: types.NamespacedName{Name: longName, Namespace: "tenant-a"}}},
		},
		{
			name: "claim without a policy reference maps to nothing",
			obj:  claimWithRef(nil),
		},
		{
			name: "reference without a name maps to nothing",
			obj:  claimWithRef(&kfplacementv1alpha1.ObjectReference{Namespace: "tenant-a"}),
		},
		{
			name: "object of another kind maps to nothing",
			obj:  &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm", Namespace: "tenant-a"}},
		},
	}

	r := &Reconciler{}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotNamespaced := r.mapClaimToPlacementPolicy(context.Background(), tc.obj)
			if diff := cmp.Diff(gotNamespaced, tc.wantNamespaced); diff != "" {
				t.Errorf("mapClaimToPlacementPolicy() mismatch (-got, +want):\n%s", diff)
			}
			gotClusterScoped := r.mapClaimToClusterPlacementPolicy(context.Background(), tc.obj)
			if diff := cmp.Diff(gotClusterScoped, tc.wantClusterScoped); diff != "" {
				t.Errorf("mapClaimToClusterPlacementPolicy() mismatch (-got, +want):\n%s", diff)
			}
		})
	}
}

// TestMemberClusterSchedulingRelevantChanges pins which member cluster updates reach the policy
// reconciler: routine heartbeat and observation timestamps must not, stale-to-fresh heartbeat
// recoveries and anything scheduling reads must.
func TestMemberClusterSchedulingRelevantChanges(t *testing.T) {
	now := metav1.Now()
	timeout := 5 * time.Minute

	base := func() *clusterv1beta1.MemberCluster {
		return &clusterv1beta1.MemberCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "member", Labels: map[string]string{"region": "eastus"}},
			Spec:       clusterv1beta1.MemberClusterSpec{Taints: []clusterv1beta1.Taint{{Key: "k", Value: "v", Effect: corev1.TaintEffectNoSchedule}}},
			Status: clusterv1beta1.MemberClusterStatus{
				AgentStatus: []clusterv1beta1.AgentStatus{{
					Type:                  clusterv1beta1.MemberAgent,
					Conditions:            []metav1.Condition{{Type: string(clusterv1beta1.AgentJoined), Status: metav1.ConditionTrue, LastTransitionTime: now}},
					LastReceivedHeartbeat: metav1.NewTime(now.Add(-time.Minute)), // fresh heartbeat (1m ago)
				}},
				Properties: map[clusterv1beta1.PropertyName]clusterv1beta1.PropertyValue{"node-count": {Value: "3", ObservationTime: now}},
			},
		}
	}

	testCases := []struct {
		name      string
		oldMutate func(*clusterv1beta1.MemberCluster)
		newMutate func(*clusterv1beta1.MemberCluster)
		want      bool
	}{
		{
			name: "heartbeat only: fresh-to-fresh remains filtered",
			newMutate: func(mc *clusterv1beta1.MemberCluster) {
				mc.Status.AgentStatus[0].LastReceivedHeartbeat = now
				mc.Status.Properties["node-count"] = clusterv1beta1.PropertyValue{Value: "3", ObservationTime: now}
			},
			want: false,
		},
		{
			name: "stale-to-fresh heartbeat update is admitted",
			oldMutate: func(mc *clusterv1beta1.MemberCluster) {
				// Old heartbeat was received 6 minutes ago (> 5m timeout).
				mc.Status.AgentStatus[0].LastReceivedHeartbeat = metav1.NewTime(now.Add(-6 * time.Minute))
			},
			newMutate: func(mc *clusterv1beta1.MemberCluster) {
				// New heartbeat is fresh (now).
				mc.Status.AgentStatus[0].LastReceivedHeartbeat = now
			},
			want: true,
		},
		{
			name: "stale-to-stale heartbeat update does not trigger recovery",
			oldMutate: func(mc *clusterv1beta1.MemberCluster) {
				// Old heartbeat was received 10 minutes ago.
				mc.Status.AgentStatus[0].LastReceivedHeartbeat = metav1.NewTime(now.Add(-10 * time.Minute))
			},
			newMutate: func(mc *clusterv1beta1.MemberCluster) {
				// New heartbeat advanced to 7 minutes ago, but is still stale (> 5m).
				mc.Status.AgentStatus[0].LastReceivedHeartbeat = metav1.NewTime(now.Add(-7 * time.Minute))
			},
			want: false,
		},
		{
			name: "missing old heartbeat to fresh heartbeat is admitted",
			oldMutate: func(mc *clusterv1beta1.MemberCluster) {
				mc.Status.AgentStatus[0].LastReceivedHeartbeat = metav1.Time{}
			},
			newMutate: func(mc *clusterv1beta1.MemberCluster) {
				mc.Status.AgentStatus[0].LastReceivedHeartbeat = now
			},
			want: true,
		},
		{
			name:      "label change",
			newMutate: func(mc *clusterv1beta1.MemberCluster) { mc.Labels["region"] = "westus" },
			want:      true,
		},
		{
			name:      "taint change",
			newMutate: func(mc *clusterv1beta1.MemberCluster) { mc.Spec.Taints = nil },
			want:      true,
		},
		{
			name: "joined condition flips",
			newMutate: func(mc *clusterv1beta1.MemberCluster) {
				mc.Status.AgentStatus[0].Conditions[0].Status = metav1.ConditionFalse
			},
			want: true,
		},
		{
			name: "healthy condition flips",
			newMutate: func(mc *clusterv1beta1.MemberCluster) {
				mc.Status.AgentStatus[0].Conditions = append(mc.Status.AgentStatus[0].Conditions, metav1.Condition{
					Type:   string(clusterv1beta1.AgentHealthy),
					Status: metav1.ConditionFalse,
				})
			},
			want: true,
		},
		{
			name: "property value change",
			newMutate: func(mc *clusterv1beta1.MemberCluster) {
				mc.Status.Properties["node-count"] = clusterv1beta1.PropertyValue{Value: "4", ObservationTime: now}
			},
			want: true,
		},
		{
			name:      "deletion starts",
			newMutate: func(mc *clusterv1beta1.MemberCluster) { mc.DeletionTimestamp = &now },
			want:      true,
		},
	}

	pred := memberClusterSchedulingRelevantChanges(timeout)
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			oldCluster := base()
			if tc.oldMutate != nil {
				tc.oldMutate(oldCluster)
			}
			newCluster := base()
			if tc.oldMutate != nil {
				tc.oldMutate(newCluster)
			}
			if tc.newMutate != nil {
				tc.newMutate(newCluster)
			}
			if got := pred.Update(event.UpdateEvent{ObjectOld: oldCluster, ObjectNew: newCluster}); got != tc.want {
				t.Errorf("memberClusterSchedulingRelevantChanges().Update() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestIsHeartbeatRecoveryAt tests deterministic boundary conditions for stale-to-fresh heartbeat recovery.
func TestIsHeartbeatRecoveryAt(t *testing.T) {
	baseTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	timeout := 5 * time.Minute

	makeCluster := func(heartbeat *time.Time) *clusterv1beta1.MemberCluster {
		mc := &clusterv1beta1.MemberCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "c1"},
		}
		if heartbeat != nil {
			mc.Status = clusterv1beta1.MemberClusterStatus{
				AgentStatus: []clusterv1beta1.AgentStatus{{
					Type:                  clusterv1beta1.MemberAgent,
					LastReceivedHeartbeat: metav1.NewTime(*heartbeat),
				}},
			}
		}
		return mc
	}

	t7mAgo := baseTime.Add(-7 * time.Minute)
	t6mAgo := baseTime.Add(-6 * time.Minute)
	t2mAgo := baseTime.Add(-2 * time.Minute)
	tNow := baseTime

	testCases := []struct {
		name         string
		oldHeartbeat *time.Time
		newHeartbeat *time.Time
		now          time.Time
		want         bool
	}{
		{
			name:         "stale to fresh: admitted",
			oldHeartbeat: &t6mAgo,
			newHeartbeat: &tNow,
			now:          baseTime,
			want:         true,
		},
		{
			name:         "fresh to fresh: not admitted",
			oldHeartbeat: &t2mAgo,
			newHeartbeat: &tNow,
			now:          baseTime,
			want:         false,
		},
		{
			name:         "stale to stale: not admitted",
			oldHeartbeat: &t7mAgo,
			newHeartbeat: &t6mAgo,
			now:          baseTime,
			want:         false,
		},
		{
			name:         "missing old heartbeat to fresh: admitted",
			oldHeartbeat: nil,
			newHeartbeat: &tNow,
			now:          baseTime,
			want:         true,
		},
		{
			name:         "new heartbeat is missing: not admitted",
			oldHeartbeat: &t6mAgo,
			newHeartbeat: nil,
			now:          baseTime,
			want:         false,
		},
		{
			name:         "new heartbeat is older than old heartbeat: not admitted",
			oldHeartbeat: &t2mAgo,
			newHeartbeat: &t6mAgo,
			now:          baseTime,
			want:         false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			oldMC := makeCluster(tc.oldHeartbeat)
			newMC := makeCluster(tc.newHeartbeat)
			got := isHeartbeatRecoveryAt(oldMC, newMC, timeout, tc.now)
			if got != tc.want {
				t.Errorf("isHeartbeatRecoveryAt() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestMemberClusterHeartbeatRecoveryEnqueuesPolicy verifies that when a member cluster recovers
// from a stale heartbeat, the watch predicate admits the event and the reconciler's mapping
// functions correctly enqueue all PlacementPolicies and ClusterPlacementPolicies.
func TestMemberClusterHeartbeatRecoveryEnqueuesPolicy(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := kfplacementv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() = %v, want no error", err)
	}
	if err := clusterv1beta1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() = %v, want no error", err)
	}

	policy := &kfplacementv1alpha1.PlacementPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "app-pp", Namespace: "default"},
	}
	clusterPolicy := &kfplacementv1alpha1.ClusterPlacementPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-pp"},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(policy, clusterPolicy).
		Build()

	r := &Reconciler{Client: c}
	pred := memberClusterSchedulingRelevantChanges(5 * time.Minute)

	now := metav1.Now()
	oldCluster := &clusterv1beta1.MemberCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "c1"},
		Status: clusterv1beta1.MemberClusterStatus{
			AgentStatus: []clusterv1beta1.AgentStatus{{
				Type:                  clusterv1beta1.MemberAgent,
				LastReceivedHeartbeat: metav1.NewTime(now.Add(-10 * time.Minute)), // stale
			}},
		},
	}
	newCluster := &clusterv1beta1.MemberCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "c1"},
		Status: clusterv1beta1.MemberClusterStatus{
			AgentStatus: []clusterv1beta1.AgentStatus{{
				Type:                  clusterv1beta1.MemberAgent,
				LastReceivedHeartbeat: now, // recovered to fresh
			}},
		},
	}

	// 1. Verify that predicate admits the stale-to-fresh recovery event.
	if !pred.Update(event.UpdateEvent{ObjectOld: oldCluster, ObjectNew: newCluster}) {
		t.Fatal("pred.Update() = false, want true for stale-to-fresh heartbeat recovery")
	}

	// 2. Verify that mapMemberClusterToPlacementPolicies enqueues the placement policy.
	ppRequests := r.mapMemberClusterToPlacementPolicies(context.Background(), newCluster)
	wantPP := []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: "default", Name: "app-pp"}}}
	if diff := cmp.Diff(ppRequests, wantPP); diff != "" {
		t.Errorf("mapMemberClusterToPlacementPolicies() mismatch (-got, +want):\n%s", diff)
	}

	// 3. Verify that mapMemberClusterToClusterPlacementPolicies enqueues the cluster placement policy.
	cppRequests := r.mapMemberClusterToClusterPlacementPolicies(context.Background(), newCluster)
	wantCPP := []reconcile.Request{{NamespacedName: types.NamespacedName{Name: "cluster-pp"}}}
	if diff := cmp.Diff(cppRequests, wantCPP); diff != "" {
		t.Errorf("mapMemberClusterToClusterPlacementPolicies() mismatch (-got, +want):\n%s", diff)
	}
}
