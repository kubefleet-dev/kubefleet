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
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

// Tests for NamespaceWithResourceSelectors SelectionScope mode
var _ = Describe("CRP with NamespaceWithResourceSelectors selecting single namespace and specific resources", Ordered, func() {
	crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())
	testNamespace := fmt.Sprintf(workNamespaceNameTemplate, GinkgoParallelProcess())
	configMapName := fmt.Sprintf(appConfigMapNameTemplate, GinkgoParallelProcess())

	BeforeAll(func() {
		By("creating test namespace and resources")
		createWorkResources()
	})

	AfterAll(func() {
		By(fmt.Sprintf("deleting CRP and related resources %s", crpName))
		ensureCRPAndRelatedResourcesDeleted(crpName, allMemberClusters)
	})

	It("should create CRP with NamespaceWithResourceSelectors mode", func() {
		// Create CRP that selects a namespace and specific ConfigMap within it
		crp := &placementv1beta1.ClusterResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name:       crpName,
				Finalizers: []string{customDeletionBlockerFinalizer},
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
					{
						Group:          "",
						Kind:           "Namespace",
						Version:        "v1",
						Name:           testNamespace,
						SelectionScope: placementv1beta1.NamespaceWithResourceSelectors,
					},
					{
						Group:   "",
						Kind:    "ConfigMap",
						Version: "v1",
						Name:    configMapName,
					},
				},
			},
		}
		By(fmt.Sprintf("creating CRP %s", crpName))
		Expect(hubClient.Create(ctx, crp)).To(Succeed(), "Failed to create CRP %s", crpName)
	})

	It("should update CRP status as expected", func() {
		// Expected resources: Namespace + ConfigMap
		expectedResources := []placementv1beta1.ResourceIdentifier{
			{
				Group:   "",
				Kind:    "Namespace",
				Version: "v1",
				Name:    testNamespace,
			},
			{
				Group:     "",
				Kind:      "ConfigMap",
				Version:   "v1",
				Name:      configMapName,
				Namespace: testNamespace,
			},
		}
		crpStatusUpdatedActual := crpStatusUpdatedActual(expectedResources, allMemberClusterNames, nil, "0")
		Eventually(crpStatusUpdatedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update CRP %s status as expected", crpName)
	})

	It("should place the namespace and configmap on member clusters", func() {
		for idx := range allMemberClusters {
			cluster := allMemberClusters[idx]

			// Check namespace is placed
			ns := &corev1.Namespace{}
			Eventually(func() error {
				return cluster.KubeClient.Get(ctx, types.NamespacedName{Name: testNamespace}, ns)
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to get namespace %s on cluster %s", testNamespace, cluster.ClusterName)

			// Check configmap is placed
			cm := &corev1.ConfigMap{}
			Eventually(func() error {
				return cluster.KubeClient.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: testNamespace}, cm)
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to get configmap %s on cluster %s", configMapName, cluster.ClusterName)
		}
	})

	It("can delete the CRP", func() {
		crp := &placementv1beta1.ClusterResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name: crpName,
			},
		}
		Expect(hubClient.Delete(ctx, crp)).To(Succeed(), "Failed to delete CRP %s", crpName)
	})

	It("should remove placed resources from all member clusters", checkIfRemovedWorkResourcesFromAllMemberClusters)

	It("should remove controller finalizers from CRP", func() {
		finalizerRemovedActual := allFinalizersExceptForCustomDeletionBlockerRemovedFromPlacementActual(types.NamespacedName{Name: crpName})
		Eventually(finalizerRemovedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to remove controller finalizers from CRP %s", crpName)
	})
})

