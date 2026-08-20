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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
)

const (
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

	// nameHashLength is the number of hexadecimal characters of the name hash carried by
	// generated names. Sixteen characters (64 bits) keep collisions out of reach even for a
	// deliberate search, which matters because two policies whose names collide would select
	// each other's claims.
	nameHashLength = 16

	// labelValuePrefixMaxLength bounds the policy-name prefix inside the ownership label value.
	// Object names may be up to 253 characters while label values are capped at 63, so longer
	// names are shortened to this prefix plus a dash and the name hash.
	labelValuePrefixMaxLength = validation.LabelValueMaxLength - nameHashLength - 1
)

// policyNameLabelValue renders a policy name as a valid label value: names that already fit are
// used as they are, and longer ones are shortened to a prefix plus a hash of the full name, so
// that two policies whose names share a prefix still select distinct claims.
func policyNameLabelValue(name string) string {
	if len(name) <= validation.LabelValueMaxLength {
		return name
	}
	// Trim any trailing separators the truncation may have exposed; a label value must start
	// and end with an alphanumeric character. Object names always start with an alphanumeric
	// character, so the trimmed prefix can never become empty.
	return fmt.Sprintf("%s-%s", trimNameSeparators(name[:labelValuePrefixMaxLength]), hashOf(name))
}

// hashOf returns a short, collision-resistant hash of a string.
func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:nameHashLength]
}

// trimNameSeparators removes the separator characters a truncation may have left at the end of
// an object name fragment, so that appending a dash cannot produce an invalid name.
func trimNameSeparators(fragment string) string {
	return strings.TrimRight(fragment, "-_.")
}

// claimOwnershipLabels returns the labels that select the claims of a policy.
func claimOwnershipLabels(policy policyObject) client.MatchingLabels {
	return client.MatchingLabels{
		kfplacementv1alpha1.ClusterClaimPolicyNameLabel:      policyNameLabelValue(policy.GetName()),
		kfplacementv1alpha1.ClusterClaimPolicyNamespaceLabel: policy.GetNamespace(),
	}
}

// claimName derives the deterministic name for the claim serving a policy's selector. The name
// embeds a hash of the policy's namespaced name because claims are cluster-scoped while
// PlacementPolicy names are only unique per namespace; two same-named policies in different
// namespaces must not collide. Determinism matters: creation is get-or-create, so a restarted
// reconciler converges on the same claim instead of issuing a duplicate.
func claimName(policy policyObject, selectorIndex int) string {
	base := policy.GetName()
	if len(base) > claimNameBaseMaxLength {
		// Truncation can land on a separator, which would make the generated name start a
		// label with a dash and fail API validation.
		base = trimNameSeparators(base[:claimNameBaseMaxLength])
	}
	return fmt.Sprintf("%s-%d-%s", base, selectorIndex, hashOf(policy.GetNamespace()+"/"+policy.GetName()))
}

