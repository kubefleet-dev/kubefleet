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

package rollout

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/condition"
)

const (
	falseLabelValue = "false"
)

var _ = Describe("Test SetupWithManagerForResourceBinding integration", func() {
	var rpName string
	var testRP *placementv1beta1.ResourcePlacement
	var testResourceSnapshots []*placementv1beta1.ResourceSnapshot
	var testResourceBindings []*placementv1beta1.ResourceBinding
	var testResourceOverrideSnapshots []*placementv1beta1.ResourceOverrideSnapshot

	BeforeEach(func() {
		rpName = "rp-" + utils.RandStr()
		testResourceSnapshots = make([]*placementv1beta1.ResourceSnapshot, 0)
		testResourceBindings = make([]*placementv1beta1.ResourceBinding, 0)
		testResourceOverrideSnapshots = make([]*placementv1beta1.ResourceOverrideSnapshot, 0)
	})

	AfterEach(func() {
		By("Cleaning up ResourceBindings")
		for _, binding := range testResourceBindings {
			Expect(k8sClient.Delete(ctx, binding)).Should(SatisfyAny(Succeed(), utils.NotFoundMatcher{}))
		}

		By("Cleaning up ResourceSnapshots")
		for _, snapshot := range testResourceSnapshots {
			Expect(k8sClient.Delete(ctx, snapshot)).Should(SatisfyAny(Succeed(), utils.NotFoundMatcher{}))
		}

		By("Cleaning up ResourceOverrideSnapshots")
		for _, ros := range testResourceOverrideSnapshots {
			Expect(k8sClient.Delete(ctx, ros)).Should(SatisfyAny(Succeed(), utils.NotFoundMatcher{}))
		}

		By("Cleaning up ResourcePlacement")
		if testRP != nil {
			Expect(k8sClient.Delete(ctx, testRP)).Should(SatisfyAny(Succeed(), utils.NotFoundMatcher{}))
		}
	})

	Describe("Testing ResourceSnapshot events trigger RP reconciliation", func() {
		It("Should trigger RP reconciliation when ResourceSnapshot is created", func() {
			By("Creating ResourcePlacement")
			testRP = generateResourcePlacement(rpName)
			Expect(k8sClient.Create(ctx, testRP)).Should(Succeed())

			By("Creating ResourceSnapshot with latest label")
			resourceSnapshot := generateNamespacedResourceSnapshot(rpName, "test-namespace", 0, true)
			testResourceSnapshots = append(testResourceSnapshots, resourceSnapshot)
			Expect(k8sClient.Create(ctx, resourceSnapshot)).Should(Succeed())

			By("Creating ResourceBinding in scheduled state")
			binding := generateResourceBinding(placementv1beta1.BindingStateScheduled, resourceSnapshot.Name, "cluster-1", "test-namespace", rpName)
			testResourceBindings = append(testResourceBindings, binding)
			Expect(k8sClient.Create(ctx, binding)).Should(Succeed())

			By("Verifying ResourceBinding gets updated to bound state")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: binding.Name, Namespace: binding.Namespace}, binding)
				if err != nil {
					return false
				}
				return binding.Spec.State == placementv1beta1.BindingStateBound &&
					binding.Spec.ResourceSnapshotName == resourceSnapshot.Name
			}, timeout, interval).Should(BeTrue(), "ResourceBinding should be updated to bound state when ResourceSnapshot is created")

			By("Verifying ResourceBinding has rollout started condition")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: binding.Name, Namespace: binding.Namespace}, binding)
				if err != nil {
					return false
				}
				rolloutStartedCondition := binding.GetCondition(string(placementv1beta1.ResourceBindingRolloutStarted))
				return condition.IsConditionStatusTrue(rolloutStartedCondition, binding.GetGeneration())
			}, timeout, interval).Should(BeTrue(), "ResourceBinding should have rollout started condition set to true")
		})

		It("Should trigger RP reconciliation when ResourceSnapshot is updated to latest", func() {
			By("Creating ResourcePlacement")
			testRP = generateResourcePlacement(rpName)
			Expect(k8sClient.Create(ctx, testRP)).Should(Succeed())

			By("Creating first ResourceSnapshot")
			firstSnapshot := generateNamespacedResourceSnapshot(rpName, "test-namespace", 0, true)
			testResourceSnapshots = append(testResourceSnapshots, firstSnapshot)
			Expect(k8sClient.Create(ctx, firstSnapshot)).Should(Succeed())

			By("Creating ResourceBinding in bound state with first snapshot")
			binding := generateResourceBinding(placementv1beta1.BindingStateBound, firstSnapshot.Name, "cluster-1", "test-namespace", rpName)
			testResourceBindings = append(testResourceBindings, binding)
			Expect(k8sClient.Create(ctx, binding)).Should(Succeed())

			By("Waiting for initial binding to be processed")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: binding.Name, Namespace: binding.Namespace}, binding)
				if err != nil {
					return false
				}
				rolloutStartedCondition := binding.GetCondition(string(placementv1beta1.ResourceBindingRolloutStarted))
				return condition.IsConditionStatusTrue(rolloutStartedCondition, binding.GetGeneration())
			}, timeout, interval).Should(BeTrue())

			By("Marking first snapshot as not latest")
			firstSnapshot.Labels[placementv1beta1.IsLatestSnapshotLabel] = falseLabelValue
			Expect(k8sClient.Update(ctx, firstSnapshot)).Should(Succeed())

			By("Creating second ResourceSnapshot with latest label")
			secondSnapshot := generateNamespacedResourceSnapshot(rpName, "test-namespace", 1, true)
			testResourceSnapshots = append(testResourceSnapshots, secondSnapshot)
			Expect(k8sClient.Create(ctx, secondSnapshot)).Should(Succeed())

			By("Verifying ResourceBinding gets updated to use second snapshot")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: binding.Name, Namespace: binding.Namespace}, binding)
				if err != nil {
					return false
				}
				return binding.Spec.ResourceSnapshotName == secondSnapshot.Name
			}, timeout, interval).Should(BeTrue(), "ResourceBinding should be updated to use the latest ResourceSnapshot")
		})
	})

	Describe("Testing ResourceBinding events trigger RP reconciliation", func() {
		It("Should trigger RP reconciliation when ResourceBinding is created", func() {
			By("Creating ResourcePlacement")
			testRP = generateResourcePlacement(rpName)
			Expect(k8sClient.Create(ctx, testRP)).Should(Succeed())

			By("Creating ResourceSnapshot")
			resourceSnapshot := generateNamespacedResourceSnapshot(rpName, "test-namespace", 0, true)
			testResourceSnapshots = append(testResourceSnapshots, resourceSnapshot)
			Expect(k8sClient.Create(ctx, resourceSnapshot)).Should(Succeed())

			By("Creating ResourceBinding in scheduled state")
			binding := generateResourceBinding(placementv1beta1.BindingStateScheduled, resourceSnapshot.Name, "cluster-1", "test-namespace", rpName)
			testResourceBindings = append(testResourceBindings, binding)
			Expect(k8sClient.Create(ctx, binding)).Should(Succeed())

			By("Verifying ResourceBinding gets processed and updated to bound state")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: binding.Name, Namespace: binding.Namespace}, binding)
				if err != nil {
					return false
				}
				return binding.Spec.State == placementv1beta1.BindingStateBound
			}, timeout, interval).Should(BeTrue(), "ResourceBinding should be processed when created")
		})

		It("Should trigger RP reconciliation when ResourceBinding status is updated", func() {
			By("Creating ResourcePlacement")
			testRP = generateResourcePlacement(rpName)
			Expect(k8sClient.Create(ctx, testRP)).Should(Succeed())

			By("Creating ResourceSnapshot")
			resourceSnapshot := generateNamespacedResourceSnapshot(rpName, "test-namespace", 0, true)
			testResourceSnapshots = append(testResourceSnapshots, resourceSnapshot)
			Expect(k8sClient.Create(ctx, resourceSnapshot)).Should(Succeed())

			By("Creating ResourceBinding in bound state")
			binding := generateResourceBinding(placementv1beta1.BindingStateBound, resourceSnapshot.Name, "cluster-1", "test-namespace", rpName)
			testResourceBindings = append(testResourceBindings, binding)
			Expect(k8sClient.Create(ctx, binding)).Should(Succeed())

			By("Waiting for initial processing")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: binding.Name, Namespace: binding.Namespace}, binding)
				if err != nil {
					return false
				}
				rolloutStartedCondition := binding.GetCondition(string(placementv1beta1.ResourceBindingRolloutStarted))
				return condition.IsConditionStatusTrue(rolloutStartedCondition, binding.GetGeneration())
			}, timeout, interval).Should(BeTrue())

			By("Updating ResourceBinding status with Available condition")
			binding.SetConditions(metav1.Condition{
				Type:               string(placementv1beta1.ResourceBindingAvailable),
				Status:             metav1.ConditionTrue,
				ObservedGeneration: binding.GetGeneration(),
				Reason:             "TestUpdate",
				Message:            "Test status update",
			})
			Expect(k8sClient.Status().Update(ctx, binding)).Should(Succeed())

			By("Marking first snapshot as not latest")
			resourceSnapshot.Labels[placementv1beta1.IsLatestSnapshotLabel] = falseLabelValue
			Expect(k8sClient.Update(ctx, resourceSnapshot)).Should(Succeed())

			By("Creating second ResourceSnapshot")
			secondSnapshot := generateNamespacedResourceSnapshot(rpName, "test-namespace", 1, true)
			testResourceSnapshots = append(testResourceSnapshots, secondSnapshot)
			Expect(k8sClient.Create(ctx, secondSnapshot)).Should(Succeed())

			By("Verifying ResourceBinding gets updated due to status change triggering reconciliation")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: binding.Name, Namespace: binding.Namespace}, binding)
				if err != nil {
					return false
				}
				return binding.Spec.ResourceSnapshotName == secondSnapshot.Name
			}, timeout, interval).Should(BeTrue(), "ResourceBinding should be updated when status changes trigger reconciliation")
		})

		It("Should trigger RP reconciliation when ResourceBinding spec is updated", func() {
			By("Creating ResourcePlacement")
			testRP = generateResourcePlacement(rpName)
			Expect(k8sClient.Create(ctx, testRP)).Should(Succeed())

			By("Creating ResourceSnapshot")
			resourceSnapshot := generateNamespacedResourceSnapshot(rpName, "test-namespace", 0, true)
			testResourceSnapshots = append(testResourceSnapshots, resourceSnapshot)
			Expect(k8sClient.Create(ctx, resourceSnapshot)).Should(Succeed())

			By("Creating ResourceBinding in scheduled state")
			binding := generateResourceBinding(placementv1beta1.BindingStateScheduled, resourceSnapshot.Name, "cluster-1", "test-namespace", rpName)
			testResourceBindings = append(testResourceBindings, binding)
			Expect(k8sClient.Create(ctx, binding)).Should(Succeed())

			By("Waiting for initial processing")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: binding.Name, Namespace: binding.Namespace}, binding)
				if err != nil {
					return false
				}
				return binding.Spec.State == placementv1beta1.BindingStateBound
			}, timeout, interval).Should(BeTrue())

			By("Updating ResourceBinding spec to change cluster")
			binding.Spec.TargetCluster = "cluster-2"
			Expect(k8sClient.Update(ctx, binding)).Should(Succeed())

			By("Verifying ResourceBinding spec change triggers reconciliation")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: binding.Name, Namespace: binding.Namespace}, binding)
				if err != nil {
					return false
				}
				rolloutStartedCondition := binding.GetCondition(string(placementv1beta1.ResourceBindingRolloutStarted))
				return condition.IsConditionStatusTrue(rolloutStartedCondition, binding.GetGeneration()) &&
					binding.Spec.TargetCluster == "cluster-2"
			}, timeout, interval).Should(BeTrue(), "ResourceBinding should be processed when spec changes")
		})
	})

	Describe("Testing ResourceOverrideSnapshot events trigger RP reconciliation", func() {
		It("Should trigger RP reconciliation when ResourceOverrideSnapshot is created", func() {
			By("Creating ResourcePlacement")
			testRP = generateResourcePlacement(rpName)
			Expect(k8sClient.Create(ctx, testRP)).Should(Succeed())

			By("Creating ResourceSnapshot")
			resourceSnapshot := generateNamespacedResourceSnapshot(rpName, "test-namespace", 0, true)
			testResourceSnapshots = append(testResourceSnapshots, resourceSnapshot)
			Expect(k8sClient.Create(ctx, resourceSnapshot)).Should(Succeed())

			By("Creating ResourceBinding in bound state")
			binding := generateResourceBinding(placementv1beta1.BindingStateBound, resourceSnapshot.Name, "cluster-1", "test-namespace", rpName)
			testResourceBindings = append(testResourceBindings, binding)
			Expect(k8sClient.Create(ctx, binding)).Should(Succeed())

			By("Waiting for initial processing")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: binding.Name, Namespace: binding.Namespace}, binding)
				if err != nil {
					return false
				}
				rolloutStartedCondition := binding.GetCondition(string(placementv1beta1.ResourceBindingRolloutStarted))
				return condition.IsConditionStatusTrue(rolloutStartedCondition, binding.GetGeneration())
			}, timeout, interval).Should(BeTrue())

			By("Creating ResourceOverrideSnapshot with latest label")
			ros := generateResourceOverrideSnapshot(rpName, "test-namespace", 0, true)
			testResourceOverrideSnapshots = append(testResourceOverrideSnapshots, ros)
			Expect(k8sClient.Create(ctx, ros)).Should(Succeed())

			By("Verifying ResourceBinding gets updated when ResourceOverrideSnapshot is created")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: binding.Name, Namespace: binding.Namespace}, binding)
				if err != nil {
					return false
				}
				// Check if ResourceOverrideSnapshots are included in the binding spec
				return len(binding.Spec.ResourceOverrideSnapshots) > 0
			}, timeout, interval).Should(BeTrue(), "ResourceBinding should be updated when ResourceOverrideSnapshot is created")
		})
	})

	Describe("Testing ResourceOverride deletion events trigger RP reconciliation", func() {
		It("Should trigger RP reconciliation when ResourceOverride is deleted", func() {
			By("Creating ResourcePlacement")
			testRP = generateResourcePlacement(rpName)
			Expect(k8sClient.Create(ctx, testRP)).Should(Succeed())

			By("Creating ResourceOverride with placement reference")
			ro := &placementv1beta1.ResourceOverride{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ro-" + utils.RandStr(),
					Namespace: "test-namespace",
				},
				Spec: placementv1beta1.ResourceOverrideSpec{
					Placement: &placementv1beta1.PlacementRef{
						Name: rpName,
					},
				},
			}
			Expect(k8sClient.Create(ctx, ro)).Should(Succeed())

			By("Creating ResourceSnapshot")
			resourceSnapshot := generateNamespacedResourceSnapshot(rpName, "test-namespace", 0, true)
			testResourceSnapshots = append(testResourceSnapshots, resourceSnapshot)
			Expect(k8sClient.Create(ctx, resourceSnapshot)).Should(Succeed())

			By("Creating ResourceBinding in bound state")
			binding := generateResourceBinding(placementv1beta1.BindingStateBound, resourceSnapshot.Name, "cluster-1", "test-namespace", rpName)
			testResourceBindings = append(testResourceBindings, binding)
			Expect(k8sClient.Create(ctx, binding)).Should(Succeed())

			By("Waiting for initial processing")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: binding.Name, Namespace: binding.Namespace}, binding)
				if err != nil {
					return false
				}
				rolloutStartedCondition := binding.GetCondition(string(placementv1beta1.ResourceBindingRolloutStarted))
				return condition.IsConditionStatusTrue(rolloutStartedCondition, binding.GetGeneration())
			}, timeout, interval).Should(BeTrue())

			By("Deleting ResourceOverride")
			Expect(k8sClient.Delete(ctx, ro)).Should(Succeed())

			By("Creating new ResourceSnapshot to verify reconciliation occurs")
			secondSnapshot := generateNamespacedResourceSnapshot(rpName, "test-namespace", 1, false)
			testResourceSnapshots = append(testResourceSnapshots, secondSnapshot)
			Expect(k8sClient.Create(ctx, secondSnapshot)).Should(Succeed())

			By("Marking first snapshot as not latest and second as latest")
			resourceSnapshot.Labels[placementv1beta1.IsLatestSnapshotLabel] = falseLabelValue
			Expect(k8sClient.Update(ctx, resourceSnapshot)).Should(Succeed())
			secondSnapshot.Labels[placementv1beta1.IsLatestSnapshotLabel] = "true"
			Expect(k8sClient.Update(ctx, secondSnapshot)).Should(Succeed())

			By("Verifying ResourceBinding gets updated after ResourceOverride deletion")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: binding.Name, Namespace: binding.Namespace}, binding)
				if err != nil {
					return false
				}
				return binding.Spec.ResourceSnapshotName == secondSnapshot.Name
			}, timeout, interval).Should(BeTrue(), "ResourceBinding should be updated after ResourceOverride deletion triggers reconciliation")
		})
	})

	Describe("Testing ResourcePlacement events trigger RP reconciliation", func() {
		It("Should trigger RP reconciliation when ResourcePlacement apply strategy is updated", func() {
			By("Creating ResourcePlacement with initial apply strategy")
			testRP = generateResourcePlacement(rpName)
			testRP.Spec.Strategy.ApplyStrategy = &placementv1beta1.ApplyStrategy{
				Type:             placementv1beta1.ApplyStrategyTypeClientSideApply,
				ComparisonOption: placementv1beta1.ComparisonOptionTypePartialComparison,
				WhenToApply:      placementv1beta1.WhenToApplyTypeAlways,
				WhenToTakeOver:   placementv1beta1.WhenToTakeOverTypeAlways,
			}
			Expect(k8sClient.Create(ctx, testRP)).Should(Succeed())

			By("Creating ResourceSnapshot")
			resourceSnapshot := generateNamespacedResourceSnapshot(rpName, "test-namespace", 0, true)
			testResourceSnapshots = append(testResourceSnapshots, resourceSnapshot)
			Expect(k8sClient.Create(ctx, resourceSnapshot)).Should(Succeed())

			By("Creating ResourceBinding in bound state")
			binding := generateResourceBinding(placementv1beta1.BindingStateBound, resourceSnapshot.Name, "cluster-1", "test-namespace", rpName)
			testResourceBindings = append(testResourceBindings, binding)
			Expect(k8sClient.Create(ctx, binding)).Should(Succeed())

			By("Waiting for initial processing")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: binding.Name, Namespace: binding.Namespace}, binding)
				if err != nil {
					return false
				}
				return binding.Spec.ApplyStrategy != nil &&
					binding.Spec.ApplyStrategy.Type == placementv1beta1.ApplyStrategyTypeClientSideApply
			}, timeout, interval).Should(BeTrue())

			By("Updating ResourcePlacement apply strategy")
			testRP.Spec.Strategy.ApplyStrategy = &placementv1beta1.ApplyStrategy{
				Type:             placementv1beta1.ApplyStrategyTypeServerSideApply,
				ComparisonOption: placementv1beta1.ComparisonOptionTypeFullComparison,
				WhenToApply:      placementv1beta1.WhenToApplyTypeIfNotDrifted,
				WhenToTakeOver:   placementv1beta1.WhenToTakeOverTypeIfNoDiff,
			}
			Expect(k8sClient.Update(ctx, testRP)).Should(Succeed())

			By("Verifying ResourceBinding gets updated with new apply strategy")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: binding.Name, Namespace: binding.Namespace}, binding)
				if err != nil {
					return false
				}
				return binding.Spec.ApplyStrategy != nil &&
					binding.Spec.ApplyStrategy.Type == placementv1beta1.ApplyStrategyTypeServerSideApply &&
					binding.Spec.ApplyStrategy.ComparisonOption == placementv1beta1.ComparisonOptionTypeFullComparison
			}, timeout, interval).Should(BeTrue(), "ResourceBinding should be updated when ResourcePlacement apply strategy changes")
		})

		It("Should trigger RP reconciliation when ResourcePlacement rollout strategy type is updated", func() {
			By("Creating ResourcePlacement with RollingUpdate strategy")
			testRP = generateResourcePlacement(rpName)
			testRP.Spec.Strategy.Type = placementv1beta1.RollingUpdateRolloutStrategyType
			testRP.Spec.Strategy.RollingUpdate = generateDefaultRollingUpdateConfig()
			Expect(k8sClient.Create(ctx, testRP)).Should(Succeed())

			By("Creating ResourceSnapshot")
			resourceSnapshot := generateNamespacedResourceSnapshot(rpName, "test-namespace", 0, true)
			testResourceSnapshots = append(testResourceSnapshots, resourceSnapshot)
			Expect(k8sClient.Create(ctx, resourceSnapshot)).Should(Succeed())

			By("Creating ResourceBinding in scheduled state")
			binding := generateResourceBinding(placementv1beta1.BindingStateScheduled, resourceSnapshot.Name, "cluster-1", "test-namespace", rpName)
			testResourceBindings = append(testResourceBindings, binding)
			Expect(k8sClient.Create(ctx, binding)).Should(Succeed())

			By("Waiting for initial processing - should be processed with RollingUpdate")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: binding.Name, Namespace: binding.Namespace}, binding)
				if err != nil {
					return false
				}
				return binding.Spec.State == placementv1beta1.BindingStateBound
			}, timeout, interval).Should(BeTrue())

			By("Updating ResourcePlacement strategy type to External")
			testRP.Spec.Strategy.Type = placementv1beta1.ExternalRolloutStrategyType
			testRP.Spec.Strategy.RollingUpdate = nil
			Expect(k8sClient.Update(ctx, testRP)).Should(Succeed())

			By("Creating new ResourceSnapshot to trigger potential rollout")
			secondSnapshot := generateNamespacedResourceSnapshot(rpName, "test-namespace", 1, false)
			testResourceSnapshots = append(testResourceSnapshots, secondSnapshot)
			Expect(k8sClient.Create(ctx, secondSnapshot)).Should(Succeed())

			By("Marking first snapshot as not latest and second as latest")
			resourceSnapshot.Labels[placementv1beta1.IsLatestSnapshotLabel] = falseLabelValue
			Expect(k8sClient.Update(ctx, resourceSnapshot)).Should(Succeed())
			secondSnapshot.Labels[placementv1beta1.IsLatestSnapshotLabel] = "true"
			Expect(k8sClient.Update(ctx, secondSnapshot)).Should(Succeed())

			By("Verifying ResourceBinding is NOT updated due to External strategy")
			Consistently(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: binding.Name, Namespace: binding.Namespace}, binding)
				if err != nil {
					return false
				}
				return binding.Spec.ResourceSnapshotName == resourceSnapshot.Name
			}, 5*time.Second, interval).Should(BeTrue(), "ResourceBinding should NOT be updated when strategy is External")
		})
	})
})