var _ = Describe("CRP with NamespaceWithResourceSelectors selecting namespace by label and resources", Ordered, func() {
	crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())
	testNamespace := fmt.Sprintf(workNamespaceNameTemplate, GinkgoParallelProcess())
	configMapName := fmt.Sprintf(appConfigMapNameTemplate, GinkgoParallelProcess())

	BeforeAll(func() {
		By("creating test namespace and resources")
		createWorkResources()
	})

	AfterAll(func() {
		By(fmt.Sprintf("deleting CRP and related resources %s", crpName))
		ensureCRPAndRelatedResourcesDeleted(crpName, allMemberClusters)
	})

	It("should create CRP with NamespaceWithResourceSelectors mode using label selector", func() {
		// Create CRP that selects namespace by label and ConfigMaps within it
		crp := &placementv1beta1.ClusterResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name:       crpName,
				Finalizers: []string{customDeletionBlockerFinalizer},
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
					{
						Group:          "",
						Kind:           "Namespace",
						Version:        "v1",
						SelectionScope: placementv1beta1.NamespaceWithResourceSelectors,
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								workNamespaceLabelName: strconv.Itoa(GinkgoParallelProcess()),
							},
						},
					},
					{
						Group:   "",
						Kind:    "ConfigMap",
						Version: "v1",
						Name:    configMapName,
					},
				},
			},
		}
		By(fmt.Sprintf("creating CRP %s", crpName))
		Expect(hubClient.Create(ctx, crp)).To(Succeed(), "Failed to create CRP %s", crpName)
	})

	It("should update CRP status as expected", func() {
		expectedResources := []placementv1beta1.ResourceIdentifier{
			{
				Group:   "",
				Kind:    "Namespace",
				Version: "v1",
				Name:    testNamespace,
			},
			{
				Group:     "",
				Kind:      "ConfigMap",
				Version:   "v1",
				Name:      configMapName,
				Namespace: testNamespace,
			},
		}
		crpStatusUpdatedActual := crpStatusUpdatedActual(expectedResources, allMemberClusterNames, nil, "0")
		Eventually(crpStatusUpdatedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update CRP %s status as expected", crpName)
	})

	It("should place the resources on member clusters", checkIfPlacedWorkResourcesOnAllMemberClusters)

	It("can delete the CRP", func() {
		crp := &placementv1beta1.ClusterResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name: crpName,
			},
		}
		Expect(hubClient.Delete(ctx, crp)).To(Succeed(), "Failed to delete CRP %s", crpName)
	})

	It("should remove placed resources from all member clusters", checkIfRemovedWorkResourcesFromAllMemberClusters)

	It("should remove controller finalizers from CRP", func() {
		finalizerRemovedActual := allFinalizersExceptForCustomDeletionBlockerRemovedFromPlacementActual(types.NamespacedName{Name: crpName})
		Eventually(finalizerRemovedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to remove controller finalizers from CRP %s", crpName)
	})
})

var _ = Describe("CRP with NamespaceWithResourceSelectors selecting only namespace", Ordered, func() {
	crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())
	testNamespace := fmt.Sprintf(workNamespaceNameTemplate, GinkgoParallelProcess())

	BeforeAll(func() {
		By("creating test namespace and resources")
		createWorkResources()
	})

	AfterAll(func() {
		By(fmt.Sprintf("deleting CRP and related resources %s", crpName))
		ensureCRPAndRelatedResourcesDeleted(crpName, allMemberClusters)
	})

	It("should create CRP with NamespaceWithResourceSelectors selecting only namespace", func() {
		// Create CRP that only selects a namespace, no other resources
		crp := &placementv1beta1.ClusterResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name:       crpName,
				Finalizers: []string{customDeletionBlockerFinalizer},
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
					{
						Group:          "",
						Kind:           "Namespace",
						Version:        "v1",
						Name:           testNamespace,
						SelectionScope: placementv1beta1.NamespaceWithResourceSelectors,
					},
				},
			},
		}
		By(fmt.Sprintf("creating CRP %s", crpName))
		Expect(hubClient.Create(ctx, crp)).To(Succeed(), "Failed to create CRP %s", crpName)
	})

	It("should update CRP status with only namespace", func() {
		expectedResources := []placementv1beta1.ResourceIdentifier{
			{
				Group:   "",
				Kind:    "Namespace",
				Version: "v1",
				Name:    testNamespace,
			},
		}
		crpStatusUpdatedActual := crpStatusUpdatedActual(expectedResources, allMemberClusterNames, nil, "0")
		Eventually(crpStatusUpdatedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update CRP %s status as expected", crpName)
	})

	It("should place only the namespace on member clusters", func() {
		for idx := range allMemberClusters {
			cluster := allMemberClusters[idx]

			// Check namespace is placed
			ns := &corev1.Namespace{}
			Eventually(func() error {
				return cluster.KubeClient.Get(ctx, types.NamespacedName{Name: testNamespace}, ns)
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to get namespace %s on cluster %s", testNamespace, cluster.ClusterName)
		}
	})

	It("can delete the CRP", func() {
		crp := &placementv1beta1.ClusterResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name: crpName,
			},
		}
		Expect(hubClient.Delete(ctx, crp)).To(Succeed(), "Failed to delete CRP %s", crpName)
	})

	It("should remove placed namespace from all member clusters", func() {
		for idx := range allMemberClusters {
			cluster := allMemberClusters[idx]
			Eventually(func() bool {
				ns := &corev1.Namespace{}
				err := cluster.KubeClient.Get(ctx, types.NamespacedName{Name: testNamespace}, ns)
				return err != nil
			}, eventuallyDuration, eventuallyInterval).Should(BeTrue(), "Namespace %s should be removed from cluster %s", testNamespace, cluster.ClusterName)
		}
	})

	It("should remove controller finalizers from CRP", func() {
		finalizerRemovedActual := allFinalizersExceptForCustomDeletionBlockerRemovedFromPlacementActual(types.NamespacedName{Name: crpName})
		Eventually(finalizerRemovedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to remove controller finalizers from CRP %s", crpName)
	})
})

