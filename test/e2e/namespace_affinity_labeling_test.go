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

package e2e

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils"
)

var _ = Describe("MemberCluster namespace affinity labeling", Label("resourceplacement", "namespaceaffinity"), Ordered, func() {
	crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())
	testNamespace1 := fmt.Sprintf("test-ns1-%d", GinkgoParallelProcess())
	testNamespace2 := fmt.Sprintf("test-ns2-%d", GinkgoParallelProcess())

	BeforeAll(func() {
		By("creating test namespaces on hub cluster")
		// Create test namespaces on hub cluster that we'll select in our CRPs
		for _, nsName := range []string{testNamespace1, testNamespace2} {
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: nsName,
					Labels: map[string]string{
						"test-label": "e2e-namespace-affinity",
					},
				},
			}
			Expect(hubClient.Create(ctx, ns)).Should(SatisfyAny(Succeed(), &utils.AlreadyExistMatcher{}))
		}
	})

	AfterAll(func() {
		By("cleaning up test namespaces on hub cluster")
		for _, nsName := range []string{testNamespace1, testNamespace2} {
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nsName}}
			_ = hubClient.Delete(ctx, ns) // Ignore errors as namespace might not exist
		}

		By("cleaning up CRP")
		ensureCRPAndRelatedResourcesDeleted(crpName, allMemberClusters)
	})

	Context("when CRP selects namespaces directly", func() {
		It("should add namespace affinity labels to MemberClusters", func() {
			By("creating a CRP that selects test namespaces")
			crp := &placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{{
						Group:   "",
						Version: "v1",
						Kind:    "Namespace",
						Name:    testNamespace1,
					}, {
						Group:   "",
						Version: "v1",
						Kind:    "Namespace",
						Name:    testNamespace2,
					},
					},
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType: placementv1beta1.PickAllPlacementType,
					},
				},
			}
			Expect(hubClient.Create(ctx, crp)).To(Succeed(), "Failed to create CRP")

			By("verifying CRP is applied successfully on all member clusters")
			namespaceResourceIdentifiers := []placementv1beta1.ResourceIdentifier{
				{
					Group:     "",
					Version:   "v1",
					Kind:      "Namespace",
					Name:      testNamespace1,
					Namespace: "",
				},
				{
					Group:     "",
					Version:   "v1",
					Kind:      "Namespace",
					Name:      testNamespace2,
					Namespace: "",
				},
			}
			crpStatusUpdatedActual := crpStatusUpdatedActual(namespaceResourceIdentifiers, allMemberClusterNames, nil, "0")
			Eventually(crpStatusUpdatedActual, eventuallyDuration*2, eventuallyInterval).Should(Succeed(),
				"Failed to apply CRP on all member clusters")

			By("verifying MemberCluster objects get namespace affinity labels")
			expectedLabelKey1 := "kubernetes-fleet.io/namespace-" + testNamespace1
			expectedLabelKey2 := "kubernetes-fleet.io/namespace-" + testNamespace2

			for _, cluster := range allMemberClusters {
				mcName := cluster.ClusterName
				Eventually(func() bool {
					var mc clusterv1beta1.MemberCluster
					err := hubClient.Get(ctx, types.NamespacedName{Name: mcName}, &mc)
					if err != nil {
						return false
					}

					// Check if both namespace affinity labels are present
					val1, exists1 := mc.Labels[expectedLabelKey1]
					val2, exists2 := mc.Labels[expectedLabelKey2]

					return exists1 && exists2 && val1 == crpName && val2 == crpName
				}, eventuallyDuration, eventuallyInterval).Should(BeTrue(),
					"MemberCluster %s should have namespace affinity labels for both test namespaces", mcName)
			}
		})
	})

	Context("when CRP placement fails", func() {
		It("should remove namespace affinity labels when CRP is deleted", func() {
			By("deleting the CRP")
			crp := &placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{Name: crpName},
			}
			Expect(hubClient.Delete(ctx, crp)).To(Succeed(), "Failed to delete CRP")

			By("verifying namespace affinity labels are removed from MemberClusters")
			expectedLabelKey1 := "kubernetes-fleet.io/namespace-" + testNamespace1
			expectedLabelKey2 := "kubernetes-fleet.io/namespace-" + testNamespace2

			for _, cluster := range allMemberClusters {
				mcName := cluster.ClusterName
				Eventually(func() bool {
					var mc clusterv1beta1.MemberCluster
					err := hubClient.Get(ctx, types.NamespacedName{Name: mcName}, &mc)
					if err != nil {
						return false
					}

					// Labels should be removed
					_, exists1 := mc.Labels[expectedLabelKey1]
					_, exists2 := mc.Labels[expectedLabelKey2]

					return !exists1 && !exists2
				}, eventuallyDuration, eventuallyInterval).Should(BeTrue(),
					"MemberCluster %s should not have namespace affinity labels after CRP deletion", mcName)
			}
		})
	})

	Context("when CRP targets non-existent namespaces", func() {
		It("should not add labels for failed placements", func() {
			nonExistentNS := fmt.Sprintf("non-existent-ns-%d", GinkgoParallelProcess())
			crpNameFail := fmt.Sprintf("crp-fail-%d", GinkgoParallelProcess())

			defer func() {
				By("cleaning up failed CRP")
				ensureCRPAndRelatedResourcesDeleted(crpNameFail, allMemberClusters)
			}()

			By("creating a CRP that selects non-existent namespace")
			crp := &placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpNameFail,
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{{
						Group:   "",
						Version: "v1",
						Kind:    "Namespace",
						Name:    nonExistentNS,
					}},
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType: placementv1beta1.PickAllPlacementType,
					},
				},
			}
			Expect(hubClient.Create(ctx, crp)).To(Succeed(), "Failed to create CRP")

			By("verifying MemberCluster objects do not get namespace affinity labels for failed placement")
			expectedLabelKey := "kubernetes-fleet.io/namespace-" + nonExistentNS

			for _, cluster := range allMemberClusters {
				mcName := cluster.ClusterName
				Consistently(func() bool {
					var mc clusterv1beta1.MemberCluster
					err := hubClient.Get(ctx, types.NamespacedName{Name: mcName}, &mc)
					if err != nil {
						return true // If we can't get MC, assume no label
					}

					// Label should NOT be present for failed placement
					_, exists := mc.Labels[expectedLabelKey]
					return !exists
				}, eventuallyDuration, eventuallyInterval).Should(BeTrue(),
					"MemberCluster %s should not have namespace affinity labels for failed placement", mcName)
			}
		})
	})

	Context("when multiple CRPs target the same namespace", func() {
		It("should handle label conflicts appropriately", func() {
			crpName2 := fmt.Sprintf("crp2-%d", GinkgoParallelProcess())
			sharedNamespace := fmt.Sprintf("shared-ns-%d", GinkgoParallelProcess())

			defer func() {
				By("cleaning up both CRPs")
				ensureCRPAndRelatedResourcesDeleted(crpName2, allMemberClusters)
				// Note: original crp cleanup is handled in AfterAll
			}()

			By("creating shared namespace on hub cluster")
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: sharedNamespace,
				},
			}
			Expect(hubClient.Create(ctx, ns)).Should(SatisfyAny(Succeed(), &utils.AlreadyExistMatcher{}))

			By("creating first CRP targeting shared namespace")
			crp1 := &placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{Name: crpName},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{{
						Group:   "",
						Version: "v1",
						Kind:    "Namespace",
						Name:    sharedNamespace,
					}},
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType: placementv1beta1.PickAllPlacementType,
					},
				},
			}
			Expect(hubClient.Create(ctx, crp1)).To(Succeed())

			By("creating second CRP targeting the same shared namespace")
			crp2 := &placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{Name: crpName2},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{{
						Group:   "",
						Version: "v1",
						Kind:    "Namespace",
						Name:    sharedNamespace,
					}},
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType: placementv1beta1.PickAllPlacementType,
					},
				},
			}
			Expect(hubClient.Create(ctx, crp2)).To(Succeed())

			By("verifying exactly one MemberCluster label exists from the successful CRP")
			expectedLabelKey := "kubernetes-fleet.io/namespace-" + sharedNamespace

			for _, cluster := range allMemberClusters {
				mcName := cluster.ClusterName
				Eventually(func() bool {
					var mc clusterv1beta1.MemberCluster
					err := hubClient.Get(ctx, types.NamespacedName{Name: mcName}, &mc)
					if err != nil {
						return false
					}

					// Label should exist with exactly one of the CRP names (whichever succeeded)
					val, exists := mc.Labels[expectedLabelKey]
					return exists && (val == crpName || val == crpName2)
				}, eventuallyDuration, eventuallyInterval).Should(BeTrue(),
					"MemberCluster %s should have namespace affinity label from the successful CRP", mcName)

				// Verify there's exactly one label (not conflicting labels)
				Eventually(func() bool {
					var mc clusterv1beta1.MemberCluster
					err := hubClient.Get(ctx, types.NamespacedName{Name: mcName}, &mc)
					if err != nil {
						return false
					}

					// Count namespace affinity labels - should be exactly 1
					count := 0
					for k := range mc.Labels {
						if k == expectedLabelKey {
							count++
						}
					}
					return count == 1
				}, eventuallyDuration, eventuallyInterval).Should(BeTrue(),
					"MemberCluster %s should have exactly one namespace affinity label", mcName)
			}

			By("cleaning up shared namespace")
			ns = &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: sharedNamespace}}
			Expect(hubClient.Delete(ctx, ns)).Should(SatisfyAny(Succeed(), &utils.NotFoundMatcher{}))
		})
	})
})