// Helper functions for generating test resources

func generateResourcePlacement(name string) *placementv1beta1.ResourcePlacement {
	return &placementv1beta1.ResourcePlacement{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "test-namespace",
		},
		Spec: placementv1beta1.PlacementSpec{
			ResourceSelectors: []placementv1beta1.ClusterResourceSelector{
				{
					Group:   "",
					Version: "v1",
					Kind:    "ConfigMap",
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"test": "label"},
					},
				},
			},
			Policy: &placementv1beta1.PlacementPolicy{
				PlacementType:    placementv1beta1.PickAllPlacementType,
				NumberOfClusters: ptr.To(int32(1)),
			},
			Strategy: placementv1beta1.RolloutStrategy{
				Type:          placementv1beta1.RollingUpdateRolloutStrategyType,
				RollingUpdate: generateDefaultRollingUpdateConfig(),
			},
		},
	}
}

func generateNamespacedResourceSnapshot(placementName, namespace string, resourceIndex int, isLatest bool) *placementv1beta1.ResourceSnapshot {
	return &placementv1beta1.ResourceSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf(placementv1beta1.ResourceSnapshotNameFmt, placementName, resourceIndex),
			Namespace: namespace,
			Labels: map[string]string{
				placementv1beta1.PlacementTrackingLabel: placementName,
				placementv1beta1.IsLatestSnapshotLabel:  fmt.Sprintf("%t", isLatest),
			},
			Annotations: map[string]string{
				placementv1beta1.ResourceGroupHashAnnotation: "hash",
			},
		},
		Spec: placementv1beta1.ResourceSnapshotSpec{
			SelectedResources: []placementv1beta1.ResourceContent{
				{
					RawExtension: runtime.RawExtension{Raw: testConfigMap},
				},
			},
		},
	}
}

