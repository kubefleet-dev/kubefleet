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
	"fmt"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	prometheusclientmodel "github.com/prometheus/client_model/go"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/controllers/updaterun/testutils"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/condition"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/controller/metrics"
)

var _ = Describe("Test the clusterStagedUpdateRun controller", func() {
	var tc *testutils.TestConfig

	BeforeEach(func() {
		tc = testutils.GenerateDefaultTestConfig(k8sClient)
	})

	AfterEach(func() {
		By("Checking the update run status metrics are removed")
		// No metrics are emitted as all are removed after updateRun is deleted.
		tc.ValidateUpdateRunMetricsEmitted()
		resetUpdateRunMetrics()
	})

	Context("Test reconciling a clusterStagedUpdateRun", func() {
		It("Should add the finalizer to the clusterStagedUpdateRun", func() {
			By("Creating a new clusterStagedUpdateRun")
			updateRun := tc.GenerateTestClusterStagedUpdateRun(0)
			Expect(k8sClient.Create(ctx, updateRun)).Should(Succeed())

			By("Checking the finalizer is added")
			tc.ValidateUpdateRunWithFunc(ctx, updateRunHasFinalizer)

			By("Deleting the clusterStagedUpdateRun")
			Expect(k8sClient.Delete(ctx, updateRun)).Should(Succeed())

			By("Checking the clusterStagedUpdateRun is deleted")
			tc.ValidateUpdateRunIsDeleted(ctx)
		})
	})

	Context("Test deleting a clusterStagedUpdateRun", func() {
		It("Should delete the clusterStagedUpdateRun without any clusterApprovalRequests", func() {
			By("Creating a new clusterStagedUpdateRun")
			updateRun := tc.GenerateTestClusterStagedUpdateRun(0)
			Expect(k8sClient.Create(ctx, updateRun)).Should(Succeed())

			By("Checking the finalizer is added")
			tc.ValidateUpdateRunWithFunc(ctx, updateRunHasFinalizer)

			By("Deleting the clusterStagedUpdateRun")
			Expect(k8sClient.Delete(ctx, updateRun)).Should(Succeed())

			By("Checking the clusterStagedUpdateRun is deleted")
			tc.ValidateUpdateRunIsDeleted(ctx)
		})

		It("Should delete the clusterStagedUpdateRun if it failed", func() {
			By("Creating a new clusterStagedUpdateRun")
			updateRun := tc.GenerateTestClusterStagedUpdateRun(0)
			Expect(k8sClient.Create(ctx, updateRun)).Should(Succeed())

			By("Checking the finalizer is added")
			tc.ValidateUpdateRunWithFunc(ctx, updateRunHasFinalizer)

			By("Updating the clusterStagedUpdateRun to failed")
			startedcond := generateTrueCondition(updateRun, placementv1beta1.StagedUpdateRunConditionProgressing)
			finishedcond := generateFalseCondition(updateRun, placementv1beta1.StagedUpdateRunConditionSucceeded)
			meta.SetStatusCondition(&updateRun.Status.Conditions, startedcond)
			meta.SetStatusCondition(&updateRun.Status.Conditions, finishedcond)
			Expect(k8sClient.Status().Update(ctx, updateRun)).Should(Succeed(), "failed to update the clusterStagedUpdateRun")

			By("Creating a clusterApprovalRequest")
			approvalRequest := tc.GenerateTestApprovalRequest("req1")
			Expect(k8sClient.Create(ctx, approvalRequest)).Should(Succeed())

			By("Deleting the clusterStagedUpdateRun")
			Expect(k8sClient.Delete(ctx, updateRun)).Should(Succeed())

			By("Checking the clusterStagedUpdateRun is deleted")
			tc.ValidateUpdateRunIsDeleted(ctx)

			By("Checking the clusterApprovalRequest is deleted")
			tc.ValidateApprovalRequestCount(ctx, 0)
		})

		It("Should not block deletion though the clusterStagedUpdateRun is still processing", func() {
			By("Creating a new clusterStagedUpdateRun")
			updateRun := tc.GenerateTestClusterStagedUpdateRun(0)
			Expect(k8sClient.Create(ctx, updateRun)).Should(Succeed())

			By("Checking the finalizer is added")
			tc.ValidateUpdateRunWithFunc(ctx, updateRunHasFinalizer)

			By("Updating the clusterStagedUpdateRun status to processing")
			startedcond := generateTrueCondition(updateRun, placementv1beta1.StagedUpdateRunConditionProgressing)
			meta.SetStatusCondition(&updateRun.Status.Conditions, startedcond)
			Expect(k8sClient.Status().Update(ctx, updateRun)).Should(Succeed(), "failed to add condition to the clusterStagedUpdateRun")

			By("Creating a clusterApprovalRequest")
			approvalRequest := tc.GenerateTestApprovalRequest("req1")
			Expect(k8sClient.Create(ctx, approvalRequest)).Should(Succeed())

			By("Deleting the clusterStagedUpdateRun")
			Expect(k8sClient.Delete(ctx, updateRun)).Should(Succeed())

			By("Checking the clusterStagedUpdateRun is deleted")
			tc.ValidateUpdateRunIsDeleted(ctx)

			By("Checking the clusterApprovalRequest is deleted")
			tc.ValidateApprovalRequestCount(ctx, 0)
		})

		It("Should delete all ClusterApprovalRequest objects associated with the clusterStagedUpdateRun", func() {
			By("Creating a new clusterStagedUpdateRun")
			updateRun := tc.GenerateTestClusterStagedUpdateRun(0)
			Expect(k8sClient.Create(ctx, updateRun)).Should(Succeed())

			By("Creating ClusterApprovalRequests")
			approvalRequests := []*placementv1beta1.ClusterApprovalRequest{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "req1",
						Labels: map[string]string{
							placementv1beta1.TargetUpdateRunLabel: updateRun.Name,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "req2",
						Labels: map[string]string{
							placementv1beta1.TargetUpdateRunLabel: updateRun.Name,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "req3",
						Labels: map[string]string{
							placementv1beta1.TargetUpdateRunLabel: updateRun.Name + "1", // different update run
						},
					},
				},
			}
			for _, req := range approvalRequests {
				Expect(k8sClient.Create(ctx, req)).Should(Succeed())
			}

			By("Checking the finalizer is added")
			tc.ValidateUpdateRunWithFunc(ctx, updateRunHasFinalizer)

			By("Deleting the clusterStagedUpdateRun")
			Expect(k8sClient.Delete(ctx, updateRun)).Should(Succeed())

			By("Checking the clusterStagedUpdateRun is deleted")
			tc.ValidateUpdateRunIsDeleted(ctx)

			By("Checking the clusterApprovalRequests are deleted")
			tc.ValidateApprovalRequestCount(ctx, 1)
		})

	})
})

