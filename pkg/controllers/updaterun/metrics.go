package updaterun

import (
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/klog/v2"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	hubmetrics "github.com/kubefleet-dev/kubefleet/pkg/metrics/hub"
)

// emitUpdateRunStatusMetric emits the update run status metric based on status conditions in the updateRun.
func emitUpdateRunStatusMetric(updateRun placementv1beta1.UpdateRunObj) {
	generation := updateRun.GetGeneration()
	state := updateRun.GetUpdateRunSpec().State

	updateRunStatus := updateRun.GetUpdateRunStatus()
	succeedCond := meta.FindStatusCondition(updateRunStatus.Conditions, string(placementv1beta1.StagedUpdateRunConditionSucceeded))
	if succeedCond != nil && succeedCond.ObservedGeneration == generation {
		hubmetrics.FleetUpdateRunStatusLastTimestampSeconds.WithLabelValues(updateRun.GetNamespace(), updateRun.GetName(), string(state),
			string(placementv1beta1.StagedUpdateRunConditionSucceeded), string(succeedCond.Status), succeedCond.Reason).SetToCurrentTime()
		return
	}

	progressingCond := meta.FindStatusCondition(updateRunStatus.Conditions, string(placementv1beta1.StagedUpdateRunConditionProgressing))
	if progressingCond != nil && progressingCond.ObservedGeneration == generation {
		hubmetrics.FleetUpdateRunStatusLastTimestampSeconds.WithLabelValues(updateRun.GetNamespace(), updateRun.GetName(), string(state),
			string(placementv1beta1.StagedUpdateRunConditionProgressing), string(progressingCond.Status), progressingCond.Reason).SetToCurrentTime()
		return
	}

	initializedCond := meta.FindStatusCondition(updateRunStatus.Conditions, string(placementv1beta1.StagedUpdateRunConditionInitialized))
	if initializedCond != nil && initializedCond.ObservedGeneration == generation {
		hubmetrics.FleetUpdateRunStatusLastTimestampSeconds.WithLabelValues(updateRun.GetNamespace(), updateRun.GetName(), string(state),
			string(placementv1beta1.StagedUpdateRunConditionInitialized), string(initializedCond.Status), initializedCond.Reason).SetToCurrentTime()
		return
	}

	// We should rarely reach here, it can only happen when updating updateRun status fails.
	klog.V(2).InfoS("There's no valid status condition on updateRun, status updating failed possibly", "updateRun", klog.KObj(updateRun))
}

// recordApprovalRequestLatency records the time from approval request creation to user approval.
func recordApprovalRequestLatency(
	approvalRequest placementv1beta1.ApprovalRequestObj,
	updateRun placementv1beta1.UpdateRunObj,
	stageTaskType string,
) {
	approvedCond := meta.FindStatusCondition(
		approvalRequest.GetApprovalRequestStatus().Conditions,
		string(placementv1beta1.ApprovalRequestConditionApproved))
	if approvedCond == nil {
		return
	}
	latencySeconds := approvedCond.LastTransitionTime.Sub(
		approvalRequest.GetCreationTimestamp().Time).Seconds()
	hubmetrics.FleetUpdateRunApprovalRequestLatencySeconds.WithLabelValues(
		updateRun.GetNamespace(),
		updateRun.GetName(),
		stageTaskType,
	).Observe(latencySeconds)
}

// recordStageClusterUpdatingDuration records the time from stage start to when all clusters finish updating.
func recordStageClusterUpdatingDuration(stageStatus *placementv1beta1.StageUpdatingStatus, updateRun placementv1beta1.UpdateRunObj) {
	if stageStatus.StartTime == nil {
		return
	}
	durationSeconds := time.Since(stageStatus.StartTime.Time).Seconds()
	hubmetrics.FleetUpdateRunStageClusterUpdatingDurationSeconds.WithLabelValues(
		updateRun.GetNamespace(),
		updateRun.GetName(),
	).Observe(durationSeconds)
}
