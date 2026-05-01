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

package v1beta1

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/test/apis/placement/testutils"
)

var _ = Describe("Test CRP/RP CEL validation rules", func() {
	crpName := fmt.Sprintf("test-crp-cel-%d", GinkgoParallelProcess())

	defaultResourceSelectors := []placementv1beta1.ResourceSelectorTerm{
		{
			Group:   "",
			Version: "v1",
			Kind:    "Namespace",
			Name:    "test-ns",
		},
	}

	Context("CRP name length validation", func() {
		It("should deny creation of a CRP with name exceeding 63 characters", func() {
			longName := strings.Repeat("a", 64)
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: longName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
				},
			}
			err := hubClient.Create(ctx, &crp)
			testutils.ExpectValidationError(err, "name must not exceed 63 characters")
		})

		It("should allow creation of a CRP with name of exactly 63 characters", func() {
			name63 := strings.Repeat("b", 63)
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: name63,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
				},
			}
			Expect(hubClient.Create(ctx, &crp)).Should(Succeed())
			Expect(hubClient.Delete(ctx, &crp)).Should(Succeed())
		})
	})

	Context("RP name length validation", func() {
		It("should deny creation of an RP with name exceeding 63 characters", func() {
			longName := strings.Repeat("c", 64)
			rp := placementv1beta1.ResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name:      longName,
					Namespace: testNamespace,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
				},
			}
			err := hubClient.Create(ctx, &rp)
			testutils.ExpectValidationError(err, "name must not exceed 63 characters")
		})
	})

	Context("PlacementPolicy PickFixed CEL rules", func() {
		It("should deny PickFixed with empty clusterNames", func() {
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType: placementv1beta1.PickFixedPlacementType,
						ClusterNames:  []string{},
					},
				},
			}
			err := hubClient.Create(ctx, &crp)
			testutils.ExpectValidationError(err, "clusterNames cannot be empty for PickFixed placement type")
		})

		It("should deny PickFixed with numberOfClusters set", func() {
			numClusters := int32(1)
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType:    placementv1beta1.PickFixedPlacementType,
						ClusterNames:     []string{"cluster1"},
						NumberOfClusters: &numClusters,
					},
				},
			}
			err := hubClient.Create(ctx, &crp)
			testutils.ExpectValidationError(err, "numberOfClusters must not be set for PickFixed placement type")
		})

		It("should deny PickFixed with affinity set", func() {
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType: placementv1beta1.PickFixedPlacementType,
						ClusterNames:  []string{"cluster1"},
						Affinity: &placementv1beta1.Affinity{
							ClusterAffinity: &placementv1beta1.ClusterAffinity{
								RequiredDuringSchedulingIgnoredDuringExecution: &placementv1beta1.ClusterSelector{
									ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
										{
											LabelSelector: &metav1.LabelSelector{
												MatchLabels: map[string]string{"env": "prod"},
											},
										},
									},
								},
							},
						},
					},
				},
			}
			err := hubClient.Create(ctx, &crp)
			testutils.ExpectValidationError(err, "affinity must not be set for PickFixed placement type")
		})

		It("should deny PickFixed with non-empty topologySpreadConstraints", func() {
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType: placementv1beta1.PickFixedPlacementType,
						ClusterNames:  []string{"cluster1"},
						TopologySpreadConstraints: []placementv1beta1.TopologySpreadConstraint{
							{
								TopologyKey: "region",
							},
						},
					},
				},
			}
			err := hubClient.Create(ctx, &crp)
			testutils.ExpectValidationError(err, "topologySpreadConstraints must be empty for PickFixed placement type")
		})

		It("should deny PickFixed with non-empty tolerations", func() {
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType: placementv1beta1.PickFixedPlacementType,
						ClusterNames:  []string{"cluster1"},
						Tolerations: []placementv1beta1.Toleration{
							{
								Key:   "key1",
								Value: "value1",
							},
						},
					},
				},
			}
			err := hubClient.Create(ctx, &crp)
			testutils.ExpectValidationError(err, "tolerations must be empty for PickFixed placement type")
		})

		It("should allow PickFixed with explicitly empty tolerations", func() {
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType: placementv1beta1.PickFixedPlacementType,
						ClusterNames:  []string{"cluster1"},
						Tolerations:   []placementv1beta1.Toleration{},
					},
				},
			}
			Expect(hubClient.Create(ctx, &crp)).Should(Succeed())
			Expect(hubClient.Delete(ctx, &crp)).Should(Succeed())
		})

		It("should allow valid PickFixed CRP", func() {
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType: placementv1beta1.PickFixedPlacementType,
						ClusterNames:  []string{"cluster1", "cluster2"},
					},
				},
			}
			Expect(hubClient.Create(ctx, &crp)).Should(Succeed())
			Expect(hubClient.Delete(ctx, &crp)).Should(Succeed())
		})
	})

	Context("PlacementPolicy PickAll CEL rules", func() {
		It("should deny PickAll with non-empty clusterNames", func() {
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType: placementv1beta1.PickAllPlacementType,
						ClusterNames:  []string{"cluster1"},
					},
				},
			}
			err := hubClient.Create(ctx, &crp)
			testutils.ExpectValidationError(err, "clusterNames must be empty for PickAll placement type")
		})

		It("should deny PickAll with numberOfClusters set", func() {
			numClusters := int32(1)
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType:    placementv1beta1.PickAllPlacementType,
						NumberOfClusters: &numClusters,
					},
				},
			}
			err := hubClient.Create(ctx, &crp)
			testutils.ExpectValidationError(err, "numberOfClusters must not be set for PickAll placement type")
		})

		It("should deny PickAll with non-empty topologySpreadConstraints", func() {
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType: placementv1beta1.PickAllPlacementType,
						TopologySpreadConstraints: []placementv1beta1.TopologySpreadConstraint{
							{
								TopologyKey: "region",
							},
						},
					},
				},
			}
			err := hubClient.Create(ctx, &crp)
			testutils.ExpectValidationError(err, "topologySpreadConstraints must be empty for PickAll placement type")
		})

		It("should deny PickAll with preferredDuringScheduling affinity", func() {
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType: placementv1beta1.PickAllPlacementType,
						Affinity: &placementv1beta1.Affinity{
							ClusterAffinity: &placementv1beta1.ClusterAffinity{
								PreferredDuringSchedulingIgnoredDuringExecution: []placementv1beta1.PreferredClusterSelector{
									{
										Weight: 10,
										Preference: placementv1beta1.ClusterSelectorTerm{
											LabelSelector: &metav1.LabelSelector{
												MatchLabels: map[string]string{"env": "prod"},
											},
										},
									},
								},
							},
						},
					},
				},
			}
			err := hubClient.Create(ctx, &crp)
			testutils.ExpectValidationError(err, "preferredDuringSchedulingIgnoredDuringExecution is not allowed for PickAll placement type")
		})

		It("should allow valid PickAll CRP with requiredDuringScheduling affinity", func() {
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType: placementv1beta1.PickAllPlacementType,
						Affinity: &placementv1beta1.Affinity{
							ClusterAffinity: &placementv1beta1.ClusterAffinity{
								RequiredDuringSchedulingIgnoredDuringExecution: &placementv1beta1.ClusterSelector{
									ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
										{
											LabelSelector: &metav1.LabelSelector{
												MatchLabels: map[string]string{"env": "prod"},
											},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(hubClient.Create(ctx, &crp)).Should(Succeed())
			Expect(hubClient.Delete(ctx, &crp)).Should(Succeed())
		})
	})

	Context("PlacementPolicy PickN CEL rules", func() {
		It("should deny PickN with non-empty clusterNames", func() {
			numClusters := int32(1)
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType:    placementv1beta1.PickNPlacementType,
						ClusterNames:     []string{"cluster1"},
						NumberOfClusters: &numClusters,
					},
				},
			}
			err := hubClient.Create(ctx, &crp)
			testutils.ExpectValidationError(err, "clusterNames must be empty for PickN placement type")
		})

		It("should deny PickN without numberOfClusters", func() {
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType: placementv1beta1.PickNPlacementType,
					},
				},
			}
			err := hubClient.Create(ctx, &crp)
			testutils.ExpectValidationError(err, "numberOfClusters must be set for PickN placement type")
		})

		It("should allow valid PickN CRP", func() {
			numClusters := int32(1)
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType:    placementv1beta1.PickNPlacementType,
						NumberOfClusters: &numClusters,
					},
				},
			}
			Expect(hubClient.Create(ctx, &crp)).Should(Succeed())
			Expect(hubClient.Delete(ctx, &crp)).Should(Succeed())
		})
	})

	Context("PickFixed clusterNames length validation", func() {
		It("should deny PickFixed with a cluster name exceeding 63 characters", func() {
			longClusterName := strings.Repeat("x", 64)
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType: placementv1beta1.PickFixedPlacementType,
						ClusterNames:  []string{longClusterName},
					},
				},
			}
			err := hubClient.Create(ctx, &crp)
			testutils.ExpectValidationError(err, "Too long: may not be more than 63 bytes")
		})

		It("should allow PickFixed with cluster names of exactly 63 characters", func() {
			name63 := strings.Repeat("y", 63)
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType: placementv1beta1.PickFixedPlacementType,
						ClusterNames:  []string{name63},
					},
				},
			}
			Expect(hubClient.Create(ctx, &crp)).Should(Succeed())
			Expect(hubClient.Delete(ctx, &crp)).Should(Succeed())
		})
	})

	Context("ResourceSelectorTerm labelSelector and name mutual exclusivity", func() {
		It("should deny creation when both labelSelector and name are set on a ResourceSelectorTerm", func() {
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
						{
							Group:   "",
							Version: "v1",
							Kind:    "Namespace",
							Name:    "test-ns",
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"app": "test"},
							},
						},
					},
				},
			}
			err := hubClient.Create(ctx, &crp)
			testutils.ExpectValidationError(err, "labelSelector and name are mutually exclusive")
		})

		It("should allow creation with only labelSelector set on a ResourceSelectorTerm", func() {
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
						{
							Group:   "",
							Version: "v1",
							Kind:    "Namespace",
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"app": "test"},
							},
						},
					},
				},
			}
			Expect(hubClient.Create(ctx, &crp)).Should(Succeed())
			Expect(hubClient.Delete(ctx, &crp)).Should(Succeed())
		})
	})

	Context("ClusterSelector propertySorter not allowed in requiredDuringScheduling", func() {
		It("should deny propertySorter in requiredDuringSchedulingIgnoredDuringExecution", func() {
			numClusters := int32(1)
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType:    placementv1beta1.PickNPlacementType,
						NumberOfClusters: &numClusters,
						Affinity: &placementv1beta1.Affinity{
							ClusterAffinity: &placementv1beta1.ClusterAffinity{
								RequiredDuringSchedulingIgnoredDuringExecution: &placementv1beta1.ClusterSelector{
									ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
										{
											PropertySorter: &placementv1beta1.PropertySorter{
												Name:      "resources.cpu",
												SortOrder: placementv1beta1.Descending,
											},
										},
									},
								},
							},
						},
					},
				},
			}
			err := hubClient.Create(ctx, &crp)
			testutils.ExpectValidationError(err, "propertySorter is not allowed in requiredDuringSchedulingIgnoredDuringExecution affinity terms")
		})
	})

	Context("PreferredClusterSelector propertySelector not allowed in preferredDuringScheduling", func() {
		It("should deny propertySelector in preferredDuringSchedulingIgnoredDuringExecution", func() {
			numClusters := int32(1)
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType:    placementv1beta1.PickNPlacementType,
						NumberOfClusters: &numClusters,
						Affinity: &placementv1beta1.Affinity{
							ClusterAffinity: &placementv1beta1.ClusterAffinity{
								PreferredDuringSchedulingIgnoredDuringExecution: []placementv1beta1.PreferredClusterSelector{
									{
										Weight: 10,
										Preference: placementv1beta1.ClusterSelectorTerm{
											PropertySelector: &placementv1beta1.PropertySelector{
												MatchExpressions: []placementv1beta1.PropertySelectorRequirement{
													{
														Name:     "resources.cpu",
														Operator: placementv1beta1.PropertySelectorGreaterThan,
														Values:   []string{"10"},
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
			err := hubClient.Create(ctx, &crp)
			testutils.ExpectValidationError(err, "propertySelector is not allowed in preferredDuringSchedulingIgnoredDuringExecution affinity terms")
		})
	})

	Context("RolloutStrategy CEL rules", func() {
		It("should deny External rollout strategy type with rollingUpdate config", func() {
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Strategy: placementv1beta1.RolloutStrategy{
						Type: placementv1beta1.ExternalRolloutStrategyType,
						RollingUpdate: &placementv1beta1.RollingUpdateConfig{
							UnavailablePeriodSeconds: nil,
						},
					},
				},
			}
			err := hubClient.Create(ctx, &crp)
			testutils.ExpectValidationError(err, "rollingUpdate config is not valid for External rollout strategy type")
		})

		It("should allow RollingUpdate strategy type with rollingUpdate config", func() {
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Strategy: placementv1beta1.RolloutStrategy{
						Type: placementv1beta1.RollingUpdateRolloutStrategyType,
						RollingUpdate: &placementv1beta1.RollingUpdateConfig{
							UnavailablePeriodSeconds: nil,
						},
					},
				},
			}
			Expect(hubClient.Create(ctx, &crp)).Should(Succeed())
			Expect(hubClient.Delete(ctx, &crp)).Should(Succeed())
		})
	})

	Context("ApplyStrategy CEL rules", func() {
		It("should deny serverSideApplyConfig when type is not ServerSideApply", func() {
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Strategy: placementv1beta1.RolloutStrategy{
						ApplyStrategy: &placementv1beta1.ApplyStrategy{
							Type: placementv1beta1.ApplyStrategyTypeClientSideApply,
							ServerSideApplyConfig: &placementv1beta1.ServerSideApplyConfig{
								ForceConflicts: true,
							},
						},
					},
				},
			}
			err := hubClient.Create(ctx, &crp)
			testutils.ExpectValidationError(err, "serverSideApplyConfig is only valid for ServerSideApply strategy type")
		})

		It("should allow serverSideApplyConfig when type is ServerSideApply", func() {
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Strategy: placementv1beta1.RolloutStrategy{
						ApplyStrategy: &placementv1beta1.ApplyStrategy{
							Type: placementv1beta1.ApplyStrategyTypeServerSideApply,
							ServerSideApplyConfig: &placementv1beta1.ServerSideApplyConfig{
								ForceConflicts: true,
							},
						},
					},
				},
			}
			Expect(hubClient.Create(ctx, &crp)).Should(Succeed())
			Expect(hubClient.Delete(ctx, &crp)).Should(Succeed())
		})
	})

	Context("Toleration CEL rules", func() {
		It("should deny Exists operator with non-empty value", func() {
			numClusters := int32(1)
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType:    placementv1beta1.PickNPlacementType,
						NumberOfClusters: &numClusters,
						Tolerations: []placementv1beta1.Toleration{
							{
								Key:      "key1",
								Operator: "Exists",
								Value:    "should-be-empty",
							},
						},
					},
				},
			}
			err := hubClient.Create(ctx, &crp)
			testutils.ExpectValidationError(err, "value must be empty when operator is Exists")
		})

		It("should deny Equal operator with empty key", func() {
			numClusters := int32(1)
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType:    placementv1beta1.PickNPlacementType,
						NumberOfClusters: &numClusters,
						Tolerations: []placementv1beta1.Toleration{
							{
								Key:      "",
								Operator: "Equal",
								Value:    "value1",
							},
						},
					},
				},
			}
			err := hubClient.Create(ctx, &crp)
			testutils.ExpectValidationError(err, "key must not be empty when operator is Equal")
		})

		It("should deny toleration with invalid key format", func() {
			numClusters := int32(1)
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType:    placementv1beta1.PickNPlacementType,
						NumberOfClusters: &numClusters,
						Tolerations: []placementv1beta1.Toleration{
							{
								Key:      "-invalid-key",
								Operator: "Equal",
								Value:    "value1",
							},
						},
					},
				},
			}
			err := hubClient.Create(ctx, &crp)
			testutils.ExpectValidationError(err, "toleration key must be a valid qualified name")
		})

		It("should deny toleration key with invalid DNS prefix", func() {
			numClusters := int32(1)
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType:    placementv1beta1.PickNPlacementType,
						NumberOfClusters: &numClusters,
						Tolerations: []placementv1beta1.Toleration{
							{
								Key:      "Bad_Prefix/key",
								Operator: "Equal",
								Value:    "value1",
							},
						},
					},
				},
			}
			err := hubClient.Create(ctx, &crp)
			testutils.ExpectValidationError(err, "toleration key must be a valid qualified name")
		})

		It("should deny toleration key with consecutive dots in prefix", func() {
			numClusters := int32(1)
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType:    placementv1beta1.PickNPlacementType,
						NumberOfClusters: &numClusters,
						Tolerations: []placementv1beta1.Toleration{
							{
								Key:      "a..b/key",
								Operator: "Equal",
								Value:    "value1",
							},
						},
					},
				},
			}
			err := hubClient.Create(ctx, &crp)
			testutils.ExpectValidationError(err, "toleration key must be a valid qualified name")
		})

		It("should deny unprefixed toleration key exceeding 63 characters", func() {
			numClusters := int32(1)
			longKey := strings.Repeat("k", 64)
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType:    placementv1beta1.PickNPlacementType,
						NumberOfClusters: &numClusters,
						Tolerations: []placementv1beta1.Toleration{
							{
								Key:      longKey,
								Operator: "Equal",
								Value:    "value1",
							},
						},
					},
				},
			}
			err := hubClient.Create(ctx, &crp)
			testutils.ExpectValidationError(err, "toleration key name segment must not exceed 63 characters")
		})

		It("should deny prefixed toleration key when name segment exceeds 63 characters", func() {
			numClusters := int32(1)
			longNameSegment := strings.Repeat("k", 64)
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType:    placementv1beta1.PickNPlacementType,
						NumberOfClusters: &numClusters,
						Tolerations: []placementv1beta1.Toleration{
							{
								Key:      "example.com/" + longNameSegment,
								Operator: "Equal",
								Value:    "value1",
							},
						},
					},
				},
			}
			err := hubClient.Create(ctx, &crp)
			testutils.ExpectValidationError(err, "toleration key name segment must not exceed 63 characters")
		})

		It("should deny toleration with invalid value format", func() {
			numClusters := int32(1)
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType:    placementv1beta1.PickNPlacementType,
						NumberOfClusters: &numClusters,
						Tolerations: []placementv1beta1.Toleration{
							{
								Key:      "key1",
								Operator: "Equal",
								Value:    "-invalid-value",
							},
						},
					},
				},
			}
			err := hubClient.Create(ctx, &crp)
			testutils.ExpectValidationError(err, "toleration value must be a valid label value")
		})

		It("should deny toleration value exceeding 63 characters", func() {
			numClusters := int32(1)
			longValue := strings.Repeat("v", 64)
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType:    placementv1beta1.PickNPlacementType,
						NumberOfClusters: &numClusters,
						Tolerations: []placementv1beta1.Toleration{
							{
								Key:      "key1",
								Operator: "Equal",
								Value:    longValue,
							},
						},
					},
				},
			}
			err := hubClient.Create(ctx, &crp)
			testutils.ExpectValidationError(err, "Too long: may not be more than 63 bytes")
		})

		It("should allow valid toleration with Exists operator and empty value", func() {
			numClusters := int32(1)
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType:    placementv1beta1.PickNPlacementType,
						NumberOfClusters: &numClusters,
						Tolerations: []placementv1beta1.Toleration{
							{
								Key:      "key1",
								Operator: "Exists",
							},
						},
					},
				},
			}
			Expect(hubClient.Create(ctx, &crp)).Should(Succeed())
			Expect(hubClient.Delete(ctx, &crp)).Should(Succeed())
		})

		It("should allow valid toleration with Equal operator and non-empty key and value", func() {
			numClusters := int32(1)
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType:    placementv1beta1.PickNPlacementType,
						NumberOfClusters: &numClusters,
						Tolerations: []placementv1beta1.Toleration{
							{
								Key:      "valid-key",
								Operator: "Equal",
								Value:    "valid-value",
							},
						},
					},
				},
			}
			Expect(hubClient.Create(ctx, &crp)).Should(Succeed())
			Expect(hubClient.Delete(ctx, &crp)).Should(Succeed())
		})

		It("should allow valid toleration with DNS-prefixed key", func() {
			numClusters := int32(1)
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType:    placementv1beta1.PickNPlacementType,
						NumberOfClusters: &numClusters,
						Tolerations: []placementv1beta1.Toleration{
							{
								Key:      "example.com/my-key",
								Operator: "Equal",
								Value:    "value1",
							},
						},
					},
				},
			}
			Expect(hubClient.Create(ctx, &crp)).Should(Succeed())
			Expect(hubClient.Delete(ctx, &crp)).Should(Succeed())
		})
	})

	Context("Spec-level transition rules", func() {
		It("should deny removing PickAll policy once set", func() {
			name := fmt.Sprintf("%s-rm-pickall", crpName)
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType: placementv1beta1.PickAllPlacementType,
					},
				},
			}
			Expect(hubClient.Create(ctx, &crp)).Should(Succeed())

			// Fetch the latest version to get the correct resourceVersion.
			var fetched placementv1beta1.ClusterResourcePlacement
			Expect(hubClient.Get(ctx, client.ObjectKeyFromObject(&crp), &fetched)).Should(Succeed())

			fetched.Spec.Policy = nil
			err := hubClient.Update(ctx, &fetched)
			testutils.ExpectValidationError(err, "policy cannot be removed once set")

			Expect(hubClient.Delete(ctx, &crp)).Should(Succeed())
		})

		It("should allow appending a new toleration to existing list", func() {
			name := fmt.Sprintf("%s-app-tol", crpName)
			numClusters := int32(1)
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType:    placementv1beta1.PickNPlacementType,
						NumberOfClusters: &numClusters,
						Tolerations: []placementv1beta1.Toleration{
							{
								Key:      "key1",
								Operator: "Equal",
								Value:    "value1",
							},
						},
					},
				},
			}
			Expect(hubClient.Create(ctx, &crp)).Should(Succeed())

			// Fetch the latest version to get the correct resourceVersion.
			var fetched placementv1beta1.ClusterResourcePlacement
			Expect(hubClient.Get(ctx, client.ObjectKeyFromObject(&crp), &fetched)).Should(Succeed())

			fetched.Spec.Policy.Tolerations = append(fetched.Spec.Policy.Tolerations, placementv1beta1.Toleration{
				Key:      "key2",
				Operator: "Equal",
				Value:    "value2",
			})
			Expect(hubClient.Update(ctx, &fetched)).Should(Succeed())

			Expect(hubClient.Delete(ctx, &crp)).Should(Succeed())
		})

		It("should deny deleting existing tolerations", func() {
			name := fmt.Sprintf("%s-del-tol", crpName)
			numClusters := int32(1)
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType:    placementv1beta1.PickNPlacementType,
						NumberOfClusters: &numClusters,
						Tolerations: []placementv1beta1.Toleration{
							{
								Key:      "key1",
								Operator: "Equal",
								Value:    "value1",
							},
						},
					},
				},
			}
			Expect(hubClient.Create(ctx, &crp)).Should(Succeed())

			// Fetch the latest version to get the correct resourceVersion.
			var fetched placementv1beta1.ClusterResourcePlacement
			Expect(hubClient.Get(ctx, client.ObjectKeyFromObject(&crp), &fetched)).Should(Succeed())

			fetched.Spec.Policy.Tolerations = nil
			err := hubClient.Update(ctx, &fetched)
			testutils.ExpectValidationError(err, "tolerations have been updated/deleted, only additions to tolerations are allowed")

			Expect(hubClient.Delete(ctx, &crp)).Should(Succeed())
		})

		It("should deny updating existing tolerations", func() {
			name := fmt.Sprintf("%s-upd-tol", crpName)
			numClusters := int32(1)
			crp := placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: defaultResourceSelectors,
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType:    placementv1beta1.PickNPlacementType,
						NumberOfClusters: &numClusters,
						Tolerations: []placementv1beta1.Toleration{
							{
								Key:      "key1",
								Operator: "Equal",
								Value:    "value1",
							},
						},
					},
				},
			}
			Expect(hubClient.Create(ctx, &crp)).Should(Succeed())

			// Fetch the latest version to get the correct resourceVersion.
			var fetched placementv1beta1.ClusterResourcePlacement
			Expect(hubClient.Get(ctx, client.ObjectKeyFromObject(&crp), &fetched)).Should(Succeed())

			fetched.Spec.Policy.Tolerations[0].Value = "value2"
			err := hubClient.Update(ctx, &fetched)
			testutils.ExpectValidationError(err, "tolerations have been updated/deleted, only additions to tolerations are allowed")

			Expect(hubClient.Delete(ctx, &crp)).Should(Succeed())
		})
	})

})
