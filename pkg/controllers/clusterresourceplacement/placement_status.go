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

package clusterresourceplacement

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	fleetv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/condition"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/controller"
)

// calculateFailedToScheduleClusterCount calculates the count of failed to schedule clusters based on the scheduling policy.
func calculateFailedToScheduleClusterCount(placementObj fleetv1beta1.PlacementObj, selected, unselected []*fleetv1beta1.ClusterDecision) int {
	failedToScheduleClusterCount := 0
	placementSpec := placementObj.GetPlacementSpec()
	switch {
	case placementSpec.Policy == nil:
		// No scheduling policy is set; Fleet assumes that a PickAll scheduling policy
		// is specified and in this case there is no need to calculate the count of
		// failed to schedule clusters as the scheduler will always set all eligible clusters.
	case placementSpec.Policy.PlacementType == fleetv1beta1.PickNPlacementType && placementSpec.Policy.NumberOfClusters != nil:
		// The PickN scheduling policy is used; in this case the count of failed to schedule
		// clusters is equal to the difference between the specified N number and the actual
		// number of selected clusters.
		failedToScheduleClusterCount = int(*placementSpec.Policy.NumberOfClusters) - len(selected)
	case placementSpec.Policy.PlacementType == fleetv1beta1.PickFixedPlacementType:
		// The PickFixed scheduling policy is used; in this case the count of failed to schedule
		// clusters is equal to the difference between the number of specified cluster names and
		// the actual number of selected clusters.
		failedToScheduleClusterCount = len(placementSpec.Policy.ClusterNames) - len(selected)
	default:
		// The PickAll scheduling policy is used; as explained earlier, in this case there is
		// no need to calculate the count of failed to schedule clusters.
	}

	if failedToScheduleClusterCount > len(unselected) {
		// There exists a corner case where the failed to schedule cluster count exceeds the
		// total number of unselected clusters; this can occur when there does not exist
		// enough clusters in the fleet for the scheduler to pick. In this case, the count is
		// set to the total number of unselected clusters.
		failedToScheduleClusterCount = len(unselected)
	}

	return failedToScheduleClusterCount
}

// appendFailedToScheduleResourcePlacementStatuses appends the resource placement statuses for
// the failed to schedule clusters to the list of all resource placement statuses.
func appendFailedToScheduleResourcePlacementStatuses(
	allRPS []fleetv1beta1.ResourcePlacementStatus,
	unselected []*fleetv1beta1.ClusterDecision,
	failedToScheduleClusterCount int,
	placementObj fleetv1beta1.PlacementObj,
) []fleetv1beta1.ResourcePlacementStatus {
	// In the earlier step it has been guaranteed that failedToScheduleClusterCount is less than or equal to the
	// total number of unselected clusters; here Fleet still performs a sanity check.
	for i := 0; i < failedToScheduleClusterCount && i < len(unselected); i++ {
		rps := &fleetv1beta1.ResourcePlacementStatus{}

		failedToScheduleCond := metav1.Condition{
			Status:             metav1.ConditionFalse,
			Type:               string(fleetv1beta1.ResourceScheduledConditionType),
			Reason:             condition.ResourceScheduleFailedReason,
			Message:            unselected[i].Reason,
			ObservedGeneration: placementObj.GetGeneration(),
		}
		meta.SetStatusCondition(&rps.Conditions, failedToScheduleCond)
		// The allRPS slice has been pre-allocated, so the append call will never produce a new
		// slice; here, however, Fleet will still return the old slice just in case.
		allRPS = append(allRPS, *rps)
		klog.V(2).InfoS("Populated the resource placement status for the unscheduled cluster", "placement", klog.KObj(placementObj), "cluster", unselected[i].ClusterName)
	}

	return allRPS
}

// determineExpectedPlacementAndResourcePlacementStatusCondType determines the expected condition types for the CRP and resource placement statuses
// given the currently in-use apply strategy.
func determineExpectedPlacementAndResourcePlacementStatusCondType(placementObj fleetv1beta1.PlacementObj) []condition.ResourceCondition {
	placementSpec := placementObj.GetPlacementSpec()
	switch {
	case placementSpec.Strategy.ApplyStrategy == nil:
		return condition.CondTypesForClientSideServerSideApplyStrategies
	case placementSpec.Strategy.ApplyStrategy.Type == fleetv1beta1.ApplyStrategyTypeReportDiff:
		return condition.CondTypesForReportDiffApplyStrategy
	default:
		return condition.CondTypesForClientSideServerSideApplyStrategies
	}
}

