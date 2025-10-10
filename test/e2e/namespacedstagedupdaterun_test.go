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

package e2e

import (
	"fmt"
	"time"

	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	scheduler "github.com/kubefleet-dev/kubefleet/pkg/scheduler/framework"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/condition"
	"github.com/kubefleet-dev/kubefleet/test/e2e/framework"
)

const (
	// The current stage wait between clusters are 15 seconds for namespaced updaterun
	namespacedUpdateRunEventuallyDuration = time.Minute

	// Template names for namespaced staged update run resources
	namespacedUpdateRunStrategyNameTemplate     = "sus-%d"     // StagedUpdateStrategy
	namespacedUpdateRunNameWithSubIndexTemplate = "sur-%d-%d"  // StagedUpdateRun
	namespacedApprovalRequestNameTemplate       = "areq-%d-%d" // ApprovalRequest
)

// Note that this container will run in parallel with other containers.
var _ = Describe("test RP rollout with namespaced staged update run", func() {
	crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())
	rpName := fmt.Sprintf(rpNameTemplate, GinkgoParallelProcess())
	strategyName := fmt.Sprintf(namespacedUpdateRunStrategyNameTemplate, GinkgoParallelProcess())
	testNamespace := fmt.Sprintf(workNamespaceNameTemplate, GinkgoParallelProcess())

	Context("Test resource rollout and rollback with namespaced staged update run", Ordered, func() {
		updateRunNames := []string{}
		var strategy *placementv1beta1.StagedUpdateStrategy
		var oldConfigMap, newConfigMap corev1.ConfigMap

		BeforeAll(func() {
			// Create a test namespace and a configMap inside it on the hub cluster.
			createWorkResources()

			// Create the CRP with Namespace-only selector.
			createNamespaceOnlyCRP(crpName)

			By("should update CRP status as expected")
			crpStatusUpdatedActual := crpStatusUpdatedActual(workNamespaceIdentifiers(), allMemberClusterNames, nil, "0")
			Eventually(crpStatusUpdatedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update CRP status as expected")

			// Create the RP with external rollout strategy.
			rp := &placementv1beta1.ResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name:      rpName,
					Namespace: testNamespace,
					// Add a custom finalizer; this would allow us to better observe
					// the behavior of the controllers.
					Finalizers: []string{customDeletionBlockerFinalizer},
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: configMapSelector(),
					Strategy: placementv1beta1.RolloutStrategy{
						Type: placementv1beta1.ExternalRolloutStrategyType,
					},
				},
			}
			Expect(hubClient.Create(ctx, rp)).To(Succeed(), "Failed to create RP")

			// Create the stagedUpdateStrategy.
			strategy = createNamespacedStagedUpdateStrategySucceed(strategyName, testNamespace)

			for i := 0; i < 3; i++ {
				updateRunNames = append(updateRunNames, fmt.Sprintf(namespacedUpdateRunNameWithSubIndexTemplate, GinkgoParallelProcess(), i))
			}

			oldConfigMap = appConfigMap()
			newConfigMap = appConfigMap()
			newConfigMap.Data["data"] = testConfigMapDataValue
		})

		AfterAll(func() {
			// Remove the custom deletion blocker finalizer from the RP.
			ensureRPAndRelatedResourcesDeleted(types.NamespacedName{Name: rpName, Namespace: testNamespace}, allMemberClusters)

			// Remove all the stagedUpdateRuns.
			for _, name := range updateRunNames {
				ensureNamespacedUpdateRunDeletion(name, testNamespace)
			}

			// Delete the stagedUpdateStrategy.
			ensureNamespacedUpdateRunStrategyDeletion(strategyName, testNamespace)
			// Delete the namespace only CRP.
			ensureCRPAndRelatedResourcesDeleted(crpName, allMemberClusters)
		})

		It("Should not rollout any resources to member clusters as there's no update run yet", checkIfRemovedConfigMapFromAllMemberClustersConsistently)

		It("Should have the latest resource snapshot", func() {
			validateLatestNamespacedResourceSnapshot(rpName, testNamespace, resourceSnapshotIndex1st)
		})

		It("Should successfully schedule the rp", func() {
			validateLatestNamespacedPolicySnapshot(rpName, testNamespace, policySnapshotIndex1st, 3)
		})

		It("Should update rp status as pending rollout", func() {
			rpStatusUpdatedActual := rpStatusWithExternalStrategyActual(nil, "", false, allMemberClusterNames, []string{"", "", ""}, []bool{false, false, false}, nil, nil, rpName, testNamespace)
			Eventually(rpStatusUpdatedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update RP %s status as expected", rpName)
		})

		It("Should create a namespaced staged update run successfully", func() {
			createNamespacedStagedUpdateRunSucceed(updateRunNames[0], testNamespace, rpName, resourceSnapshotIndex1st, strategyName)
		})

		It("Should rollout resources to member-cluster-2 only and complete stage canary", func() {
			checkIfPlacedConfigMapOnMemberClustersInUpdateRun([]*framework.Cluster{allMemberClusters[1]})
			checkIfRemovedConfigMapFromMemberClustersConsistently([]*framework.Cluster{allMemberClusters[0], allMemberClusters[2]})

			By("Validating rp status as member-cluster-2 updated")
			rpStatusUpdatedActual := rpStatusWithExternalStrategyActual(nil, "", false, allMemberClusterNames, []string{"", resourceSnapshotIndex1st, ""}, []bool{false, true, false}, nil, nil, rpName, testNamespace)
			Eventually(rpStatusUpdatedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update RP %s status as expected", rpName)

			validateAndApproveNamespacedApprovalRequests(updateRunNames[0], testNamespace, envCanary)
		})

		It("Should rollout resources to member-cluster-1 first because of its name", func() {
			checkIfPlacedConfigMapOnMemberClustersInUpdateRun([]*framework.Cluster{allMemberClusters[0]})
		})

		It("Should rollout resources to all the members and complete the staged update run successfully", func() {
			namespacedUpdateRunSucceededActual := namespacedUpdateRunStatusSucceededActual(updateRunNames[0], testNamespace, policySnapshotIndex1st, len(allMemberClusters), defaultApplyStrategy, &strategy.Spec, [][]string{{allMemberClusterNames[1]}, {allMemberClusterNames[0], allMemberClusterNames[2]}}, nil, nil, nil)
			Eventually(namespacedUpdateRunSucceededActual, namespacedUpdateRunEventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to validate updateRun %s succeeded", updateRunNames[0])
			checkIfPlacedConfigMapOnMemberClustersInUpdateRun(allMemberClusters)
		})

		It("Should update rp status as completed", func() {
			rpStatusUpdatedActual := rpStatusWithExternalStrategyActual(appConfigMapIdentifiers(), resourceSnapshotIndex1st, true, allMemberClusterNames,
				[]string{resourceSnapshotIndex1st, resourceSnapshotIndex1st, resourceSnapshotIndex1st}, []bool{true, true, true}, nil, nil, rpName, testNamespace)
			Eventually(rpStatusUpdatedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update RP %s status as expected", rpName)
		})

		It("Should update the configmap successfully on hub but not change member clusters", func() {
			updateConfigMapSucceed(&newConfigMap)

			for _, cluster := range allMemberClusters {
				configMapActual := configMapPlacedOnClusterActual(cluster, &oldConfigMap)
				Consistently(configMapActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "Failed to keep configmap %s data as expected", oldConfigMap.Name)
			}
		})

		It("Should not update rp status, should still be completed", func() {
			rpStatusUpdatedActual := rpStatusWithExternalStrategyActual(appConfigMapIdentifiers(), resourceSnapshotIndex1st, true, allMemberClusterNames,
				[]string{resourceSnapshotIndex1st, resourceSnapshotIndex1st, resourceSnapshotIndex1st}, []bool{true, true, true}, nil, nil, rpName, testNamespace)
			Consistently(rpStatusUpdatedActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "Failed to keep RP %s status as expected", rpName)
		})

		It("Should create a new latest resource snapshot", func() {
			rsList := &placementv1beta1.ResourceSnapshotList{}
			Eventually(func() error {
				if err := hubClient.List(ctx, rsList, client.InNamespace(testNamespace), client.MatchingLabels{placementv1beta1.PlacementTrackingLabel: rpName, placementv1beta1.IsLatestSnapshotLabel: "true"}); err != nil {
					return fmt.Errorf("failed to list the resourcesnapshot: %w", err)
				}
				if len(rsList.Items) != 1 {
					return fmt.Errorf("got %d latest resourcesnapshots, want 1", len(rsList.Items))
				}
				if rsList.Items[0].Labels[placementv1beta1.ResourceIndexLabel] != resourceSnapshotIndex2nd {
					return fmt.Errorf("got resource snapshot index %s, want %s", rsList.Items[0].Labels[placementv1beta1.ResourceIndexLabel], resourceSnapshotIndex2nd)
				}
				return nil
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed get the new latest resourcensnapshot")
		})

		It("Should create a new namespaced staged update run successfully", func() {
			createNamespacedStagedUpdateRunSucceed(updateRunNames[1], testNamespace, rpName, resourceSnapshotIndex2nd, strategyName)
		})

		It("Should rollout resources to member-cluster-2 only and complete stage canary", func() {
			By("Verify that the new configmap is updated on member-cluster-2")
			configMapActual := configMapPlacedOnClusterActual(allMemberClusters[1], &newConfigMap)
			Eventually(configMapActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update to the new configmap %s on cluster %s", newConfigMap.Name, allMemberClusterNames[1])
			By("Verify that the configmap is not updated on member-cluster-1 and member-cluster-3")
			for _, cluster := range []*framework.Cluster{allMemberClusters[0], allMemberClusters[2]} {
				configMapActual := configMapPlacedOnClusterActual(cluster, &oldConfigMap)
				Consistently(configMapActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "Failed to keep configmap %s data as expected", newConfigMap.Name)
			}

			By("Validating rp status as member-cluster-2 updated")
			rpStatusUpdatedActual := rpStatusWithExternalStrategyActual(nil, "", false, allMemberClusterNames,
				[]string{resourceSnapshotIndex1st, resourceSnapshotIndex2nd, resourceSnapshotIndex1st}, []bool{true, true, true}, nil, nil, rpName, testNamespace)
			Eventually(rpStatusUpdatedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update RP %s status as expected", rpName)

			validateAndApproveNamespacedApprovalRequests(updateRunNames[1], testNamespace, envCanary)
		})

		It("Should rollout resources to member-cluster-1 and member-cluster-3 too and complete the staged update run successfully", func() {
			namespacedUpdateRunSucceededActual := namespacedUpdateRunStatusSucceededActual(updateRunNames[1], testNamespace, policySnapshotIndex1st, len(allMemberClusters), defaultApplyStrategy, &strategy.Spec, [][]string{{allMemberClusterNames[1]}, {allMemberClusterNames[0], allMemberClusterNames[2]}}, nil, nil, nil)
			Eventually(namespacedUpdateRunSucceededActual, namespacedUpdateRunEventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to validate updateRun %s succeeded", updateRunNames[1])
			By("Verify that new the configmap is updated on all member clusters")
			for idx := range allMemberClusters {
				configMapActual := configMapPlacedOnClusterActual(allMemberClusters[idx], &newConfigMap)
				Eventually(configMapActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update to the new configmap %s on cluster %s as expected", newConfigMap.Name, allMemberClusterNames[idx])
			}
		})

		It("Should update rp status as completed", func() {
			rpStatusUpdatedActual := rpStatusWithExternalStrategyActual(appConfigMapIdentifiers(), resourceSnapshotIndex2nd, true, allMemberClusterNames,
				[]string{resourceSnapshotIndex2nd, resourceSnapshotIndex2nd, resourceSnapshotIndex2nd}, []bool{true, true, true}, nil, nil, rpName, testNamespace)
			Eventually(rpStatusUpdatedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update RP %s status as expected", rpName)
		})

		It("Should create a new namespaced staged update run with old resourceSnapshotIndex successfully to rollback", func() {
			createNamespacedStagedUpdateRunSucceed(updateRunNames[2], testNamespace, rpName, resourceSnapshotIndex1st, strategyName)
		})

		It("Should rollback resources to member-cluster-2 only and completes stage canary", func() {
			By("Verify that the configmap is rolled back on member-cluster-2")
			configMapActual := configMapPlacedOnClusterActual(allMemberClusters[1], &oldConfigMap)
			Eventually(configMapActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to rollback the configmap change on cluster %s", allMemberClusterNames[1])
			By("Verify that the configmap is not rolled back on member-cluster-1 and member-cluster-3")
			for _, cluster := range []*framework.Cluster{allMemberClusters[0], allMemberClusters[2]} {
				configMapActual := configMapPlacedOnClusterActual(cluster, &newConfigMap)
				Consistently(configMapActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "Failed to keep configmap %s data as expected", newConfigMap.Name)
			}

			By("Validating rp status as member-cluster-2 updated")
			rpStatusUpdatedActual := rpStatusWithExternalStrategyActual(nil, "", false, allMemberClusterNames,
				[]string{resourceSnapshotIndex2nd, resourceSnapshotIndex1st, resourceSnapshotIndex2nd}, []bool{true, true, true}, nil, nil, rpName, testNamespace)
			Eventually(rpStatusUpdatedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update RP %s status as expected", rpName)

			validateAndApproveNamespacedApprovalRequests(updateRunNames[2], testNamespace, envCanary)
		})

		It("Should rollback resources to member-cluster-1 and member-cluster-3 too and complete the staged update run successfully", func() {
			namespacedUpdateRunSucceededActual := namespacedUpdateRunStatusSucceededActual(updateRunNames[2], testNamespace, policySnapshotIndex1st, len(allMemberClusters), defaultApplyStrategy, &strategy.Spec, [][]string{{allMemberClusterNames[1]}, {allMemberClusterNames[0], allMemberClusterNames[2]}}, nil, nil, nil)
			Eventually(namespacedUpdateRunSucceededActual, namespacedUpdateRunEventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to validate updateRun %s succeeded", updateRunNames[1])
			for idx := range allMemberClusters {
				configMapActual := configMapPlacedOnClusterActual(allMemberClusters[idx], &oldConfigMap)
				Eventually(configMapActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to rollback the configmap %s data on cluster %s as expected", oldConfigMap.Name, allMemberClusterNames[idx])
			}
		})

		It("Should update rp status as completed", func() {
			rpStatusUpdatedActual := rpStatusWithExternalStrategyActual(appConfigMapIdentifiers(), resourceSnapshotIndex1st, true, allMemberClusterNames,
				[]string{resourceSnapshotIndex1st, resourceSnapshotIndex1st, resourceSnapshotIndex1st}, []bool{true, true, true}, nil, nil, rpName, testNamespace)
			Eventually(rpStatusUpdatedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update RP %s status as expected", rpName)
		})
	})
})

// Utility functions for namespaced staged update run

func createNamespacedStagedUpdateStrategySucceed(strategyName, namespace string) *placementv1beta1.StagedUpdateStrategy {
	strategy := &placementv1beta1.StagedUpdateStrategy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      strategyName,
			Namespace: namespace,
		},
		Spec: placementv1beta1.UpdateStrategySpec{
			Stages: []placementv1beta1.StageConfig{
				{
					Name: envCanary,
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							envLabelName: envCanary, // member-cluster-2
						},
					},
					AfterStageTasks: []placementv1beta1.AfterStageTask{
						{
							Type: placementv1beta1.AfterStageTaskTypeApproval,
						},
						{
							Type: placementv1beta1.AfterStageTaskTypeTimedWait,
							WaitTime: &metav1.Duration{
								Duration: time.Second * 5,
							},
						},
					},
				},
				{
					Name: envProd,
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							envLabelName: envProd, // member-cluster-1 and member-cluster-3
						},
					},
				},
			},
		},
	}
	Expect(hubClient.Create(ctx, strategy)).To(Succeed(), "Failed to create StagedUpdateStrategy")
	return strategy
}

func validateLatestNamespacedPolicySnapshot(rpName, namespace, wantPolicySnapshotIndex string, wantSelectedClusterCount int) {
	Eventually(func() (string, error) {
		var policySnapshotList placementv1beta1.SchedulingPolicySnapshotList
		if err := hubClient.List(ctx, &policySnapshotList, client.InNamespace(namespace), client.MatchingLabels{
			placementv1beta1.PlacementTrackingLabel: rpName,
			placementv1beta1.IsLatestSnapshotLabel:  "true",
		}); err != nil {
			return "", fmt.Errorf("failed to list the latest scheduling policy snapshot: %w", err)
		}
		if len(policySnapshotList.Items) != 1 {
			return "", fmt.Errorf("failed to find the latest scheduling policy snapshot")
		}
		latestPolicySnapshot := policySnapshotList.Items[0]
		if !condition.IsConditionStatusTrue(latestPolicySnapshot.GetCondition(string(placementv1beta1.PolicySnapshotScheduled)), latestPolicySnapshot.Generation) {
			return "", fmt.Errorf("the latest scheduling policy snapshot is not scheduled yet")
		}

		selectedClusterCount := 0
		for _, decision := range latestPolicySnapshot.Status.ClusterDecisions {
			if decision.Selected {
				selectedClusterCount++
			}
		}
		if selectedClusterCount != wantSelectedClusterCount {
			return "", fmt.Errorf("want %d selected clusters, got %d", wantSelectedClusterCount, selectedClusterCount)
		}
		return latestPolicySnapshot.Labels[placementv1beta1.PolicyIndexLabel], nil
	}, eventuallyDuration, eventuallyInterval).Should(Equal(wantPolicySnapshotIndex), "Policy snapshot index does not match")
}

func validateLatestNamespacedResourceSnapshot(rpName, namespace, wantResourceSnapshotIndex string) {
	Eventually(func() (string, error) {
		rsList := &placementv1beta1.ResourceSnapshotList{}
		if err := hubClient.List(ctx, rsList, client.InNamespace(namespace), client.MatchingLabels{
			placementv1beta1.PlacementTrackingLabel: rpName,
			placementv1beta1.IsLatestSnapshotLabel:  "true",
		}); err != nil {
			return "", fmt.Errorf("failed to list the latestresourcesnapshot: %w", err)
		}
		if len(rsList.Items) != 1 {
			return "", fmt.Errorf("got %d resourcesnapshots, want 1", len(rsList.Items))
		}
		return rsList.Items[0].Labels[placementv1beta1.ResourceIndexLabel], nil
	}, eventuallyDuration, eventuallyInterval).Should(Equal(wantResourceSnapshotIndex), "Resource snapshot index does not match")
}

func createNamespacedStagedUpdateRunSucceed(updateRunName, namespace, rpName, resourceSnapshotIndex, strategyName string) {
	updateRun := &placementv1beta1.StagedUpdateRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      updateRunName,
			Namespace: namespace,
		},
		Spec: placementv1beta1.UpdateRunSpec{
			PlacementName:            rpName,
			ResourceSnapshotIndex:    resourceSnapshotIndex,
			StagedUpdateStrategyName: strategyName,
		},
	}
	Expect(hubClient.Create(ctx, updateRun)).To(Succeed(), "Failed to create StagedUpdateRun %s", updateRunName)
}

func validateAndApproveNamespacedApprovalRequests(updateRunName, namespace, stageName string) {
	Eventually(func() error {
		appReqList := &placementv1beta1.ApprovalRequestList{}
		if err := hubClient.List(ctx, appReqList, client.InNamespace(namespace), client.MatchingLabels{
			placementv1beta1.TargetUpdatingStageNameLabel: stageName,
			placementv1beta1.TargetUpdateRunLabel:         updateRunName,
		}); err != nil {
			return fmt.Errorf("failed to list approval requests: %w", err)
		}

		if len(appReqList.Items) != 1 {
			return fmt.Errorf("got %d approval requests, want 1", len(appReqList.Items))
		}
		appReq := &appReqList.Items[0]
		meta.SetStatusCondition(&appReq.Status.Conditions, metav1.Condition{
			Status:             metav1.ConditionTrue,
			Type:               string(placementv1beta1.ApprovalRequestConditionApproved),
			ObservedGeneration: appReq.GetGeneration(),
			Reason:             "lgtm",
		})
		return hubClient.Status().Update(ctx, appReq)
	}, namespacedUpdateRunEventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to get or approve approval request")
}

func ensureNamespacedUpdateRunDeletion(updateRunName, namespace string) {
	Eventually(func() error {
		updateRun := &placementv1beta1.StagedUpdateRun{}
		if err := hubClient.Get(ctx, client.ObjectKey{Name: updateRunName, Namespace: namespace}, updateRun); err != nil {
			return client.IgnoreNotFound(err)
		}
		return hubClient.Delete(ctx, updateRun)
	}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to delete StagedUpdateRun %s", updateRunName)

	// Wait for the staged update run to be deleted.
	Eventually(func() bool {
		updateRun := &placementv1beta1.StagedUpdateRun{}
		return hubClient.Get(ctx, client.ObjectKey{Name: updateRunName, Namespace: namespace}, updateRun) != nil
	}, eventuallyDuration, eventuallyInterval).Should(BeTrue(), "Failed to delete StagedUpdateRun %s", updateRunName)
}

func ensureNamespacedUpdateRunStrategyDeletion(strategyName, namespace string) {
	Eventually(func() error {
		strategy := &placementv1beta1.StagedUpdateStrategy{}
		if err := hubClient.Get(ctx, client.ObjectKey{Name: strategyName, Namespace: namespace}, strategy); err != nil {
			return client.IgnoreNotFound(err)
		}
		return hubClient.Delete(ctx, strategy)
	}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to delete StagedUpdateStrategy %s", strategyName)

	// Wait for the staged update strategy to be deleted.
	Eventually(func() bool {
		strategy := &placementv1beta1.StagedUpdateStrategy{}
		return hubClient.Get(ctx, client.ObjectKey{Name: strategyName, Namespace: namespace}, strategy) != nil
	}, eventuallyDuration, eventuallyInterval).Should(BeTrue(), "Failed to delete StagedUpdateStrategy %s", strategyName)
}

func rpStatusWithExternalStrategyActual(
	wantSelectedResourceIdentifiers []placementv1beta1.ResourceIdentifier,
	wantObservedResourceIndex string,
	wantRPRolloutCompleted bool,
	wantSelectedClusters []string,
	wantObservedResourceIndexPerCluster []string,
	wantRolloutCompletedPerCluster []bool,
	wantClusterResourceOverrides map[string][]string,
	wantResourceOverrides map[string][]placementv1beta1.NamespacedName,
	rpName, namespace string,
) func() error {
	nsName := fmt.Sprintf(workNamespaceNameTemplate, GinkgoParallelProcess())
	cmName := fmt.Sprintf(appConfigMapNameTemplate, GinkgoParallelProcess())

	return func() error {
		rp := &placementv1beta1.ResourcePlacement{}
		if err := hubClient.Get(ctx, client.ObjectKey{Name: rpName, Namespace: namespace}, rp); err != nil {
			return err
		}

		reportDiff := rp.Spec.Strategy.ApplyStrategy != nil && rp.Spec.Strategy.ApplyStrategy.Type == placementv1beta1.ApplyStrategyTypeReportDiff

		var wantPlacementStatus []placementv1beta1.PerClusterPlacementStatus
		rpHasOverrides := false
		for i, name := range wantSelectedClusters {
			if !wantRolloutCompletedPerCluster[i] {
				// No observed resource index for this cluster, assume rollout is still pending.
				wantPlacementStatus = append(wantPlacementStatus, placementv1beta1.PerClusterPlacementStatus{
					ClusterName:           name,
					Conditions:            perClusterRolloutUnknownConditions(rp.Generation),
					ObservedResourceIndex: wantObservedResourceIndexPerCluster[i],
				})
			} else {
				wantResourceOverrides, hasRO := wantResourceOverrides[name]
				wantClusterResourceOverrides, hasCRO := wantClusterResourceOverrides[name]
				hasOverrides := (hasRO && len(wantResourceOverrides) > 0) || (hasCRO && len(wantClusterResourceOverrides) > 0)
				if hasOverrides {
					rpHasOverrides = true
				}
				if reportDiff {
					wantPlacementStatus = append(wantPlacementStatus, placementv1beta1.PerClusterPlacementStatus{
						ClusterName:                        name,
						Conditions:                         perClusterDiffReportedConditions(rp.Generation),
						ApplicableResourceOverrides:        wantResourceOverrides,
						ApplicableClusterResourceOverrides: wantClusterResourceOverrides,
						ObservedResourceIndex:              wantObservedResourceIndexPerCluster[i],
						DiffedPlacements: []placementv1beta1.DiffedResourcePlacement{
							{
								ResourceIdentifier: placementv1beta1.ResourceIdentifier{
									Version: "v1",
									Kind:    "Namespace",
									Name:    nsName,
								},
								ObservedDiffs: []placementv1beta1.PatchDetail{
									{
										Path:       "/",
										ValueInHub: "(the whole object)",
									},
								},
							},
							{
								ResourceIdentifier: placementv1beta1.ResourceIdentifier{
									Version:   "v1",
									Kind:      "ConfigMap",
									Name:      cmName,
									Namespace: nsName,
								},
								ObservedDiffs: []placementv1beta1.PatchDetail{
									{
										Path:       "/",
										ValueInHub: "(the whole object)",
									},
								},
							},
						},
					})
				} else {
					wantPlacementStatus = append(wantPlacementStatus, placementv1beta1.PerClusterPlacementStatus{
						ClusterName:                        name,
						Conditions:                         perClusterRolloutCompletedConditions(rp.Generation, true, hasOverrides),
						ApplicableResourceOverrides:        wantResourceOverrides,
						ApplicableClusterResourceOverrides: wantClusterResourceOverrides,
						ObservedResourceIndex:              wantObservedResourceIndexPerCluster[i],
					})
				}
			}
		}

		wantStatus := placementv1beta1.PlacementStatus{
			PerClusterPlacementStatuses: wantPlacementStatus,
			SelectedResources:           wantSelectedResourceIdentifiers,
			ObservedResourceIndex:       wantObservedResourceIndex,
		}
		if wantRPRolloutCompleted {
			if reportDiff {
				wantStatus.Conditions = rpDiffReportedConditions(rp.Generation, rpHasOverrides)
			} else {
				wantStatus.Conditions = rpRolloutCompletedConditions(rp.Generation, rpHasOverrides)
			}
		} else {
			wantStatus.Conditions = rpRolloutPendingDueToExternalStrategyConditions(rp.Generation)
		}

		if diff := cmp.Diff(rp.Status, wantStatus, placementStatusCmpOptions...); diff != "" {
			return fmt.Errorf("RP status diff (-got, +want): %s", diff)
		}
		return nil
	}
}

func namespacedUpdateRunStatusSucceededActual(
	updateRunName, namespace string,
	wantPolicyIndex string,
	wantClusterCount int,
	wantApplyStrategy *placementv1beta1.ApplyStrategy,
	wantStrategySpec *placementv1beta1.UpdateStrategySpec,
	wantSelectedClusters [][]string,
	wantUnscheduledClusters []string,
	wantCROs map[string][]string,
	wantROs map[string][]placementv1beta1.NamespacedName,
) func() error {
	return func() error {
		updateRun := &placementv1beta1.StagedUpdateRun{}
		if err := hubClient.Get(ctx, client.ObjectKey{Name: updateRunName, Namespace: namespace}, updateRun); err != nil {
			return err
		}

		wantStatus := placementv1beta1.UpdateRunStatus{
			PolicySnapshotIndexUsed:    wantPolicyIndex,
			PolicyObservedClusterCount: wantClusterCount,
			ApplyStrategy:              wantApplyStrategy.DeepCopy(),
			UpdateStrategySnapshot:     wantStrategySpec,
		}
		stagesStatus := make([]placementv1beta1.StageUpdatingStatus, len(wantStrategySpec.Stages))
		for i, stage := range wantStrategySpec.Stages {
			stagesStatus[i].StageName = stage.Name
			stagesStatus[i].Clusters = make([]placementv1beta1.ClusterUpdatingStatus, len(wantSelectedClusters[i]))
			for j := range stagesStatus[i].Clusters {
				stagesStatus[i].Clusters[j].ClusterName = wantSelectedClusters[i][j]
				stagesStatus[i].Clusters[j].ClusterResourceOverrideSnapshots = wantCROs[wantSelectedClusters[i][j]]
				stagesStatus[i].Clusters[j].ResourceOverrideSnapshots = wantROs[wantSelectedClusters[i][j]]
				stagesStatus[i].Clusters[j].Conditions = updateRunClusterRolloutSucceedConditions(updateRun.Generation)
			}
			stagesStatus[i].AfterStageTaskStatus = make([]placementv1beta1.AfterStageTaskStatus, len(stage.AfterStageTasks))
			for j, task := range stage.AfterStageTasks {
				stagesStatus[i].AfterStageTaskStatus[j].Type = task.Type
				if task.Type == placementv1beta1.AfterStageTaskTypeApproval {
					stagesStatus[i].AfterStageTaskStatus[j].ApprovalRequestName = fmt.Sprintf(placementv1beta1.ApprovalTaskNameFmt, updateRun.Name, stage.Name)
				}
				stagesStatus[i].AfterStageTaskStatus[j].Conditions = updateRunAfterStageTaskSucceedConditions(updateRun.Generation, task.Type)
			}
			stagesStatus[i].Conditions = updateRunStageRolloutSucceedConditions(updateRun.Generation)
		}

		deleteStageStatus := &placementv1beta1.StageUpdatingStatus{
			StageName: "kubernetes-fleet.io/deleteStage",
		}
		deleteStageStatus.Clusters = make([]placementv1beta1.ClusterUpdatingStatus, len(wantUnscheduledClusters))
		for i := range deleteStageStatus.Clusters {
			deleteStageStatus.Clusters[i].ClusterName = wantUnscheduledClusters[i]
			deleteStageStatus.Clusters[i].Conditions = updateRunClusterRolloutSucceedConditions(updateRun.Generation)
		}
		deleteStageStatus.Conditions = updateRunStageRolloutSucceedConditions(updateRun.Generation)

		wantStatus.StagesStatus = stagesStatus
		wantStatus.DeletionStageStatus = deleteStageStatus
		wantStatus.Conditions = updateRunSucceedConditions(updateRun.Generation)
		if diff := cmp.Diff(updateRun.Status, wantStatus, updateRunStatusCmpOption...); diff != "" {
			return fmt.Errorf("UpdateRun status diff (-got, +want): %s", diff)
		}
		return nil
	}
}

// ConfigMap-specific utility functions for namespaced staged update run tests

func checkIfPlacedConfigMapOnMemberClustersInUpdateRun(clusters []*framework.Cluster) {
	for idx := range clusters {
		memberCluster := clusters[idx]
		configMap := appConfigMap()
		configMapPlacedActual := configMapPlacedOnClusterActual(memberCluster, &configMap)
		Eventually(configMapPlacedActual, updateRunEventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to place config map on member cluster %s", memberCluster.ClusterName)
	}
}

func checkIfRemovedConfigMapFromMemberClustersConsistently(clusters []*framework.Cluster) {
	for idx := range clusters {
		memberCluster := clusters[idx]
		configMapRemovedActual := namespacedResourcesRemovedFromClusterActual(memberCluster)
		Consistently(configMapRemovedActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "Failed to remove config map from member cluster %s consistently", memberCluster.ClusterName)
	}
}

func checkIfRemovedConfigMapFromAllMemberClustersConsistently() {
	checkIfRemovedConfigMapFromMemberClustersConsistently(allMemberClusters)
}

// Condition functions for namespaced resources - adapted from CRP equivalents

func rpRolloutPendingDueToExternalStrategyConditions(generation int64) []metav1.Condition {
	return []metav1.Condition{
		{
			Type:               string(placementv1beta1.ResourcePlacementScheduledConditionType),
			Status:             metav1.ConditionTrue,
			Reason:             scheduler.FullyScheduledReason,
			ObservedGeneration: generation,
		},
		{
			Type:               string(placementv1beta1.ResourcePlacementRolloutStartedConditionType),
			Status:             metav1.ConditionUnknown,
			Reason:             condition.RolloutControlledByExternalControllerReason,
			ObservedGeneration: generation,
		},
	}
}
