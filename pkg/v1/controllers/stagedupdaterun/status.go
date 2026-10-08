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

package stagedupdaterun

import (
	"context"
	"fmt"

	"github.com/kubefleet-dev/kubefleet/pkg/utils/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	rolloutv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/rollout/v1alpha1"
)

func (r *Reconciler) markStagedUpdateRunAsInTerminalState(
	ctx context.Context,
	stagedUpdateRun rolloutv1alpha1.StagedUpdateRunAccessor,
	message string,
) error {
	initCond := meta.FindStatusCondition(stagedUpdateRun.GetStatus().Conditions, rolloutv1alpha1.StagedUpdateRunCondTypeInitialized)
	completedCond := meta.FindStatusCondition(stagedUpdateRun.GetStatus().Conditions, rolloutv1alpha1.StagedUpdateRunCondTypeCompleted)
	stagedUpdateRunStatus := stagedUpdateRun.GetStatus()

	switch {
	case initCond == nil:
		// Add the failed to initialize condition if the staged update run has not been initialized yet; this alone
		// puts the staged update run in a terminal state, so the completed condition is not needed.
		meta.SetStatusCondition(&stagedUpdateRunStatus.Conditions, metav1.Condition{
			Type:               rolloutv1alpha1.StagedUpdateRunCondTypeInitialized,
			Status:             metav1.ConditionFalse,
			Reason:             rolloutv1alpha1.StagedUpdateRunInitializedCondReasonFailed,
			Message:            message,
			ObservedGeneration: stagedUpdateRun.GetGeneration(),
		})
	case initCond.Status == metav1.ConditionFalse:
		// The staged update run has already failed to initialize; there is nothing to add.
	case completedCond == nil:
		// The staged update run has been initialized but has not completed yet; add the failed to complete condition.
		meta.SetStatusCondition(&stagedUpdateRunStatus.Conditions, metav1.Condition{
			Type:               rolloutv1alpha1.StagedUpdateRunCondTypeCompleted,
			Status:             metav1.ConditionTrue,
			Reason:             rolloutv1alpha1.StagedUpdateRunCompletedCondReasonFailed,
			Message:            message,
			ObservedGeneration: stagedUpdateRun.GetGeneration(),
		})
	default:
		// The staged update run has already failed to complete; there is nothing to add.
	}

	if err := r.HubClient.Status().Update(ctx, stagedUpdateRun); err != nil {
		return errors.NewAPIServerError(err, "failed to update the staged update run status", false)
	}
	return nil
}

func (r *Reconciler) markStagedUpdateRunAsFailedToInitialize(
	ctx context.Context,
	stagedUpdateRun rolloutv1alpha1.StagedUpdateRunAccessor,
	message string,
) error {
	stagedUpdateRunStatus := stagedUpdateRun.GetStatus()

	meta.SetStatusCondition(&stagedUpdateRunStatus.Conditions, metav1.Condition{
		Type:               rolloutv1alpha1.StagedUpdateRunCondTypeInitialized,
		Status:             metav1.ConditionFalse,
		Reason:             rolloutv1alpha1.StagedUpdateRunInitializedCondReasonFailed,
		Message:            message,
		ObservedGeneration: stagedUpdateRun.GetGeneration(),
	})

	if err := r.HubClient.Status().Update(ctx, stagedUpdateRun); err != nil {
		return errors.NewAPIServerError(err, "failed to update the staged update run status", false)
	}
	return nil
}

func (r *Reconciler) syncStagedUpdateRunInitializedStatus(
	ctx context.Context,
	stagedUpdateRun rolloutv1alpha1.StagedUpdateRunAccessor,
	perStageStatuses []rolloutv1alpha1.PerStageStatus,
	resourceSnapshotName string,
) error {
	stagedUpdateRunStatus := stagedUpdateRun.GetStatus()

	// Set the resource snapshot name to roll out, the prepped stages, and the initialized condition.
	stagedUpdateRunStatus.ResourceSnapshotNameToRollout = resourceSnapshotName
	stagedUpdateRunStatus.Stages = perStageStatuses

	meta.SetStatusCondition(&stagedUpdateRunStatus.Conditions, metav1.Condition{
		Type:   rolloutv1alpha1.StagedUpdateRunCondTypeInitialized,
		Status: metav1.ConditionTrue,
		Reason: rolloutv1alpha1.StagedUpdateRunInitializedCondReasonPreppedResourceSnapshotAndAllStages,
		Message: fmt.Sprintf("the staged update run has been initialized (resource snapshot to roll out: %s; prepped %d stages)",
			resourceSnapshotName, len(perStageStatuses)),
		ObservedGeneration: stagedUpdateRun.GetGeneration(),
	})

	if err := r.HubClient.Status().Update(ctx, stagedUpdateRun); err != nil {
		return errors.NewAPIServerError(err, "failed to update the staged update run status", false)
	}
	return nil
}
