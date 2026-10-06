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
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
)

const (
	// defaultMaxProvisionDuration applies when the class sets none, per the API doc.
	defaultMaxProvisionDuration = time.Hour
	// MaxWithdrawalHold is how long a provider may hold a withdrawn claim before the contract
	// counts it as overrun; it is a reported bound, not an enforced one, since nothing but the
	// provider may remove its finalizer. The conformance suite asserts it.
	MaxWithdrawalHold = time.Hour
	// classRetryAfter is the wait before re-checking a claim whose class is missing; the class may
	// be on its way, and a missing class must never fail a claim.
	classRetryAfter = time.Minute
)

// Reconcile drives one claim through the contract: ignore what is not ours or not ready, accept,
// provision, report, and on withdrawal or expiry clean up what never joined.
//
// The claim is read from the API server rather than the cache. Claims are few (at most the fleet's
// concurrency limit), every transition here is a status write that re-enqueues the claim, and a
// round that starts on a cache lagging that write would repeat the provider call it just made and
// then conflict on its own status write. The uncached read keeps every provider call to one.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	claim := &kfplacementv1alpha1.ClusterClaim{}
	if err := r.reader.Get(ctx, req.NamespacedName, claim); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if claim.Spec.ClusterProviderClassName == "" {
		// Not stamped yet; the stamp is an update event.
		return ctrl.Result{}, nil
	}
	class := &kfplacementv1alpha1.ClusterProviderClass{}
	if err := r.Get(ctx, client.ObjectKey{Name: claim.Spec.ClusterProviderClassName}, class); err != nil {
		if apierrors.IsNotFound(err) {
			// Without the class there is no provisioner name to match and no parameters to hand
			// the provider, on withdrawal included; a missing class never fails a claim.
			klog.V(2).InfoS("The claim's class is missing; will check again", "clusterClaim", claim.Name, "class", claim.Spec.ClusterProviderClassName)
			return ctrl.Result{RequeueAfter: classRetryAfter}, nil
		}
		return ctrl.Result{}, err
	}
	if class.Spec.ProvisionerName != r.provisionerName {
		return ctrl.Result{}, nil
	}
	request := Request{
		ClaimName:                claim.Name,
		ClaimUID:                 claim.UID,
		ClusterName:              claim.Annotations[kfplacementv1alpha1.ProvisionedClusterNameAnnotation],
		ClusterSelectorTerms:     claim.Spec.ClusterSelectorTerms,
		ClusterProviderClassName: class.Name,
		Parameters:               class.Spec.Parameters,
	}
	if request.ClusterName == "" {
		request.ClusterName = ClusterNameFor(claim)
	}

	if !claim.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, r.withdrawn(ctx, claim, request)
	}
	if expired := meta.FindStatusCondition(claim.Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeExpired); expired != nil && expired.Status == metav1.ConditionTrue {
		return ctrl.Result{}, r.expired(ctx, claim, request, expired.Reason)
	}
	if isTerminal(claim) || !meta.IsStatusConditionTrue(claim.Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeApproved) {
		// Failed or denied claims are the record of what happened; unapproved ones are not ours
		// to act on. Either way the next transition is an update event.
		return ctrl.Result{}, nil
	}
	if meta.IsStatusConditionTrue(claim.Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeCompleted) {
		return ctrl.Result{}, nil
	}

	clusters := &clusterv1beta1.MemberClusterList{}
	if err := r.List(ctx, clusters); err != nil {
		return ctrl.Result{}, err
	}
	if !accepted(claim) {
		if stale(claim, clusters.Items) {
			// KubeFleet re-evaluates a claim whenever a cluster joins and either withdraws it or
			// advances its marker; a stale claim is requeued, never failed, and staleness only
			// keeps a provision from starting, never from resuming.
			klog.V(2).InfoS("The claim is stale; waiting for KubeFleet to re-evaluate it", "clusterClaim", claim.Name)
			return ctrl.Result{RequeueAfter: r.pollInterval}, nil
		}
		if err := r.hold(ctx, claim, request); err != nil {
			return ctrl.Result{}, err
		}
	}
	if !meta.IsStatusConditionTrue(claim.Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeAccepted) {
		// The second half of acceptance, which a crash after the first half may have skipped. The
		// status write is resourceVersion-conditional, so an expiry stamped since the read makes
		// it conflict, and the retry sees the expiry before any provider call.
		if err := r.markAccepted(ctx, claim, request); err != nil {
			return ctrl.Result{}, err
		}
	}
	return r.provision(ctx, claim, class, request, clusters.Items)
}

// accepted reports whether this provider holds the claim: the finalizer is the durable half of
// acceptance, and is what KubeFleet looks at too.
func accepted(claim *kfplacementv1alpha1.ClusterClaim) bool {
	return controllerutil.ContainsFinalizer(claim, kfplacementv1alpha1.FulfillerFinalizer)
}

