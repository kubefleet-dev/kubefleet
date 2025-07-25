/*
Copyright 2025 The KubeFleet Authors.

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

// Package clusterresourceplacement features a controller to reconcile the clusterResourcePlacement changes.
package clusterresourceplacement

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	fleetv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/scheduler/queue"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/annotations"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/condition"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/controller"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/controller/metrics"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/defaulter"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/labels"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/resource"
	fleettime "github.com/kubefleet-dev/kubefleet/pkg/utils/time"
)

// The max size of an object in k8s is 1.5MB because of ETCD limit https://etcd.io/docs/v3.3/dev-guide/limit/.
// We choose 800KB as the soft limit for all the selected resources within one clusterResourceSnapshot object because of this test in k8s which checks
// if object size is greater than 1MB https://github.com/kubernetes/kubernetes/blob/db1990f48b92d603f469c1c89e2ad36da1b74846/test/integration/master/synthetic_master_test.go#L337
var resourceSnapshotResourceSizeLimit = 800 * (1 << 10) // 800KB

// We use a safety resync period to requeue all the finished request just in case there is a bug in the system.
// TODO: unify all the controllers with this pattern and make this configurable in place of the controller runtime resync period.
const controllerResyncPeriod = 30 * time.Minute

func (r *Reconciler) Reconcile(ctx context.Context, key controller.QueueKey) (ctrl.Result, error) {
	placementKey, ok := key.(string)
	if !ok {
		err := fmt.Errorf("get place key %+v not of type string", key)
		klog.ErrorS(controller.NewUnexpectedBehaviorError(err), "We have encountered a fatal error that can't be retried, requeue after a day")
		return ctrl.Result{}, nil // ignore this unexpected error
	}

	startTime := time.Now()
	klog.V(2).InfoS("Placement reconciliation starts", "placementKey", placementKey)
	defer func() {
		latency := time.Since(startTime).Milliseconds()
		klog.V(2).InfoS("Placement reconciliation ends", "placementKey", placementKey, "latency", latency)
	}()

	placementObj, err := controller.FetchPlacementFromKey(ctx, r.Client, queue.PlacementKey(placementKey))
	if err != nil {
		if apierrors.IsNotFound(err) {
			klog.V(4).InfoS("Ignoring NotFound placement", "placementKey", placementKey)
			return ctrl.Result{}, nil
		}
		klog.ErrorS(err, "Failed to get placement", "placementKey", placementKey)
		return ctrl.Result{}, controller.NewAPIServerError(true, err)
	}

	if placementObj.GetDeletionTimestamp() != nil {
		return r.handleDelete(ctx, placementObj)
	}

	// register finalizer
	if !controllerutil.ContainsFinalizer(placementObj, fleetv1beta1.PlacementCleanupFinalizer) {
		controllerutil.AddFinalizer(placementObj, fleetv1beta1.PlacementCleanupFinalizer)
		if err := r.Client.Update(ctx, placementObj); err != nil {
			klog.ErrorS(err, "Failed to add placement finalizer", "placement", klog.KObj(placementObj))
			return ctrl.Result{}, controller.NewUpdateIgnoreConflictError(err)
		}
	}
	defer emitPlacementStatusMetric(placementObj)
	return r.handleUpdate(ctx, placementObj)
}

func (r *Reconciler) handleDelete(ctx context.Context, placementObj fleetv1beta1.PlacementObj) (ctrl.Result, error) {
	placementKObj := klog.KObj(placementObj)
	if !controllerutil.ContainsFinalizer(placementObj, fleetv1beta1.PlacementCleanupFinalizer) {
		klog.V(4).InfoS("placement is being deleted and no cleanup work needs to be done by the CRP controller, waiting for the scheduler to cleanup the bindings", "placement", placementKObj)
		return ctrl.Result{}, nil
	}
	klog.V(2).InfoS("Removing snapshots created by placement", "placement", placementKObj)
	if err := controller.DeletePolicySnapshots(ctx, r.Client, placementObj); err != nil {
		return ctrl.Result{}, err
	}
	if err := controller.DeleteResourceSnapshots(ctx, r.Client, placementObj); err != nil {
		return ctrl.Result{}, err
	}
	// change the metrics to add nameplace of namespace
	metrics.FleetPlacementStatusLastTimeStampSeconds.DeletePartialMatch(prometheus.Labels{"name": placementObj.GetName()})
	controllerutil.RemoveFinalizer(placementObj, fleetv1beta1.PlacementCleanupFinalizer)
	if err := r.Client.Update(ctx, placementObj); err != nil {
		klog.ErrorS(err, "Failed to remove placement finalizer", "placement", placementKObj)
		return ctrl.Result{}, err
	}
	klog.V(2).InfoS("Removed placement-cleanup finalizer", "placement", placementKObj)
	r.Recorder.Event(placementObj, corev1.EventTypeNormal, "PlacementCleanupFinalizerRemoved", "Deleted the snapshots and removed the placement cleanup finalizer")
	return ctrl.Result{}, nil
}

// handleUpdate handles the create/update placement event.
// It creates corresponding clusterSchedulingPolicySnapshot and clusterResourceSnapshot if needed and updates the status based on
// clusterSchedulingPolicySnapshot status and work status.
// If the error type is ErrUnexpectedBehavior, the controller will skip the reconciling.
func (r *Reconciler) handleUpdate(ctx context.Context, placementObj fleetv1beta1.PlacementObj) (ctrl.Result, error) {
	revisionLimit := int32(defaulter.DefaultRevisionHistoryLimitValue)
	placementKObj := klog.KObj(placementObj)
	oldPlacement := placementObj.DeepCopyObject().(fleetv1beta1.PlacementObj)
	placementSpec := placementObj.GetPlacementSpec()

	if placementSpec.RevisionHistoryLimit != nil {
		if revisionLimit <= 0 {
			err := fmt.Errorf("invalid placement %s: invalid revisionHistoryLimit %d", placementObj.GetName(), revisionLimit)
			klog.ErrorS(controller.NewUnexpectedBehaviorError(err), "Invalid revisionHistoryLimit value and using default value instead", "placement", placementKObj)
		} else {
			revisionLimit = *placementSpec.RevisionHistoryLimit
		}
	}

	// validate the resource selectors first before creating any snapshot
	envelopeObjCount, selectedResources, selectedResourceIDs, err := r.selectResourcesForPlacement(placementObj)
	if err != nil {
		klog.ErrorS(err, "Failed to select the resources", "placement", placementKObj)
		if !errors.Is(err, controller.ErrUserError) {
			return ctrl.Result{}, err
		}

		// TODO, create a separate user type error struct to improve the user facing messages
		scheduleCondition := metav1.Condition{
			Status:             metav1.ConditionFalse,
			Type:               string(fleetv1beta1.ClusterResourcePlacementScheduledConditionType),
			Reason:             condition.InvalidResourceSelectorsReason,
			Message:            fmt.Sprintf("The resource selectors are invalid: %v", err),
			ObservedGeneration: placementObj.GetGeneration(),
		}
		placementObj.SetConditions(scheduleCondition)
		if updateErr := r.Client.Status().Update(ctx, placementObj); updateErr != nil {
			klog.ErrorS(updateErr, "Failed to update the status", "placement", placementKObj)
			return ctrl.Result{}, controller.NewUpdateIgnoreConflictError(updateErr)
		}
		// no need to retry faster, the user needs to fix the resource selectors
		return ctrl.Result{RequeueAfter: controllerResyncPeriod}, nil
	}

	latestSchedulingPolicySnapshot, err := r.getOrCreateSchedulingPolicySnapshot(ctx, placementObj, int(revisionLimit))
	if err != nil {
		klog.ErrorS(err, "Failed to select resources for placement", "placement", placementKObj)
		return ctrl.Result{}, err
	}

	createResourceSnapshotRes, latestResourceSnapshot, err := r.getOrCreateResourceSnapshot(ctx, placementObj, envelopeObjCount,
		&fleetv1beta1.ResourceSnapshotSpec{SelectedResources: selectedResources}, int(revisionLimit))
	if err != nil {
		return ctrl.Result{}, err
	}

	// We don't requeue the request here immediately so that placement can keep tracking the rollout status.
	if createResourceSnapshotRes.Requeue {
		latestResourceSnapshotKObj := klog.KObj(latestResourceSnapshot)
		// We cannot create the resource snapshot immediately because of the resource snapshot creation interval.
		// Rebuild the seletedResourceIDs using the latestResourceSnapshot.
		latestResourceSnapshotIndex, err := labels.ExtractResourceIndexFromResourceSnapshot(latestResourceSnapshot)
		if err != nil {
			klog.ErrorS(err, "Failed to extract the resource index from the clusterResourceSnapshot", "placement", placementKObj, "clusterResourceSnapshot", latestResourceSnapshotKObj)
			return ctrl.Result{}, controller.NewUnexpectedBehaviorError(err)
		}
		selectedResourceIDs, err = controller.CollectResourceIdentifiersUsingMasterResourceSnapshot(ctx, r.Client, placementObj.GetName(), latestResourceSnapshot, strconv.Itoa(latestResourceSnapshotIndex))
		if err != nil {
			klog.ErrorS(err, "Failed to collect resource identifiers from the clusterResourceSnapshot", "placement", placementKObj, "clusterResourceSnapshot", latestResourceSnapshotKObj)
			return ctrl.Result{}, err
		}
		klog.V(2).InfoS("Fetched the selected resources from the lastestResourceSnapshot", "placement", placementKObj, "clusterResourceSnapshot", latestResourceSnapshotKObj, "generation", placementObj.GetGeneration())
	}

	// isClusterScheduled is to indicate whether we need to requeue the placement request to track the rollout status.
	isClusterScheduled, err := r.setPlacementStatus(ctx, placementObj, selectedResourceIDs, latestSchedulingPolicySnapshot, latestResourceSnapshot)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.Client.Status().Update(ctx, placementObj); err != nil {
		klog.ErrorS(err, "Failed to update the status", "placement", placementKObj)
		return ctrl.Result{}, err
	}
	klog.V(2).InfoS("Updated the placement status", "placement", placementKObj)

	// We skip checking the last resource condition (available) because it will be covered by checking isRolloutCompleted func.
	for i := condition.RolloutStartedCondition; i < condition.TotalCondition-1; i++ {
		oldCond := oldPlacement.GetCondition(string(i.ClusterResourcePlacementConditionType()))
		newCond := placementObj.GetCondition(string(i.ClusterResourcePlacementConditionType()))
		if !condition.IsConditionStatusTrue(oldCond, oldPlacement.GetGeneration()) &&
			condition.IsConditionStatusTrue(newCond, placementObj.GetGeneration()) {
			klog.V(2).InfoS("Placement resource condition status has been changed to true", "placement", placementKObj, "generation", placementObj.GetGeneration(), "condition", i.ClusterResourcePlacementConditionType())
			r.Recorder.Event(placementObj, corev1.EventTypeNormal, i.EventReasonForTrue(), i.EventMessageForTrue())
		}
	}

	// Rollout is considered to be completed when all the expected condition types are set to the
	// True status.
	if isRolloutCompleted(placementObj) {
		if !isRolloutCompleted(oldPlacement) {
			klog.V(2).InfoS("Placement has finished the rollout process and reached the desired status", "placement", placementKObj, "generation", placementObj.GetGeneration())
			r.Recorder.Event(placementObj, corev1.EventTypeNormal, "PlacementRolloutCompleted", "Placement has finished the rollout process and reached the desired status")
		}
		if createResourceSnapshotRes.Requeue {
			klog.V(2).InfoS("Requeue the request to handle the new resource snapshot", "placement", placementKObj, "generation", placementObj.GetGeneration())
			// We requeue the request to handle the resource snapshot.
			return createResourceSnapshotRes, nil
		}
		// We don't need to requeue any request now by watching the binding changes
		return ctrl.Result{}, nil
	}

	if !isClusterScheduled {
		// Note:
		// If the scheduledCondition is failed, it means the placement requirement cannot be satisfied fully. For example,
		// pickN deployment requires 5 clusters and scheduler schedules the resources on 3 clusters. And the appliedCondition
		// could be true when resources are applied successfully on these 3 clusters and the detailed the resourcePlacementStatuses
		// need to be populated.
		// So that we cannot rely on the scheduledCondition as false to decide whether to requeue the request.

		// When isClusterScheduled is false, either scheduler has not finished the scheduling or none of the clusters could be selected.
		// Once the policy snapshot status changes, the policy snapshot watcher should enqueue the request.
		// Here we requeue the request to prevent a bug in the watcher.
		klog.V(2).InfoS("Scheduler has not scheduled any cluster yet and requeue the request as a backup",
			"placement", placementKObj, "scheduledCondition", placementObj.GetCondition(string(fleetv1beta1.ClusterResourcePlacementScheduledConditionType)), "generation", placementObj.GetGeneration())
		if createResourceSnapshotRes.Requeue {
			klog.V(2).InfoS("Requeue the request to handle the new resource snapshot", "placement", placementKObj, "generation", placementObj.GetGeneration())
			// We requeue the request to handle the resource snapshot.
			return createResourceSnapshotRes, nil
		}
		return ctrl.Result{RequeueAfter: controllerResyncPeriod}, nil
	}
	klog.V(2).InfoS("Placement rollout has not finished yet and requeue the request", "placement", placementKObj, "status", placementObj.GetPlacementStatus(), "generation", placementObj.GetGeneration())
	if createResourceSnapshotRes.Requeue {
		klog.V(2).InfoS("Requeue the request to handle the new resource snapshot", "placement", placementKObj, "generation", placementObj.GetGeneration())
		// We requeue the request to handle the resource snapshot.
		return createResourceSnapshotRes, nil
	}
	// no need to requeue the request as the binding status will be changed but we add a long resync loop just in case.
	return ctrl.Result{RequeueAfter: controllerResyncPeriod}, nil
}

func (r *Reconciler) getOrCreateSchedulingPolicySnapshot(ctx context.Context, placementObj fleetv1beta1.PlacementObj, revisionHistoryLimit int) (fleetv1beta1.PolicySnapshotObj, error) {
	placementKObj := klog.KObj(placementObj)
	placementSpec := placementObj.GetPlacementSpec()
	schedulingPolicy := placementSpec.Policy.DeepCopy()
	if schedulingPolicy != nil {
		schedulingPolicy.NumberOfClusters = nil // will exclude the numberOfClusters
	}
	policyHash, err := resource.HashOf(schedulingPolicy)
	if err != nil {
		klog.ErrorS(err, "Failed to generate policy hash of placement", "placement", placementKObj)
		return nil, controller.NewUnexpectedBehaviorError(err)
	}

	// Use the unified helper function to fetch the latest policy snapshot
	placementKey := types.NamespacedName{Name: placementObj.GetName(), Namespace: placementObj.GetNamespace()}
	latestPolicySnapshot, latestPolicySnapshotIndex, err := controller.FetchLatestPolicySnapshot(ctx, r.Client, placementKey)
	if err != nil {
		return nil, err
	}

	if latestPolicySnapshot != nil && string(latestPolicySnapshot.GetPolicySnapshotSpec().PolicyHash) == policyHash {
		if err := r.ensureLatestPolicySnapshot(ctx, placementObj, latestPolicySnapshot); err != nil {
			return nil, err
		}
		klog.V(2).InfoS("Policy has not been changed and updated the existing policySnapshot", "placement", placementKObj, "policySnapshot", klog.KObj(latestPolicySnapshot))
		return latestPolicySnapshot, nil
	}

	// Need to create new snapshot when 1) there is no snapshots or 2) the latest snapshot hash != current one.
	// mark the last policy snapshot as inactive if it is different from what we have now
	if latestPolicySnapshot != nil &&
		string(latestPolicySnapshot.GetPolicySnapshotSpec().PolicyHash) != policyHash &&
		latestPolicySnapshot.GetLabels()[fleetv1beta1.IsLatestSnapshotLabel] == strconv.FormatBool(true) {
		// set the latest label to false first to make sure there is only one or none active policy snapshot
		labels := latestPolicySnapshot.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}
		labels[fleetv1beta1.IsLatestSnapshotLabel] = strconv.FormatBool(false)
		latestPolicySnapshot.SetLabels(labels)
		if err := r.Client.Update(ctx, latestPolicySnapshot); err != nil {
			klog.ErrorS(err, "Failed to set the isLatestSnapshot label to false", "placement", placementKObj, "policySnapshot", klog.KObj(latestPolicySnapshot))
			return nil, controller.NewUpdateIgnoreConflictError(err)
		}
		klog.V(2).InfoS("Marked the existing policySnapshot as inactive", "placement", placementKObj, "policySnapshot", klog.KObj(latestPolicySnapshot))
	}

	// delete redundant snapshot revisions before creating a new snapshot to guarantee that the number of snapshots
	// won't exceed the limit.
	if err := r.deleteRedundantSchedulingPolicySnapshots(ctx, placementObj, revisionHistoryLimit); err != nil {
		return nil, err
	}

	// create a new policy snapshot
	latestPolicySnapshotIndex++
	newPolicySnapshot := controller.BuildPolicySnapshot(placementObj, latestPolicySnapshotIndex, policyHash)

	policySnapshotKObj := klog.KObj(newPolicySnapshot)
	if err := controllerutil.SetControllerReference(placementObj, newPolicySnapshot, r.Scheme); err != nil {
		klog.ErrorS(err, "Failed to set owner reference", "policySnapshot", policySnapshotKObj)
		// should never happen
		return nil, controller.NewUnexpectedBehaviorError(err)
	}

	if err := r.Client.Create(ctx, newPolicySnapshot); err != nil {
		klog.ErrorS(err, "Failed to create new policySnapshot", "policySnapshot", policySnapshotKObj)
		return nil, controller.NewAPIServerError(false, err)
	}
	klog.V(2).InfoS("Created new policySnapshot", "placement", placementKObj, "policySnapshot", policySnapshotKObj)
	return newPolicySnapshot, nil
}

func (r *Reconciler) deleteRedundantSchedulingPolicySnapshots(ctx context.Context, placementObj fleetv1beta1.PlacementObj, revisionHistoryLimit int) error {
	sortedList, err := r.listSortedClusterSchedulingPolicySnapshots(ctx, placementObj)
	if err != nil {
		return err
	}

	items := sortedList.GetPolicySnapshotObjs()
	if len(items) < revisionHistoryLimit {
		return nil
	}

	if len(items)-revisionHistoryLimit > 0 {
		// We always delete before creating a new snapshot, the snapshot size should never exceed the limit as there is
		// no finalizer added and object should be deleted immediately.
		klog.Warning("The number of policySnapshots exceeds the revisionHistoryLimit and it should never happen", "placement", klog.KObj(placementObj), "numberOfSnapshots", len(items), "revisionHistoryLimit", revisionHistoryLimit)
	}

	// In normal situation, The max of len(sortedList) should be revisionHistoryLimit.
	// We just need to delete one policySnapshot before creating a new one.
	// As a result of defensive programming, it will delete any redundant snapshots which could be more than one.
	for i := 0; i <= len(items)-revisionHistoryLimit; i++ { // need to reserve one slot for the new snapshot
		if err := r.Client.Delete(ctx, items[i]); err != nil && !apierrors.IsNotFound(err) {
			klog.ErrorS(err, "Failed to delete policySnapshot", "placement", klog.KObj(placementObj), "policySnapshot", klog.KObj(items[i]))
			return controller.NewAPIServerError(false, err)
		}
	}
	return nil
}

// deleteRedundantResourceSnapshots handles multiple snapshots in a group.
func (r *Reconciler) deleteRedundantResourceSnapshots(ctx context.Context, placementObj fleetv1beta1.PlacementObj, revisionHistoryLimit int) error {
	sortedList, err := r.listSortedResourceSnapshots(ctx, placementObj)
	if err != nil {
		return err
	}

	items := sortedList.GetResourceSnapshotObjs()
	if len(items) < revisionHistoryLimit {
		// If the number of existing snapshots is less than the limit no matter how many snapshots in a group, we don't
		// need to delete any snapshots.
		// Skip the checking and deleting.
		return nil
	}

	placementKObj := klog.KObj(placementObj)
	lastGroupIndex := -1
	groupCounter := 0

	// delete the snapshots from the end as there are could be multiple snapshots in a group in order to keep the latest
	// snapshots from the end.
	for i := len(items) - 1; i >= 0; i-- {
		snapshotKObj := klog.KObj(items[i])
		ii, err := labels.ExtractResourceIndexFromResourceSnapshot(items[i])
		if err != nil {
			klog.ErrorS(err, "Failed to parse the resource index label", "placement", placementKObj, "resourceSnapshot", snapshotKObj)
			return controller.NewUnexpectedBehaviorError(err)
		}
		if ii != lastGroupIndex {
			groupCounter++
			lastGroupIndex = ii
		}
		if groupCounter < revisionHistoryLimit { // need to reserve one slot for the new snapshot
			// When the number of group is less than the revision limit, skipping deleting the snapshot.
			continue
		}
		if err := r.Client.Delete(ctx, items[i]); err != nil && !apierrors.IsNotFound(err) {
			klog.ErrorS(err, "Failed to delete resourceSnapshot", "placement", placementKObj, "resourceSnapshot", snapshotKObj)
			return controller.NewAPIServerError(false, err)
		}
	}
	if groupCounter-revisionHistoryLimit > 0 {
		// We always delete before creating a new snapshot, the snapshot group size should never exceed the limit
		// as there is no finalizer added and the object should be deleted immediately.
		klog.Warning("The number of resourceSnapshot groups exceeds the revisionHistoryLimit and it should never happen", "placement", placementKObj, "numberOfSnapshotGroups", groupCounter, "revisionHistoryLimit", revisionHistoryLimit)
	}
	return nil
}

// getOrCreateResourceSnapshot gets or creates a resource snapshot for the given placement.
// It returns the latest resource snapshot if it exists and is up to date, otherwise it creates a new one.
// It also returns the ctrl.Result to indicate whether the request should be requeued or not.
// Note: when the ctrl.Result.Requeue is true, it still returns the current latest resourceSnapshot so that
// placement can update the rollout status.
func (r *Reconciler) getOrCreateResourceSnapshot(ctx context.Context, placement fleetv1beta1.PlacementObj, envelopeObjCount int, resourceSnapshotSpec *fleetv1beta1.ResourceSnapshotSpec, revisionHistoryLimit int) (ctrl.Result, fleetv1beta1.ResourceSnapshotObj, error) {
	placementKObj := klog.KObj(placement)
	resourceHash, err := resource.HashOf(resourceSnapshotSpec)
	if err != nil {
		klog.ErrorS(err, "Failed to generate resource hash", "placement", placementKObj)
		return ctrl.Result{}, nil, controller.NewUnexpectedBehaviorError(err)
	}

	// latestResourceSnapshotIndex should be -1 when there is no snapshot.
	latestResourceSnapshot, latestResourceSnapshotIndex, err := r.lookupLatestResourceSnapshot(ctx, placement)
	if err != nil {
		return ctrl.Result{}, nil, err
	}

	latestResourceSnapshotHash := ""
	numberOfSnapshots := -1
	if latestResourceSnapshot != nil {
		latestResourceSnapshotHash, err = annotations.ParseResourceGroupHashFromAnnotation(latestResourceSnapshot)
		if err != nil {
			klog.ErrorS(err, "Failed to get the ResourceGroupHashAnnotation", "clusterResourceSnapshot", klog.KObj(latestResourceSnapshot))
			return ctrl.Result{}, nil, controller.NewUnexpectedBehaviorError(err)
		}
		numberOfSnapshots, err = annotations.ExtractNumberOfResourceSnapshotsFromResourceSnapshot(latestResourceSnapshot)
		if err != nil {
			klog.ErrorS(err, "Failed to get the NumberOfResourceSnapshotsAnnotation", "clusterResourceSnapshot", klog.KObj(latestResourceSnapshot))
			return ctrl.Result{}, nil, controller.NewUnexpectedBehaviorError(err)
		}
	}

	shouldCreateNewMasterResourceSnapshot := true
	// This index indicates the selected resource in the split selectedResourceList, if this index is zero we start
	// from creating the master clusterResourceSnapshot if it's greater than zero it means that the master clusterResourceSnapshot
	// got created but not all sub-indexed clusterResourceSnapshots have been created yet. It covers the corner case where the
	// controller crashes in the middle.
	resourceSnapshotStartIndex := 0
	if latestResourceSnapshot != nil && latestResourceSnapshotHash == resourceHash {
		if err := r.ensureLatestResourceSnapshot(ctx, latestResourceSnapshot); err != nil {
			return ctrl.Result{}, nil, err
		}
		// check to see all that the master cluster resource snapshot and sub-indexed snapshots belonging to the same group index exists.
		resourceSnapshotList, err := controller.ListAllResourceSnapshotWithAnIndex(ctx, r.Client, latestResourceSnapshot.GetLabels()[fleetv1beta1.ResourceIndexLabel], placement.GetName(), placement.GetNamespace())
		if err != nil {
			klog.ErrorS(err, "Failed to list the latest group resourceSnapshots associated with the placement", "placement", placement.GetName())
			return ctrl.Result{}, nil, controller.NewAPIServerError(true, err)
		}
		if len(resourceSnapshotList.GetResourceSnapshotObjs()) == numberOfSnapshots {
			klog.V(2).InfoS("ClusterResourceSnapshots have not changed", "placement", placementKObj, "clusterResourceSnapshot", klog.KObj(latestResourceSnapshot))
			return ctrl.Result{}, latestResourceSnapshot, nil
		}
		// we should not create a new master cluster resource snapshot.
		shouldCreateNewMasterResourceSnapshot = false
		// set resourceSnapshotStartIndex to start from this index, so we don't try to recreate existing sub-indexed cluster resource snapshots.
		resourceSnapshotStartIndex = len(resourceSnapshotList.GetResourceSnapshotObjs())
	}

	// Need to create new snapshot when 1) there is no snapshots or 2) the latest snapshot hash != current one.
	// mark the last resource snapshot as inactive if it is different from what we have now or 3) when some
	// sub-indexed cluster resource snapshots belonging to the same group have not been created, the master
	// cluster resource snapshot should exist and be latest.
	if latestResourceSnapshot != nil && latestResourceSnapshotHash != resourceHash && latestResourceSnapshot.GetLabels()[fleetv1beta1.IsLatestSnapshotLabel] == strconv.FormatBool(true) {
		// When the latest resource snapshot without the isLastest label, it means it fails to create the new
		// resource snapshot in the last reconcile and we don't need to check and delay the request.
		res, error := r.shouldCreateNewResourceSnapshotNow(ctx, latestResourceSnapshot)
		if error != nil {
			return ctrl.Result{}, nil, error
		}
		if res.Requeue {
			// If the latest resource snapshot is not ready to be updated, we requeue the request.
			return res, latestResourceSnapshot, nil
		}
		shouldCreateNewMasterResourceSnapshot = true
		// set the latest label to false first to make sure there is only one or none active resource snapshot
		labels := latestResourceSnapshot.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}
		labels[fleetv1beta1.IsLatestSnapshotLabel] = strconv.FormatBool(false)
		latestResourceSnapshot.SetLabels(labels)
		if err := r.Client.Update(ctx, latestResourceSnapshot); err != nil {
			klog.ErrorS(err, "Failed to set the isLatestSnapshot label to false", "clusterResourceSnapshot", klog.KObj(latestResourceSnapshot))
			return ctrl.Result{}, nil, controller.NewUpdateIgnoreConflictError(err)
		}
		klog.V(2).InfoS("Marked the existing clusterResourceSnapshot as inactive", "placement", placementKObj, "clusterResourceSnapshot", klog.KObj(latestResourceSnapshot))
	}

	// only delete redundant resource snapshots and increment the latest resource snapshot index if new master resource snapshot is to be created.
	if shouldCreateNewMasterResourceSnapshot {
		// delete redundant snapshot revisions before creating a new master resource snapshot to guarantee that the number of snapshots won't exceed the limit.
		if err := r.deleteRedundantResourceSnapshots(ctx, placement, revisionHistoryLimit); err != nil {
			return ctrl.Result{}, nil, err
		}
		latestResourceSnapshotIndex++
	}
	// split selected resources as list of lists.
	selectedResourcesList := controller.SplitSelectedResources(resourceSnapshotSpec.SelectedResources, resourceSnapshotResourceSizeLimit)
	var resourceSnapshot fleetv1beta1.ResourceSnapshotObj
	for i := resourceSnapshotStartIndex; i < len(selectedResourcesList); i++ {
		if i == 0 {
			resourceSnapshot = BuildMasterResourceSnapshot(latestResourceSnapshotIndex, len(selectedResourcesList), envelopeObjCount, placement.GetName(), placement.GetNamespace(), resourceHash, selectedResourcesList[i])
			latestResourceSnapshot = resourceSnapshot
		} else {
			resourceSnapshot = BuildSubIndexResourceSnapshot(latestResourceSnapshotIndex, i-1, placement.GetName(), placement.GetNamespace(), selectedResourcesList[i])
		}
		if err = r.createResourceSnapshot(ctx, placement, resourceSnapshot); err != nil {
			return ctrl.Result{}, nil, err
		}
	}
	// shouldCreateNewMasterResourceSnapshot is used here to be defensive in case of the regression.
	if shouldCreateNewMasterResourceSnapshot && len(selectedResourcesList) == 0 {
		resourceSnapshot = BuildMasterResourceSnapshot(latestResourceSnapshotIndex, 1, envelopeObjCount, placement.GetName(), placement.GetNamespace(), resourceHash, []fleetv1beta1.ResourceContent{})
		latestResourceSnapshot = resourceSnapshot
		if err = r.createResourceSnapshot(ctx, placement, resourceSnapshot); err != nil {
			return ctrl.Result{}, nil, err
		}
	}
	return ctrl.Result{}, latestResourceSnapshot, nil
}

// shouldCreateNewResourceSnapshotNow checks whether it is ready to create the new resource snapshot to avoid too frequent creation
// based on the configured resourceSnapshotCreationMinimumInterval and resourceChangesCollectionDuration.
func (r *Reconciler) shouldCreateNewResourceSnapshotNow(ctx context.Context, latestResourceSnapshot fleetv1beta1.ResourceSnapshotObj) (ctrl.Result, error) {
	if r.ResourceSnapshotCreationMinimumInterval <= 0 && r.ResourceChangesCollectionDuration <= 0 {
		return ctrl.Result{}, nil
	}

	// We respect the ResourceChangesCollectionDuration to allow the controller to bundle all the resource changes into one snapshot.
	snapshotKObj := klog.KObj(latestResourceSnapshot)
	now := time.Now()
	nextResourceSnapshotCandidateDetectionTime, err := annotations.ExtractNextResourceSnapshotCandidateDetectionTimeFromResourceSnapshot(latestResourceSnapshot)
	if nextResourceSnapshotCandidateDetectionTime.IsZero() || err != nil {
		if err != nil {
			klog.ErrorS(controller.NewUnexpectedBehaviorError(err), "Failed to get the NextResourceSnapshotCandidateDetectionTimeAnnotation", "clusterResourceSnapshot", snapshotKObj)
		}
		// If the annotation is not set, set next resource snapshot candidate detection time is now.
		if latestResourceSnapshot.GetAnnotations() == nil {
			latestResourceSnapshot.SetAnnotations(make(map[string]string))
		}
		latestResourceSnapshot.GetAnnotations()[fleetv1beta1.NextResourceSnapshotCandidateDetectionTimeAnnotation] = now.Format(time.RFC3339)
		if err := r.Client.Update(ctx, latestResourceSnapshot); err != nil {
			klog.ErrorS(err, "Failed to update the NextResourceSnapshotCandidateDetectionTime annotation", "clusterResourceSnapshot", snapshotKObj)
			return ctrl.Result{}, controller.NewUpdateIgnoreConflictError(err)
		}
		nextResourceSnapshotCandidateDetectionTime = now
		klog.V(2).InfoS("Updated the NextResourceSnapshotCandidateDetectionTime annotation", "clusterResourceSnapshot", snapshotKObj, "nextResourceSnapshotCandidateDetectionTimeAnnotation", now.Format(time.RFC3339))
	}
	nextCreationTime := fleettime.MaxTime(nextResourceSnapshotCandidateDetectionTime.Add(r.ResourceChangesCollectionDuration), latestResourceSnapshot.GetCreationTimestamp().Add(r.ResourceSnapshotCreationMinimumInterval))
	if now.Before(nextCreationTime) {
		// If the next resource snapshot creation time is not reached, we requeue the request to avoid too frequent update.
		klog.V(2).InfoS("Delaying the new resourceSnapshot creation",
			"clusterResourceSnapshot", snapshotKObj, "nextCreationTime", nextCreationTime, "latestResourceSnapshotCreationTime", latestResourceSnapshot.GetCreationTimestamp(),
			"resourceSnapshotCreationMinimumInterval", r.ResourceSnapshotCreationMinimumInterval, "resourceChangesCollectionDuration", r.ResourceChangesCollectionDuration,
			"afterDuration", nextCreationTime.Sub(now))
		return ctrl.Result{Requeue: true, RequeueAfter: nextCreationTime.Sub(now)}, nil
	}
	return ctrl.Result{}, nil
}

// buildMasterResourceSnapshot builds and returns the master resource snapshot for the latest resource snapshot index and selected resources.
func BuildMasterResourceSnapshot(latestResourceSnapshotIndex, resourceSnapshotCount, envelopeObjCount int, placementName, placementNamespace, resourceHash string, selectedResources []fleetv1beta1.ResourceContent) fleetv1beta1.ResourceSnapshotObj {
	labels := map[string]string{
		fleetv1beta1.PlacementTrackingLabel: placementName,
		fleetv1beta1.IsLatestSnapshotLabel:  strconv.FormatBool(true),
		fleetv1beta1.ResourceIndexLabel:     strconv.Itoa(latestResourceSnapshotIndex),
	}
	annotations := map[string]string{
		fleetv1beta1.ResourceGroupHashAnnotation:         resourceHash,
		fleetv1beta1.NumberOfResourceSnapshotsAnnotation: strconv.Itoa(resourceSnapshotCount),
		fleetv1beta1.NumberOfEnvelopedObjectsAnnotation:  strconv.Itoa(envelopeObjCount),
	}
	spec := fleetv1beta1.ResourceSnapshotSpec{
		SelectedResources: selectedResources,
	}
	if placementNamespace == "" {
		// Cluster-scoped placement
		return &fleetv1beta1.ClusterResourceSnapshot{
			ObjectMeta: metav1.ObjectMeta{
				Name:        fmt.Sprintf(fleetv1beta1.ResourceSnapshotNameFmt, placementName, latestResourceSnapshotIndex),
				Labels:      labels,
				Annotations: annotations,
			},
			Spec: spec,
		}
	} else {
		// Namespace-scoped placement
		return &fleetv1beta1.ResourceSnapshot{
			ObjectMeta: metav1.ObjectMeta{
				Name:        fmt.Sprintf(fleetv1beta1.ResourceSnapshotNameFmt, placementName, latestResourceSnapshotIndex),
				Namespace:   placementNamespace,
				Labels:      labels,
				Annotations: annotations,
			},
			Spec: spec,
		}
	}
}

// BuildSubIndexResourceSnapshot builds and returns the sub index resource snapshot for both cluster-scoped and namespace-scoped placements.
// Returns a ClusterResourceSnapshot for cluster-scoped placements (empty namespace) or ResourceSnapshot for namespace-scoped placements.
func BuildSubIndexResourceSnapshot(latestResourceSnapshotIndex, resourceSnapshotSubIndex int, placementName, placementNamespace string, selectedResources []fleetv1beta1.ResourceContent) fleetv1beta1.ResourceSnapshotObj {
	labels := map[string]string{
		fleetv1beta1.PlacementTrackingLabel: placementName,
		fleetv1beta1.ResourceIndexLabel:     strconv.Itoa(latestResourceSnapshotIndex),
	}
	annotations := map[string]string{
		fleetv1beta1.SubindexOfResourceSnapshotAnnotation: strconv.Itoa(resourceSnapshotSubIndex),
	}
	spec := fleetv1beta1.ResourceSnapshotSpec{
		SelectedResources: selectedResources,
	}
	if placementNamespace == "" {
		// Cluster-scoped placement
		return &fleetv1beta1.ClusterResourceSnapshot{
			ObjectMeta: metav1.ObjectMeta{
				Name:        fmt.Sprintf(fleetv1beta1.ResourceSnapshotNameWithSubindexFmt, placementName, latestResourceSnapshotIndex, resourceSnapshotSubIndex),
				Labels:      labels,
				Annotations: annotations,
			},
			Spec: spec,
		}
	} else {
		// Namespace-scoped placement
		return &fleetv1beta1.ResourceSnapshot{
			ObjectMeta: metav1.ObjectMeta{
				Name:        fmt.Sprintf(fleetv1beta1.ResourceSnapshotNameWithSubindexFmt, placementName, latestResourceSnapshotIndex, resourceSnapshotSubIndex),
				Namespace:   placementNamespace,
				Labels:      labels,
				Annotations: annotations,
			},
			Spec: spec,
		}
	}
}

// createResourceSnapshot sets placement owner reference on the resource snapshot and creates it.
// Now supports both cluster-scoped and namespace-scoped placements using interface types.
func (r *Reconciler) createResourceSnapshot(ctx context.Context, placementObj fleetv1beta1.PlacementObj, resourceSnapshot fleetv1beta1.ResourceSnapshotObj) error {
	resourceSnapshotKObj := klog.KObj(resourceSnapshot)
	if err := controllerutil.SetControllerReference(placementObj, resourceSnapshot, r.Scheme); err != nil {
		klog.ErrorS(err, "Failed to set owner reference", "resourceSnapshot", resourceSnapshotKObj)
		// should never happen
		return controller.NewUnexpectedBehaviorError(err)
	}
	if err := r.Client.Create(ctx, resourceSnapshot); err != nil {
		klog.ErrorS(err, "Failed to create new resourceSnapshot", "resourceSnapshot", resourceSnapshotKObj)
		return controller.NewAPIServerError(false, err)
	}
	klog.V(2).InfoS("Created new resourceSnapshot", "placement", klog.KObj(placementObj), "resourceSnapshot", resourceSnapshotKObj)
	return nil
}

// ensureLatestPolicySnapshot ensures the latest policySnapshot has the isLatest label and the numberOfClusters are updated for interface types.
func (r *Reconciler) ensureLatestPolicySnapshot(ctx context.Context, placementObj fleetv1beta1.PlacementObj, latest fleetv1beta1.PolicySnapshotObj) error {
	needUpdate := false
	latestKObj := klog.KObj(latest)
	labels := latest.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	if labels[fleetv1beta1.IsLatestSnapshotLabel] != strconv.FormatBool(true) {
		// When latestPolicySnapshot.Spec.PolicyHash == policyHash,
		// It could happen when the controller just sets the latest label to false for the old snapshot, and fails to
		// create a new policy snapshot.
		// And then the customers revert back their policy to the old one again.
		// In this case, the "latest" snapshot without isLatest label has the same policy hash as the current policy.

		labels[fleetv1beta1.IsLatestSnapshotLabel] = strconv.FormatBool(true)
		latest.SetLabels(labels)
		needUpdate = true
	}
	crpGeneration, err := annotations.ExtractObservedCRPGenerationFromPolicySnapshot(latest)
	if err != nil {
		klog.ErrorS(err, "Failed to parse the CRPGeneration from the annotations", "policySnapshot", latestKObj)
		return controller.NewUnexpectedBehaviorError(err)
	}
	if crpGeneration != placementObj.GetGeneration() {
		annotations := latest.GetAnnotations()
		if annotations == nil {
			annotations = make(map[string]string)
		}
		annotations[fleetv1beta1.CRPGenerationAnnotation] = strconv.FormatInt(placementObj.GetGeneration(), 10)
		latest.SetAnnotations(annotations)
		needUpdate = true
	}

	// Handle NumberOfClusters annotation for selectN type placements
	placementSpec := placementObj.GetPlacementSpec()
	if placementSpec.Policy != nil &&
		placementSpec.Policy.PlacementType == fleetv1beta1.PickNPlacementType &&
		placementSpec.Policy.NumberOfClusters != nil {
		oldCount, err := annotations.ExtractNumOfClustersFromPolicySnapshot(latest)
		if err != nil {
			klog.ErrorS(err, "Failed to parse the numberOfClusterAnnotation", "policySnapshot", latestKObj)
			return controller.NewUnexpectedBehaviorError(err)
		}
		newCount := int(*placementSpec.Policy.NumberOfClusters)
		if oldCount != newCount {
			annotations := latest.GetAnnotations()
			annotations[fleetv1beta1.NumberOfClustersAnnotation] = strconv.Itoa(newCount)
			latest.SetAnnotations(annotations)
			needUpdate = true
		}
	}
	if !needUpdate {
		return nil
	}
	if err := r.Client.Update(ctx, latest); err != nil {
		klog.ErrorS(err, "Failed to update the policySnapshot", "policySnapshot", latestKObj)
		return controller.NewUpdateIgnoreConflictError(err)
	}
	return nil
}

// ensureLatestResourceSnapshot ensures the latest resourceSnapshot has the isLatest label, working with interface types.
func (r *Reconciler) ensureLatestResourceSnapshot(ctx context.Context, latest fleetv1beta1.ResourceSnapshotObj) error {
	labels := latest.GetLabels()
	if labels != nil && labels[fleetv1beta1.IsLatestSnapshotLabel] == strconv.FormatBool(true) {
		return nil
	}
	// It could happen when the controller just sets the latest label to false for the old snapshot, and fails to
	// create a new resource snapshot.
	// And then the customers revert back their resource to the old one again.
	// In this case, the "latest" snapshot without isLatest label has the same resource hash as the current one.
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[fleetv1beta1.IsLatestSnapshotLabel] = strconv.FormatBool(true)
	latest.SetLabels(labels)
	if err := r.Client.Update(ctx, latest); err != nil {
		klog.ErrorS(err, "Failed to update the resourceSnapshot", "resourceSnapshot", klog.KObj(latest))
		return controller.NewUpdateIgnoreConflictError(err)
	}
	klog.V(2).InfoS("ResourceSnapshot's IsLatestSnapshotLabel was updated to true", "resourceSnapshot", klog.KObj(latest))
	return nil
}

// listSortedClusterSchedulingPolicySnapshots returns the policy snapshots sorted by the policy index.
// Now works with both cluster-scoped and namespaced policy snapshots using interface types.
func (r *Reconciler) listSortedClusterSchedulingPolicySnapshots(ctx context.Context, placementObj fleetv1beta1.PlacementObj) (fleetv1beta1.PolicySnapshotList, error) {
	placementKey := types.NamespacedName{
		Namespace: placementObj.GetNamespace(),
		Name:      placementObj.GetName(),
	}

	snapshotList, err := controller.ListPolicySnapshots(ctx, r.Client, placementKey)
	if err != nil {
		klog.ErrorS(err, "Failed to list all policySnapshots", "placement", klog.KObj(placementObj))
		// CRP controller needs a scheduling policy snapshot watcher to enqueue the CRP request.
		// So the snapshots should be read from cache.
		return nil, controller.NewAPIServerError(true, err)
	}

	items := snapshotList.GetPolicySnapshotObjs()
	var errs []error
	sort.Slice(items, func(i, j int) bool {
		ii, err := labels.ParsePolicyIndexFromLabel(items[i])
		if err != nil {
			klog.ErrorS(err, "Failed to parse the policy index label", "placement", klog.KObj(placementObj), "policySnapshot", klog.KObj(items[i]))
			errs = append(errs, err)
		}
		ji, err := labels.ParsePolicyIndexFromLabel(items[j])
		if err != nil {
			klog.ErrorS(err, "Failed to parse the policy index label", "placement", klog.KObj(placementObj), "policySnapshot", klog.KObj(items[j]))
			errs = append(errs, err)
		}
		return ii < ji
	})

	if len(errs) > 0 {
		return nil, controller.NewUnexpectedBehaviorError(utilerrors.NewAggregate(errs))
	}

	return snapshotList, nil
}

// lookupLatestResourceSnapshot finds the latest snapshots and.
// There will be only one active resource snapshot if exists.
// It first checks whether there is an active resource snapshot.
// If not, it finds the one whose resourceIndex label is the largest.
// The resource index will always start from 0.
// lookupLatestResourceSnapshot finds the latest resource snapshots for the given placement.
// It works with both cluster-scoped (ClusterResourcePlacement) and namespace-scoped (ResourcePlacement) placements.
// There will be only one active resource snapshot if exists.
// It first checks whether there is an active resource snapshot.
// If not, it finds the one whose resourceIndex label is the largest.
// The resource index will always start from 0.
// Return error when 1) cannot list the snapshots 2) there are more than one active resource snapshots 3) snapshot has the
// invalid label value.
// 2 & 3 should never happen.
func (r *Reconciler) lookupLatestResourceSnapshot(ctx context.Context, placement fleetv1beta1.PlacementObj) (fleetv1beta1.ResourceSnapshotObj, int, error) {
	placementKObj := klog.KObj(placement)

	// Use the existing FetchLatestMasterResourceSnapshot function to get the master snapshot
	masterSnapshot, err := controller.FetchLatestMasterResourceSnapshot(ctx, r.Client, types.NamespacedName{Namespace: placement.GetNamespace(), Name: placement.GetName()})
	if err != nil {
		return nil, -1, err
	}
	if masterSnapshot != nil {
		// Extract resource index from the master snapshot
		resourceIndex, err := labels.ExtractResourceIndexFromResourceSnapshot(masterSnapshot)
		if err != nil {
			klog.ErrorS(err, "Failed to parse the resource index label", "resourceSnapshot", klog.KObj(masterSnapshot))
			return nil, -1, controller.NewUnexpectedBehaviorError(err)
		}
		return masterSnapshot, resourceIndex, nil
	}
	// When there are no active snapshots, find the first snapshot who has the largest resource index.
	// It should be rare only when CRP is crashed before creating the new active snapshot.
	sortedList, err := r.listSortedResourceSnapshots(ctx, placement)
	if err != nil {
		return nil, -1, err
	}
	if len(sortedList.GetResourceSnapshotObjs()) == 0 {
		// The resource index of the first snapshot will start from 0.
		return nil, -1, nil
	}
	latestSnapshot := sortedList.GetResourceSnapshotObjs()[len(sortedList.GetResourceSnapshotObjs())-1]
	resourceIndex, err := labels.ExtractResourceIndexFromResourceSnapshot(latestSnapshot)
	if err != nil {
		klog.ErrorS(err, "Failed to parse the resource index label", "placement", placementKObj, "resourceSnapshot", klog.KObj(latestSnapshot))
		return nil, -1, controller.NewUnexpectedBehaviorError(err)
	}
	return latestSnapshot, resourceIndex, nil
}

// listSortedResourceSnapshots returns the resource snapshots sorted by its index and its subindex.
// Now works with both cluster-scoped and namespaced resource snapshots using interface types.
// The resourceSnapshot is less than the other one when resourceIndex is less.
// When the resourceIndex is equal, then order by the subindex.
// Note: the snapshot does not have subindex is the largest of a group and there should be only one in a group.
func (r *Reconciler) listSortedResourceSnapshots(ctx context.Context, placementObj fleetv1beta1.PlacementObj) (fleetv1beta1.ResourceSnapshotObjList, error) {
	placementKey := types.NamespacedName{
		Namespace: placementObj.GetNamespace(),
		Name:      placementObj.GetName(),
	}

	snapshotList, err := controller.ListAllResourceSnapshots(ctx, r.Client, placementKey)
	if err != nil {
		klog.ErrorS(err, "Failed to list all resourceSnapshots", "placement", klog.KObj(placementObj))
		return nil, controller.NewAPIServerError(true, err)
	}

	items := snapshotList.GetResourceSnapshotObjs()
	var errs []error
	sort.Slice(items, func(i, j int) bool {
		iKObj := klog.KObj(items[i])
		jKObj := klog.KObj(items[j])
		ii, err := labels.ExtractResourceIndexFromResourceSnapshot(items[i])
		if err != nil {
			klog.ErrorS(err, "Failed to parse the resource index label", "placement", klog.KObj(placementObj), "resourceSnapshot", iKObj)
			errs = append(errs, err)
		}
		ji, err := labels.ExtractResourceIndexFromResourceSnapshot(items[j])
		if err != nil {
			klog.ErrorS(err, "Failed to parse the resource index label", "placement", klog.KObj(placementObj), "resourceSnapshot", jKObj)
			errs = append(errs, err)
		}
		if ii != ji {
			return ii < ji
		}

		iDoesExist, iSubindex, err := annotations.ExtractSubindexFromResourceSnapshot(items[i])
		if err != nil {
			klog.ErrorS(err, "Failed to parse the subindex index", "placement", klog.KObj(placementObj), "resourceSnapshot", iKObj)
			errs = append(errs, err)
		}
		jDoesExist, jSubindex, err := annotations.ExtractSubindexFromResourceSnapshot(items[j])
		if err != nil {
			klog.ErrorS(err, "Failed to parse the subindex index", "placement", klog.KObj(placementObj), "resourceSnapshot", jKObj)
			errs = append(errs, err)
		}

		// Both of the snapshots do not have subindex, which should not happen.
		if !iDoesExist && !jDoesExist {
			klog.ErrorS(err, "There are more than one resource snapshot which do not have subindex in a group", "placement", klog.KObj(placementObj), "resourceSnapshot", iKObj, "resourceSnapshot", jKObj)
			errs = append(errs, err)
		}

		if !iDoesExist { // check if it's the first snapshot
			return false
		}
		if !jDoesExist { // check if it's the first snapshot
			return true
		}
		return iSubindex < jSubindex
	})

	if len(errs) > 0 {
		return nil, controller.NewUnexpectedBehaviorError(utilerrors.NewAggregate(errs))
	}

	return snapshotList, nil
}

// setPlacementStatus returns if there is a cluster scheduled by the scheduler.
func (r *Reconciler) setPlacementStatus(
	ctx context.Context,
	placementObj fleetv1beta1.PlacementObj,
	selectedResourceIDs []fleetv1beta1.ResourceIdentifier,
	latestSchedulingPolicySnapshot fleetv1beta1.PolicySnapshotObj,
	latestResourceSnapshot fleetv1beta1.ResourceSnapshotObj,
) (bool, error) {
	placementStatus := placementObj.GetPlacementStatus()
	placementStatus.SelectedResources = selectedResourceIDs

	scheduledCondition := buildScheduledCondition(placementObj, latestSchedulingPolicySnapshot)
	placementObj.SetConditions(scheduledCondition)
	// set ObservedResourceIndex from the latest resource snapshot's resource index label, before we set Synchronized, Applied conditions.
	placementStatus.ObservedResourceIndex = latestResourceSnapshot.GetLabels()[fleetv1beta1.ResourceIndexLabel]

	// When scheduledCondition is unknown, appliedCondition should be unknown too.
	// Note: If the scheduledCondition is failed, it means the placement requirement cannot be satisfied fully. For example,
	// pickN deployment requires 5 clusters and scheduler schedules the resources on 3 clusters. And the appliedCondition
	// could be true when resources are applied successfully on these 3 clusters and the detailed the resourcePlacementStatuses
	// need to be populated.
	if scheduledCondition.Status == metav1.ConditionUnknown {
		// For the new conditions, we no longer populate the remaining otherwise it's too complicated and the default condition
		// will be unknown.
		// skip populating detailed resourcePlacementStatus & work related conditions
		// reset other status fields
		// TODO: need to track whether we have deleted the resources for the last decisions.
		// The undeleted resources on these old clusters could lead to failed synchronized or applied condition.
		// Today, we only track the resources progress if the same cluster is selected again.

		// For now, only CRP supports placement statuses - namespace-scoped placements would need different handling
		if crp, ok := placementObj.(*fleetv1beta1.ClusterResourcePlacement); ok {
			crp.Status.PlacementStatuses = []fleetv1beta1.ResourcePlacementStatus{}
		}
		return false, nil
	}

	// skip the resource placement status and condition update if the scheduler has not made any decision.
	if latestSchedulingPolicySnapshot.GetPolicySnapshotStatus() == nil || len(latestSchedulingPolicySnapshot.GetPolicySnapshotStatus().ClusterDecisions) == 0 {
		klog.V(2).InfoS("Skipping the resource placement status update since scheduler has not made any decision", "placement", klog.KObj(placementObj))
		return false, nil
	}

	// Classify cluster decisions; find out clusters that have been selected and
	// have not been selected.
	selected, unselected := classifyClusterDecisions(latestSchedulingPolicySnapshot.GetPolicySnapshotStatus().ClusterDecisions)
	// Calculate the number of clusters that should have been selected yet cannot be, due to
	// scheduling constraints.
	failedToScheduleClusterCount := calculateFailedToScheduleClusterCount(placementObj, selected, unselected)

	// Prepare the resource placement status (status per cluster) in the CRP status.
	allRPS := make([]fleetv1beta1.ResourcePlacementStatus, 0, len(latestSchedulingPolicySnapshot.GetPolicySnapshotStatus().ClusterDecisions))

	// For clusters that have been selected, set the resource placement status based on the
	// respective resource binding status for each of them.
	expectedCondTypes := determineExpectedCRPAndResourcePlacementStatusCondType(placementObj)
	allRPS, rpsSetCondTypeCounter, err := r.appendScheduledResourcePlacementStatuses(
		ctx, allRPS, selected, expectedCondTypes, placementObj, latestSchedulingPolicySnapshot, latestResourceSnapshot)
	if err != nil {
		return false, err
	}

	// For clusters that failed to get scheduled, set a resource placement status with the
	// failed to schedule condition for each of them.
	// This is currently CRP-specific functionality
	if crp, ok := placementObj.(*fleetv1beta1.ClusterResourcePlacement); ok {
		allRPS = appendFailedToScheduleResourcePlacementStatuses(allRPS, unselected, failedToScheduleClusterCount, crp)

		crp.Status.PlacementStatuses = allRPS

		// Prepare the conditions for the CRP object itself.

		if len(selected) == 0 {
			// There is no selected cluster at all. It could be that there is no matching cluster
			// given the current scheduling policy; there remains a corner case as well where a cluster
			// has been selected before (with resources being possibly applied), but has now
			// left the fleet. To address this corner case, Fleet here will remove all lingering
			// conditions (any condition type other than CRPScheduled).

			// Note that the scheduled condition has been set earlier in this method.
			crp.Status.Conditions = []metav1.Condition{*crp.GetCondition(string(fleetv1beta1.ClusterResourcePlacementScheduledConditionType))}
			return false, nil
		}

		if crp.Spec.Strategy.Type == fleetv1beta1.ExternalRolloutStrategyType {
			// For external rollout strategy, if clusters observe different resource snapshot versions,
			// we set RolloutStarted to Unknown without any other conditions since we do not know exactly which version is rolling out.
			// We also need to reset ObservedResourceIndex and selectedResources.
			rolloutStartedUnknown, err := r.determineRolloutStateForCRPWithExternalRolloutStrategy(ctx, crp, selected, allRPS, selectedResourceIDs)
			if err != nil || rolloutStartedUnknown {
				return true, err
			}
		}

		setCRPConditions(crp, allRPS, rpsSetCondTypeCounter, expectedCondTypes)
	}

	return true, nil
}

func (r *Reconciler) determineRolloutStateForCRPWithExternalRolloutStrategy(
	ctx context.Context,
	crp *fleetv1beta1.ClusterResourcePlacement,
	selected []*fleetv1beta1.ClusterDecision,
	allRPS []fleetv1beta1.ResourcePlacementStatus,
	selectedResourceIDs []fleetv1beta1.ResourceIdentifier,
) (bool, error) {
	if len(selected) == 0 {
		// This should not happen as we already checked in setPlacementStatus.
		err := controller.NewUnexpectedBehaviorError(fmt.Errorf("selected cluster list is empty for placement %s when checking per-cluster rollout state", crp.Name))
		klog.ErrorS(err, "Should not happen: selected cluster list is empty in determineRolloutStateForCRPWithExternalRolloutStrategy()")
		return false, err
	}

	differentResourceIndicesObserved := false
	observedResourceIndex := allRPS[0].ObservedResourceIndex
	for i := range len(selected) - 1 {
		if allRPS[i].ObservedResourceIndex != allRPS[i+1].ObservedResourceIndex {
			differentResourceIndicesObserved = true
			break
		}
	}

	if differentResourceIndicesObserved {
		// If clusters observe different resource snapshot versions, we set RolloutStarted condition to Unknown.
		// ObservedResourceIndex and selectedResources are reset, too.
		klog.V(2).InfoS("Placement has External rollout strategy and different resource snapshot versions are observed across clusters, set RolloutStarted condition to Unknown", "clusterResourcePlacement", klog.KObj(crp))
		crp.Status.ObservedResourceIndex = ""
		crp.Status.SelectedResources = []fleetv1beta1.ResourceIdentifier{}
		crp.SetConditions(metav1.Condition{
			Type:               string(fleetv1beta1.ClusterResourcePlacementRolloutStartedConditionType),
			Status:             metav1.ConditionUnknown,
			Reason:             condition.RolloutControlledByExternalControllerReason,
			Message:            "Rollout is controlled by an external controller and different resource snapshot versions are observed across clusters",
			ObservedGeneration: crp.Generation,
		})
		// As CRP status will refresh even if the spec has not changed, we reset any unused conditions
		// to avoid confusion.
		for i := condition.RolloutStartedCondition + 1; i < condition.TotalCondition; i++ {
			meta.RemoveStatusCondition(&crp.Status.Conditions, string(i.ClusterResourcePlacementConditionType()))
		}
		return true, nil
	}

	if observedResourceIndex == "" {
		// All bindings have empty resource snapshot name, we set the rollout condition to Unknown.
		// ObservedResourceIndex and selectedResources are reset, too.
		klog.V(2).InfoS("Placement has External rollout strategy and no resource snapshot name is observed across clusters, set RolloutStarted condition to Unknown", "clusterResourcePlacement", klog.KObj(crp))
		crp.Status.ObservedResourceIndex = ""
		crp.Status.SelectedResources = []fleetv1beta1.ResourceIdentifier{}
		crp.SetConditions(metav1.Condition{
			Type:               string(fleetv1beta1.ClusterResourcePlacementRolloutStartedConditionType),
			Status:             metav1.ConditionUnknown,
			Reason:             condition.RolloutControlledByExternalControllerReason,
			Message:            "Rollout is controlled by an external controller and no resource snapshot name is observed across clusters, probably rollout has not started yet",
			ObservedGeneration: crp.Generation,
		})
		// As CRP status will refresh even if the spec has not changed, we reset any unused conditions
		// to avoid confusion.
		for i := condition.RolloutStartedCondition + 1; i < condition.TotalCondition; i++ {
			meta.RemoveStatusCondition(&crp.Status.Conditions, string(i.ClusterResourcePlacementConditionType()))
		}
		return true, nil
	}

	// All bindings have the same observed resource snapshot.
	// We only set the ObservedResourceIndex and selectedResources, as the conditions will be set with setCRPConditions.
	// If all clusters observe the latest resource snapshot, we do not need to go through all the resource snapshots again to collect selected resources.
	if observedResourceIndex == crp.Status.ObservedResourceIndex {
		crp.Status.SelectedResources = selectedResourceIDs
	} else {
		crp.Status.ObservedResourceIndex = observedResourceIndex
		selectedResources, err := controller.CollectResourceIdentifiersFromResourceSnapshot(ctx, r.Client, crp.Name, observedResourceIndex)
		if err != nil {
			klog.ErrorS(err, "Failed to collect resource identifiers from clusterResourceSnapshot", "clusterResourcePlacement", klog.KObj(crp), "resourceSnapshotIndex", observedResourceIndex)
			return false, err
		}
		crp.Status.SelectedResources = selectedResources
	}

	for i := range len(selected) {
		rolloutStartedCond := meta.FindStatusCondition(allRPS[i].Conditions, string(fleetv1beta1.ResourceRolloutStartedConditionType))
		if !condition.IsConditionStatusTrue(rolloutStartedCond, crp.Generation) &&
			!condition.IsConditionStatusFalse(rolloutStartedCond, crp.Generation) {
			klog.V(2).InfoS("Placement has External rollout strategy and some cluster is in RolloutStarted Unknown state, set RolloutStarted condition to Unknown",
				"clusterName", allRPS[i].ClusterName, "observedResourceIndex", observedResourceIndex, "clusterResourcePlacement", klog.KObj(crp))
			crp.SetConditions(metav1.Condition{
				Type:               string(fleetv1beta1.ClusterResourcePlacementRolloutStartedConditionType),
				Status:             metav1.ConditionUnknown,
				Reason:             condition.RolloutControlledByExternalControllerReason,
				Message:            fmt.Sprintf("Rollout is controlled by an external controller and cluster %s is in RolloutStarted Unknown state", allRPS[i].ClusterName),
				ObservedGeneration: crp.Generation,
			})
			// As CRP status will refresh even if the spec has not changed, we reset any unused conditions
			// to avoid confusion.
			for i := condition.RolloutStartedCondition + 1; i < condition.TotalCondition; i++ {
				meta.RemoveStatusCondition(&crp.Status.Conditions, string(i.ClusterResourcePlacementConditionType()))
			}
			return true, nil
		}
	}
	return false, nil
}

func buildScheduledCondition(placementObj fleetv1beta1.PlacementObj, latestSchedulingPolicySnapshot fleetv1beta1.PolicySnapshotObj) metav1.Condition {
	scheduledCondition := latestSchedulingPolicySnapshot.GetCondition(string(fleetv1beta1.PolicySnapshotScheduled))

	if scheduledCondition == nil ||
		// defensive check and not needed for now as the policySnapshot should be immutable.
		scheduledCondition.ObservedGeneration < latestSchedulingPolicySnapshot.GetGeneration() ||
		// We have numberOfCluster annotation added on the CRP and it won't change the CRP generation.
		// So that we need to compare the CRP observedCRPGeneration reported by the scheduler.
		latestSchedulingPolicySnapshot.GetPolicySnapshotStatus().ObservedCRPGeneration < placementObj.GetGeneration() ||
		scheduledCondition.Status == metav1.ConditionUnknown {
		return metav1.Condition{
			Status:             metav1.ConditionUnknown,
			Type:               string(fleetv1beta1.ClusterResourcePlacementScheduledConditionType),
			Reason:             condition.SchedulingUnknownReason,
			Message:            "Scheduling has not completed",
			ObservedGeneration: placementObj.GetGeneration(),
		}
	}
	return metav1.Condition{
		Status:             scheduledCondition.Status,
		Type:               string(fleetv1beta1.ClusterResourcePlacementScheduledConditionType),
		Reason:             scheduledCondition.Reason,
		Message:            scheduledCondition.Message,
		ObservedGeneration: placementObj.GetGeneration(),
	}
}

func classifyClusterDecisions(decisions []fleetv1beta1.ClusterDecision) (selected []*fleetv1beta1.ClusterDecision, unselected []*fleetv1beta1.ClusterDecision) {
	selected = make([]*fleetv1beta1.ClusterDecision, 0, len(decisions))
	unselected = make([]*fleetv1beta1.ClusterDecision, 0, len(decisions))

	for i := range decisions {
		if decisions[i].Selected {
			selected = append(selected, &decisions[i])
		} else {
			unselected = append(unselected, &decisions[i])
		}
	}
	return selected, unselected
}

func buildResourcePlacementStatusMap(placementObj fleetv1beta1.PlacementObj) map[string][]metav1.Condition {
	placementStatus := placementObj.GetPlacementStatus()
	status := placementStatus.PlacementStatuses
	m := make(map[string][]metav1.Condition, len(status))
	for i := range status {
		if len(status[i].ClusterName) == 0 || len(status[i].Conditions) == 0 {
			continue
		}
		m[status[i].ClusterName] = status[i].Conditions
	}
	return m
}

func isRolloutCompleted(placementObj fleetv1beta1.PlacementObj) bool {
	if !isCRPScheduled(placementObj) {
		return false
	}

	expectedCondTypes := determineExpectedCRPAndResourcePlacementStatusCondType(placementObj)
	for _, i := range expectedCondTypes {
		if !condition.IsConditionStatusTrue(placementObj.GetCondition(string(i.ClusterResourcePlacementConditionType())), placementObj.GetGeneration()) {
			return false
		}
	}
	return true
}

func isCRPScheduled(placementObj fleetv1beta1.PlacementObj) bool {
	return condition.IsConditionStatusTrue(placementObj.GetCondition(string(fleetv1beta1.ClusterResourcePlacementScheduledConditionType)), placementObj.GetGeneration())
}

func emitPlacementStatusMetric(placementObj fleetv1beta1.PlacementObj) {
	// Check Placement Scheduled condition.
	status := "nil"
	reason := "nil"
	cond := placementObj.GetCondition(string(fleetv1beta1.ClusterResourcePlacementScheduledConditionType))
	if !condition.IsConditionStatusTrue(cond, placementObj.GetGeneration()) {
		if cond != nil && cond.ObservedGeneration == placementObj.GetGeneration() {
			status = string(cond.Status)
			reason = cond.Reason
		}
		metrics.FleetPlacementStatusLastTimeStampSeconds.WithLabelValues(placementObj.GetName(), strconv.FormatInt(placementObj.GetGeneration(), 10), string(fleetv1beta1.ClusterResourcePlacementScheduledConditionType), status, reason).SetToCurrentTime()
		return
	}

	// Check placement expected conditions.
	expectedCondTypes := determineExpectedCRPAndResourcePlacementStatusCondType(placementObj)
	for _, condType := range expectedCondTypes {
		cond = placementObj.GetCondition(string(condType.ClusterResourcePlacementConditionType()))
		if !condition.IsConditionStatusTrue(cond, placementObj.GetGeneration()) {
			if cond != nil && cond.ObservedGeneration == placementObj.GetGeneration() {
				status = string(cond.Status)
				reason = cond.Reason
			}
			metrics.FleetPlacementStatusLastTimeStampSeconds.WithLabelValues(placementObj.GetName(), strconv.FormatInt(placementObj.GetGeneration(), 10), string(condType.ClusterResourcePlacementConditionType()), status, reason).SetToCurrentTime()
			return
		}
	}

	// Emit the "Completed" condition metric to indicate that the placement has completed.
	// This condition is used solely for metric reporting purposes.
	metrics.FleetPlacementStatusLastTimeStampSeconds.WithLabelValues(placementObj.GetName(), strconv.FormatInt(placementObj.GetGeneration(), 10), "Completed", string(metav1.ConditionTrue), "Completed").SetToCurrentTime()
}
