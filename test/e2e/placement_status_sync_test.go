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
	"github.com/google/go-cmp/cmp/cmpopts"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

const (
	crpsEventuallyDuration = time.Second * 20
)

var (
	// Define comparison options for ignoring auto-generated and time-dependent fields.
	crpsCmpOpts = []cmp.Option{
		cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion", "UID", "CreationTimestamp", "Generation", "ManagedFields"),
		cmpopts.IgnoreFields(placementv1beta1.ClusterResourcePlacementStatus{}, "LastUpdatedTime"),
		cmpopts.IgnoreFields(metav1.Condition{}, "LastTransitionTime"),
	}
)

var _ = Describe("ClusterResourcePlacementStatus E2E Tests", Ordered, func() {
	Context("Create and Update ClusterResourcePlacementStatus, StatusReportingScope is NamespaceAccessible", func() {
		var crpName string
		var crp *placementv1beta1.ClusterResourcePlacement

		BeforeAll(func() {
			// Create test resources that will be selected by the CRP.
			createWorkResources()

			crpName = fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())
			crp = &placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name:       crpName,
					Finalizers: []string{customDeletionBlockerFinalizer},
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: workResourceSelector(),
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType:    placementv1beta1.PickNPlacementType,
						NumberOfClusters: ptr.To(int32(2)),
					},
					StatusReportingScope: placementv1beta1.NamespaceAccessible,
				},
			}
			Expect(hubClient.Create(ctx, crp)).To(Succeed(), "Failed to create CRP")
		})

		AfterAll(func() {
			ensureCRPAndRelatedResourcesDeleted(crpName, allMemberClusters)
		})

		It("should update CRP status with 2 clusters as expected", func() {
			expectedClusters := []string{memberCluster2EastCanaryName, memberCluster3WestProdName}
			statusUpdatedActual := crpStatusUpdatedActual(workResourceIdentifiers(), expectedClusters, nil, "0")
			Eventually(statusUpdatedActual, crpsEventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update CRP status with 2 clusters")
		})

		It("should sync ClusterResourcePlacementStatus with initial CRP status (2 clusters)", func() {
			crpsMatchesActual := crpsStatusMatchesCRPActual(crpName, appNamespace().Name, crp)
			Eventually(crpsMatchesActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "ClusterResourcePlacementStatus should match expected structure and CRP status for 2 clusters")
		})

		It("should update CRP to select 3 clusters", func() {
			// Update CRP to select 3 clusters.
			Eventually(func() error {
				if err := hubClient.Get(ctx, types.NamespacedName{Name: crpName}, crp); err != nil {
					return err
				}
				crp.Spec.Policy.NumberOfClusters = ptr.To(int32(3))
				return hubClient.Update(ctx, crp)
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update CRP to select 3 clusters")
		})

		It("should update CRP status with 3 clusters as expected", func() {
			statusUpdatedActual := crpStatusUpdatedActual(workResourceIdentifiers(), allMemberClusterNames, nil, "0")
			Eventually(statusUpdatedActual, crpsEventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update CRP status with 3 clusters")
		})

		It("should sync ClusterResourcePlacementStatus with updated CRP status (3 clusters)", func() {
			crpsMatchesActual := crpsStatusMatchesCRPActual(crpName, appNamespace().Name, crp)
			Eventually(crpsMatchesActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "ClusterResourcePlacementStatus should match expected structure and CRP status for 3 clusters")
		})
	})

	Context("StatusReportingScope is not NamespaceAccessible", func() {
		var crpName string
		var crp *placementv1beta1.ClusterResourcePlacement

		BeforeAll(func() {
			// Create test resources that will be selected by the CRP.
			createWorkResources()

			crpName = fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())
			crp = &placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name:       crpName,
					Finalizers: []string{customDeletionBlockerFinalizer},
				},
				Spec: placementv1beta1.PlacementSpec{
					ResourceSelectors: workResourceSelector(),
					Policy: &placementv1beta1.PlacementPolicy{
						PlacementType: placementv1beta1.PickAllPlacementType,
					},
					StatusReportingScope: placementv1beta1.ClusterScopeOnly,
				},
			}
		})

		AfterAll(func() {
			ensureCRPAndRelatedResourcesDeleted(crpName, allMemberClusters)
		})

		It("should create CRP with ClusterScopeOnly StatusReportingScope", func() {
			Expect(hubClient.Create(ctx, crp)).To(Succeed(), "Failed to create CRP")
		})

		It("should update CRP status as expected", func() {
			crpStatusUpdatedActual := crpStatusUpdatedActual(workResourceIdentifiers(), allMemberClusterNames, nil, "0")
			Eventually(crpStatusUpdatedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update CRP status as expected")
		})

		It("should not create ClusterResourcePlacementStatus when StatusReportingScope is ClusterScopeOnly", func() {
			crpStatus := &placementv1beta1.ClusterResourcePlacementStatus{}
			crpStatusKey := types.NamespacedName{
				Name:      crpName,
				Namespace: appNamespace().Name,
			}

			Consistently(func() bool {
				err := hubClient.Get(ctx, crpStatusKey, crpStatus)
				return err != nil // Should continue to return error (not found).
			}, 10*time.Second, 1*time.Second).Should(BeTrue(), "ClusterResourcePlacementStatus should not be created when StatusReportingScope is ClusterScopeOnly")
		})
	})
})

func crpsStatusMatchesCRPActual(crpName, targetNamespace string, crp *placementv1beta1.ClusterResourcePlacement) func() error {
	return func() error {
		crpStatus := &placementv1beta1.ClusterResourcePlacementStatus{}
		crpStatusKey := types.NamespacedName{
			Name:      crpName,
			Namespace: targetNamespace,
		}

		if err := hubClient.Get(ctx, crpStatusKey, crpStatus); err != nil {
			return fmt.Errorf("failed to get CRPS: %w", err)
		}

		// Get latest CRP status.
		if err := hubClient.Get(ctx, types.NamespacedName{Name: crpName}, crp); err != nil {
			return fmt.Errorf("failed to get CRP: %w", err)
		}

		// Construct expected CRPS.
		wantCRPS := &placementv1beta1.ClusterResourcePlacementStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name:      crpName,
				Namespace: targetNamespace,
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion:         placementv1beta1.GroupVersion.String(),
						Kind:               "ClusterResourcePlacement",
						Name:               crpName,
						UID:                crp.UID,
						Controller:         ptr.To(true),
						BlockOwnerDeletion: ptr.To(true),
					},
				},
			},
			PlacementStatus: crp.Status,
		}

		// Compare CRPS with expected, ignoring fields that vary.
		if diff := cmp.Diff(wantCRPS, crpStatus, crpsCmpOpts...); diff != "" {
			return fmt.Errorf("CRPS does not match expected (-want, +got): %s", diff)
		}

		return nil
	}
}