// isTerminal reports whether the claim has failed or been denied.
func isTerminal(claim *kfplacementv1alpha1.ClusterClaim) bool {
	completed := meta.FindStatusCondition(claim.Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeCompleted)
	if completed != nil && completed.Status == metav1.ConditionFalse && completed.Reason == kfplacementv1alpha1.ClusterClaimCompletedCondReasonFailed {
		return true
	}
	approved := meta.FindStatusCondition(claim.Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeApproved)
	return approved != nil && approved.Status == metav1.ConditionFalse && approved.Reason == kfplacementv1alpha1.ClusterClaimApprovedCondReasonDenied
}

// stale reports whether a cluster joined the fleet after KubeFleet last evaluated the claim. An
// unset marker means KubeFleet has not stamped it yet or the fleet had no clusters when the claim
// was issued; either way there is nothing to be stale against, so the claim is fresh. A cluster
// created in the same second as the marker is not newer: the marker is the newest cluster's own
// creation time, to the second.
func stale(claim *kfplacementv1alpha1.ClusterClaim, clusters []clusterv1beta1.MemberCluster) bool {
	observed := claim.Status.LastObservedMostRecentClusterCreationTimestamp
	if observed == nil {
		return false
	}
	for i := range clusters {
		if clusters[i].CreationTimestamp.After(observed.Time) {
			return true
		}
	}
	return false
}

// hold takes the claim: the cluster name and the finalizer go in one resourceVersion-conditional
// update of the claim itself, so an expiry stamped after the read makes this write conflict and
// the retry sees it. The Accepted condition follows in markAccepted, so that a crash in between
// is repaired on the next round rather than leaving a held claim with no acceptance clock.
func (r *Reconciler) hold(ctx context.Context, claim *kfplacementv1alpha1.ClusterClaim, request Request) error {
	if claim.Annotations == nil {
		claim.Annotations = make(map[string]string, 1)
	}
	claim.Annotations[kfplacementv1alpha1.ProvisionedClusterNameAnnotation] = request.ClusterName
	controllerutil.AddFinalizer(claim, kfplacementv1alpha1.FulfillerFinalizer)
	return r.Update(ctx, claim)
}

// markAccepted writes the Accepted condition, which starts the maxProvisionDuration clock.
func (r *Reconciler) markAccepted(ctx context.Context, claim *kfplacementv1alpha1.ClusterClaim, request Request) error {
	meta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{
		Type:               kfplacementv1alpha1.ClusterClaimCondTypeAccepted,
		Status:             metav1.ConditionTrue,
		Reason:             kfplacementv1alpha1.ClusterClaimAcceptedCondReasonAccepted,
		Message:            fmt.Sprintf("Accepted by provider %s; the cluster will be named %s", r.provisionerName, request.ClusterName),
		ObservedGeneration: claim.Generation,
	})
	if err := r.Status().Update(ctx, claim); err != nil {
		return err
	}
	r.event(claim, corev1.EventTypeNormal, "Accepted", "Accepted by provider %s", r.provisionerName)
	klog.V(2).InfoS("Accepted a cluster claim", "clusterClaim", claim.Name, "cluster", request.ClusterName, "provisioner", r.provisionerName)
	return nil
}

