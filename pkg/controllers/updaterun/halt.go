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

package updaterun

import (
	"errors"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/condition"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/controller"
)

// haltUpdateRun handles the abandoning or pausing of the update run.
func (r *Reconciler) haltUpdateRun(
	updateRun placementv1beta1.UpdateRunObj,
	updatingStageIndex int,
	toBeUpdatedBindings, toBeDeletedBindings []placementv1beta1.BindingObj,
) (bool, time.Duration, error) {
	if updateRun.GetUpdateRunSpec().State == placementv1beta1.StateAbandoned {
		markUpdateRunAbandoning(updateRun)
	}

	updateRunStatus := updateRun.GetUpdateRunStatus()
	if updatingStageIndex < len(updateRunStatus.StagesStatus) {
		maxConcurrency, err := calculateMaxConcurrencyValue(updateRunStatus, updatingStageIndex)
		if err != nil {
			return false, 0, err
		}
		updatingStage := &updateRunStatus.StagesStatus[updatingStageIndex]
		finished, waitTime, execErr := r.haltUpdatingStage(updateRun, updatingStageIndex, toBeUpdatedBindings, maxConcurrency)
		if errors.Is(execErr, errStagedUpdatedAborted) {
			markStageUpdatingFailed(updatingStage, updateRun.GetGeneration(), execErr.Error())
			return true, waitTime, execErr
		}
		return finished, waitTime, execErr
	}
	// All the stages have finished, halt the delete stage.
	finished, execErr := r.haltDeleteStage(updateRun, toBeDeletedBindings)
	if errors.Is(execErr, errStagedUpdatedAborted) {
		markStageUpdatingFailed(updateRunStatus.DeletionStageStatus, updateRun.GetGeneration(), execErr.Error())
		return true, 0, execErr
	}
	return finished, clusterUpdatingWaitTime, execErr
}

