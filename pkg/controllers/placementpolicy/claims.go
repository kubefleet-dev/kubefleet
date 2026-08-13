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
	"hash/fnv"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
)

const (
	// claimPolicyNameLabel and claimPolicyNamespaceLabel identify the policy a cluster claim
	// belongs to. ClusterClaim objects are cluster-scoped and cannot carry an owner reference
	// to a namespaced PlacementPolicy (Kubernetes garbage collection rejects cross-scope
	// owners), so ownership is tracked with labels and cleaned up with a finalizer instead.
	claimPolicyNameLabel      = "placement.kubefleet.dev/policy-name"
	claimPolicyNamespaceLabel = "placement.kubefleet.dev/policy-namespace"

	// claimCleanupFinalizer marks policies with outstanding cluster claims; deleting such a
	// policy first withdraws its claims.
	claimCleanupFinalizer = "placement.kubefleet.dev/claim-cleanup"

	// maxConcurrentClaimsPerPolicy is the number of cluster claims a single policy may have in
	// flight, per the FEP's default of one; the user-configurable per-fleet and per-placement
	// limits are a config surface that arrives separately.
	maxConcurrentClaimsPerPolicy = 1

	// claimNameBaseMaxLength bounds the policy-name prefix inside a generated claim name so
	// the full name stays well within the 253-character object name limit.
	claimNameBaseMaxLength = 200
)

// claimName derives the deterministic name for the claim serving a policy's selector. The name
// embeds a hash of the policy's namespaced name because claims are cluster-scoped while
// PlacementPolicy names are only unique per namespace; two same-named policies in different
// namespaces must not collide. Determinism matters: creation is get-or-create, so a restarted
// reconciler converges on the same claim instead of issuing a duplicate.
func claimName(policy policyObject, selectorIndex int) string {
	base := policy.GetName()
	if len(base) > claimNameBaseMaxLength {
		base = base[:claimNameBaseMaxLength]
	}
	hasher := fnv.New32a()
	// Write on a FNV hasher never returns an error.
	_, _ = hasher.Write([]byte(policy.GetNamespace() + "/" + policy.GetName()))
	return fmt.Sprintf("%s-%d-%08x", base, selectorIndex, hasher.Sum32())
}

// desiredClaim describes a claim the policy currently wants outstanding.
type desiredClaim struct {
	name  string
	terms []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm
}

// desiredClaims returns the claims the policy should have outstanding given the selector
// outcomes: one claim per unfulfilled selector that opted into AddClusterClaim, in selector
// order, capped by the per-policy concurrency limit.
func desiredClaims(policy policyObject, outcomes []selectorOutcome) []desiredClaim {
	wanted := make([]desiredClaim, 0, maxConcurrentClaimsPerPolicy)
	for i := range outcomes {
		o := &outcomes[i]
		if o.satisfiedInFull() || o.whenUnfulfilled != kfplacementv1alpha1.WhenUnfulfilledOptionAddClusterClaim {
			continue
		}
		wanted = append(wanted, desiredClaim{name: claimName(policy, i), terms: o.terms})
		if len(wanted) >= maxConcurrentClaimsPerPolicy {
			break
		}
	}
	return wanted
}

// reconcileClaims drives the policy's cluster claims toward the desired set: it withdraws
// claims whose selector is fulfilled or gone, refreshes the freshness marker on claims that
// are still wanted, and issues new claims within the concurrency budget. It returns the number
// of claims outstanding for the policy.
//
// A claim held in Terminating by a provisioner finalizer still counts toward the concurrency
// budget (its deterministic name also blocks re-creation), so a slow provisioner teardown can
// never cause double-provisioning for the same selector.
func (r *Reconciler) reconcileClaims(ctx context.Context, policy policyObject, outcomes []selectorOutcome, mostRecentClusterCreation metav1.Time) (int32, error) {
	existing, err := r.listClaims(ctx, policy)
	if err != nil {
		return 0, err
	}
	if len(existing) > 0 {
		// Re-assert the cleanup finalizer while any claim exists, so an out-of-band finalizer
		// removal self-heals instead of leaving claims orphanable.
		if err := r.ensureFinalizer(ctx, policy); err != nil {
			return 0, err
		}
	}

	wanted := desiredClaims(policy, outcomes)
	wantedByName := make(map[string]desiredClaim, len(wanted))
	for _, w := range wanted {
		wantedByName[w.name] = w
	}

	var outstanding int32
	for i := range existing {
		claim := &existing[i]
		if !claim.DeletionTimestamp.IsZero() {
			// Already being withdrawn: the claim still occupies its name and budget slot
			// regardless of whether its terms happen to match the currently wanted set (a
			// fulfillment flap can re-want identical terms mid-teardown), and an object on
			// its way out receives no further status writes.
			outstanding++
			delete(wantedByName, claim.Name)
			continue
		}
		w, stillWanted := wantedByName[claim.Name]
		// A claim only serves its original terms; if the policy's selector changed, the
		// outstanding claim is withdrawn and a fresh one is issued on a later pass.
		if stillWanted && apiequality.Semantic.DeepEqual(claim.Spec.ClusterSelectorTerms, w.terms) {
			delete(wantedByName, claim.Name)
			outstanding++
			if err := r.refreshClaimFreshness(ctx, claim, mostRecentClusterCreation); err != nil {
				return outstanding, err
			}
			continue
		}
		klog.V(2).InfoS("Withdrawing a cluster claim", "clusterClaim", claim.Name, "placementPolicy", klog.KObj(policy.Unwrap()))
		if err := r.Delete(ctx, claim); err != nil && !errors.IsNotFound(err) {
			return outstanding, err
		}
	}

	if len(wantedByName) == 0 {
		return outstanding, nil
	}

	// The cleanup finalizer lands on the policy before any claim is created, so a crash
	// between the two writes cannot orphan a claim.
	if err := r.ensureFinalizer(ctx, policy); err != nil {
		return outstanding, err
	}
	for _, w := range wanted {
		if _, still := wantedByName[w.name]; !still {
			continue
		}
		claim := &kfplacementv1alpha1.ClusterClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name: w.name,
				Labels: map[string]string{
					claimPolicyNameLabel:      policy.GetName(),
					claimPolicyNamespaceLabel: policy.GetNamespace(),
				},
			},
			Spec: kfplacementv1alpha1.ClusterClaimSpec{
				PlacementPolicyRef:   policyReference(policy),
				ClusterSelectorTerms: w.terms,
			},
		}
		klog.V(2).InfoS("Adding a cluster claim", "clusterClaim", claim.Name, "placementPolicy", klog.KObj(policy.Unwrap()))
		if err := r.Create(ctx, claim); err != nil {
			if errors.IsAlreadyExists(err) {
				// The deterministic name is still occupied — most commonly by this round's own
				// withdraw of a same-named claim that a provisioner finalizer holds in
				// Terminating. The claim watch re-queues the policy once the old object is
				// gone, and the create is retried then.
				continue
			}
			return outstanding, err
		}
		outstanding++
		// Stamp the freshness marker at creation, per the FEP: the claim carries the latest
		// observed cluster creation timestamp from the moment provisioners can see it.
		if err := r.refreshClaimFreshness(ctx, claim, mostRecentClusterCreation); err != nil {
			return outstanding, err
		}
	}
	return outstanding, nil
}

