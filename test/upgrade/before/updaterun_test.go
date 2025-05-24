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

package before

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

// The names used in this test must match with the corresponding test in the after stage
const (
	updateRunBackwardCompatCRPName       = "crp-updaterun-backward-compat"
	updateRunBackwardCompatStrategyName  = "strategy-updaterun-backward-compat"
	updateRunBackwardCompatUpdateRunName = "updaterun-backward-compat"
	updateRunBackwardCompatNamespace     = "ns-updaterun-backward-compat"
	updateRunBackwardCompatConfigMap     = "cm-updaterun-backward-compat"
)

var _ = Describe("ClusterStagedUpdateRun backward compatibility (before upgrade)", Ordered, func() {
	var strategy *placementv1beta1.ClusterStagedUpdateStrategy

	BeforeAll(func() {
		// Create a namespace and a ConfigMap for testing
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: updateRunBackwardCompatNamespace,
			},
		}
		Expect(hubClient.Create(ctx, ns)).To(Succeed(), "Failed to create namespace %s", updateRunBackwardCompatNamespace)

		// Create a ConfigMap
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      updateRunBackwardCompatConfigMap,
				Namespace: updateRunBackwardCompatNamespace,
			},
			Data: map[string]string{
				"key": "value-before-upgrade",
			},
		}
		Expect(hubClient.Create(ctx, cm)).To(Succeed(), "Failed to create ConfigMap %s", updateRunBackwardCompatConfigMap)

		// Create the CRP with external rollout strategy
		crp := &placementv1beta1.ClusterResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name: updateRunBackwardCompatCRPName,
			},
			Spec: placementv1beta1.ClusterResourcePlacementSpec{
				ResourceSelectors: []placementv1beta1.ClusterResourceSelector{
					{
						Group:   "",
						Version: "v1",
						Kind:    "Namespace",
						Name:    updateRunBackwardCompatNamespace,
					},
					{
						Group:     "",
						Version:   "v1",
						Kind:      "ConfigMap",
						Namespace: updateRunBackwardCompatNamespace,
						Name:      updateRunBackwardCompatConfigMap,
					},
				},
				Strategy: placementv1beta1.RolloutStrategy{
					Type: placementv1beta1.ExternalRolloutStrategyType,
				},
			},
		}
		Expect(hubClient.Create(ctx, crp)).To(Succeed(), "Failed to create CRP %s", updateRunBackwardCompatCRPName)

		// Create the ClusterStagedUpdateStrategy
		strategy = &placementv1beta1.ClusterStagedUpdateStrategy{
			ObjectMeta: metav1.ObjectMeta{
				Name: updateRunBackwardCompatStrategyName,
			},
			Spec: placementv1beta1.StagedUpdateStrategySpec{
				Stages: []placementv1beta1.StageConfig{
					{
						Name: "canary",
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"environment": "canary", // member-cluster-2
							},
						},
						AfterStageTasks: []placementv1beta1.AfterStageTask{
							{
								Type: placementv1beta1.AfterStageTaskTypeApproval,
							},
							{
								Type: placementv1beta1.AfterStageTaskTypeTimedWait,
								WaitTime: metav1.Duration{
									Duration: time.Second * 5,
								},
							},
						},
					},
					{
						Name: "prod",
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"environment": "prod", // member-cluster-1 and member-cluster-3
							},
						},
					},
				},
			},
		}
		Expect(hubClient.Create(ctx, strategy)).To(Succeed(), "Failed to create ClusterStagedUpdateStrategy %s", updateRunBackwardCompatStrategyName)
	})

	It("should create a staged update run successfully", func() {
		updateRun := &placementv1beta1.ClusterStagedUpdateRun{
			ObjectMeta: metav1.ObjectMeta{
				Name: updateRunBackwardCompatUpdateRunName,
			},
			Spec: placementv1beta1.StagedUpdateRunSpec{
				PlacementName:            updateRunBackwardCompatCRPName,
				ResourceSnapshotIndex:    "0", // Use the first resource snapshot
				StagedUpdateStrategyName: updateRunBackwardCompatStrategyName,
			},
		}
		Expect(hubClient.Create(ctx, updateRun)).To(Succeed(), "Failed to create ClusterStagedUpdateRun %s", updateRunBackwardCompatUpdateRunName)
	})

	It("should wait for resources to be scheduled", func() {
		// Wait for the CRP to be processed
		By("Waiting for the latest resource snapshot to be created")
		Eventually(func() error {
			crsList := &placementv1beta1.ClusterResourceSnapshotList{}
			if err := hubClient.List(ctx, crsList, client.MatchingLabels{
				placementv1beta1.CRPTrackingLabel:      updateRunBackwardCompatCRPName,
				placementv1beta1.IsLatestSnapshotLabel: "true",
			}); err != nil {
				return err
			}
			if len(crsList.Items) != 1 {
				return fmt.Errorf("expected 1 resource snapshot, got %d", len(crsList.Items))
			}
			return nil
		}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to find the resource snapshot")

		By("Waiting for the latest policy snapshot to be created")
		Eventually(func() error {
			cpsList := &placementv1beta1.ClusterSchedulingPolicySnapshotList{}
			if err := hubClient.List(ctx, cpsList, client.MatchingLabels{
				placementv1beta1.CRPTrackingLabel:      updateRunBackwardCompatCRPName,
				placementv1beta1.IsLatestSnapshotLabel: "true",
			}); err != nil {
				return err
			}
			if len(cpsList.Items) != 1 {
				return fmt.Errorf("expected 1 policy snapshot, got %d", len(cpsList.Items))
			}
			return nil
		}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to find the policy snapshot")
	})

	It("should have the ClusterStagedUpdateRun initialized with the strategy", func() {
		Eventually(func() error {
			updateRun := &placementv1beta1.ClusterStagedUpdateRun{}
			if err := hubClient.Get(ctx, types.NamespacedName{Name: updateRunBackwardCompatUpdateRunName}, updateRun); err != nil {
				return err
			}

			// Check that the UpdateRun is initialized with the staged update strategy
			if updateRun.Status.StagedUpdateStrategySnapshot == nil {
				return fmt.Errorf("updateRun status doesn't have StagedUpdateStrategySnapshot set")
			}

			// Check the stages are correctly set up
			if len(updateRun.Status.StagesStatus) != 2 {
				return fmt.Errorf("expected 2 stages in the UpdateRun, got %d", len(updateRun.Status.StagesStatus))
			}

			// Check the policy snapshot index is set
			if updateRun.Status.PolicySnapshotIndexUsed == "" {
				return fmt.Errorf("updateRun status doesn't have PolicySnapshotIndexUsed set")
			}

			// Check progressing condition is set
			var progressingCondFound bool
			for _, cond := range updateRun.Status.Conditions {
				if cond.Type == string(placementv1beta1.StagedUpdateRunConditionProgressing) {
					progressingCondFound = true
					break
				}
			}
			if !progressingCondFound {
				return fmt.Errorf("updateRun doesn't have progressing condition set")
			}

			return nil
		}, eventuallyDuration*3, eventuallyInterval).Should(Succeed(), "Failed to validate the UpdateRun has been initialized")
	})

	It("should validate the first stage is progressing", func() {
		Eventually(func() error {
			updateRun := &placementv1beta1.ClusterStagedUpdateRun{}
			if err := hubClient.Get(ctx, types.NamespacedName{Name: updateRunBackwardCompatUpdateRunName}, updateRun); err != nil {
				return err
			}

			// Check that the first stage has the progressing condition
			if len(updateRun.Status.StagesStatus) == 0 {
				return fmt.Errorf("updateRun status doesn't have any stages")
			}

			firstStage := updateRun.Status.StagesStatus[0]
			var progressingCondFound bool
			for _, cond := range firstStage.Conditions {
				if cond.Type == string(placementv1beta1.StageUpdatingConditionProgressing) && cond.Status == metav1.ConditionTrue {
					progressingCondFound = true
					break
				}
			}
			if !progressingCondFound {
				return fmt.Errorf("first stage doesn't have progressing condition set to true")
			}

			return nil
		}, eventuallyDuration*3, eventuallyInterval).Should(Succeed(), "Failed to validate the first stage is progressing")
	})
})