// haltUpdatingStage halts the updating stage by letting the updating bindings finish and not starting new updates.
func (r *Reconciler) haltUpdatingStage(
	updateRun placementv1beta1.UpdateRunObj,
	updatingStageIndex int,
	toBeUpdatedBindings []placementv1beta1.BindingObj,
	maxConcurrency int,
) (bool, time.Duration, error) {
	updateRunStatus := updateRun.GetUpdateRunStatus()
	updatingStageStatus := &updateRunStatus.StagesStatus[updatingStageIndex]
	updateRunRef := klog.KObj(updateRun)
	// Create the map of the toBeUpdatedBindings.
	toBeUpdatedBindingsMap := make(map[string]placementv1beta1.BindingObj, len(toBeUpdatedBindings))
	for _, binding := range toBeUpdatedBindings {
		bindingSpec := binding.GetBindingSpec()
		toBeUpdatedBindingsMap[bindingSpec.TargetCluster] = binding
	}

	clusterUpdatingCount := 0
	clusterUpdated := false
	var stuckClusterNames []string
	var clusterUpdateErrors []error
	// Go through each cluster in the stage and check if it's updating/succeeded/failed/not started.
	for i := 0; i < len(updatingStageStatus.Clusters) && clusterUpdatingCount < maxConcurrency; i++ {
		clusterStatus := &updatingStageStatus.Clusters[i]
		clusterStartedCond := meta.FindStatusCondition(clusterStatus.Conditions, string(placementv1beta1.ClusterUpdatingConditionStarted))
		if clusterStartedCond == nil {
			// Cluster has not started updating therefore no need to do anything.
			continue
		}

		clusterUpdateSucceededCond := meta.FindStatusCondition(clusterStatus.Conditions, string(placementv1beta1.ClusterUpdatingConditionSucceeded))
		if clusterUpdateSucceededCond != nil && (clusterUpdateSucceededCond.Status == metav1.ConditionFalse || clusterUpdateSucceededCond.Status == metav1.ConditionTrue) {
			// The cluster has already been updated.
			continue
		}

		clusterUpdatingCount++

		binding := toBeUpdatedBindingsMap[clusterStatus.ClusterName]
		finished, updateErr := checkClusterUpdateResult(binding, clusterStatus, updatingStageStatus, updateRun)
		if updateErr != nil {
			clusterUpdateErrors = append(clusterUpdateErrors, updateErr)
		}
		if finished {
			// The cluster has finished successfully, we can process another cluster in this round.
			clusterUpdated = true
			clusterUpdatingCount--
		} else {
			// If cluster update has been running for more than "updateRunStuckThreshold", mark the update run as stuck.
			timeElapsed := time.Since(clusterStartedCond.LastTransitionTime.Time)
			if timeElapsed > updateRunStuckThreshold {
				klog.V(2).InfoS("Time waiting for cluster update to finish passes threshold, mark the update run as stuck", "time elapsed", timeElapsed, "threshold", updateRunStuckThreshold, "cluster", clusterStatus.ClusterName, "stage", updatingStageStatus.StageName, "updateRun", updateRunRef)
				stuckClusterNames = append(stuckClusterNames, clusterStatus.ClusterName)
			}
		}
	}

	// If there are stuck clusters, aggregate them into an error.
	aggregateUpdateRunStatus(updateRun, updatingStageStatus.StageName, stuckClusterNames)

	// Aggregate and return errors.
	if len(clusterUpdateErrors) > 0 {
		// Even though we aggregate errors, we can still check if one of the errors is a staged update aborted error by using errors.Is in the caller.
		return false, 0, utilerrors.NewAggregate(clusterUpdateErrors)
	}

	state := updateRun.GetUpdateRunSpec().State
	if clusterUpdatingCount == 0 && clusterUpdated {
		// All the clusters in the stage have finished updating or not started.
		if state == placementv1beta1.StateAbandoned {
			markStageUpdatingAbandoned(updatingStageStatus, updateRun.GetGeneration())
		}
		klog.V(2).InfoS("The stage has finished all clusters updating", "state", state, "stage", updatingStageStatus.StageName, "updateRun", updateRunRef)
		return true, 0, nil
	} else if clusterUpdatingCount == 0 && !clusterUpdated {
		// No clusters needed to be updated in this round, meaning all remaining clusters have not started yet or succeeded.
		klog.V(2).InfoS("No clusters needed to be updated", "stage", updatingStageStatus.StageName, "updateRun", updateRunRef)
		return true, 0, nil
	}
	// Some clusters are still updating.
	markStageUpdatingAbandoning(updatingStageStatus, updateRun.GetGeneration())
	return false, clusterUpdatingWaitTime, nil
}