// provision applies the identity rule and drives the provider. The order matters: a cluster
// carrying this claim's UID is this claim's own and is resumed, never remediated; an owned cluster
// of another UID that is registered but not eligible is the dark-cluster case the claim was
// re-issued for; otherwise the selector needs one more cluster.
func (r *Reconciler) provision(ctx context.Context, claim *kfplacementv1alpha1.ClusterClaim, class *kfplacementv1alpha1.ClusterProviderClass, request Request, clusters []clusterv1beta1.MemberCluster) (ctrl.Result, error) {
	var own, dark *clusterv1beta1.MemberCluster
	nameLabel := request.OwnershipLabels()[kfplacementv1alpha1.FulfilledClaimNameLabel]
	for i := range clusters {
		cluster := &clusters[i]
		switch {
		case cluster.Labels[kfplacementv1alpha1.FulfilledClaimUIDLabel] == string(claim.UID):
			own = cluster
		case cluster.Labels[kfplacementv1alpha1.FulfilledClaimNameLabel] != nameLabel, !cluster.DeletionTimestamp.IsZero():
			// Another claim's cluster, or one on its way out: not a candidate for remediation.
		default:
			// The lowest name wins, so the remediation target and the Failed message do not
			// wander between two dark clusters from one round to the next.
			if eligible, _ := r.eligibility.IsEligible(cluster); !eligible && (dark == nil || cluster.Name < dark.Name) {
				dark = cluster
			}
		}
	}

	switch {
	case own != nil && !own.DeletionTimestamp.IsZero():
		// This claim's own cluster is being removed; reporting it fulfilled would name a cluster
		// that is leaving. Wait for it to go, within the provision bound, then provision again.
		return r.retryOrFail(ctx, claim, class, request, fmt.Errorf("cluster %s is being deleted", own.Name))
	case own != nil:
		// Registered: fulfilled, whether this round or a previous process did the work.
		return ctrl.Result{}, r.complete(ctx, claim, own.Name)
	case dark != nil:
		remediator, canRemediate := r.provisioner.(Remediator)
		if !canRemediate {
			return ctrl.Result{}, r.fail(ctx, claim, request, fmt.Sprintf("cluster %s is registered for this claim but not eligible for scheduling, and provider %s cannot remediate it; delete the MemberCluster to have a replacement provisioned", dark.Name, r.provisionerName))
		}
		if err := remediator.Remediate(ctx, request, dark); err != nil {
			return r.retryOrFail(ctx, claim, class, request, err)
		}
		// Remediation is observed through eligibility on a later round, within the same time
		// bound as a provision.
		return r.retryOrFail(ctx, claim, class, request, fmt.Errorf("cluster %s is being remediated", dark.Name))
	}

	if err := r.provisioner.Provision(ctx, request); err != nil {
		return r.retryOrFail(ctx, claim, class, request, err)
	}
	return ctrl.Result{}, r.complete(ctx, claim, request.ClusterName)
}

// retryOrFail turns a provider error into a retry, or into a failed claim when it is permanent
// or the class's maxProvisionDuration has elapsed since acceptance.
func (r *Reconciler) retryOrFail(ctx context.Context, claim *kfplacementv1alpha1.ClusterClaim, class *kfplacementv1alpha1.ClusterProviderClass, request Request, err error) (ctrl.Result, error) {
	if IsPermanent(err) {
		return ctrl.Result{}, r.fail(ctx, claim, request, err.Error())
	}
	bound := defaultMaxProvisionDuration
	if class.Spec.MaxProvisionDuration != nil {
		bound = class.Spec.MaxProvisionDuration.Duration
	}
	if since := meta.FindStatusCondition(claim.Status.Conditions, kfplacementv1alpha1.ClusterClaimCondTypeAccepted); since != nil && time.Since(since.LastTransitionTime.Time) > bound {
		return ctrl.Result{}, r.fail(ctx, claim, request, fmt.Sprintf("provisioning did not complete within %s of acceptance; last error: %v", bound, err))
	}
	r.event(claim, corev1.EventTypeNormal, "Provisioning", "Still provisioning cluster %s: %v", request.ClusterName, err)
	klog.V(2).InfoS("Provisioning is still in progress", "clusterClaim", claim.Name, "cluster", request.ClusterName, "error", err)
	return ctrl.Result{RequeueAfter: r.pollInterval}, nil
}

// complete reports the registered cluster on the claim.
func (r *Reconciler) complete(ctx context.Context, claim *kfplacementv1alpha1.ClusterClaim, clusterName string) error {
	claim.Status.ProvisionedClusterName = &clusterName
	meta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{
		Type:               kfplacementv1alpha1.ClusterClaimCondTypeCompleted,
		Status:             metav1.ConditionTrue,
		Reason:             kfplacementv1alpha1.ClusterClaimCompletedCondReasonFulfilled,
		Message:            fmt.Sprintf("Cluster %s is registered with the fleet", clusterName),
		ObservedGeneration: claim.Generation,
	})
	if err := r.Status().Update(ctx, claim); err != nil {
		return err
	}
	claimOutcomes.WithLabelValues(r.provisionerName, "fulfilled").Inc()
	r.event(claim, corev1.EventTypeNormal, "Fulfilled", "Cluster %s is registered", clusterName)
	klog.V(2).InfoS("Fulfilled a cluster claim", "clusterClaim", claim.Name, "cluster", clusterName)
	return nil
}

// fail ends the claim with a permanent failure. Partial infrastructure for this claim's own
// cluster is removed first, so that a held claim does not keep it around; the withdrawal-time
// deprovision remains the idempotent backstop. A cleanup that keeps erroring keeps the round
// erroring (with the controller's backoff) and the claim not yet failed, which the event reports.
func (r *Reconciler) fail(ctx context.Context, claim *kfplacementv1alpha1.ClusterClaim, request Request, message string) error {
	if err := r.deprovision(ctx, request); err != nil {
		r.event(claim, corev1.EventTypeWarning, "CleanupFailed", "Cannot clean up cluster %s before failing the claim: %v", request.ClusterName, err)
		return err
	}
	meta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{
		Type:               kfplacementv1alpha1.ClusterClaimCondTypeCompleted,
		Status:             metav1.ConditionFalse,
		Reason:             kfplacementv1alpha1.ClusterClaimCompletedCondReasonFailed,
		Message:            message,
		ObservedGeneration: claim.Generation,
	})
	if err := r.Status().Update(ctx, claim); err != nil {
		return err
	}
	claimOutcomes.WithLabelValues(r.provisionerName, "failed").Inc()
	r.event(claim, corev1.EventTypeWarning, "Failed", "%s", message)
	klog.V(2).InfoS("Failed a cluster claim", "clusterClaim", claim.Name, "reason", message)
	return nil
}