func generateResourceBinding(state placementv1beta1.BindingState, resourceSnapshotName, targetCluster, namespace, placementName string) *placementv1beta1.ResourceBinding {
	binding := &placementv1beta1.ResourceBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "binding-" + resourceSnapshotName + "-" + targetCluster,
			Namespace: namespace,
			Labels: map[string]string{
				placementv1beta1.PlacementTrackingLabel: placementName,
			},
		},
		Spec: placementv1beta1.ResourceBindingSpec{
			State:         state,
			TargetCluster: targetCluster,
		},
	}
	if binding.Spec.State == placementv1beta1.BindingStateBound {
		binding.Spec.ResourceSnapshotName = resourceSnapshotName
	}
	return binding
}

func generateResourceOverrideSnapshot(placementName, namespace string, index int, isLatest bool) *placementv1beta1.ResourceOverrideSnapshot {
	return &placementv1beta1.ResourceOverrideSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("ros-%s-%d", placementName, index),
			Namespace: namespace,
			Labels: map[string]string{
				placementv1beta1.PlacementTrackingLabel: placementName,
				placementv1beta1.IsLatestSnapshotLabel:  fmt.Sprintf("%t", isLatest),
			},
		},
		Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
			OverrideSpec: placementv1beta1.ResourceOverrideSpec{
				Placement: &placementv1beta1.PlacementRef{
					Name: placementName,
				},
				Policy: &placementv1beta1.OverridePolicy{
					OverrideRules: []placementv1beta1.OverrideRule{
						{
							ClusterSelector: &placementv1beta1.ClusterSelector{
								ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
									{
										LabelSelector: &metav1.LabelSelector{
											MatchLabels: map[string]string{
												"test": "override",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

