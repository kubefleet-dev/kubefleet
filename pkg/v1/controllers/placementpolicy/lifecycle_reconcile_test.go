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
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/client_golang/prometheus/testutil"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clocktesting "k8s.io/utils/clock/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	hubmetrics "github.com/kubefleet-dev/kubefleet/pkg/metrics/hub"
)

func lifecycleScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := kfplacementv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := clusterv1beta1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}

// TestExpireIfDue pins the expiry write against a fake clock and a fake API server: the Expired
// condition carries the controller's clock, not the wall clock, and a resourceVersion conflict --
// a provider's acceptance landing first -- leaves no stamp, no event, and no metric behind.
func TestExpireIfDue(t *testing.T) {
	fakeNow := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	approvedAt := metav1.NewTime(fakeNow.Add(-time.Hour))
	policy := &kfplacementv1alpha1.PlacementPolicy{ObjectMeta: metav1.ObjectMeta{Name: "pp", Namespace: "work"}}
	class := &kfplacementv1alpha1.ClusterProviderClass{ObjectMeta: metav1.ObjectMeta{Name: "c"}}
	newClaim := func() *kfplacementv1alpha1.ClusterClaim {
		return &kfplacementv1alpha1.ClusterClaim{
			ObjectMeta: metav1.ObjectMeta{Name: "claim", Generation: 3},
			Spec:       kfplacementv1alpha1.ClusterClaimSpec{PlacementPolicyRef: policyReference(policy)},
			Status: kfplacementv1alpha1.ClusterClaimStatus{Conditions: []metav1.Condition{
				{Type: kfplacementv1alpha1.ClusterClaimCondTypeApproved, Status: metav1.ConditionTrue, Reason: "Approved", LastTransitionTime: approvedAt},
			}},
		}
	}
	conflict := apierrors.NewConflict(schema.GroupResource{Group: kfplacementv1alpha1.GroupVersion.Group, Resource: "clusterclaims"}, "claim", nil)

	testCases := []struct {
		name        string
		updateErr   error
		wantExpired bool
	}{
		{name: "the stamp lands with the controller's clock", wantExpired: true},
		{name: "a conflict leaves nothing behind", updateErr: conflict, wantExpired: false},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			claim := newClaim()
			updates := 0
			c := fake.NewClientBuilder().WithScheme(lifecycleScheme(t)).WithObjects(claim).WithStatusSubresource(claim).
				WithInterceptorFuncs(interceptor.Funcs{
					SubResourceUpdate: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
						updates++
						if tc.updateErr != nil {
							return tc.updateErr
						}
						return cl.Status().Update(ctx, obj, opts...)
					},
				}).Build()
			r := NewReconciler(c, c, snapshotStub{}, nil)
			r.clock = clocktesting.NewFakeClock(fakeNow)
			before := testutil.ToFloat64(hubmetrics.FleetPlacementPolicyClusterClaimExpirations.WithLabelValues("work", "pp", kfplacementv1alpha1.ClusterClaimExpiredCondReasonPendingTimeout))

			deadline, err := r.expireIfDue(context.Background(), claim, class, &desiredClaim{}, claimRound{policy: policy, now: r.clock.Now()})
			if err != nil || !deadline.IsZero() {
				t.Fatalf("expireIfDue() = %v, %v; want a zero deadline and no error", deadline, err)
			}
			if updates != 1 {
				t.Errorf("expireIfDue() made %d status updates, want exactly one resourceVersion-conditional Update", updates)
			}
			fetched := &kfplacementv1alpha1.ClusterClaim{}
			if err := c.Get(context.Background(), types.NamespacedName{Name: "claim"}, fetched); err != nil {
				t.Fatal(err)
			}
			expired := meta.FindStatusCondition(fetched.Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeExpired)
			after := testutil.ToFloat64(hubmetrics.FleetPlacementPolicyClusterClaimExpirations.WithLabelValues("work", "pp", kfplacementv1alpha1.ClusterClaimExpiredCondReasonPendingTimeout))
			if !tc.wantExpired {
				if expired != nil || after != before {
					t.Errorf("expireIfDue() on conflict left Expired=%v and counted %v expiries, want none", expired, after-before)
				}
				return
			}
			if expired == nil || expired.Reason != kfplacementv1alpha1.ClusterClaimExpiredCondReasonPendingTimeout || !expired.LastTransitionTime.Time.Equal(fakeNow) || expired.ObservedGeneration != 3 {
				t.Errorf("expireIfDue() stamped %+v, want Expired/PendingTimeout at %v observing generation 3", expired, fakeNow)
			}
			if after != before+1 {
				t.Errorf("expireIfDue() counted %v expiries, want 1", after-before)
			}
		})
	}
}