// appendScheduledResourcePlacementStatuses appends the resource placement statuses for the
// scheduled clusters to the list of all resource placement statuses.
// it returns the updated list of resource placement statuses.
func (r *Reconciler) appendScheduledResourcePlacementStatuses(
	ctx context.Context,
	allRPS []fleetv1beta1.ResourcePlacementStatus,
	selected []*fleetv1beta1.ClusterDecision,
	expectedCondTypes []condition.ResourceCondition,
	placementObj fleetv1beta1.PlacementObj,
	latestSchedulingPolicySnapshot fleetv1beta1.PolicySnapshotObj,
	latestClusterResourceSnapshot fleetv1beta1.ResourceSnapshotObj,
) (
	[]fleetv1beta1.ResourcePlacementStatus,
	[condition.TotalCondition][condition.TotalConditionStatus]int,
	error,
) {
	// Use a counter to track the number of each condition type set and their respective status.
	var rpsSetCondTypeCounter [condition.TotalCondition][condition.TotalConditionStatus]int

	oldRPSMap := buildResourcePlacementStatusMap(placementObj)
	bindingMap, err := r.buildClusterResourceBindings(ctx, placementObj, latestSchedulingPolicySnapshot)
	if err != nil {
		return allRPS, rpsSetCondTypeCounter, err
	}

	resourceSnapshotIndexMap, err := r.findClusterResourceSnapshotIndexForBindings(ctx, placementObj, bindingMap)
	if err != nil {
		return allRPS, rpsSetCondTypeCounter, err
	}

	for idx := range selected {
		clusterDecision := selected[idx]
		rps := &fleetv1beta1.ResourcePlacementStatus{}

		// Port back the old conditions.
		// This is necessary for Fleet to track the last transition times correctly.
		if oldConds, ok := oldRPSMap[clusterDecision.ClusterName]; ok {
			rps.Conditions = oldConds
		}

		// Set the scheduled condition.
		scheduledCondition := metav1.Condition{
			Status:             metav1.ConditionTrue,
			Type:               string(fleetv1beta1.ResourceScheduledConditionType),
			Reason:             condition.ScheduleSucceededReason,
			Message:            clusterDecision.Reason,
			ObservedGeneration: placementObj.GetGeneration(),
		}
		meta.SetStatusCondition(&rps.Conditions, scheduledCondition)

		// Set the cluster name.
		rps.ClusterName = clusterDecision.ClusterName

		// Prepare the new conditions.
		binding := bindingMap[clusterDecision.ClusterName]
		resourceSnapshotIndexOnBinding := resourceSnapshotIndexMap[clusterDecision.ClusterName]
		setStatusByCondType := r.setResourcePlacementStatusPerCluster(placementObj, latestClusterResourceSnapshot, resourceSnapshotIndexOnBinding, binding, rps, expectedCondTypes)

		// Update the counter.
		for condType, condStatus := range setStatusByCondType {
			switch condStatus {
			case metav1.ConditionTrue:
				rpsSetCondTypeCounter[condType][condition.TrueConditionStatus]++
			case metav1.ConditionFalse:
				rpsSetCondTypeCounter[condType][condition.FalseConditionStatus]++
			case metav1.ConditionUnknown:
				rpsSetCondTypeCounter[condType][condition.UnknownConditionStatus]++
			}
		}

		// CRP status will refresh even if the spec has not changed. To avoid confusion,
		// Fleet will reset unused conditions.
		for i := condition.RolloutStartedCondition; i < condition.TotalCondition; i++ {
			if _, ok := setStatusByCondType[i]; !ok {
				meta.RemoveStatusCondition(&rps.Conditions, string(i.ResourcePlacementConditionType()))
			}
		}
		// The allRPS slice has been pre-allocated, so the append call will never produce a new
		// slice; here, however, Fleet will still return the old slice just in case.
		allRPS = append(allRPS, *rps)
		klog.V(2).InfoS("Populated the resource placement status for the scheduled cluster", "placement", klog.KObj(placementObj), "cluster", clusterDecision.ClusterName, "resourcePlacementStatus", rps)
	}

	return allRPS, rpsSetCondTypeCounter, nil
}