var _ = Describe("CRP with NamespaceWithResourceSelectors with cluster-scoped resources", Ordered, func() {
	crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())
	testNamespace := fmt.Sprintf(workNamespaceNameTemplate, GinkgoParallelProcess())
	clusterRoleName := fmt.Sprintf("test-clusterrole-%d", GinkgoParallelProcess())

	BeforeAll(func() {
		By("creating test namespace")
		createWorkResources()
	})

	AfterAll(func() {
		By(fmt.Sprintf("deleting CRP and related resources %s", crpName))
		ensureCRPAndRelatedResourcesDeleted(crpName, allMemberClusters)
	})

	It("should create CRP with NamespaceWithResourceSelectors and cluster-scoped resource", func() {
		// NamespaceWithResourceSelectors mode should work with cluster-scoped resources too
		crp := &placementv1beta1.ClusterResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name:       crpName,
				Finalizers: []string{customDeletionBlockerFinalizer},
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
					{
						Group:          "",
						Kind:           "Namespace",
						Version:        "v1",
						Name:           testNamespace,
						SelectionScope: placementv1beta1.NamespaceWithResourceSelectors,
					},
					{
						Group:   "rbac.authorization.k8s.io",
						Kind:    "ClusterRole",
						Version: "v1",
						Name:    clusterRoleName,
					},
				},
			},
		}
		By(fmt.Sprintf("creating CRP %s", crpName))
		Expect(hubClient.Create(ctx, crp)).To(Succeed(), "Failed to create CRP %s", crpName)
	})

	It("should update CRP status with namespace only (ClusterRole doesn't exist)", func() {
		// Since ClusterRole doesn't exist, only namespace should be selected
		expectedResources := []placementv1beta1.ResourceIdentifier{
			{
				Group:   "",
				Kind:    "Namespace",
				Version: "v1",
				Name:    testNamespace,
			},
		}
		crpStatusUpdatedActual := crpStatusUpdatedActual(expectedResources, allMemberClusterNames, nil, "0")
		Eventually(crpStatusUpdatedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update CRP %s status as expected", crpName)
	})

	It("can delete the CRP", func() {
		crp := &placementv1beta1.ClusterResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name: crpName,
			},
		}
		Expect(hubClient.Delete(ctx, crp)).To(Succeed(), "Failed to delete CRP %s", crpName)
	})

	It("should remove placed resources from all member clusters", func() {
		for idx := range allMemberClusters {
			cluster := allMemberClusters[idx]
			Eventually(func() bool {
				ns := &corev1.Namespace{}
				err := cluster.KubeClient.Get(ctx, types.NamespacedName{Name: testNamespace}, ns)
				return err != nil
			}, eventuallyDuration, eventuallyInterval).Should(BeTrue(), "Namespace %s should be removed from cluster %s", testNamespace, cluster.ClusterName)
		}
	})

	It("should remove controller finalizers from CRP", func() {
		finalizerRemovedActual := allFinalizersExceptForCustomDeletionBlockerRemovedFromPlacementActual(types.NamespacedName{Name: crpName})
		Eventually(finalizerRemovedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to remove controller finalizers from CRP %s", crpName)
	})
})