// haltDeleteStage halts the delete stage by letting the deleting bindings finish.
func (r *Reconciler) haltDeleteStage(
	updateRun placementv1beta1.UpdateRunObj,
	toBeDeletedBindings []placementv1beta1.BindingObj,
) (bool, error) {
	updateRunRef := klog.KObj(updateRun)
	updateRunStatus := updateRun.GetUpdateRunStatus()
	existingDeleteStageStatus := updateRunStatus.DeletionStageStatus
	existingDeleteStageClusterMap := make(map[string]*placementv1beta1.ClusterUpdatingStatus, len(existingDeleteStageStatus.Clusters))
	for i := range existingDeleteStageStatus.Clusters {
		existingDeleteStageClusterMap[existingDeleteStageStatus.Clusters[i].ClusterName] = &existingDeleteStageStatus.Clusters[i]
	}
	// Mark the delete stage as abandoning in case it's not.
	markStageUpdatingAbandoning(existingDeleteStageStatus, updateRun.GetGeneration())
	for _, binding := range toBeDeletedBindings {
		bindingSpec := binding.GetBindingSpec()
		curCluster, exist := existingDeleteStageClusterMap[bindingSpec.TargetCluster]
		if !exist {
			// The cluster is not in the delete stage. This happens when the update run is abandoned as delete stage starts.
			continue
		}
		// In validation, we already check the binding must exist in the status.
		delete(existingDeleteStageClusterMap, bindingSpec.TargetCluster)
		if condition.IsConditionStatusTrue(meta.FindStatusCondition(curCluster.Conditions, string(placementv1beta1.ClusterUpdatingConditionSucceeded)), updateRun.GetGeneration()) {
			// The cluster status is marked as deleted.
			continue
		}
		if condition.IsConditionStatusTrue(meta.FindStatusCondition(curCluster.Conditions, string(placementv1beta1.ClusterUpdatingConditionStarted)), updateRun.GetGeneration()) {
			// The cluster status is marked as being deleted.
			if binding.GetDeletionTimestamp().IsZero() {
				// The cluster is marked as deleting but the binding is not deleting.
				unexpectedErr := controller.NewUnexpectedBehaviorError(fmt.Errorf("the cluster `%s` in the deleting stage is marked as deleting but its corresponding binding is not deleting", curCluster.ClusterName))
				klog.ErrorS(unexpectedErr, "The binding should be deleting before we mark a cluster deleting", "clusterStatus", curCluster, "updateRun", updateRunRef)
				return false, fmt.Errorf("%w: %s", errStagedUpdatedAborted, unexpectedErr.Error())
			}
			return false, nil
		}
	}
	klog.V(2).InfoS("The delete stage is abandoning", "numberOfDeletingClusters", len(toBeDeletedBindings), "updateRun", updateRunRef)
	if len(toBeDeletedBindings) == 0 {
		markStageUpdatingAbandoned(updateRunStatus.DeletionStageStatus, updateRun.GetGeneration())
	}
	return len(toBeDeletedBindings) == 0, nil
}

// markUpdateRunAbandoning marks the update run as abandoning in memory.
func markUpdateRunAbandoning(updateRun placementv1beta1.UpdateRunObj) {
	updateRunStatus := updateRun.GetUpdateRunStatus()
	meta.SetStatusCondition(&updateRunStatus.Conditions, metav1.Condition{
		Type:               string(placementv1beta1.StagedUpdateRunConditionProgressing),
		Status:             metav1.ConditionFalse,
		ObservedGeneration: updateRun.GetGeneration(),
		Reason:             condition.UpdateRunAbandoningReason,
		Message:            "The update run is the process of abandoning",
	})
}

// markStageUpdatingAbandoning marks the stage updating status as abandoning in memory.
func markStageUpdatingAbandoning(stageUpdatingStatus *placementv1beta1.StageUpdatingStatus, generation int64) {
	meta.SetStatusCondition(&stageUpdatingStatus.Conditions, metav1.Condition{
		Type:               string(placementv1beta1.StageUpdatingConditionProgressing),
		Status:             metav1.ConditionFalse,
		ObservedGeneration: generation,
		Reason:             condition.StageUpdatingAbandoningReason,
		Message:            "Waiting for all the updating clusters to finish updating before completing the abandoning process",
	})
}

// markStageUpdatingAbandoned marks the stage updating status as abandoned in memory.
func markStageUpdatingAbandoned(stageUpdatingStatus *placementv1beta1.StageUpdatingStatus, generation int64) {
	if stageUpdatingStatus.EndTime == nil {
		stageUpdatingStatus.EndTime = &metav1.Time{Time: time.Now()}
	}
	meta.SetStatusCondition(&stageUpdatingStatus.Conditions, metav1.Condition{
		Type:               string(placementv1beta1.StageUpdatingConditionProgressing),
		Status:             metav1.ConditionFalse,
		ObservedGeneration: generation,
		Reason:             condition.StageUpdatingAbandonedReason,
		Message:            "All the updating clusters have finished updating and no new updates will be started",
	})
}

// abandon is a wrapper function for backward compatibility that calls haltUpdateRun.
func (r *Reconciler) abandon(
	updateRun placementv1beta1.UpdateRunObj,
	updatingStageIndex int,
	toBeUpdatedBindings, toBeDeletedBindings []placementv1beta1.BindingObj,
) (bool, time.Duration, error) {
	return r.haltUpdateRun(updateRun, updatingStageIndex, toBeUpdatedBindings, toBeDeletedBindings)
}
