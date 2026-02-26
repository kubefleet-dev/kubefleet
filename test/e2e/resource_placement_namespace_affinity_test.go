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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/test/e2e/framework"
)

// The test suite below covers the namespace affinity scheduler plugin behavior.
// The plugin ensures that an RP is only scheduled onto clusters where the target
// namespace already exists (i.e. the namespace was placed there by a CRP).
//
// Test scenario:
//   - A CRP with PickFixed policy places the namespace onto 2 out of 3 member clusters.
//   - An RP with PickAll policy selects a ConfigMap in that namespace.
//   - The namespace affinity plugin should restrict the RP to only the 2 clusters
//     that actually have the namespace; the RP must still be considered successful.

var _ = Describe("placing namespaced scoped resources using an RP with PickAll policy and namespace affinity", Label("resourceplacement", "namespaceaffinity"), func() {
	crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())
	rpName := fmt.Sprintf(rpNameTemplate, GinkgoParallelProcess())
	rpKey := types.NamespacedName{Name: rpName, Namespace: appNamespace().Name}

	BeforeEach(OncePerOrdered, func() {
		// Create the work resources (namespace + configmap) on the hub cluster.
		createWorkResources()

		// Create a CRP with PickFixed policy that places the namespace on only 2 of the 3
		// member clusters. The namespace affinity plugin will later use the namespace
		// presence information propagated from these 2 clusters to filter RP scheduling.
		crp := &placementv1beta1.ClusterResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name:       crpName,
				Finalizers: []string{customDeletionBlockerFinalizer},
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: namespaceOnlySelector(),
				Policy: &placementv1beta1.PlacementPolicy{
					PlacementType: placementv1beta1.PickFixedPlacementType,
					ClusterNames: []string{
						memberCluster1EastProdName,
						memberCluster2EastCanaryName,
					},
				},
				Strategy: placementv1beta1.RolloutStrategy{
					Type: placementv1beta1.RollingUpdateRolloutStrategyType,
					RollingUpdate: &placementv1beta1.RollingUpdateConfig{
						UnavailablePeriodSeconds: ptr.To(2),
					},
				},
			},
		}
		Expect(hubClient.Create(ctx, crp)).To(Succeed(), "Failed to create CRP")

		// Wait until the CRP has placed the namespace on exactly the 2 fixed clusters.
		// With PickFixed, non-targeted clusters are not listed as unselected — pass nil.
		crpStatusUpdatedActual := crpStatusUpdatedActual(
			workNamespaceIdentifiers(),
			[]string{memberCluster1EastProdName, memberCluster2EastCanaryName},
			nil,
			"0",
		)
		Eventually(crpStatusUpdatedActual, workloadEventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update CRP status as expected")
	})

	AfterEach(OncePerOrdered, func() {
		ensureRPAndRelatedResourcesDeleted(rpKey, allMemberClusters)
		ensureCRPAndRelatedResourcesDeleted(crpName, allMemberClusters)
	})

	Context("namespace affinity restricts RP placement to clusters with the namespace", Ordered, func() {
		It("should create RP with PickAll policy successfully", func() {
			rp := &placementv1beta1.ResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name:       rpName,
					Namespace:  appNamespace().Name,
					Finalizers: []string{customDeletionBlockerFinalizer},
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: configMapSelector(),
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType: placementv1beta1.PickAllPlacementType,
					},
					Strategy: placementv1beta1.RolloutStrategy{
						Type: placementv1beta1.RollingUpdateRolloutStrategyType,
						RollingUpdate: &placementv1beta1.RollingUpdateConfig{
							UnavailablePeriodSeconds: ptr.To(2),
						},
					},
				},
			}
			Expect(hubClient.Create(ctx, rp)).To(Succeed(), "Failed to create RP")
		})

		It("should update RP status showing placement only on clusters with the namespace", func() {
			// The RP uses PickAll. Namespace affinity silently filters out cluster-3 (no namespace there),
			// so the scheduler considers the policy fulfilled — only cluster-1 and cluster-2 appear as selected.
			// cluster-3 does not appear as an unselected entry in RP status.
			// Use a longer timeout since namespace collection status must propagate before scheduling.
			rpStatusUpdatedActual := rpStatusUpdatedActual(
				appConfigMapIdentifiers(),
				[]string{memberCluster1EastProdName, memberCluster2EastCanaryName},
				nil,
				"0",
			)
			Eventually(rpStatusUpdatedActual, workloadEventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update RP status as expected")
		})

		It("should place the ConfigMap on the clusters that have the namespace", func() {
			for _, cluster := range []*framework.Cluster{memberCluster1EastProd, memberCluster2EastCanary} {
				resourcePlacedActual := workNamespaceAndConfigMapPlacedOnClusterActual(cluster)
				Eventually(resourcePlacedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to place resources on cluster %s", cluster.ClusterName)
			}
		})

		It("should not place the ConfigMap on the cluster without the namespace", func() {
			checkIfRemovedConfigMapFromMemberClusters([]*framework.Cluster{memberCluster3WestProd})
		})
	})
})