// desiredClaim describes a claim the policy currently wants outstanding.
type desiredClaim struct {
	name  string
	terms []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm
	// outcome is the selector this claim serves. It is what lets the reconcile decide when a
	// completed claim has done its job and should be rotated to provision the next cluster.
	outcome *selectorOutcome
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
		wanted = append(wanted, desiredClaim{name: claimName(policy, i), terms: o.terms, outcome: o})
		if len(wanted) >= maxConcurrentClaimsPerPolicy {
			break
		}
	}
	return wanted
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
		keep := stillWanted && apiequality.Semantic.DeepEqual(claim.Spec.ClusterSelectorTerms, w.terms)
		if keep && claimReadyToRotate(claim, w.outcome) {
			// The provisioner has provisioned an eligible cluster for this claim, but the selector
			// still needs more. The completed claim is not kept: it falls through to the withdrawal
			// below and, its entry left in wantedByName, is reissued on a later pass so the provisioner
			// gets a fresh claim for the next cluster. Rotation is gated on the provisioned cluster
			// being eligible, so the next claim is never issued before this one's cluster is confirmed.
			keep = false
		}
		if keep {
			delete(wantedByName, claim.Name)
			outstanding++
			if err := r.refreshClaimFreshness(ctx, claim, mostRecentClusterCreation); err != nil {
				return outstanding, err
			}
			continue
		}
		klog.V(2).InfoS("Withdrawing a cluster claim", "clusterClaim", claim.Name, "placementPolicy", klog.KObj(policy.Unwrap()))
		if err := r.Delete(ctx, claim); err != nil {
			if errors.IsNotFound(err) {
				// Already fully gone; it occupies nothing.
				continue
			}
			return outstanding, err
		}
		// A claim withdrawn this pass still occupies its budget slot: a provisioner finalizer
		// can hold it in Terminating past this reconcile, and a differently-named claim created
		// below would otherwise stand beside it, exceeding the concurrency budget. The claim
		// watch re-queues the policy once the object is truly gone, and the slot frees then.
		outstanding++
	}

	if len(wantedByName) == 0 {
		// Nothing more to create. If nothing is outstanding either, the policy is claim-free, so the
		// cleanup finalizer -- whose only job is to withdraw claims before the policy is deleted --
		// has nothing left to guard and is released. Without this a policy that once had a claim would
		// carry the finalizer forever, and disabling the feature and then deleting such a claim-free
		// policy would hang its deletion with no controller left to clear it, outside the documented
		// outstanding-claims caveat.
		if outstanding == 0 {
			if err := r.releaseFinalizerIfNoClaims(ctx, policy); err != nil {
				return outstanding, err
			}
		}
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
				// The labels select a policy's claims; spec.placementPolicyRef below carries
				// the policy's authoritative identity, which a label value cannot always hold.
				Labels: claimOwnershipLabels(policy),
			},
			Spec: kfplacementv1alpha1.ClusterClaimSpec{
				PlacementPolicyRef:   policyReference(policy),
				ClusterSelectorTerms: w.terms,
			},
		}
		if outstanding >= maxConcurrentClaimsPerPolicy {
			// Every slot is taken, by kept claims or by ones withdrawn moments ago that may
			// still be terminating; the create is retried when a watch frees a slot.
			break
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

	// The release decision cannot rest on the ownership labels: labels are mutable, and a claim
	// whose labels were stripped would vanish from a label-selected list, releasing the
	// finalizer over a claim that still exists -- permanently orphaned, since nothing else ever
	// looks at claims of a gone policy. The claim's spec.placementPolicyRef is immutable, so
	// every claim is listed and ownership is decided by the reference.
	claims := &kfplacementv1alpha1.ClusterClaimList{}
	if err := r.uncachedReader.List(ctx, claims); err != nil {
		klog.ErrorS(err, "Failed to list cluster claims for the deleted policy", "placementPolicy", klog.KObj(policy.Unwrap()))
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
	// Once the finalizer is gone the policy is deleted, and the reconcile that observes its
	// absence drops the metric series.
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

// claimBelongsTo reports whether a claim's immutable back-reference names the given policy.
// The API enforces the reference's immutability, which is what makes it, unlike the ownership
// labels, fit to gate the cleanup finalizer's release.
//
// The comparison deliberately ignores the reference's API group and version: kind, namespace,
// and name identify the same object across an API promotion, and a version-strict comparison
// would orphan every outstanding claim the moment the policy API moved to a new version.
func claimBelongsTo(claim *kfplacementv1alpha1.ClusterClaim, ref *kfplacementv1alpha1.ObjectReference) bool {
	got := claim.Spec.PlacementPolicyRef
	if got == nil || ref == nil {
		return false
	}
	return got.Kind == ref.Kind && got.Name == ref.Name && got.Namespace == ref.Namespace
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
func (r *Reconciler) listClaims(ctx context.Context, policy policyObject) ([]kfplacementv1alpha1.ClusterClaim, error) {
	all := &kfplacementv1alpha1.ClusterClaimList{}
	if err := r.List(ctx, all); err != nil {
		klog.ErrorS(err, "Failed to list cluster claims for the policy", "placementPolicy", klog.KObj(policy.Unwrap()))
		return nil, err
	}
	return claimsForPolicy(all.Items, policy), nil
}

// claimsForPolicy returns the claims whose immutable reference names the policy.
func claimsForPolicy(all []kfplacementv1alpha1.ClusterClaim, policy policyObject) []kfplacementv1alpha1.ClusterClaim {
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
func (r *Reconciler) releaseFinalizerIfNoClaims(ctx context.Context, policy policyObject) error {
	obj := policy.Unwrap()
	if !controllerutil.ContainsFinalizer(obj, claimCleanupFinalizer) {
		return nil
	}
	claims := &kfplacementv1alpha1.ClusterClaimList{}
	if err := r.uncachedReader.List(ctx, claims); err != nil {
		klog.ErrorS(err, "Failed to list cluster claims to release the cleanup finalizer", "placementPolicy", klog.KObj(obj))
		return err
	}
	if len(claimsForPolicy(claims.Items, policy)) > 0 {
		return nil
	}
	controllerutil.RemoveFinalizer(obj, claimCleanupFinalizer)
	return r.Update(ctx, obj)
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
