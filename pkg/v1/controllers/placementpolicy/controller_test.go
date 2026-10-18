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
	"errors"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/v1/utils/fieldindexers"
)

// TestReconcilePreservesClaimCountOnFailedRound pins that a claim round failing before it could
// count anything does not publish its partial count: a zero from a failed list would falsely
// report the policy's claims gone, on the status and on the metric derived from it.
func TestReconcilePreservesClaimCountOnFailedRound(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := kfplacementv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() = %v, want no error", err)
	}
	if err := clusterv1beta1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() = %v, want no error", err)
	}

	policy := &kfplacementv1alpha1.PlacementPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "pp", Namespace: "default", Generation: 1},
		Spec: kfplacementv1alpha1.PlacementPolicySpec{
			ClusterSelectors: []kfplacementv1alpha1.ClusterSelector{{
				Count:           ptr.To(intstr.FromInt32(1)),
				WhenUnfulfilled: kfplacementv1alpha1.WhenUnfulfilledOptionAddClusterClaim,
			}},
			ResourceSelectors: []kfplacementv1alpha1.ResourceSelector{{APIVersion: "v1", Kind: "ConfigMap", Name: "app"}},
		},
		Status: kfplacementv1alpha1.PlacementPolicyStatus{
			// The last completed round counted one active claim.
			ActiveClusterClaims: ptr.To(int32(1)),
		},
	}

	listErr := errors.New("the claim list is unwell")
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(policy).
		WithStatusSubresource(policy).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, isClaims := list.(*kfplacementv1alpha1.ClusterClaimList); isClaims {
					return listErr
				}
				return cl.List(ctx, list, opts...)
			},
		}).
		Build()

	r := NewReconciler(c, c)
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "pp"}})
	if !errors.Is(err, listErr) {
		t.Fatalf("Reconcile() = %v, want the claim list error %v so the round is retried", err, listErr)
	}

	fetched := &kfplacementv1alpha1.PlacementPolicy{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "pp"}, fetched); err != nil {
		t.Fatalf("Get() = %v, want no error", err)
	}
	if fetched.Status.ActiveClusterClaims == nil || *fetched.Status.ActiveClusterClaims != 1 {
		t.Errorf("Reconcile() left activeClusterClaims = %v, want the last completed round's 1 preserved", fetched.Status.ActiveClusterClaims)
	}
}

// TestReconcileIgnoresDeletedPolicy pins that a policy that is already gone ends the round quietly.
// The read is wrapped as a categorized API server error, and IsNotFound has to keep resolving
// through that wrap: an error utility that flattened the cause instead would turn every deleted
// policy into an endless retry, with its metric series never forgotten.
func TestReconcileIgnoresDeletedPolicy(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := kfplacementv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() = %v, want no error", err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := NewReconciler(c, c)

	for _, name := range []types.NamespacedName{{Namespace: "default", Name: "gone"}, {Name: "gone"}} {
		res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: name})
		if err != nil {
			t.Errorf("Reconcile(%v) = %v, want no error for a policy that no longer exists", name, err)
		}
		if res.RequeueAfter != 0 {
			t.Errorf("Reconcile(%v) requeued after %v, want no requeue for a policy that no longer exists", name, res.RequeueAfter)
		}
	}
}

