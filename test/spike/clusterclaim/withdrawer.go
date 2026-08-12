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

package clusterclaim

import (
	"context"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	placementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/scheduler/clustereligibilitychecker"
)

// Withdrawer prototypes the claim-management slice of the FEP-0001 placement
// policy controller (#786): per the FEP, KubeFleet evaluates outstanding
// claims whenever the member cluster set changes, withdraws (deletes) a claim
// as soon as its selector terms are satisfied by ANY member cluster —
// regardless of the claim's own Completed status — and otherwise refreshes
// status.lastObservedMostRecentClusterCreationTimestamp so provisioners can
// tell the claim has been re-evaluated and is still wanted.
//
// The FEP says a claim is withdrawn once a matching cluster "is joined to the
// fleet" without saying whether "joined" means the MemberCluster object exists
// or the cluster is actually usable by the scheduler. The two readings are
// prototyped side by side, toggled by SetEligibilityGate:
//
//   - gate off (default, FEP-as-written reading): withdraw on raw selector
//     match against the MemberCluster object;
//   - gate on (recommended reading): additionally require the cluster to pass
//     the scheduler's real eligibility gate
//     (clustereligibilitychecker.IsEligible: member agent online, recent
//     heartbeat, Joined=True, healthy) before the match counts.
type Withdrawer struct {
	client.Client

	checker *clustereligibilitychecker.ClusterEligibilityChecker

	mu              sync.Mutex
	eligibilityGate bool
}

func NewWithdrawer(c client.Client) *Withdrawer {
	return &Withdrawer{Client: c, checker: clustereligibilitychecker.New()}
}

// SetEligibilityGate toggles between the FEP-as-written withdrawal predicate
// (raw selector match, gate off) and the eligibility-gated variant (gate on).
func (w *Withdrawer) SetEligibilityGate(on bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.eligibilityGate = on
}

func (w *Withdrawer) gated() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.eligibilityGate
}

func (w *Withdrawer) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	claim := &placementv1alpha1.ClusterRequest{}
	if err := w.Get(ctx, req.NamespacedName, claim); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	memberClusters := &clusterv1beta1.MemberClusterList{}
	if err := w.List(ctx, memberClusters); err != nil {
		return ctrl.Result{}, err
	}

	var mostRecent metav1.Time
	for i := range memberClusters.Items {
		mc := &memberClusters.Items[i]
		if clusterMatchesTerms(mc, claim.Spec.ClusterSelectorTerms) {
			eligible := true
			if w.gated() {
				eligible, _ = w.checker.IsEligible(mc)
			}
			if eligible {
				// Withdraw: the need is met, whether by the provisioned cluster or
				// any other cluster that joined or was relabeled.
				return ctrl.Result{}, client.IgnoreNotFound(w.Delete(ctx, claim))
			}
		}
		if mc.CreationTimestamp.After(mostRecent.Time) {
			mostRecent = mc.CreationTimestamp
		}
	}

	// Still unfulfilled: refresh the freshness marker if the fleet has moved on.
	ts := claim.Status.LastObservedMostRecentClusterCreationTimestamp
	if !mostRecent.IsZero() && (ts == nil || mostRecent.After(ts.Time)) {
		claim.Status.LastObservedMostRecentClusterCreationTimestamp = &mostRecent
		return ctrl.Result{}, w.Status().Update(ctx, claim)
	}
	return ctrl.Result{}, nil
}

func (w *Withdrawer) SetupWithManager(mgr ctrl.Manager) error {
	// Re-evaluate every claim when any MemberCluster changes; this is the
	// "evaluates unfulfilled cluster selectors as soon as a new cluster is
	// joined ... or an existing cluster has been relabeled" behavior.
	mapAllClaims := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, _ client.Object) []reconcile.Request {
		claims := &placementv1alpha1.ClusterRequestList{}
		if err := w.List(ctx, claims); err != nil {
			return nil
		}
		reqs := make([]reconcile.Request, 0, len(claims.Items))
		for i := range claims.Items {
			reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&claims.Items[i])})
		}
		return reqs
	})

	return ctrl.NewControllerManagedBy(mgr).
		Named("spike-claim-withdrawer").
		For(&placementv1alpha1.ClusterRequest{}).
		Watches(&clusterv1beta1.MemberCluster{}, mapAllClaims).
		Complete(w)
}