func resetUpdateRunMetrics() {
	// Reset the update run status metrics before each test
	metrics.FleetUpdateRunStatusLastTimestampSeconds.Reset()
}

func updateRunHasFinalizer(updateRun *placementv1beta1.ClusterStagedUpdateRun) error {
	if !controllerutil.ContainsFinalizer(updateRun, placementv1beta1.ClusterStagedUpdateRunFinalizer) {
		return fmt.Errorf("finalizer not added to clusterStagedUpdateRun %s", updateRun.Name)
	}
	return nil
}

func generateMetricsLabels(
	updateRun *placementv1beta1.ClusterStagedUpdateRun,
	condition, status, reason string,
) []*prometheusclientmodel.LabelPair {
	return []*prometheusclientmodel.LabelPair{
		{Name: ptr.To("name"), Value: &updateRun.Name},
		{Name: ptr.To("generation"), Value: ptr.To(strconv.FormatInt(updateRun.Generation, 10))},
		{Name: ptr.To("condition"), Value: ptr.To(condition)},
		{Name: ptr.To("status"), Value: ptr.To(status)},
		{Name: ptr.To("reason"), Value: ptr.To(reason)},
	}
}

func generateInitializationFailedMetric(updateRun *placementv1beta1.ClusterStagedUpdateRun) *prometheusclientmodel.Metric {
	return &prometheusclientmodel.Metric{
		Label: generateMetricsLabels(updateRun, string(placementv1beta1.StagedUpdateRunConditionInitialized),
			string(metav1.ConditionFalse), condition.UpdateRunInitializeFailedReason),
		Gauge: &prometheusclientmodel.Gauge{
			Value: ptr.To(float64(time.Now().UnixNano()) / 1e9),
		},
	}
}