// TODO: make this work with RP
// setPlacementConditions currently sets the CRP conditions based on the resource placement statuses.
func setPlacementConditions(
	placementObj fleetv1beta1.PlacementObj,
	allRPS []fleetv1beta1.ResourcePlacementStatus,
	rpsSetCondTypeCounter [condition.TotalCondition][condition.TotalConditionStatus]int,
	expectedCondTypes []condition.ResourceCondition,
) {
	// Track all the condition types that have been set.
	setCondTypes := make(map[condition.ResourceCondition]interface{})

	for _, i := range expectedCondTypes {
		setCondTypes[i] = nil
		// If any given condition type is set to False or Unknown, Fleet will skip evaluation of the
		// rest conditions.
		shouldSkipRestCondTypes := false
		switch {
		case rpsSetCondTypeCounter[i][condition.UnknownConditionStatus] > 0:
			// There is at least one Unknown condition of the given type being set on the per cluster placement statuses.
			placementObj.SetConditions(i.UnknownClusterResourcePlacementCondition(placementObj.GetGeneration(), rpsSetCondTypeCounter[i][condition.UnknownConditionStatus]))
			shouldSkipRestCondTypes = true
		case rpsSetCondTypeCounter[i][condition.FalseConditionStatus] > 0:
			// There is at least one False condition of the given type being set on the per cluster placement statuses.
			placementObj.SetConditions(i.FalseClusterResourcePlacementCondition(placementObj.GetGeneration(), rpsSetCondTypeCounter[i][condition.FalseConditionStatus]))
			shouldSkipRestCondTypes = true
		default:
			// All the conditions of the given type are True.
			cond := i.TrueClusterResourcePlacementCondition(placementObj.GetGeneration(), rpsSetCondTypeCounter[i][condition.TrueConditionStatus])
			if i == condition.OverriddenCondition {
				hasOverride := false
				for _, status := range allRPS {
					if len(status.ApplicableResourceOverrides) > 0 || len(status.ApplicableClusterResourceOverrides) > 0 {
						hasOverride = true
						break
					}
				}
				if !hasOverride {
					cond.Reason = condition.OverrideNotSpecifiedReason
					cond.Message = "No override rules are configured for the selected resources"
				}
			}
			placementObj.SetConditions(cond)
		}

		if shouldSkipRestCondTypes {
			break
		}
	}

	// As CRP status will refresh even if the spec has not changed, Fleet will reset any unused conditions
	// to avoid confusion.
	for i := condition.RolloutStartedCondition; i < condition.TotalCondition; i++ {
		if _, ok := setCondTypes[i]; !ok {
			meta.RemoveStatusCondition(&placementObj.GetPlacementStatus().Conditions, string(i.ClusterResourcePlacementConditionType()))
		}
	}

	klog.V(2).InfoS("Populated the placement conditions", "placement", klog.KObj(placementObj))
}

// TODO: make this work with RP
func (r *Reconciler) buildClusterResourceBindings(ctx context.Context, placementObj fleetv1beta1.PlacementObj, latestSchedulingPolicySnapshot fleetv1beta1.PolicySnapshotObj) (map[string]fleetv1beta1.BindingObj, error) {
	// List all bindings derived from the CRP.
	bindingList := &fleetv1beta1.ClusterResourceBindingList{}
	listOptions := client.MatchingLabels{
		fleetv1beta1.PlacementTrackingLabel: placementObj.GetName(),
	}
	crpKObj := klog.KObj(placementObj)
	if err := r.Client.List(ctx, bindingList, listOptions); err != nil {
		klog.ErrorS(err, "Failed to list all bindings", "clusterResourcePlacement", crpKObj)
		return nil, controller.NewAPIServerError(true, err)
	}

	res := make(map[string]fleetv1beta1.BindingObj, len(bindingList.Items))
	bindings := bindingList.Items
	// filter out the latest resource bindings
	for i := range bindings {
		if !bindings[i].DeletionTimestamp.IsZero() {
			klog.V(2).InfoS("Filtering out the deleting clusterResourceBinding", "clusterResourceBinding", klog.KObj(&bindings[i]))
			continue
		}

		if len(bindings[i].Spec.TargetCluster) == 0 {
			err := fmt.Errorf("targetCluster is empty on clusterResourceBinding %s", bindings[i].Name)
			klog.ErrorS(controller.NewUnexpectedBehaviorError(err), "Found an invalid clusterResourceBinding and skipping it when building placement status", "clusterResourceBinding", klog.KObj(&bindings[i]), "placement", crpKObj)
			continue
		}

		// We don't check the bindings[i].Spec.ResourceSnapshotName != latestResourceSnapshot.Name here.
		// The existing conditions are needed when building the new ones.
		if bindings[i].Spec.SchedulingPolicySnapshotName != latestSchedulingPolicySnapshot.GetName() {
			continue
		}
		res[bindings[i].Spec.TargetCluster] = &bindings[i]
	}
	return res, nil
}