// refreshClaimFreshness advances the claim's freshness marker when clusters joined after the
// last observation; provisioners use the marker to tell that the claim has been re-evaluated
// and is still wanted.
func (r *Reconciler) refreshClaimFreshness(ctx context.Context, claim *kfplacementv1alpha1.ClusterClaim, mostRecent metav1.Time) error {
	if mostRecent.IsZero() {
		return nil
	}
	observed := claim.Status.LastObservedMostRecentClusterCreationTimestamp
	if observed != nil && !mostRecent.After(observed.Time) {
		return nil
	}
	claim.Status.LastObservedMostRecentClusterCreationTimestamp = &mostRecent
	// Conflicts are expected steady-state once a provisioner co-writes the claim status; the
	// provisioner's own write re-enqueues the policy through the claim watch, so the refresh
	// simply retries then. This only-advance guard above is also what terminates the
	// claim-watch self-loop: a refresh fires one echo reconcile, which then no-ops here.
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
func (r *Reconciler) cleanupClaims(ctx context.Context, policy policyObject) error {
	if !controllerutil.ContainsFinalizer(policy.Unwrap(), claimCleanupFinalizer) {
		return nil
	}

	claims := &kfplacementv1alpha1.ClusterClaimList{}
	if err := r.uncachedReader.List(ctx, claims, client.MatchingLabels{
		claimPolicyNameLabel:      policy.GetName(),
		claimPolicyNamespaceLabel: policy.GetNamespace(),
	}); err != nil {
		klog.ErrorS(err, "Failed to list cluster claims for the deleted policy", "placementPolicy", klog.KObj(policy.Unwrap()))
		return err
	}
	remaining := 0
	for i := range claims.Items {
		claim := &claims.Items[i]
		if !claim.DeletionTimestamp.IsZero() {
			remaining++
			continue
		}
		klog.V(2).InfoS("Withdrawing a cluster claim of a deleted policy", "clusterClaim", claim.Name, "placementPolicy", klog.KObj(policy.Unwrap()))
		if err := r.Delete(ctx, claim); err != nil {
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

	obj := policy.Unwrap()
	controllerutil.RemoveFinalizer(obj, claimCleanupFinalizer)
	return r.Update(ctx, obj)
}

// ensureFinalizer adds the claim cleanup finalizer to the policy if not present yet.
func (r *Reconciler) ensureFinalizer(ctx context.Context, policy policyObject) error {
	obj := policy.Unwrap()
	if controllerutil.ContainsFinalizer(obj, claimCleanupFinalizer) {
		return nil
	}
	controllerutil.AddFinalizer(obj, claimCleanupFinalizer)
	return r.Update(ctx, obj)
}

// listClaims lists the cluster claims belonging to a policy via the ownership labels.
func (r *Reconciler) listClaims(ctx context.Context, policy policyObject) ([]kfplacementv1alpha1.ClusterClaim, error) {
	claims := &kfplacementv1alpha1.ClusterClaimList{}
	if err := r.List(ctx, claims, client.MatchingLabels{
		claimPolicyNameLabel:      policy.GetName(),
		claimPolicyNamespaceLabel: policy.GetNamespace(),
	}); err != nil {
		klog.ErrorS(err, "Failed to list cluster claims for the policy", "placementPolicy", klog.KObj(policy.Unwrap()))
		return nil, err
	}
	return claims.Items, nil
}

// policyReference builds the claim's back-reference to its policy.
func policyReference(policy policyObject) *kfplacementv1alpha1.ObjectReference {
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