// withdrawn handles a claim with a deletion timestamp. The claim's conditions are still readable,
// and they decide: a fulfilled claim's cluster belongs to its MemberCluster now, so only one that
// never joined and is not eligible now is deprovisioned (a withdrawal inside the join window);
// an in-flight or failed claim's cluster is removed; nothing else is ours.
func (r *Reconciler) withdrawn(ctx context.Context, claim *kfplacementv1alpha1.ClusterClaim, request Request) error {
	if !accepted(claim) {
		return nil
	}
	if time.Since(claim.DeletionTimestamp.Time) > MaxWithdrawalHold {
		klog.Warningf("Cluster claim %s has been withdrawn for longer than %s and is still held by provider %s", claim.Name, MaxWithdrawalHold, r.provisionerName)
	}
	if err := r.deprovision(ctx, request); err != nil {
		r.event(claim, corev1.EventTypeWarning, "CleanupFailed", "Cannot clean up cluster %s for the withdrawn claim: %v", request.ClusterName, err)
		return err
	}
	return r.release(ctx, claim)
}

// expired handles a claim KubeFleet marked expired while it still exists: a join timeout means
// the cluster is removed if it never joined, a not-matching cluster is left alone, and a pending
// timeout means nothing should have been started, so anything the pre-recorded name points at is
// removed. In every case the claim is released; KubeFleet keeps it as the record.
func (r *Reconciler) expired(ctx context.Context, claim *kfplacementv1alpha1.ClusterClaim, request Request, reason string) error {
	if !accepted(claim) {
		return nil
	}
	if reason != kfplacementv1alpha1.ClusterClaimExpiredCondReasonNotMatching {
		if err := r.deprovision(ctx, request); err != nil {
			r.event(claim, corev1.EventTypeWarning, "CleanupFailed", "Cannot clean up cluster %s for the expired claim: %v", request.ClusterName, err)
			return err
		}
	}
	return r.release(ctx, claim)
}

// deprovision applies the one rule that gates every Deprovision call: a cluster is removed only
// if it has not joined the fleet and is not eligible for scheduling now, re-read from the API
// server right here, since the cache can lag the member agent's join by the very moment this
// decides. A cluster that joined belongs to its MemberCluster, whatever its eligibility is at
// this instant; a cluster that is eligible now is one the policy controller is about to withdraw
// by rotation.
func (r *Reconciler) deprovision(ctx context.Context, request Request) error {
	cluster := &clusterv1beta1.MemberCluster{}
	switch err := r.reader.Get(ctx, client.ObjectKey{Name: request.ClusterName}, cluster); {
	case apierrors.IsNotFound(err):
		// Nothing registered; the provider may still hold infrastructure, and Deprovision is
		// idempotent.
	case err != nil:
		return err
	case joined(cluster):
		klog.V(2).InfoS("Leaving a cluster that joined the fleet in place", "clusterClaim", request.ClaimName, "cluster", request.ClusterName)
		return nil
	default:
		if eligible, _ := r.eligibility.IsEligible(cluster); eligible {
			klog.V(2).InfoS("Leaving an eligible cluster in place", "clusterClaim", request.ClaimName, "cluster", request.ClusterName)
			return nil
		}
	}
	if err := r.provisioner.Deprovision(ctx, request); err != nil {
		return err
	}
	klog.V(2).InfoS("Deprovisioned a cluster that never joined the fleet", "clusterClaim", request.ClaimName, "cluster", request.ClusterName)
	return nil
}

// release drops the provider's finalizer.
func (r *Reconciler) release(ctx context.Context, claim *kfplacementv1alpha1.ClusterClaim) error {
	controllerutil.RemoveFinalizer(claim, kfplacementv1alpha1.FulfillerFinalizer)
	return client.IgnoreNotFound(r.Update(ctx, claim))
}

func (r *Reconciler) event(claim *kfplacementv1alpha1.ClusterClaim, eventType, reason, format string, args ...any) {
	if r.recorder != nil {
		r.recorder.Eventf(claim, eventType, reason, format, args...)
	}
}