func generateProgressingMetric(updateRun *placementv1beta1.ClusterStagedUpdateRun) *prometheusclientmodel.Metric {
	return &prometheusclientmodel.Metric{
		Label: generateMetricsLabels(updateRun, string(placementv1beta1.StagedUpdateRunConditionProgressing),
			string(metav1.ConditionTrue), condition.UpdateRunProgressingReason),
		Gauge: &prometheusclientmodel.Gauge{
			Value: ptr.To(float64(time.Now().UnixNano()) / 1e9),
		},
	}
}

func generateWaitingMetric(updateRun *placementv1beta1.ClusterStagedUpdateRun) *prometheusclientmodel.Metric {
	return &prometheusclientmodel.Metric{
		Label: generateMetricsLabels(updateRun, string(placementv1beta1.StagedUpdateRunConditionProgressing),
			string(metav1.ConditionFalse), condition.UpdateRunWaitingReason),
		Gauge: &prometheusclientmodel.Gauge{
			Value: ptr.To(float64(time.Now().UnixNano()) / 1e9),
		},
	}
}

func generateStuckMetric(updateRun *placementv1beta1.ClusterStagedUpdateRun) *prometheusclientmodel.Metric {
	return &prometheusclientmodel.Metric{
		Label: generateMetricsLabels(updateRun, string(placementv1beta1.StagedUpdateRunConditionProgressing),
			string(metav1.ConditionFalse), condition.UpdateRunStuckReason),
		Gauge: &prometheusclientmodel.Gauge{
			Value: ptr.To(float64(time.Now().UnixNano()) / 1e9),
		},
	}
}

func generateFailedMetric(updateRun *placementv1beta1.ClusterStagedUpdateRun) *prometheusclientmodel.Metric {
	return &prometheusclientmodel.Metric{
		Label: generateMetricsLabels(updateRun, string(placementv1beta1.StagedUpdateRunConditionSucceeded),
			string(metav1.ConditionFalse), condition.UpdateRunFailedReason),
		Gauge: &prometheusclientmodel.Gauge{
			Value: ptr.To(float64(time.Now().UnixNano()) / 1e9),
		},
	}
}

func generateSucceededMetric(updateRun *placementv1beta1.ClusterStagedUpdateRun) *prometheusclientmodel.Metric {
	return &prometheusclientmodel.Metric{
		Label: generateMetricsLabels(updateRun, string(placementv1beta1.StagedUpdateRunConditionSucceeded),
			string(metav1.ConditionTrue), condition.UpdateRunSucceededReason),
		Gauge: &prometheusclientmodel.Gauge{
			Value: ptr.To(float64(time.Now().UnixNano()) / 1e9),
		},
	}
}