// TODO: make this work with RP
// findClusterResourceSnapshotIndexForBindings finds the resource snapshot index for each binding.
// It returns a map which maps the target cluster name to the resource snapshot index string.
func (r *Reconciler) findClusterResourceSnapshotIndexForBindings(
	ctx context.Context,
	placementObj fleetv1beta1.PlacementObj,
	bindingMap map[string]fleetv1beta1.BindingObj,
) (map[string]string, error) {
	crpKObj := klog.KObj(placementObj)
	res := make(map[string]string, len(bindingMap))
	for targetCluster, binding := range bindingMap {
		resourceSnapshotName := binding.GetBindingSpec().ResourceSnapshotName
		if resourceSnapshotName == "" {
			klog.InfoS("Empty resource snapshot name found in binding, controller might observe in-between state", "binding", klog.KObj(binding), "placement", crpKObj)
			res[targetCluster] = ""
			continue
		}
		resourceSnapshot := &fleetv1beta1.ClusterResourceSnapshot{}
		if err := r.Client.Get(ctx, types.NamespacedName{Name: resourceSnapshotName, Namespace: ""}, resourceSnapshot); err != nil {
			if apierrors.IsNotFound(err) {
				klog.InfoS("The resource snapshot specified in binding is not found, probably deleted due to revision history limit",
					"resourceSnapshotName", resourceSnapshotName, "binding", klog.KObj(binding), "placement", crpKObj)
				res[targetCluster] = ""
				continue
			}
			klog.ErrorS(err, "Failed to get the cluster resource snapshot specified in binding", "resourceSnapshotName", resourceSnapshotName, "binding", klog.KObj(binding), "placement", crpKObj)
			return res, controller.NewAPIServerError(true, err)
		}
		res[targetCluster] = resourceSnapshot.GetLabels()[fleetv1beta1.ResourceIndexLabel]
	}

	return res, nil
}

// setResourcePlacementStatusPerCluster sets the resource related fields for each cluster.
// It returns a map which tracks the set status for each relevant condition type.
func (r *Reconciler) setResourcePlacementStatusPerCluster(
	placementObj fleetv1beta1.PlacementObj,
	latestResourceSnapshot fleetv1beta1.ResourceSnapshotObj,
	resourceSnapshotIndexOnBinding string,
	binding fleetv1beta1.BindingObj,
	status *fleetv1beta1.ResourcePlacementStatus,
	expectedCondTypes []condition.ResourceCondition,
) map[condition.ResourceCondition]metav1.ConditionStatus {
	res := make(map[condition.ResourceCondition]metav1.ConditionStatus)
	if binding == nil {
		// The binding cannot be found; Fleet might be observing an in-between state where
		// the cluster has been picked but the binding has not been created yet.
		meta.SetStatusCondition(&status.Conditions, condition.RolloutStartedCondition.UnknownResourceConditionPerCluster(placementObj.GetGeneration()))
		res[condition.RolloutStartedCondition] = metav1.ConditionUnknown
		return res
	}

	// For External rollout strategy, the per-cluster status is set to whatever exists on the binding.
	if placementObj.GetPlacementSpec().Strategy.Type == fleetv1beta1.ExternalRolloutStrategyType {
		status.ObservedResourceIndex = resourceSnapshotIndexOnBinding
		setResourcePlacementStatusBasedOnBinding(placementObj, binding, status, expectedCondTypes, res)
		return res
	}

	// TODO (wantjian): we only change the per-cluster status for External rollout strategy for now, so set the ObservedResourceIndex as the latest.
	status.ObservedResourceIndex = latestResourceSnapshot.GetLabels()[fleetv1beta1.ResourceIndexLabel]
	rolloutStartedCond := binding.GetCondition(string(condition.RolloutStartedCondition.ResourceBindingConditionType()))
	switch {
	case binding.GetBindingSpec().ResourceSnapshotName != latestResourceSnapshot.GetName() && condition.IsConditionStatusFalse(rolloutStartedCond, binding.GetGeneration()):
		// The binding uses an out of date resource snapshot and rollout controller has reported
		// that the rollout is being blocked (the RolloutStarted condition is of the False status).
		cond := metav1.Condition{
			Type:               string(condition.RolloutStartedCondition.ResourcePlacementConditionType()),
			Status:             metav1.ConditionFalse,
			ObservedGeneration: placementObj.GetGeneration(),
			Reason:             condition.RolloutNotStartedYetReason,
			Message:            "The rollout is being blocked by the rollout strategy",
		}
		meta.SetStatusCondition(&status.Conditions, cond)
		res[condition.RolloutStartedCondition] = metav1.ConditionFalse
		return res
	case binding.GetBindingSpec().ResourceSnapshotName != latestResourceSnapshot.GetName():
		// The binding uses an out of date resource snapshot, and the RolloutStarted condition is
		// set to True, Unknown, or has become stale. Fleet might be observing an in-between state.
		meta.SetStatusCondition(&status.Conditions, condition.RolloutStartedCondition.UnknownResourceConditionPerCluster(placementObj.GetGeneration()))
		klog.V(5).InfoS("The cluster resource binding has a stale RolloutStarted condition, or it links to an out of date resource snapshot yet has the RolloutStarted condition set to True or Unknown status", "clusterResourceBinding", klog.KObj(binding), "placement", klog.KObj(placementObj))
		res[condition.RolloutStartedCondition] = metav1.ConditionUnknown
		return res
	default:
		// The binding uses the latest resource snapshot.
		setResourcePlacementStatusBasedOnBinding(placementObj, binding, status, expectedCondTypes, res)
		return res
	}
}

