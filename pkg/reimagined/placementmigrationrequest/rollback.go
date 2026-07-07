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

package placementmigrationrequest

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"

	experimentalv1beta1 "github.com/kubefleet-dev/kubefleet/apis/experimental/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/errors"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/parallelizer"
)

func (r *Reconciler) rollback(ctx context.Context, req *experimentalv1beta1.PlacementMigrationRequest) (ctrl.Result, error) {
	if isRollbackNotNeededOrCompleted(req) {
		klog.V(2).InfoS("Rollback is not needed or has already been completed; skipping the rollback process", "workloadMigrationRequest", klog.KObj(req))

		rolledBackCond := meta.FindStatusCondition(req.Status.Conditions, experimentalv1beta1.PlacementMigrationRequestCondTypeRolledBack)
		if rolledBackCond == nil {
			meta.SetStatusCondition(&req.Status.Conditions, metav1.Condition{
				Type:               experimentalv1beta1.PlacementMigrationRequestCondTypeRolledBack,
				Status:             metav1.ConditionTrue,
				Reason:             experimentalv1beta1.PlacementMigrationRequestRolledBackCondReasonSucceeded,
				Message:            "The rollback process is not needed or has already been completed; no action is taken",
				ObservedGeneration: req.Generation,
			})
			if err := r.HubClient.Status().Update(ctx, req); err != nil {
				wrappedErr := errors.NewAPIServerError(err, "", true, "workloadMigrationRequest", klog.KObj(req))
				klog.ErrorS(wrappedErr, "Failed to update the status of the workload migration request after determining that rollback is not needed or has already been completed", errors.Args(wrappedErr)...)
				return ctrl.Result{}, wrappedErr
			}
		}
		return ctrl.Result{}, nil
	}

	// Prepare for the rollback process.
	migrations := req.Status.PlacementsToMigrate
	bundles := make([]migrationAttemptBundle, len(migrations))
	for idx := range migrations {
		bundles[idx] = migrationAttemptBundle{
			attempt: &migrations[idx],
		}
	}

	// Run the rollback attempts in parallel.
	workerCnt := 1
	if req.Spec.MaxConcurrency != nil {
		workerCnt = int(*req.Spec.MaxConcurrency)
	}
	p := parallelizer.NewParallelizer(workerCnt)

	deadline := time.Now().Add(r.MaxWaitTimePerRun)
	childCtx, childCancel := context.WithDeadline(ctx, deadline)
	defer childCancel()

	innerClient := r.HubClient
	doWork := func(idx int) {
		bundle := &bundles[idx]
		bundle.assignedWorkerIdx = idx

		// Return the processing result if the bundle has been processed.
		shouldSkip := shouldSkipIfRolledBackFor(bundle)
		if shouldSkip {
			klog.V(2).InfoS("A bundle has been processed", "workerIdx", idx, "result", bundle.res)
			return
		}

		migrateUntil(childCtx, innerClient, req, bundle, true)
		klog.V(2).InfoS("Finished processing a bundle", "workerIdx", idx, "result", bundle.res, "workloadMigrationAttempt", klog.KObj(req))
	}
	p.ParallelizeUntil(childCtx, len(bundles), doWork, "RollingBack")

	res, err := r.refreshStatusUponRollbackRunCompletion(ctx, req, bundles)
	if err != nil {
		wrappedErr := errors.NewAPIServerError(err, "failed to refresh the status of the workload migration request upon the completion of the rollback run", true)
		return ctrl.Result{}, wrappedErr
	}

	klog.V(2).InfoS("Finished a round of rollback processing for the migration request", "workloadMigrationRequest", klog.KObj(req), "migrationRunResult", res)
	if res == migrationRunResultInProgress {
		// The rollback run is still in progress; requeue for the next round of processing.
		return ctrl.Result{RequeueAfter: 3 * time.Second}, nil
	} else {
		// The rollback run has completed (either succeeded or failed); no need to requeue.
		return ctrl.Result{}, nil
	}
}

func isRollbackNotNeededOrCompleted(req *experimentalv1beta1.PlacementMigrationRequest) bool {
	initCond := meta.FindStatusCondition(req.Status.Conditions, experimentalv1beta1.PlacementMigrationRequestCondTypeInitialized)
	rolledBackCond := meta.FindStatusCondition(req.Status.Conditions, experimentalv1beta1.PlacementMigrationRequestCondTypeRolledBack)

	if initCond == nil || initCond.Status != metav1.ConditionTrue {
		// The migration request has not been initialized. No need to roll back.
		return true
	}

	if rolledBackCond != nil && (rolledBackCond.Status == metav1.ConditionTrue || rolledBackCond.Reason == experimentalv1beta1.PlacementMigrationRequestRolledBackCondReasonFailed) {
		// The migration request has already been rolled back or the rollback attempt has failed.
		// No need to roll back again.
		return true
	}
	return false
}

func shouldSkipIfRolledBackFor(bundle *migrationAttemptBundle) bool {
	attempt := bundle.attempt
	rolledBackCond := meta.FindStatusCondition(attempt.Conditions, experimentalv1beta1.PlacementMigrationAttemptCondTypeRolledBack)
	switch {
	case rolledBackCond == nil:
		return false
	case rolledBackCond.Status == metav1.ConditionTrue && rolledBackCond.Reason == experimentalv1beta1.PlacementMigrationAttemptRolledBackCondReasonSucceeded:
		bundle.res = migrationAttemptResultSucceeded
		return true
	case rolledBackCond.Status == metav1.ConditionFalse && rolledBackCond.Reason == experimentalv1beta1.PlacementMigrationAttemptRolledBackCondReasonFailed:
		bundle.res = migrationAttemptResultFailed
		return true
	case rolledBackCond.Status == metav1.ConditionTrue && rolledBackCond.Reason == experimentalv1beta1.PlacementMigrationAttemptRolledBackCondReasonSkipped:
		bundle.res = migrationAttemptResultSkipped
		return true
	}
	return false
}