// TestIssueClaimFleetLimitUnderContention pins the fleet-wide limit's critical section: two
// policies issuing at once against a limit of one, for an Automatic class, end with exactly one
// claim, because the count and the create run under one mutex and the stamp inside it.
func TestIssueClaimFleetLimitUnderContention(t *testing.T) {
	class := &kfplacementv1alpha1.ClusterProviderClass{
		ObjectMeta: metav1.ObjectMeta{Name: "auto"},
		Spec:       kfplacementv1alpha1.ClusterProviderClassSpec{ProvisionerName: "p", Approval: kfplacementv1alpha1.ClusterClaimApprovalModeAutomatic},
	}
	c := fake.NewClientBuilder().WithScheme(lifecycleScheme(t)).WithObjects(class).WithStatusSubresource(&kfplacementv1alpha1.ClusterClaim{}).Build()
	r := NewReconciler(c, c, snapshotStub{}, nil, WithMaxConcurrentClusterClaims(1))

	outcomes := make([]issueOutcome, 2)
	var wg sync.WaitGroup
	for i := range outcomes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			policy := &kfplacementv1alpha1.PlacementPolicy{ObjectMeta: metav1.ObjectMeta{Name: "pp-" + string(rune('a'+i)), Namespace: "work"}}
			w := &desiredClaim{name: claimName(policy, 0), class: class}
			var err error
			outcomes[i], err = r.issueClaim(context.Background(), policy, w, claimRound{now: time.Now()})
			if err != nil {
				t.Errorf("issueClaim() = %v, want no error", err)
			}
		}()
	}
	wg.Wait()

	claims := &kfplacementv1alpha1.ClusterClaimList{}
	if err := c.List(context.Background(), claims); err != nil {
		t.Fatal(err)
	}
	issuedCount, limitedCount := 0, 0
	for _, o := range outcomes {
		switch o {
		case issued:
			issuedCount++
		case issueLimited:
			limitedCount++
		}
	}
	if len(claims.Items) != 1 || issuedCount != 1 || limitedCount != 1 {
		t.Errorf("issueClaim() under contention created %d claims with outcomes %v, want one issued and one limited", len(claims.Items), outcomes)
	}
	if !meta.IsStatusConditionTrue(claims.Items[0].Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeApproved) {
		t.Errorf("issueClaim() left the Automatic-class claim unapproved; the stamp belongs inside the critical section")
	}
}

// TestReportPolicyMetrics pins the per-state gauge: every state is published, to zero when empty,
// and the sum over states is the outstanding count.
func TestReportPolicyMetrics(t *testing.T) {
	policy := &kfplacementv1alpha1.PlacementPolicy{ObjectMeta: metav1.ObjectMeta{Name: "metrics-pp", Namespace: "work"}}
	report := &claimReport{outstanding: 3, states: map[string]int{claimStatePending: 1, claimStateTerminal: 2}}
	reportPolicyMetrics(policy, report, metav1.Condition{Type: kfplacementv1alpha1.PlacementPolicyCondTypeScheduled})
	sum := 0.0
	for _, state := range claimStates {
		got := testutil.ToFloat64(hubmetrics.FleetPlacementPolicyActiveClusterClaims.WithLabelValues("work", "metrics-pp", state))
		if want := float64(report.states[state]); got != want {
			t.Errorf("active claims gauge state=%s = %v, want %v", state, got, want)
		}
		sum += got
	}
	if sum != float64(report.outstanding) {
		t.Errorf("active claims gauge sums to %v over states, want the outstanding count %d", sum, report.outstanding)
	}
	forgetPolicyMetrics("work", "metrics-pp")
}

// TestReconcileClaimsCountsUnlistedClaim pins the per-policy limit under informer-cache lag: the
// previous round's create is not in the list yet, its name comes back AlreadyExists, and that
// slot must still count, or the next selector would be issued past the limit.
func TestReconcileClaimsCountsUnlistedClaim(t *testing.T) {
	policy := &kfplacementv1alpha1.PlacementPolicy{ObjectMeta: metav1.ObjectMeta{Name: "pp", Namespace: "work"}}
	class := openClass()
	terms := func(region string) []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm {
		return []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{{MatchLabels: map[string]string{regionLabel: region}}}
	}
	first := &kfplacementv1alpha1.ClusterClaim{
		ObjectMeta: metav1.ObjectMeta{Name: claimName(policy, 0), Labels: claimOwnershipLabels(policy)},
		Spec:       kfplacementv1alpha1.ClusterClaimSpec{PlacementPolicyRef: policyReference(policy), ClusterSelectorTerms: terms("a"), ClusterProviderClassName: class.Name},
	}
	c := fake.NewClientBuilder().WithScheme(lifecycleScheme(t)).WithObjects(policy, class, first).WithStatusSubresource(first).
		WithInterceptorFuncs(interceptor.Funcs{
			// The cache lags: the claim list shows nothing, although the first claim exists.
			List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, isClaims := list.(*kfplacementv1alpha1.ClusterClaimList); isClaims {
					return nil
				}
				return cl.List(ctx, list, opts...)
			},
		}).Build()
	r := NewReconciler(c, c, snapshotStub{}, nil, WithMaxConcurrentClusterClaims(5))
	outcomes := []selectorOutcome{
		{counts: resolvedCounts{desired: 1, minimum: 1}, terms: terms("a"), whenUnfulfilled: kfplacementv1alpha1.WhenUnfulfilledOptionAddClusterClaim},
		{counts: resolvedCounts{desired: 1, minimum: 1}, terms: terms("b"), whenUnfulfilled: kfplacementv1alpha1.WhenUnfulfilledOptionAddClusterClaim},
	}
	wanted, _ := desiredClaims(policy, outcomes, class, "")
	report, err := r.reconcileClaims(context.Background(), claimRound{policy: policy, wanted: wanted, limit: 1, now: time.Now()})
	if err != nil {
		t.Fatalf("reconcileClaims() = %v, want no error", err)
	}
	if report.outstanding != 1 {
		t.Errorf("reconcileClaims() counted %d outstanding, want 1 for the unlisted claim", report.outstanding)
	}
	if want := map[string]int{claimStatePending: 1}; !cmp.Equal(report.states, want) {
		t.Errorf("reconcileClaims() reported states %v, want %v so that the metric sums to the outstanding count", report.states, want)
	}
	if err := c.Get(context.Background(), types.NamespacedName{Name: claimName(policy, 1)}, &kfplacementv1alpha1.ClusterClaim{}); !apierrors.IsNotFound(err) {
		t.Errorf("reconcileClaims() issued the second selector's claim past the limit (get = %v), want NotFound", err)
	}
}