// setResourcePlacementStatusBasedOnBinding sets the placement status based on its corresponding binding status.
// It updates the status object in place and tracks the set status for each relevant condition type in setStatusByCondType map provided.
func setResourcePlacementStatusBasedOnBinding(
	placementObj fleetv1beta1.PlacementObj,
	binding fleetv1beta1.BindingObj,
	status *fleetv1beta1.ResourcePlacementStatus,
	expectedCondTypes []condition.ResourceCondition,
	setStatusByCondType map[condition.ResourceCondition]metav1.ConditionStatus,
) {
	for _, i := range expectedCondTypes {
		bindingCond := binding.GetCondition(string(i.ResourceBindingConditionType()))
		if !condition.IsConditionStatusTrue(bindingCond, binding.GetGeneration()) &&
			!condition.IsConditionStatusFalse(bindingCond, binding.GetGeneration()) {
			meta.SetStatusCondition(&status.Conditions, i.UnknownResourceConditionPerCluster(placementObj.GetGeneration()))
			klog.V(5).InfoS("Find an unknown condition", "bindingCond", bindingCond, "clusterResourceBinding", klog.KObj(binding), "placement", klog.KObj(placementObj))
			setStatusByCondType[i] = metav1.ConditionUnknown
			break
		}

		switch i {
		case condition.RolloutStartedCondition:
			if bindingCond.Status == metav1.ConditionTrue {
				status.ApplicableResourceOverrides = binding.GetBindingSpec().ResourceOverrideSnapshots
				status.ApplicableClusterResourceOverrides = binding.GetBindingSpec().ClusterResourceOverrideSnapshots
			}
		case condition.AppliedCondition, condition.AvailableCondition:
			if bindingCond.Status == metav1.ConditionFalse {
				status.FailedPlacements = binding.GetBindingStatus().FailedPlacements
				status.DiffedPlacements = binding.GetBindingStatus().DiffedPlacements
			}
			// Note that configuration drifts can occur whether the manifests are applied
			// successfully or not.
			status.DriftedPlacements = binding.GetBindingStatus().DriftedPlacements
		case condition.DiffReportedCondition:
			if bindingCond.Status == metav1.ConditionTrue {
				status.DiffedPlacements = binding.GetBindingStatus().DiffedPlacements
			}
		}

		cond := metav1.Condition{
			Type:               string(i.ResourcePlacementConditionType()),
			Status:             bindingCond.Status,
			ObservedGeneration: placementObj.GetGeneration(),
			Reason:             bindingCond.Reason,
			Message:            bindingCond.Message,
		}
		meta.SetStatusCondition(&status.Conditions, cond)
		setStatusByCondType[i] = bindingCond.Status

		if bindingCond.Status == metav1.ConditionFalse {
			break // if the current condition is false, no need to populate the rest conditions
		}
	}
}