// TestReconcileHeartbeatDeadlineRequeue verifies that Reconcile schedules requeues based on the
// counted clusters' earliest heartbeat staleness deadline rather than fixed 120s/150s intervals.
func TestReconcileHeartbeatDeadlineRequeue(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := kfplacementv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() = %v, want no error", err)
	}
	if err := clusterv1beta1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() = %v, want no error", err)
	}

	now := metav1.Now()
	// Cluster c1 has heartbeat 2 minutes ago. With 5m timeout and 1s buffer, remaining wait is ~3m1s.
	c1 := &clusterv1beta1.MemberCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "c1"},
		Status: clusterv1beta1.MemberClusterStatus{
			AgentStatus: []clusterv1beta1.AgentStatus{{
				Type:                  clusterv1beta1.MemberAgent,
				LastReceivedHeartbeat: metav1.NewTime(time.Now().Add(-2 * time.Minute)),
				Conditions: []metav1.Condition{
					{
						Type:               string(clusterv1beta1.AgentJoined),
						Status:             metav1.ConditionTrue,
						LastTransitionTime: now,
					},
					{
						Type:               string(clusterv1beta1.AgentHealthy),
						Status:             metav1.ConditionTrue,
						LastTransitionTime: now,
					},
				},
			}},
		},
	}

	t.Run("fulfilled policy requeues at heartbeat deadline", func(t *testing.T) {
		policy := &kfplacementv1alpha1.PlacementPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "fulfilled-pp", Namespace: "default", Generation: 1},
			Spec: kfplacementv1alpha1.PlacementPolicySpec{
				ClusterSelectors: []kfplacementv1alpha1.ClusterSelector{{
					Count: ptr.To(intstr.FromInt32(1)),
				}},
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(policy, c1).
			WithStatusSubresource(policy).
			WithIndex(
				&kfplacementv1alpha1.ClusterClaim{},
				fieldindexers.ClusterClaimOwnedByPlacementPolicyCustomFieldName,
				func(obj client.Object) []string {
					claim := obj.(*kfplacementv1alpha1.ClusterClaim)
					if claim.Spec.PlacementPolicyRef == nil {
						return nil
					}
					return []string{
						fieldindexers.ClusterClaimOwnedByPlacementPolicyCustomFieldVal(
							claim.Spec.PlacementPolicyRef,
						),
					}
				},
			).
			Build()

		r := NewReconciler(c, c)
		res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "fulfilled-pp"}})
		if err != nil {
			t.Fatalf("Reconcile() = %v, want no error", err)
		}
		// Expect ~3m1s (within [3m, 3m10s] window) instead of the old fixed 150s.
		if res.RequeueAfter < 3*time.Minute || res.RequeueAfter > 3*time.Minute+10*time.Second {
			t.Errorf("Reconcile() requeued after %v, want ~3m1s (heartbeat deadline)", res.RequeueAfter)
		}
	})

	t.Run("unfulfilled policy with counted cluster requeues at heartbeat deadline", func(t *testing.T) {
		// Policy desires 2 clusters, but only 1 (c1) is available.
		policy := &kfplacementv1alpha1.PlacementPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "unfulfilled-with-cluster", Namespace: "default", Generation: 1},
			Spec: kfplacementv1alpha1.PlacementPolicySpec{
				ClusterSelectors: []kfplacementv1alpha1.ClusterSelector{{
					Count: ptr.To(intstr.FromInt32(2)),
				}},
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(policy, c1).
			WithStatusSubresource(policy).
			WithIndex(
				&kfplacementv1alpha1.ClusterClaim{},
				fieldindexers.ClusterClaimOwnedByPlacementPolicyCustomFieldName,
				func(obj client.Object) []string {
					claim := obj.(*kfplacementv1alpha1.ClusterClaim)
					if claim.Spec.PlacementPolicyRef == nil {
						return nil
					}
					return []string{
						fieldindexers.ClusterClaimOwnedByPlacementPolicyCustomFieldVal(
							claim.Spec.PlacementPolicyRef,
						),
					}
				},
			).
			Build()

		r := NewReconciler(c, c)
		res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "unfulfilled-with-cluster"}})
		if err != nil {
			t.Fatalf("Reconcile() = %v, want no error", err)
		}
		// Expect ~3m1s (within [3m, 3m10s] window) instead of the old fixed 120s.
		if res.RequeueAfter < 3*time.Minute || res.RequeueAfter > 3*time.Minute+10*time.Second {
			t.Errorf("Reconcile() requeued after %v, want ~3m1s (heartbeat deadline)", res.RequeueAfter)
		}
	})

	t.Run("unfulfilled policy with no counted clusters does not requeue", func(t *testing.T) {
		policy := &kfplacementv1alpha1.PlacementPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "unfulfilled-no-cluster", Namespace: "default", Generation: 1},
			Spec: kfplacementv1alpha1.PlacementPolicySpec{
				ClusterSelectors: []kfplacementv1alpha1.ClusterSelector{{
					Count: ptr.To(intstr.FromInt32(2)),
				}},
			},
		}

		// Empty cluster inventory.
		c := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(policy).
			WithStatusSubresource(policy).
			WithIndex(
				&kfplacementv1alpha1.ClusterClaim{},
				fieldindexers.ClusterClaimOwnedByPlacementPolicyCustomFieldName,
				func(obj client.Object) []string {
					claim := obj.(*kfplacementv1alpha1.ClusterClaim)
					if claim.Spec.PlacementPolicyRef == nil {
						return nil
					}
					return []string{
						fieldindexers.ClusterClaimOwnedByPlacementPolicyCustomFieldVal(
							claim.Spec.PlacementPolicyRef,
						),
					}
				},
			).
			Build()

		r := NewReconciler(c, c)
		res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "unfulfilled-no-cluster"}})
		if err != nil {
			t.Fatalf("Reconcile() = %v, want no error", err)
		}
		// Expect 0 requeue instead of the old fixed 120s.
		if res.RequeueAfter != 0 {
			t.Errorf("Reconcile() requeued after %v, want 0 (no counted clusters)", res.RequeueAfter)
		}
	})

	t.Run("fulfilled policy with unhealthy cluster requeues at health check deadline when earlier", func(t *testing.T) {
		// Cluster has fresh heartbeat (1m ago -> expires in 4m), but is unhealthy (3m ago -> expires in 2m).
		cUnhealthy := &clusterv1beta1.MemberCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "c-unhealthy"},
			Status: clusterv1beta1.MemberClusterStatus{
				AgentStatus: []clusterv1beta1.AgentStatus{{
					Type:                  clusterv1beta1.MemberAgent,
					LastReceivedHeartbeat: metav1.NewTime(time.Now().Add(-time.Minute)),
					Conditions: []metav1.Condition{
						{
							Type:               string(clusterv1beta1.AgentJoined),
							Status:             metav1.ConditionTrue,
							LastTransitionTime: now,
						},
						{
							Type:               string(clusterv1beta1.AgentHealthy),
							Status:             metav1.ConditionFalse,
							LastTransitionTime: metav1.NewTime(time.Now().Add(-3 * time.Minute)),
						},
					},
				}},
			},
		}

		policy := &kfplacementv1alpha1.PlacementPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "unhealthy-pp", Namespace: "default", Generation: 1},
			Spec: kfplacementv1alpha1.PlacementPolicySpec{
				ClusterSelectors: []kfplacementv1alpha1.ClusterSelector{{
					Count: ptr.To(intstr.FromInt32(1)),
				}},
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(policy, cUnhealthy).
			WithStatusSubresource(policy).
			WithIndex(
				&kfplacementv1alpha1.ClusterClaim{},
				fieldindexers.ClusterClaimOwnedByPlacementPolicyCustomFieldName,
				func(obj client.Object) []string {
					claim := obj.(*kfplacementv1alpha1.ClusterClaim)
					if claim.Spec.PlacementPolicyRef == nil {
						return nil
					}
					return []string{
						fieldindexers.ClusterClaimOwnedByPlacementPolicyCustomFieldVal(
							claim.Spec.PlacementPolicyRef,
						),
					}
				},
			).
			Build()

		r := NewReconciler(c, c)
		res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "unhealthy-pp"}})
		if err != nil {
			t.Fatalf("Reconcile() = %v, want no error", err)
		}
		// Expect ~2m1s (within [2m, 2m10s]) based on health check deadline, not ~4m1s from heartbeat.
		if res.RequeueAfter < 2*time.Minute || res.RequeueAfter > 2*time.Minute+10*time.Second {
			t.Errorf("Reconcile() requeued after %v, want ~2m1s (health check deadline)", res.RequeueAfter)
		}
	})

	t.Run("stale cluster recovery enables subsequent reconciliation to schedule cluster", func(t *testing.T) {
		// Initially stale cluster (heartbeat 10m ago).
		cStale := &clusterv1beta1.MemberCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "c-stale"},
			Status: clusterv1beta1.MemberClusterStatus{
				AgentStatus: []clusterv1beta1.AgentStatus{{
					Type:                  clusterv1beta1.MemberAgent,
					LastReceivedHeartbeat: metav1.NewTime(time.Now().Add(-10 * time.Minute)),
					Conditions: []metav1.Condition{
						{
							Type:               string(clusterv1beta1.AgentJoined),
							Status:             metav1.ConditionTrue,
							LastTransitionTime: now,
						},
						{
							Type:               string(clusterv1beta1.AgentHealthy),
							Status:             metav1.ConditionTrue,
							LastTransitionTime: now,
						},
					},
				}},
			},
		}

		policy := &kfplacementv1alpha1.PlacementPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "recovery-pp", Namespace: "default", Generation: 1},
			Spec: kfplacementv1alpha1.PlacementPolicySpec{
				ClusterSelectors: []kfplacementv1alpha1.ClusterSelector{{
					Count: ptr.To(intstr.FromInt32(1)),
				}},
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(policy, cStale).
			WithStatusSubresource(policy, cStale).
			WithIndex(
				&kfplacementv1alpha1.ClusterClaim{},
				fieldindexers.ClusterClaimOwnedByPlacementPolicyCustomFieldName,
				func(obj client.Object) []string {
					claim := obj.(*kfplacementv1alpha1.ClusterClaim)
					if claim.Spec.PlacementPolicyRef == nil {
						return nil
					}
					return []string{
						fieldindexers.ClusterClaimOwnedByPlacementPolicyCustomFieldVal(
							claim.Spec.PlacementPolicyRef,
						),
					}
				},
			).
			Build()

		r := NewReconciler(c, c)

		// First reconcile: cluster is stale, policy has no counted clusters, returns RequeueAfter == 0.
		res1, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "recovery-pp"}})
		if err != nil {
			t.Fatalf("First Reconcile() = %v, want no error", err)
		}
		if res1.RequeueAfter != 0 {
			t.Errorf("First Reconcile() requeued after %v, want 0", res1.RequeueAfter)
		}

		// Cluster recovers: heartbeat updated to fresh.
		cRecovered := cStale.DeepCopy()
		cRecovered.Status.AgentStatus[0].LastReceivedHeartbeat = metav1.Now()
		if err := c.Status().Update(context.Background(), cRecovered); err != nil {
			t.Fatalf("Update cluster status = %v, want no error", err)
		}

		// Reconcile triggered by recovery: policy successfully schedules on cRecovered.
		res2, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "recovery-pp"}})
		if err != nil {
			t.Fatalf("Second Reconcile() = %v, want no error", err)
		}
		if res2.RequeueAfter < 4*time.Minute || res2.RequeueAfter > 5*time.Minute+10*time.Second {
			t.Errorf("Second Reconcile() requeued after %v, want ~5m1s (heartbeat deadline)", res2.RequeueAfter)
		}

		var updatedPolicy kfplacementv1alpha1.PlacementPolicy
		if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "recovery-pp"}, &updatedPolicy); err != nil {
			t.Fatalf("Get policy = %v, want no error", err)
		}
		if updatedPolicy.Status.ScheduledClusters == nil || *updatedPolicy.Status.ScheduledClusters != 1 {
			t.Errorf("ScheduledClusters = %v, want 1", updatedPolicy.Status.ScheduledClusters)
		}
	})
}
