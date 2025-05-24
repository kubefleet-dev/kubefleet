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

package after

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

// The names used in this test must match with those in the corresponding test in the before stage
const (
	updateRunBackwardCompatCRPName       = "crp-updaterun-backward-compat"
	updateRunBackwardCompatStrategyName  = "strategy-updaterun-backward-compat"
	updateRunBackwardCompatUpdateRunName = "updaterun-backward-compat"
	updateRunBackwardCompatNamespace     = "ns-updaterun-backward-compat"
	updateRunBackwardCompatConfigMap     = "cm-updaterun-backward-compat"
)

var _ = Describe("ClusterStagedUpdateRun backward compatibility (after upgrade)", Ordered, func() {
	It("should maintain the UpdateRun status after upgrade", func() {
		updateRun := &placementv1beta1.ClusterStagedUpdateRun{}
		Expect(hubClient.Get(ctx, types.NamespacedName{Name: updateRunBackwardCompatUpdateRunName}, updateRun)).To(Succeed(), "Failed to get ClusterStagedUpdateRun %s after upgrade", updateRunBackwardCompatUpdateRunName)

		// Verify the UpdateRun still has the StagedUpdateStrategySnapshot
		Expect(updateRun.Status.StagedUpdateStrategySnapshot).ToNot(BeNil(), "StagedUpdateStrategySnapshot should not be nil after upgrade")
		
		// Verify stages are preserved
		Expect(updateRun.Status.StagesStatus).To(HaveLen(2), "Should have 2 stages after upgrade")
		
		// Verify the policy snapshot index is preserved
		Expect(updateRun.Status.PolicySnapshotIndexUsed).ToNot(BeEmpty(), "PolicySnapshotIndexUsed should not be empty after upgrade")
		
		// Verify the UpdateRun is still in a valid state (either progressing or completed)
		var validConditionFound bool
		for _, cond := range updateRun.Status.Conditions {
			if (cond.Type == string(placementv1beta1.StagedUpdateRunConditionProgressing) && cond.Status == metav1.ConditionTrue) ||
			   (cond.Type == string(placementv1beta1.StagedUpdateRunConditionSucceeded) && cond.Status == metav1.ConditionTrue) {
				validConditionFound = true
				break
			}
		}
		Expect(validConditionFound).To(BeTrue(), "UpdateRun should have either progressing=true or succeeded=true condition after upgrade")
	})

	It("should continue working with the ClusterStagedUpdateRun after upgrade", func() {
		// Create an approval request to continue the update process if needed
		Eventually(func() error {
			// Check if there's an approval request for this update run
			appReqList := &placementv1beta1.ClusterApprovalRequestList{}
			if err := hubClient.List(ctx, appReqList, client.MatchingLabels{
				placementv1beta1.TargetUpdateRunLabel: updateRunBackwardCompatUpdateRunName,
			}); err != nil {
				return err
			}

			// If there's an approval request, approve it
			for i := range appReqList.Items {
				appReq := &appReqList.Items[i]
				// Check if it's already approved
				alreadyApproved := false
				for _, cond := range appReq.Status.Conditions {
					if cond.Type == string(placementv1beta1.ApprovalRequestConditionApproved) && cond.Status == metav1.ConditionTrue {
						alreadyApproved = true
						break
					}
				}
				
				if !alreadyApproved {
					// Approve the request
					meta.SetStatusCondition(&appReq.Status.Conditions, metav1.Condition{
						Status:             metav1.ConditionTrue,
						Type:               string(placementv1beta1.ApprovalRequestConditionApproved),
						ObservedGeneration: appReq.GetGeneration(),
						Reason:             "ApprovedAfterUpgrade",
						Message:            "Approval given after upgrade to continue testing backward compatibility",
					})
					if err := hubClient.Status().Update(ctx, appReq); err != nil {
						return fmt.Errorf("failed to approve request: %w", err)
					}
				}
			}
			return nil
		}, longEventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to find or approve the approval request after upgrade")

		// Update the ConfigMap to trigger changes
		Eventually(func() error {
			cm := &corev1.ConfigMap{}
			if err := hubClient.Get(ctx, types.NamespacedName{Namespace: updateRunBackwardCompatNamespace, Name: updateRunBackwardCompatConfigMap}, cm); err != nil {
				return err
			}
			cm.Data["key"] = "value-after-upgrade"
			return hubClient.Update(ctx, cm)
		}, longEventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update ConfigMap after upgrade")

		// Wait for and verify the UpdateRun completes successfully
		Eventually(func() error {
			updateRun := &placementv1beta1.ClusterStagedUpdateRun{}
			if err := hubClient.Get(ctx, types.NamespacedName{Name: updateRunBackwardCompatUpdateRunName}, updateRun); err != nil {
				return err
			}

			// Check if the UpdateRun has succeeded
			var succeededCondFound bool
			for _, cond := range updateRun.Status.Conditions {
				if cond.Type == string(placementv1beta1.StagedUpdateRunConditionSucceeded) && cond.Status == metav1.ConditionTrue {
					succeededCondFound = true
					break
				}
			}

			if !succeededCondFound {
				// If it's still running, that's fine, but we need to know why
				var progressingCondFound bool
				var progressingReason string
				for _, cond := range updateRun.Status.Conditions {
					if cond.Type == string(placementv1beta1.StagedUpdateRunConditionProgressing) {
						progressingCondFound = true
						progressingReason = cond.Reason
						break
					}
				}
				
				if progressingCondFound {
					return fmt.Errorf("updateRun still progressing with reason: %s", progressingReason)
				}
				
				return fmt.Errorf("updateRun neither succeeded nor progressing")
			}

			return nil
		}, consistentlyDuration*3, consistentlyInterval).Should(Succeed(), "Failed to validate the UpdateRun completes successfully after upgrade")
	})
})