func generateTrueCondition(obj client.Object, condType any) metav1.Condition {
	reason, typeStr := "", ""
	switch cond := condType.(type) {
	case placementv1beta1.StagedUpdateRunConditionType:
		switch cond {
		case placementv1beta1.StagedUpdateRunConditionInitialized:
			reason = condition.UpdateRunInitializeSucceededReason
		case placementv1beta1.StagedUpdateRunConditionProgressing:
			reason = condition.UpdateRunProgressingReason
		case placementv1beta1.StagedUpdateRunConditionSucceeded:
			reason = condition.UpdateRunSucceededReason
		}
		typeStr = string(cond)
	case placementv1beta1.StageUpdatingConditionType:
		switch cond {
		case placementv1beta1.StageUpdatingConditionProgressing:
			reason = condition.StageUpdatingStartedReason
		case placementv1beta1.StageUpdatingConditionSucceeded:
			reason = condition.StageUpdatingSucceededReason
		}
		typeStr = string(cond)
	case placementv1beta1.ClusterUpdatingStatusConditionType:
		switch cond {
		case placementv1beta1.ClusterUpdatingConditionStarted:
			reason = condition.ClusterUpdatingStartedReason
		case placementv1beta1.ClusterUpdatingConditionSucceeded:
			reason = condition.ClusterUpdatingSucceededReason
		}
		typeStr = string(cond)
	case placementv1beta1.AfterStageTaskConditionType:
		switch cond {
		case placementv1beta1.AfterStageTaskConditionWaitTimeElapsed:
			reason = condition.AfterStageTaskWaitTimeElapsedReason
		case placementv1beta1.AfterStageTaskConditionApprovalRequestCreated:
			reason = condition.AfterStageTaskApprovalRequestCreatedReason
		case placementv1beta1.AfterStageTaskConditionApprovalRequestApproved:
			reason = condition.AfterStageTaskApprovalRequestApprovedReason
		}
		typeStr = string(cond)
	case placementv1beta1.ApprovalRequestConditionType:
		switch cond {
		case placementv1beta1.ApprovalRequestConditionApproved:
			reason = "LGTM"
		}
		typeStr = string(cond)
	case placementv1beta1.ResourceBindingConditionType:
		switch cond {
		case placementv1beta1.ResourceBindingAvailable:
			reason = condition.AvailableReason
		}
		typeStr = string(cond)
	}
	return metav1.Condition{
		Status:             metav1.ConditionTrue,
		Type:               typeStr,
		ObservedGeneration: obj.GetGeneration(),
		Reason:             reason,
	}
}

func generateFalseCondition(obj client.Object, condType any) metav1.Condition {
	reason, typeStr := "", ""
	switch cond := condType.(type) {
	case placementv1beta1.StagedUpdateRunConditionType:
		switch cond {
		case placementv1beta1.StagedUpdateRunConditionInitialized:
			reason = condition.UpdateRunInitializeFailedReason
		case placementv1beta1.StagedUpdateRunConditionSucceeded:
			reason = condition.UpdateRunFailedReason
		case placementv1beta1.StagedUpdateRunConditionProgressing:
			reason = condition.UpdateRunWaitingReason
		}
		typeStr = string(cond)
	case placementv1beta1.StageUpdatingConditionType:
		switch cond {
		case placementv1beta1.StageUpdatingConditionSucceeded:
			reason = condition.StageUpdatingFailedReason
		case placementv1beta1.StageUpdatingConditionProgressing:
			reason = condition.StageUpdatingWaitingReason
		}
		typeStr = string(cond)
	case placementv1beta1.ClusterUpdatingStatusConditionType:
		switch cond {
		case placementv1beta1.ClusterUpdatingConditionSucceeded:
			reason = condition.ClusterUpdatingFailedReason
		}
		typeStr = string(cond)
	case placementv1beta1.ResourceBindingConditionType:
		switch cond {
		case placementv1beta1.ResourceBindingApplied:
			reason = condition.ApplyFailedReason
		}
		typeStr = string(cond)
	}
	return metav1.Condition{
		Status:             metav1.ConditionFalse,
		Type:               typeStr,
		ObservedGeneration: obj.GetGeneration(),
		Reason:             reason,
	}
}

func generateFalseProgressingCondition(obj client.Object, condType any, succeeded bool) metav1.Condition {
	falseCond := generateFalseCondition(obj, condType)
	reason := ""
	switch condType {
	case placementv1beta1.StagedUpdateRunConditionProgressing:
		if succeeded {
			reason = condition.UpdateRunSucceededReason
		} else {
			reason = condition.UpdateRunFailedReason
		}
	case placementv1beta1.StageUpdatingConditionProgressing:
		if succeeded {
			reason = condition.StageUpdatingSucceededReason
		} else {
			reason = condition.StageUpdatingFailedReason
		}
	}
	falseCond.Reason = reason
	return falseCond
}
