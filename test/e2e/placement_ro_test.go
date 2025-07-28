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

	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	scheduler "github.com/kubefleet-dev/kubefleet/pkg/scheduler/framework"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/condition"
)

// Note that this container will run in parallel with other containers.
var _ = Context("creating resourceOverride (selecting all clusters) to override configMap", Ordered, func() {
	crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())
	roName := fmt.Sprintf(roNameTemplate, GinkgoParallelProcess())
	roNamespace := fmt.Sprintf(workNamespaceNameTemplate, GinkgoParallelProcess())

	BeforeAll(func() {
		By("creating work resources")
		createWorkResources()
		// Create the CRP.
		createCRP(crpName)
		// Create the ro.
		ro := &placementv1beta1.ResourceOverride{
			ObjectMeta: metav1.ObjectMeta{
				Name:      roName,
				Namespace: roNamespace,
			},
			Spec: placementv1beta1.ResourceOverrideSpec{
				Placement: &placementv1beta1.PlacementRef{
					Name: crpName, // assigned CRP name
				},
				ResourceSelectors: configMapSelector(),
				Policy: &placementv1beta1.OverridePolicy{
					OverrideRules: []placementv1beta1.OverrideRule{
						{
							ClusterSelector: &placementv1beta1.ClusterSelector{
								ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{},
							},
							JSONPatchOverrides: []placementv1beta1.JSONPatchOverride{
								{
									Operator: placementv1beta1.JSONPatchOverrideOpAdd,
									Path:     "/metadata/annotations",
									Value:    apiextensionsv1.JSON{Raw: []byte(fmt.Sprintf(`{"%s": "%s"}`, roTestAnnotationKey, roTestAnnotationValue))},
								},
							},
						},
					},
				},
			},
		}
		By(fmt.Sprintf("creating resourceOverride %s", roName))
		Expect(hubClient.Create(ctx, ro)).To(Succeed(), "Failed to create resourceOverride %s", roName)
	})

	AfterAll(func() {
		By(fmt.Sprintf("deleting resourceOverride %s", roName))
		cleanupResourceOverride(roName, roNamespace)

		By("should update CRP status to not select any override")
		crpStatusUpdatedActual := crpStatusWithOverrideUpdatedActual(workResourceIdentifiers(), allMemberClusterNames, "0", nil, nil)
		Eventually(crpStatusUpdatedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update CRP %s status as expected", crpName)

		By("should not have annotations on the configmap")
		for _, memberCluster := range allMemberClusters {
			Expect(validateConfigMapNoAnnotationKeyOnCluster(memberCluster, roTestAnnotationKey)).Should(Succeed(), "Failed to remove the annotation of config map on %s", memberCluster.ClusterName)
		}

		By(fmt.Sprintf("deleting placement %s and related resources", crpName))
		ensureCRPAndRelatedResourcesDeleted(crpName, allMemberClusters)
	})

	It("should update CRP status as expected", func() {
		wantRONames := []placementv1beta1.NamespacedName{
			{Namespace: roNamespace, Name: fmt.Sprintf(placementv1beta1.OverrideSnapshotNameFmt, roName, 0)},
		}
		crpStatusUpdatedActual := crpStatusWithOverrideUpdatedActual(workResourceIdentifiers(), allMemberClusterNames, "0", nil, wantRONames)
		Eventually(crpStatusUpdatedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update CRP %s status as expected", crpName)
	})

	// This check will ignore the annotation of resources.
	It("should place the selected resources on member clusters", checkIfPlacedWorkResourcesOnAllMemberClusters)

	It("should have override annotations on the configmap", func() {
		want := map[string]string{roTestAnnotationKey: roTestAnnotationValue}
		checkIfOverrideAnnotationsOnAllMemberClusters(false, want)
	})

	It("update ro attached to this CRP only and change annotation value", func() {
		Eventually(func() error {
			ro := &placementv1beta1.ResourceOverride{}
			if err := hubClient.Get(ctx, types.NamespacedName{Name: roName, Namespace: roNamespace}, ro); err != nil {
				return err
			}
			ro.Spec = placementv1beta1.ResourceOverrideSpec{
				Placement: &placementv1beta1.PlacementRef{
					Name: crpName,
				},
				ResourceSelectors: configMapSelector(),
				Policy: &placementv1beta1.OverridePolicy{
					OverrideRules: []placementv1beta1.OverrideRule{
						{
							ClusterSelector: &placementv1beta1.ClusterSelector{
								ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{},
							},
							JSONPatchOverrides: []placementv1beta1.JSONPatchOverride{
								{
									Operator: placementv1beta1.JSONPatchOverrideOpAdd,
									Path:     "/metadata/annotations",
									Value:    apiextensionsv1.JSON{Raw: []byte(fmt.Sprintf(`{"%s": "%s"}`, roTestAnnotationKey, roTestAnnotationValue1))},
								},
							},
						},
					},
				},
			}
			return hubClient.Update(ctx, ro)
		}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update ro as expected", crpName)
	})

	It("should update CRP status as expected", func() {
		wantRONames := []placementv1beta1.NamespacedName{
			{Namespace: roNamespace, Name: fmt.Sprintf(placementv1beta1.OverrideSnapshotNameFmt, roName, 1)},
		}
		crpStatusUpdatedActual := crpStatusWithOverrideUpdatedActual(workResourceIdentifiers(), allMemberClusterNames, "0", nil, wantRONames)
		Eventually(crpStatusUpdatedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update CRP %s status as expected", crpName)
	})

	// This check will ignore the annotation of resources.
	It("should place the selected resources on member clusters", checkIfPlacedWorkResourcesOnAllMemberClusters)

	It("should have override annotations on the configmap", func() {
		want := map[string]string{roTestAnnotationKey: roTestAnnotationValue1}
		checkIfOverrideAnnotationsOnAllMemberClusters(false, want)
	})

	It("update ro attached to this CRP only and no update on the configmap itself", func() {
		Eventually(func() error {
			ro := &placementv1beta1.ResourceOverride{}
			if err := hubClient.Get(ctx, types.NamespacedName{Name: roName, Namespace: roNamespace}, ro); err != nil {
				return err
			}
			ro.Spec.Policy.OverrideRules = append(ro.Spec.Policy.OverrideRules, placementv1beta1.OverrideRule{
				ClusterSelector: &placementv1beta1.ClusterSelector{
					ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"invalid-key": "invalid-value",
								},
							},
						},
					},
				},
				OverrideType: placementv1beta1.DeleteOverrideType,
			})
			return hubClient.Update(ctx, ro)
		}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update ro as expected", crpName)
	})

	It("should refresh the CRP status even as there is no change on the resources", func() {
		wantRONames := []placementv1beta1.NamespacedName{
			{Namespace: roNamespace, Name: fmt.Sprintf(placementv1beta1.OverrideSnapshotNameFmt, roName, 2)},
		}
		crpStatusUpdatedActual := crpStatusWithOverrideUpdatedActual(workResourceIdentifiers(), allMemberClusterNames, "0", nil, wantRONames)
		Eventually(crpStatusUpdatedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update CRP %s status as expected", crpName)
	})

	// This check will ignore the annotation of resources.
	It("should place the selected resources on member clusters", checkIfPlacedWorkResourcesOnAllMemberClusters)

	It("should have override annotations on the configmap", func() {
		want := map[string]string{roTestAnnotationKey: roTestAnnotationValue1}
		checkIfOverrideAnnotationsOnAllMemberClusters(false, want)
	})
})

var _ = Context("creating resourceOverride with multiple jsonPatchOverrides to override configMap", Ordered, func() {
	crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())
	roName := fmt.Sprintf(roNameTemplate, GinkgoParallelProcess())
	roNamespace := fmt.Sprintf(workNamespaceNameTemplate, GinkgoParallelProcess())
	roSnapShotName := fmt.Sprintf(placementv1beta1.OverrideSnapshotNameFmt, roName, 0)

	BeforeAll(func() {
		By("creating work resources")
		createWorkResources()

		ro := &placementv1beta1.ResourceOverride{
			ObjectMeta: metav1.ObjectMeta{
				Name:      roName,
				Namespace: roNamespace,
			},
			Spec: placementv1beta1.ResourceOverrideSpec{
				ResourceSelectors: configMapSelector(),
				Policy: &placementv1beta1.OverridePolicy{
					OverrideRules: []placementv1beta1.OverrideRule{
						{
							ClusterSelector: &placementv1beta1.ClusterSelector{
								ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{},
							},
							JSONPatchOverrides: []placementv1beta1.JSONPatchOverride{
								{
									Operator: placementv1beta1.JSONPatchOverrideOpAdd,
									Path:     "/metadata/annotations",
									Value:    apiextensionsv1.JSON{Raw: []byte(fmt.Sprintf(`{"%s": "%s"}`, roTestAnnotationKey, roTestAnnotationValue))},
								},
								{
									Operator: placementv1beta1.JSONPatchOverrideOpAdd,
									Path:     fmt.Sprintf("/metadata/annotations/%s", roTestAnnotationKey1),
									Value:    apiextensionsv1.JSON{Raw: []byte(fmt.Sprintf(`"%s"`, roTestAnnotationValue1))},
								},
							},
						},
					},
				},
			},
		}
		By(fmt.Sprintf("creating resourceOverride %s", roName))
		Expect(hubClient.Create(ctx, ro)).To(Succeed(), "Failed to create resourceOverride %s", roName)
		// wait until the snapshot is created so that the observed resource index is predictable.
		Eventually(func() error {
			roSnap := &placementv1beta1.ResourceOverrideSnapshot{}
			return hubClient.Get(ctx, types.NamespacedName{Name: roSnapShotName, Namespace: roNamespace}, roSnap)
		}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update ro as expected", crpName)

		// Create the CRP.
		createCRP(crpName)
	})

	AfterAll(func() {
		By(fmt.Sprintf("deleting placement %s and related resources", crpName))
		ensureCRPAndRelatedResourcesDeleted(crpName, allMemberClusters)

		By(fmt.Sprintf("deleting resourceOverride %s", roName))
		cleanupResourceOverride(roName, roNamespace)
	})

	It("should update CRP status as expected", func() {
		wantRONames := []placementv1beta1.NamespacedName{
			{Namespace: roNamespace, Name: roSnapShotName},
		}
		crpStatusUpdatedActual := crpStatusWithOverrideUpdatedActual(workResourceIdentifiers(), allMemberClusterNames, "0", nil, wantRONames)
		Eventually(crpStatusUpdatedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update CRP %s status as expected", crpName)
	})

	// This check will ignore the annotation of resources.
	It("should place the selected resources on member clusters", checkIfPlacedWorkResourcesOnAllMemberClusters)

	It("should have override annotations on the configmap", func() {
		wantAnnotations := map[string]string{roTestAnnotationKey: roTestAnnotationValue, roTestAnnotationKey1: roTestAnnotationValue1}
		checkIfOverrideAnnotationsOnAllMemberClusters(false, wantAnnotations)
	})

	It("update ro attached to an invalid CRP", func() {
		Eventually(func() error {
			ro := &placementv1beta1.ResourceOverride{}
			if err := hubClient.Get(ctx, types.NamespacedName{Name: roName, Namespace: roNamespace}, ro); err != nil {
				return err
			}
			ro.Spec.Placement = &placementv1beta1.PlacementRef{
				Name: "invalid-crp", // assigned CRP name
			}
			return hubClient.Update(ctx, ro)
		}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update ro as expected", crpName)
	})

	It("CRP status should not be changed", func() {
		wantRONames := []placementv1beta1.NamespacedName{
			{Namespace: roNamespace, Name: fmt.Sprintf(placementv1beta1.OverrideSnapshotNameFmt, roName, 0)},
		}
		crpStatusUpdatedActual := crpStatusWithOverrideUpdatedActual(workResourceIdentifiers(), allMemberClusterNames, "0", nil, wantRONames)
		Consistently(crpStatusUpdatedActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "CRP %s status has been changed", crpName)
	})

})

var _ = Context("creating resourceOverride with different rules for each cluster to override configMap", Ordered, func() {
	crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())
	roName := fmt.Sprintf(roNameTemplate, GinkgoParallelProcess())
	roNamespace := fmt.Sprintf(workNamespaceNameTemplate, GinkgoParallelProcess())

	BeforeAll(func() {
		By("creating work resources")
		createWorkResources()
		// Create the CRP.
		createCRP(crpName)
		// Create the ro.
		ro := &placementv1beta1.ResourceOverride{
			ObjectMeta: metav1.ObjectMeta{
				Name:      roName,
				Namespace: roNamespace,
			},
			Spec: placementv1beta1.ResourceOverrideSpec{
				Placement: &placementv1beta1.PlacementRef{
					Name: crpName, // assigned CRP name
				},
				ResourceSelectors: configMapSelector(),
				Policy: &placementv1beta1.OverridePolicy{
					OverrideRules: []placementv1beta1.OverrideRule{
						{
							ClusterSelector: &placementv1beta1.ClusterSelector{
								ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
									{
										LabelSelector: &metav1.LabelSelector{
											MatchLabels: map[string]string{regionLabelName: regionEast, envLabelName: envProd},
										},
									},
								},
							},
							JSONPatchOverrides: []placementv1beta1.JSONPatchOverride{
								{
									Operator: placementv1beta1.JSONPatchOverrideOpAdd,
									Path:     "/metadata/annotations",
									Value:    apiextensionsv1.JSON{Raw: []byte(fmt.Sprintf(`{"%s": "%s-0"}`, roTestAnnotationKey, roTestAnnotationValue))},
								},
							},
						},
						{
							ClusterSelector: &placementv1beta1.ClusterSelector{
								ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
									{
										LabelSelector: &metav1.LabelSelector{
											MatchLabels: map[string]string{regionLabelName: regionEast, envLabelName: envCanary},
										},
									},
								},
							},
							JSONPatchOverrides: []placementv1beta1.JSONPatchOverride{
								{
									Operator: placementv1beta1.JSONPatchOverrideOpAdd,
									Path:     "/metadata/annotations",
									Value:    apiextensionsv1.JSON{Raw: []byte(fmt.Sprintf(`{"%s": "%s-1"}`, roTestAnnotationKey, roTestAnnotationValue))},
								},
							},
						},
						{
							ClusterSelector: &placementv1beta1.ClusterSelector{
								ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
									{
										LabelSelector: &metav1.LabelSelector{
											MatchLabels: map[string]string{regionLabelName: regionWest, envLabelName: envProd},
										},
									},
								},
							},
							JSONPatchOverrides: []placementv1beta1.JSONPatchOverride{
								{
									Operator: placementv1beta1.JSONPatchOverrideOpAdd,
									Path:     "/metadata/annotations",
									Value:    apiextensionsv1.JSON{Raw: []byte(fmt.Sprintf(`{"%s": "%s-2"}`, roTestAnnotationKey, roTestAnnotationValue))},
								},
							},
						},
					},
				},
			},
		}
		By(fmt.Sprintf("creating resourceOverride %s", roName))
		Expect(hubClient.Create(ctx, ro)).To(Succeed(), "Failed to create resourceOverride %s", roName)
	})

	AfterAll(func() {
		By(fmt.Sprintf("deleting placement %s and related resources", crpName))
		ensureCRPAndRelatedResourcesDeleted(crpName, allMemberClusters)

		By(fmt.Sprintf("deleting resourceOverride %s", roName))
		cleanupResourceOverride(roName, roNamespace)
	})

	It("should update CRP status as expected", func() {
		wantRONames := []placementv1beta1.NamespacedName{
			{Namespace: roNamespace, Name: fmt.Sprintf(placementv1beta1.OverrideSnapshotNameFmt, roName, 0)},
		}
		crpStatusUpdatedActual := crpStatusWithOverrideUpdatedActual(workResourceIdentifiers(), allMemberClusterNames, "0", nil, wantRONames)
		Eventually(crpStatusUpdatedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update CRP %s status as expected", crpName)
	})

	// This check will ignore the annotation of resources.
	It("should place the selected resources on member clusters", checkIfPlacedWorkResourcesOnAllMemberClusters)

	It("should have override annotations on the configmap", func() {
		for i, cluster := range allMemberClusters {
			wantAnnotations := map[string]string{roTestAnnotationKey: fmt.Sprintf("%s-%d", roTestAnnotationValue, i)}
			Expect(validateOverrideAnnotationOfConfigMapOnCluster(cluster, wantAnnotations)).Should(Succeed(), "Failed to override the annotation of configmap on %s", cluster.ClusterName)
		}
	})
})

var _ = Context("creating resourceOverride and clusterResourceOverride, resourceOverride should win to override configMap", Ordered, func() {
	crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())
	croName := fmt.Sprintf(croNameTemplate, GinkgoParallelProcess())
	roName := fmt.Sprintf(roNameTemplate, GinkgoParallelProcess())
	roNamespace := fmt.Sprintf(workNamespaceNameTemplate, GinkgoParallelProcess())
	roSnapShotName := fmt.Sprintf(placementv1beta1.OverrideSnapshotNameFmt, roName, 0)
	croSnapShotName := fmt.Sprintf(placementv1beta1.OverrideSnapshotNameFmt, croName, 0)

	BeforeAll(func() {
		By("creating work resources")
		createWorkResources()
		cro := &placementv1beta1.ClusterResourceOverride{
			ObjectMeta: metav1.ObjectMeta{
				Name: croName,
			},
			Spec: placementv1beta1.ClusterResourceOverrideSpec{
				ClusterResourceSelectors: workResourceSelector(),
				Policy: &placementv1beta1.OverridePolicy{
					OverrideRules: []placementv1beta1.OverrideRule{
						{
							ClusterSelector: &placementv1beta1.ClusterSelector{
								ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{},
							},
							JSONPatchOverrides: []placementv1beta1.JSONPatchOverride{
								{
									Operator: placementv1beta1.JSONPatchOverrideOpAdd,
									Path:     "/metadata/annotations",
									Value:    apiextensionsv1.JSON{Raw: []byte(fmt.Sprintf(`{"%s": "%s"}`, croTestAnnotationKey, croTestAnnotationValue))},
								},
							},
						},
					},
				},
			},
		}
		By(fmt.Sprintf("creating clusterResourceOverride %s", croName))
		Expect(hubClient.Create(ctx, cro)).To(Succeed(), "Failed to create clusterResourceOverride %s", croName)
		ro := &placementv1beta1.ResourceOverride{
			ObjectMeta: metav1.ObjectMeta{
				Name:      roName,
				Namespace: roNamespace,
			},
			Spec: placementv1beta1.ResourceOverrideSpec{
				ResourceSelectors: configMapSelector(),
				Policy: &placementv1beta1.OverridePolicy{
					OverrideRules: []placementv1beta1.OverrideRule{
						{
							ClusterSelector: &placementv1beta1.ClusterSelector{
								ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{},
							},
							JSONPatchOverrides: []placementv1beta1.JSONPatchOverride{
								{
									Operator: placementv1beta1.JSONPatchOverrideOpAdd,
									Path:     "/metadata/annotations",
									Value:    apiextensionsv1.JSON{Raw: []byte(fmt.Sprintf(`{"%s": "%s"}`, roTestAnnotationKey, roTestAnnotationValue))},
								},
							},
						},
					},
				},
			},
		}
		By(fmt.Sprintf("creating resourceOverride %s", roName))
		Expect(hubClient.Create(ctx, ro)).To(Succeed(), "Failed to create resourceOverride %s", roName)
		// wait until the snapshot is created so that the observed resource index is predictable.
		Eventually(func() error {
			roSnap := &placementv1beta1.ResourceOverrideSnapshot{}
			return hubClient.Get(ctx, types.NamespacedName{Name: roSnapShotName, Namespace: roNamespace}, roSnap)
		}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update ro as expected", crpName)
		Eventually(func() error {
			croSnap := &placementv1beta1.ClusterResourceOverrideSnapshot{}
			return hubClient.Get(ctx, types.NamespacedName{Name: croSnapShotName}, croSnap)
		}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update ro as expected", crpName)

		// Create the CRP.
		createCRP(crpName)
	})

	AfterAll(func() {
		By(fmt.Sprintf("deleting placement %s and related resources", crpName))
		ensureCRPAndRelatedResourcesDeleted(crpName, allMemberClusters)

		By(fmt.Sprintf("deleting resourceOverride %s", roName))
		cleanupResourceOverride(roName, roNamespace)

		By(fmt.Sprintf("deleting clusterResourceOverride %s", croName))
		cleanupClusterResourceOverride(croName)
	})

	It("should update CRP status as expected", func() {
		wantRONames := []placementv1beta1.NamespacedName{
			{Namespace: roNamespace, Name: roSnapShotName},
		}
		wantCRONames := []string{croSnapShotName}
		crpStatusUpdatedActual := crpStatusWithOverrideUpdatedActual(workResourceIdentifiers(), allMemberClusterNames, "0", wantCRONames, wantRONames)
		Eventually(crpStatusUpdatedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update CRP %s status as expected", crpName)
	})

	// This check will ignore the annotation of resources.
	It("should place the selected resources on member clusters", checkIfPlacedWorkResourcesOnAllMemberClusters)

	It("should have resource override annotations on the configmap", func() {
		want := map[string]string{roTestAnnotationKey: roTestAnnotationValue}
		checkIfOverrideAnnotationsOnAllMemberClusters(false, want)
	})

	It("should not have cluster resource override annotations on the configmap, but present on configmap", func() {
		want := map[string]string{croTestAnnotationKey: croTestAnnotationValue}
		for _, cluster := range allMemberClusters {
			Expect(validateAnnotationOfWorkNamespaceOnCluster(cluster, want)).Should(Succeed(), "Failed to override the annotation of work namespace on %s", cluster.ClusterName)
			Expect(validateOverrideAnnotationOfConfigMapOnCluster(cluster, want)).ShouldNot(Succeed(), "ResourceOverride Should win, ClusterResourceOverride annotated on $s", cluster.ClusterName)
		}
	})
})

var _ = Context("creating resourceOverride with incorrect path", Ordered, func() {
	crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())
	roName := fmt.Sprintf(croNameTemplate, GinkgoParallelProcess())
	roNamespace := fmt.Sprintf(workNamespaceNameTemplate, GinkgoParallelProcess())
	roSnapShotName := fmt.Sprintf(placementv1beta1.OverrideSnapshotNameFmt, roName, 0)

	BeforeAll(func() {
		By("creating work resources")
		createWorkResources()
		// Create the bad ro.
		ro := &placementv1beta1.ResourceOverride{
			ObjectMeta: metav1.ObjectMeta{
				Name:      roName,
				Namespace: roNamespace,
			},
			Spec: placementv1beta1.ResourceOverrideSpec{
				Placement: &placementv1beta1.PlacementRef{
					Name: crpName, // assigned CRP name
				},
				ResourceSelectors: configMapSelector(),
				Policy: &placementv1beta1.OverridePolicy{
					OverrideRules: []placementv1beta1.OverrideRule{
						{
							ClusterSelector: &placementv1beta1.ClusterSelector{
								ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{},
							},
							JSONPatchOverrides: []placementv1beta1.JSONPatchOverride{
								{
									Operator: placementv1beta1.JSONPatchOverrideOpAdd,
									Path:     fmt.Sprintf("/metadata/annotations/%s", roTestAnnotationKey),
									Value:    apiextensionsv1.JSON{Raw: []byte(fmt.Sprintf(`"%s"`, roTestAnnotationValue))},
								},
							},
						},
					},
				},
			},
		}
		By(fmt.Sprintf("creating the bad resourceOverride %s", roName))
		Expect(hubClient.Create(ctx, ro)).To(Succeed(), "Failed to create resourceOverride %s", roName)
		// wait until the snapshot is created so that failed override won't block the rollout
		Eventually(func() error {
			roSnap := &placementv1beta1.ResourceOverrideSnapshot{}
			return hubClient.Get(ctx, types.NamespacedName{Name: roSnapShotName, Namespace: roNamespace}, roSnap)
		}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update ro as expected", crpName)

		// Create the CRP later
		createCRP(crpName)
	})

	AfterAll(func() {
		By(fmt.Sprintf("deleting placement %s and related resources", crpName))
		ensureCRPAndRelatedResourcesDeleted(crpName, allMemberClusters)

		By(fmt.Sprintf("deleting resourceOverride %s", roName))
		cleanupResourceOverride(roName, roNamespace)
	})

	It("should update CRP status as failed to override", func() {
		wantRONames := []placementv1beta1.NamespacedName{
			{Namespace: roNamespace, Name: roSnapShotName},
		}
		crpStatusUpdatedActual := crpStatusWithOverrideUpdatedFailedActual(workResourceIdentifiers(), allMemberClusterNames, "0", nil, wantRONames)
		Eventually(crpStatusUpdatedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update CRP %s status as expected", crpName)
	})

	// This check will ignore the annotation of resources.
	It("should not place the selected resources on member clusters", checkIfRemovedWorkResourcesFromAllMemberClusters)
})

var _ = Context("creating resourceOverride and resource becomes invalid after override", Ordered, func() {
	crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())
	roName := fmt.Sprintf(croNameTemplate, GinkgoParallelProcess())
	roNamespace := fmt.Sprintf(workNamespaceNameTemplate, GinkgoParallelProcess())

	BeforeAll(func() {
		By("creating work resources")
		createWorkResources()
		// Create the CRP.
		createCRP(crpName)
		// Create the ro.
		ro := &placementv1beta1.ResourceOverride{
			ObjectMeta: metav1.ObjectMeta{
				Name:      roName,
				Namespace: roNamespace,
			},
			Spec: placementv1beta1.ResourceOverrideSpec{
				Placement: &placementv1beta1.PlacementRef{
					Name: crpName, // assigned CRP name
				},
				ResourceSelectors: configMapSelector(),
				Policy: &placementv1beta1.OverridePolicy{
					OverrideRules: []placementv1beta1.OverrideRule{
						{
							ClusterSelector: &placementv1beta1.ClusterSelector{
								ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{},
							},
							JSONPatchOverrides: []placementv1beta1.JSONPatchOverride{
								{
									Operator: placementv1beta1.JSONPatchOverrideOpAdd,
									Path:     "/metadata/annotations",
									Value:    apiextensionsv1.JSON{Raw: []byte(fmt.Sprintf(`"%s"`, roTestAnnotationValue))},
								},
							},
						},
					},
				},
			},
		}
		By(fmt.Sprintf("creating resourceOverride %s", roName))
		Expect(hubClient.Create(ctx, ro)).To(Succeed(), "Failed to create resourceOverride %s", roName)
	})

	AfterAll(func() {
		By(fmt.Sprintf("deleting placement %s and related resources", crpName))
		ensureCRPAndRelatedResourcesDeleted(crpName, allMemberClusters)

		By(fmt.Sprintf("deleting resourceOverride %s", roName))
		cleanupResourceOverride(roName, roNamespace)
	})

	It("should update CRP status as expected", func() {
		wantRONames := []placementv1beta1.NamespacedName{
			{Namespace: roNamespace, Name: fmt.Sprintf(placementv1beta1.OverrideSnapshotNameFmt, roName, 0)},
		}
		crpStatusUpdatedActual := crpStatusWithWorkSynchronizedUpdatedFailedActual(workResourceIdentifiers(), allMemberClusterNames, "0", nil, wantRONames)
		Eventually(crpStatusUpdatedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update CRP %s status as expected", crpName)
	})

	// This check will ignore the annotation of resources.
	It("should not place the selected resources on member clusters", checkIfRemovedWorkResourcesFromAllMemberClusters)
})

var _ = Context("creating resourceOverride with a templated rules with cluster name to override configMap", Ordered, func() {
	crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())
	roName := fmt.Sprintf(roNameTemplate, GinkgoParallelProcess())
	roNamespace := fmt.Sprintf(workNamespaceNameTemplate, GinkgoParallelProcess())
	roSnapShotName := fmt.Sprintf(placementv1beta1.OverrideSnapshotNameFmt, roName, 0)

	BeforeAll(func() {
		By("creating work resources")
		createWorkResources()

		// Create the ro before crp so that the observed resource index is predictable.
		ro := &placementv1beta1.ResourceOverride{
			ObjectMeta: metav1.ObjectMeta{
				Name:      roName,
				Namespace: roNamespace,
			},
			Spec: placementv1beta1.ResourceOverrideSpec{
				ResourceSelectors: configMapSelector(),
				Policy: &placementv1beta1.OverridePolicy{
					OverrideRules: []placementv1beta1.OverrideRule{
						{
							ClusterSelector: &placementv1beta1.ClusterSelector{
								ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
									{
										LabelSelector: &metav1.LabelSelector{
											MatchExpressions: []metav1.LabelSelectorRequirement{
												{
													Key:      regionLabelName,
													Operator: metav1.LabelSelectorOpExists,
												},
											},
										},
									},
								},
							},
							JSONPatchOverrides: []placementv1beta1.JSONPatchOverride{
								{
									Operator: placementv1beta1.JSONPatchOverrideOpReplace,
									Path:     "/data/data",
									Value:    apiextensionsv1.JSON{Raw: []byte(fmt.Sprintf(`"%s"`, placementv1beta1.OverrideClusterNameVariable))},
								},
								{
									Operator: placementv1beta1.JSONPatchOverrideOpAdd,
									Path:     "/data/newField",
									Value:    apiextensionsv1.JSON{Raw: []byte(fmt.Sprintf(`"new-%s"`, placementv1beta1.OverrideClusterNameVariable))},
								},
							},
						},
					},
				},
			},
		}
		By(fmt.Sprintf("creating resourceOverride %s", roName))
		Expect(hubClient.Create(ctx, ro)).To(Succeed(), "Failed to create resourceOverride %s", roName)
		Eventually(func() error {
			roSnap := &placementv1beta1.ResourceOverrideSnapshot{}
			return hubClient.Get(ctx, types.NamespacedName{Name: roSnapShotName, Namespace: roNamespace}, roSnap)
		}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update ro as expected", crpName)

		// Create the CRP.
		createCRP(crpName)
	})

	AfterAll(func() {
		By(fmt.Sprintf("deleting placement %s and related resources", crpName))
		ensureCRPAndRelatedResourcesDeleted(crpName, allMemberClusters)

		By(fmt.Sprintf("deleting resourceOverride %s", roName))
		cleanupResourceOverride(roName, roNamespace)
	})

	It("should update CRP status as expected", func() {
		wantRONames := []placementv1beta1.NamespacedName{
			{Namespace: roNamespace, Name: roSnapShotName},
		}
		crpStatusUpdatedActual := crpStatusWithOverrideUpdatedActual(workResourceIdentifiers(), allMemberClusterNames, "0", nil, wantRONames)
		Eventually(crpStatusUpdatedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update CRP %s status as expected", crpName)
	})

	It("should have override configMap on the member clusters", func() {
		cmName := fmt.Sprintf(appConfigMapNameTemplate, GinkgoParallelProcess())
		cmNamespace := fmt.Sprintf(workNamespaceNameTemplate, GinkgoParallelProcess())
		for _, cluster := range allMemberClusters {
			wantConfigMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cmName,
					Namespace: cmNamespace,
				},
				Data: map[string]string{
					"data":     cluster.ClusterName,
					"newField": fmt.Sprintf("new-%s", cluster.ClusterName),
				},
			}
			configMapActual := configMapPlacedOnClusterActual(cluster, wantConfigMap)
			Eventually(configMapActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update configmap %s data as expected", cmName)
		}
	})
})

var _ = Context("creating resourceOverride with delete configMap", Ordered, func() {
	crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())
	roName := fmt.Sprintf(roNameTemplate, GinkgoParallelProcess())
	roNamespace := fmt.Sprintf(workNamespaceNameTemplate, GinkgoParallelProcess())
	roSnapShotName := fmt.Sprintf(placementv1beta1.OverrideSnapshotNameFmt, roName, 0)

	BeforeAll(func() {
		By("creating work resources")
		createWorkResources()

		// Create the ro before crp so that the observed resource index is predictable.
		ro := &placementv1beta1.ResourceOverride{
			ObjectMeta: metav1.ObjectMeta{
				Name:      roName,
				Namespace: roNamespace,
			},
			Spec: placementv1beta1.ResourceOverrideSpec{
				ResourceSelectors: configMapSelector(),
				Policy: &placementv1beta1.OverridePolicy{
					OverrideRules: []placementv1beta1.OverrideRule{
						{
							ClusterSelector: &placementv1beta1.ClusterSelector{
								ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
									{
										LabelSelector: &metav1.LabelSelector{
											MatchLabels: map[string]string{regionLabelName: regionEast},
										},
									},
								},
							},
							JSONPatchOverrides: []placementv1beta1.JSONPatchOverride{
								{
									Operator: placementv1beta1.JSONPatchOverrideOpAdd,
									Path:     "/metadata/annotations",
									Value:    apiextensionsv1.JSON{Raw: []byte(fmt.Sprintf(`{"%s": "%s"}`, roTestAnnotationKey, roTestAnnotationValue))},
								},
							},
						},
						{
							ClusterSelector: &placementv1beta1.ClusterSelector{
								ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
									{
										LabelSelector: &metav1.LabelSelector{
											MatchLabels: map[string]string{regionLabelName: regionWest},
										},
									},
								},
							},
							OverrideType: placementv1beta1.DeleteOverrideType,
						},
					},
				},
			},
		}
		By(fmt.Sprintf("creating resourceOverride %s", roName))
		Expect(hubClient.Create(ctx, ro)).To(Succeed(), "Failed to create resourceOverride %s", roName)
		Eventually(func() error {
			roSnap := &placementv1beta1.ResourceOverrideSnapshot{}
			return hubClient.Get(ctx, types.NamespacedName{Name: roSnapShotName, Namespace: roNamespace}, roSnap)
		}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update ro as expected", crpName)

		// Create the CRP.
		createCRP(crpName)
	})

	AfterAll(func() {
		By(fmt.Sprintf("deleting placement %s and related resources", crpName))
		ensureCRPAndRelatedResourcesDeleted(crpName, allMemberClusters)

		By(fmt.Sprintf("deleting resourceOverride %s", roName))
		cleanupResourceOverride(roName, roNamespace)
	})

	It("should update CRP status as expected", func() {
		wantRONames := []placementv1beta1.NamespacedName{
			{Namespace: roNamespace, Name: roSnapShotName},
		}
		crpStatusUpdatedActual := crpStatusWithOverrideUpdatedActual(workResourceIdentifiers(), allMemberClusterNames, "0", nil, wantRONames)
		Eventually(crpStatusUpdatedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update CRP %s status as expected", crpName)
	})

	It("should place the namespaces on all member clusters", func() {
		for idx := 0; idx < 3; idx++ {
			memberCluster := allMemberClusters[idx]
			workResourcesPlacedActual := workNamespacePlacedOnClusterActual(memberCluster)
			Eventually(workResourcesPlacedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to place work resources on member cluster %s", memberCluster.ClusterName)
		}
	})

	// This check will ignore the annotation of resources.
	It("should place the configmap on member clusters that are patched", func() {
		for idx := 0; idx < 2; idx++ {
			memberCluster := allMemberClusters[idx]
			workResourcesPlacedActual := workNamespaceAndConfigMapPlacedOnClusterActual(memberCluster)
			Eventually(workResourcesPlacedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to place work resources on member cluster %s", memberCluster.ClusterName)
		}
	})

	It("should have override annotations on the configmap on the member clusters that are patched", func() {
		for idx := 0; idx < 2; idx++ {
			cluster := allMemberClusters[idx]
			wantAnnotations := map[string]string{roTestAnnotationKey: roTestAnnotationValue}
			Expect(validateOverrideAnnotationOfConfigMapOnCluster(cluster, wantAnnotations)).Should(Succeed(), "Failed to override the annotation of configmap on %s", cluster.ClusterName)
		}
	})

	It("should not place the configmap on the member clusters that are deleted", func() {
		memberCluster := allMemberClusters[2]
		Consistently(func() bool {
			namespaceName := fmt.Sprintf(workNamespaceNameTemplate, GinkgoParallelProcess())
			configMapName := fmt.Sprintf(appConfigMapNameTemplate, GinkgoParallelProcess())
			configMap := corev1.ConfigMap{}
			err := memberCluster.KubeClient.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: namespaceName}, &configMap)
			return k8serrors.IsNotFound(err)
		}, consistentlyDuration, consistentlyInterval).Should(BeTrue(), "Failed to delete work resources on member cluster %s", memberCluster.ClusterName)
	})
})

var _ = Context("creating resourceOverride with a templated rules with cluster label key replacement", Ordered, func() {
	crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())
	roName := fmt.Sprintf(roNameTemplate, GinkgoParallelProcess())
	roNamespace := fmt.Sprintf(workNamespaceNameTemplate, GinkgoParallelProcess())

	BeforeAll(func() {
		By("creating work resources")
		createWorkResources()

		// Create the ro before crp so that the observed resource index is predictable.
		ro := &placementv1beta1.ResourceOverride{
			ObjectMeta: metav1.ObjectMeta{
				Name:      roName,
				Namespace: roNamespace,
			},
			Spec: placementv1beta1.ResourceOverrideSpec{
				Placement: &placementv1beta1.PlacementRef{
					Name: crpName, // assigned CRP name
				},
				ResourceSelectors: configMapSelector(),
				Policy: &placementv1beta1.OverridePolicy{
					OverrideRules: []placementv1beta1.OverrideRule{
						{
							ClusterSelector: &placementv1beta1.ClusterSelector{
								ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
									{
										LabelSelector: &metav1.LabelSelector{
											MatchExpressions: []metav1.LabelSelectorRequirement{
												{
													Key:      regionLabelName,
													Operator: metav1.LabelSelectorOpExists,
												},
												{
													Key:      envLabelName,
													Operator: metav1.LabelSelectorOpExists,
												},
											},
										},
									},
								},
							},
							JSONPatchOverrides: []placementv1beta1.JSONPatchOverride{
								{
									Operator: placementv1beta1.JSONPatchOverrideOpAdd,
									Path:     "/data/region",
									Value:    apiextensionsv1.JSON{Raw: []byte(fmt.Sprintf(`"%s%s}"`, placementv1beta1.OverrideClusterLabelKeyVariablePrefix, regionLabelName))},
								},
								{
									Operator: placementv1beta1.JSONPatchOverrideOpReplace,
									Path:     "/data/data",
									Value:    apiextensionsv1.JSON{Raw: []byte(fmt.Sprintf(`"newdata-%s%s}"`, placementv1beta1.OverrideClusterLabelKeyVariablePrefix, envLabelName))},
								},
							},
						},
					},
				},
			},
		}
		By(fmt.Sprintf("creating resourceOverride %s", roName))
		Expect(hubClient.Create(ctx, ro)).To(Succeed(), "Failed to create resourceOverride %s", roName)

		// Create the CRP.
		createCRP(crpName)
	})

	AfterAll(func() {
		By(fmt.Sprintf("deleting placement %s and related resources", crpName))
		ensureCRPAndRelatedResourcesDeleted(crpName, allMemberClusters)

		By(fmt.Sprintf("deleting resourceOverride %s", roName))
		cleanupResourceOverride(roName, roNamespace)
	})

	It("should update CRP status as expected", func() {
		wantRONames := []placementv1beta1.NamespacedName{
			{Namespace: roNamespace, Name: fmt.Sprintf(placementv1beta1.OverrideSnapshotNameFmt, roName, 0)},
		}
		crpStatusUpdatedActual := crpStatusWithOverrideUpdatedActual(workResourceIdentifiers(), allMemberClusterNames, "0", nil, wantRONames)
		Eventually(crpStatusUpdatedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update CRP %s status as expected", crpName)
	})

	It("should replace the cluster label key in the configMap", func() {
		cmName := fmt.Sprintf(appConfigMapNameTemplate, GinkgoParallelProcess())
		cmNamespace := fmt.Sprintf(workNamespaceNameTemplate, GinkgoParallelProcess())
		for _, cluster := range allMemberClusters {
			wantConfigMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cmName,
					Namespace: cmNamespace,
				},
				Data: map[string]string{
					"data":   fmt.Sprintf("newdata-%s", labelsByClusterName[cluster.ClusterName][envLabelName]),
					"region": labelsByClusterName[cluster.ClusterName][regionLabelName],
				},
			}
			configMapActual := configMapPlacedOnClusterActual(cluster, wantConfigMap)
			Eventually(configMapActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update configmap %s data as expected", cmName)
		}
	})

	It("should handle non-existent cluster label key gracefully", func() {
		By("Update the ResourceOverride to use a non-existent label key")
		Eventually(func() error {
			ro := &placementv1beta1.ResourceOverride{}
			if err := hubClient.Get(ctx, types.NamespacedName{Name: roName, Namespace: roNamespace}, ro); err != nil {
				return err
			}
			ro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Value = apiextensionsv1.JSON{Raw: []byte(fmt.Sprintf(`"%snon-existent-label}"`, placementv1beta1.OverrideClusterLabelKeyVariablePrefix))}
			return hubClient.Update(ctx, ro)
		}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update resourceOverride %s with non-existent label key", roName)

		By("Verify the CRP status should have one cluster failed to override while the rest stuck in rollout")
		Eventually(func() error {
			crp := &placementv1beta1.ClusterResourcePlacement{}
			if err := hubClient.Get(ctx, types.NamespacedName{Name: crpName}, crp); err != nil {
				return err
			}
			wantCondition := []metav1.Condition{
				{
					Type:               string(placementv1beta1.ClusterResourcePlacementScheduledConditionType),
					Status:             metav1.ConditionTrue,
					Reason:             scheduler.FullyScheduledReason,
					ObservedGeneration: crp.Generation,
				},
				{
					Type:               string(placementv1beta1.ClusterResourcePlacementRolloutStartedConditionType),
					Status:             metav1.ConditionFalse,
					Reason:             condition.RolloutNotStartedYetReason,
					ObservedGeneration: crp.Generation,
				},
			}
			if diff := cmp.Diff(crp.Status.Conditions, wantCondition, crpStatusCmpOptions...); diff != "" {
				return fmt.Errorf("CRP condition diff (-got, +want): %s", diff)
			}
			return nil
		}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "CRP %s failed to show the override failed and stuck in rollout", crpName)

		By("Verify the configMap remains unchanged")
		cmName := fmt.Sprintf(appConfigMapNameTemplate, GinkgoParallelProcess())
		cmNamespace := fmt.Sprintf(workNamespaceNameTemplate, GinkgoParallelProcess())
		for _, cluster := range allMemberClusters {
			wantConfigMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cmName,
					Namespace: cmNamespace,
				},
				Data: map[string]string{
					"data":   fmt.Sprintf("newdata-%s", labelsByClusterName[cluster.ClusterName][envLabelName]),
					"region": labelsByClusterName[cluster.ClusterName][regionLabelName],
				},
			}
			configMapActual := configMapPlacedOnClusterActual(cluster, wantConfigMap)
			Consistently(configMapActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "ConfigMap %s should remain unchanged", cmName)
		}
	})
})

var _ = Context("creating resourceOverride with non-exist label", Ordered, func() {
	crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())
	roName := fmt.Sprintf(croNameTemplate, GinkgoParallelProcess())
	roNamespace := fmt.Sprintf(workNamespaceNameTemplate, GinkgoParallelProcess())
	roSnapShotName := fmt.Sprintf(placementv1beta1.OverrideSnapshotNameFmt, roName, 0)

	BeforeAll(func() {
		By("creating work resources")
		createWorkResources()
		// Create the bad ro.
		ro := &placementv1beta1.ResourceOverride{
			ObjectMeta: metav1.ObjectMeta{
				Name:      roName,
				Namespace: roNamespace,
			},
			Spec: placementv1beta1.ResourceOverrideSpec{
				Placement: &placementv1beta1.PlacementRef{
					Name: crpName, // assigned CRP name
				},
				ResourceSelectors: configMapSelector(),
				Policy: &placementv1beta1.OverridePolicy{
					OverrideRules: []placementv1beta1.OverrideRule{
						{
							ClusterSelector: &placementv1beta1.ClusterSelector{
								ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
									{
										LabelSelector: &metav1.LabelSelector{
											MatchExpressions: []metav1.LabelSelectorRequirement{
												{
													Key:      regionLabelName,
													Operator: metav1.LabelSelectorOpExists,
												},
												{
													Key:      envLabelName,
													Operator: metav1.LabelSelectorOpExists,
												},
											},
										},
									},
								},
							},
							JSONPatchOverrides: []placementv1beta1.JSONPatchOverride{
								{
									Operator: placementv1beta1.JSONPatchOverrideOpAdd,
									Path:     "/data/region",
									Value:    apiextensionsv1.JSON{Raw: []byte(fmt.Sprintf(`"%s%s}"`, placementv1beta1.OverrideClusterLabelKeyVariablePrefix, "non-existent-label"))},
								},
								{
									Operator: placementv1beta1.JSONPatchOverrideOpReplace,
									Path:     "/data/data",
									Value:    apiextensionsv1.JSON{Raw: []byte(fmt.Sprintf(`"newdata-%s%s}"`, placementv1beta1.OverrideClusterLabelKeyVariablePrefix, envLabelName))},
								},
							},
						},
					},
				},
			},
		}
		By(fmt.Sprintf("creating the bad resourceOverride %s", roName))
		Expect(hubClient.Create(ctx, ro)).To(Succeed(), "Failed to create resourceOverride %s", roName)
		Eventually(func() error {
			roSnap := &placementv1beta1.ResourceOverrideSnapshot{}
			return hubClient.Get(ctx, types.NamespacedName{Name: roSnapShotName, Namespace: roNamespace}, roSnap)
		}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update ro as expected", crpName)

		// Create the CRP later so that failed override won't block the rollout
		createCRP(crpName)
	})

	AfterAll(func() {
		By(fmt.Sprintf("deleting placement %s and related resources", crpName))
		ensureCRPAndRelatedResourcesDeleted(crpName, allMemberClusters)

		By(fmt.Sprintf("deleting resourceOverride %s", roName))
		cleanupResourceOverride(roName, roNamespace)
	})

	It("should update CRP status as failed to override", func() {
		wantRONames := []placementv1beta1.NamespacedName{
			{Namespace: roNamespace, Name: roSnapShotName},
		}
		crpStatusUpdatedActual := crpStatusWithOverrideUpdatedFailedActual(workResourceIdentifiers(), allMemberClusterNames, "0", nil, wantRONames)
		Eventually(crpStatusUpdatedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to update CRP %s status as expected", crpName)
	})

	// This check will ignore the annotation of resources.
	It("should not place the selected resources on member clusters", checkIfRemovedWorkResourcesFromAllMemberClusters)
})

var _ = Context("creating resourceOverride in one namespace should not override resources in another namespace", Ordered, func() {
	// Use different parallel process numbers for the two namespaces to ensure uniqueness
	crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())
	roName := fmt.Sprintf(roNameTemplate, GinkgoParallelProcess())
	
	// First namespace will have the resourceoverride
	namespace1 := fmt.Sprintf(workNamespaceNameTemplate, GinkgoParallelProcess())
	
	// Second namespace will not have the resourceoverride
	namespace2 := fmt.Sprintf("%s-second", fmt.Sprintf(workNamespaceNameTemplate, GinkgoParallelProcess()))
	
	// ConfigMap names for both namespaces
	configMap1Name := fmt.Sprintf(appConfigMapNameTemplate, GinkgoParallelProcess())
	configMap2Name := configMap1Name

	BeforeAll(func() {
		By("creating namespace1 resources")
		// Create namespace1 with configmap
		ns1 := corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace1,
				Labels: map[string]string{
					workNamespaceLabelName: strconv.Itoa(GinkgoParallelProcess()),
				},
			},
		}
		Expect(hubClient.Create(ctx, &ns1)).To(Succeed(), "Failed to create namespace %s", ns1.Name)

		cm1 := corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      configMap1Name,
				Namespace: namespace1,
			},
			Data: map[string]string{
				"data": "test",
			},
		}
		Expect(hubClient.Create(ctx, &cm1)).To(Succeed(), "Failed to create config map %s in namespace %s", configMap1Name, namespace1)

		By("creating namespace2 resources")
		// Create namespace2 with configmap
		ns2 := corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace2,
				Labels: map[string]string{
					workNamespaceLabelName: strconv.Itoa(GinkgoParallelProcess()),
				},
			},
		}
		Expect(hubClient.Create(ctx, &ns2)).To(Succeed(), "Failed to create namespace %s", ns2.Name)

		cm2 := corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      configMap2Name,
				Namespace: namespace2,
			},
			Data: map[string]string{
				"data": "test",
			},
		}
		Expect(hubClient.Create(ctx, &cm2)).To(Succeed(), "Failed to create config map %s in namespace %s", configMap2Name, namespace2)

		By("creating CRP to place both namespaces")
		crp := &placementv1beta1.ClusterResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name: crpName,
			},
			Spec: placementv1beta1.ClusterResourcePlacementSpec{
				ResourceSelectors: []placementv1beta1.ClusterResourceSelector{
					{
						Group:   "",
						Kind:    "Namespace",
						Version: "v1",
						Name:    namespace1,
					},
					{
						Group:   "",
						Kind:    "Namespace",
						Version: "v1",
						Name:    namespace2,
					},
				},
				Strategy: placementv1beta1.RolloutStrategy{
					Type: placementv1beta1.RollingUpdateRolloutStrategyType,
				},
			},
		}
		Expect(hubClient.Create(ctx, crp)).To(Succeed(), "Failed to create CRP %s", crpName)

		By(fmt.Sprintf("creating resourceOverride %s in namespace %s only", roName, namespace1))
		ro := &placementv1alpha1.ResourceOverride{
			ObjectMeta: metav1.ObjectMeta{
				Name:      roName,
				Namespace: namespace1,
			},
			Spec: placementv1alpha1.ResourceOverrideSpec{
				Placement: &placementv1alpha1.PlacementRef{
					Name: crpName,
				},
				ResourceSelectors: []placementv1alpha1.ResourceSelector{
					{
						Group:   "",
						Kind:    "ConfigMap",
						Version: "v1",
						Name:    configMap1Name,
					},
				},
				Policy: &placementv1alpha1.OverridePolicy{
					OverrideRules: []placementv1alpha1.OverrideRule{
						{
							ClusterSelector: &placementv1beta1.ClusterSelector{
								ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{},
							},
							JSONPatchOverrides: []placementv1alpha1.JSONPatchOverride{
								{
									Operator: placementv1alpha1.JSONPatchOverrideOpAdd,
									Path:     "/metadata/annotations",
									Value:    apiextensionsv1.JSON{Raw: []byte(fmt.Sprintf(`{"%s": "%s"}`, roTestAnnotationKey, roTestAnnotationValue))},
								},
							},
						},
					},
				},
			},
		}
		Expect(hubClient.Create(ctx, ro)).To(Succeed(), "Failed to create resourceOverride %s", roName)
	})

	AfterAll(func() {
		By(fmt.Sprintf("deleting resourceOverride %s", roName))
		cleanupResourceOverride(roName, namespace1)

		By(fmt.Sprintf("deleting placement %s and related resources", crpName))
		ensureCRPAndRelatedResourcesDeleted(crpName, allMemberClusters)

		// Clean up namespaces
		By(fmt.Sprintf("deleting namespace %s", namespace1))
		ns1 := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace1,
			},
		}
		Expect(client.IgnoreNotFound(hubClient.Delete(ctx, ns1))).To(Succeed(), "Failed to delete namespace %s", namespace1)
		
		By(fmt.Sprintf("deleting namespace %s", namespace2))
		ns2 := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace2,
			},
		}
		Expect(client.IgnoreNotFound(hubClient.Delete(ctx, ns2))).To(Succeed(), "Failed to delete namespace %s", namespace2)
		
		// Wait for namespaces to be deleted
		Eventually(func() error {
			var ns1Check corev1.Namespace
			err1 := hubClient.Get(ctx, types.NamespacedName{Name: namespace1}, &ns1Check)
			if !k8serrors.IsNotFound(err1) {
				return fmt.Errorf("namespace %s still exists", namespace1)
			}
			
			var ns2Check corev1.Namespace
			err2 := hubClient.Get(ctx, types.NamespacedName{Name: namespace2}, &ns2Check)
			if !k8serrors.IsNotFound(err2) {
				return fmt.Errorf("namespace %s still exists", namespace2)
			}
			
			return nil
		}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Failed to delete namespaces")
	})

	It("should place both namespaces and their resources on member clusters", func() {
		for _, memberCluster := range allMemberClusters {
			// Check if namespace1 and its configmap are placed
			Eventually(func() error {
				var ns1 corev1.Namespace
				if err := memberCluster.KubeClient.Get(ctx, types.NamespacedName{Name: namespace1}, &ns1); err != nil {
					return fmt.Errorf("failed to get namespace %s on cluster %s: %w", namespace1, memberCluster.ClusterName, err)
				}
				
				var cm1 corev1.ConfigMap
				if err := memberCluster.KubeClient.Get(ctx, types.NamespacedName{Namespace: namespace1, Name: configMap1Name}, &cm1); err != nil {
					return fmt.Errorf("failed to get configmap %s in namespace %s on cluster %s: %w", 
						configMap1Name, namespace1, memberCluster.ClusterName, err)
				}
				
				// Check if namespace2 and its configmap are placed
				var ns2 corev1.Namespace
				if err := memberCluster.KubeClient.Get(ctx, types.NamespacedName{Name: namespace2}, &ns2); err != nil {
					return fmt.Errorf("failed to get namespace %s on cluster %s: %w", namespace2, memberCluster.ClusterName, err)
				}
				
				var cm2 corev1.ConfigMap
				if err := memberCluster.KubeClient.Get(ctx, types.NamespacedName{Namespace: namespace2, Name: configMap2Name}, &cm2); err != nil {
					return fmt.Errorf("failed to get configmap %s in namespace %s on cluster %s: %w", 
						configMap2Name, namespace2, memberCluster.ClusterName, err)
				}
				
				return nil
			}, workloadEventuallyDuration, eventuallyInterval).Should(Succeed(), 
				"Failed to place namespaces and resources on member cluster %s", memberCluster.ClusterName)
		}
	})

	It("should have override annotations on the configmap in namespace1", func() {
		wantAnnotations := map[string]string{roTestAnnotationKey: roTestAnnotationValue}
		
		for _, memberCluster := range allMemberClusters {
			Eventually(func() error {
				var cm1 corev1.ConfigMap
				if err := memberCluster.KubeClient.Get(ctx, types.NamespacedName{Namespace: namespace1, Name: configMap1Name}, &cm1); err != nil {
					return fmt.Errorf("failed to get configmap %s in namespace %s on cluster %s: %w", 
						configMap1Name, namespace1, memberCluster.ClusterName, err)
				}
				
				for key, value := range wantAnnotations {
					if actualValue, exists := cm1.Annotations[key]; !exists || actualValue != value {
						return fmt.Errorf("configmap %s in namespace %s on cluster %s does not have expected annotation %s: %s", 
							configMap1Name, namespace1, memberCluster.ClusterName, key, value)
					}
				}
				
				return nil
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), 
				"Failed to override the annotation of configmap in namespace %s on cluster %s", namespace1, memberCluster.ClusterName)
		}
	})

	It("should not have override annotations on the configmap in namespace2", func() {
		// The configmap in namespace2 should not have the annotations
		for _, memberCluster := range allMemberClusters {
			Consistently(func() bool {
				var cm2 corev1.ConfigMap
				err := memberCluster.KubeClient.Get(ctx, types.NamespacedName{Namespace: namespace2, Name: configMap2Name}, &cm2)
				if err != nil {
					return false
				}
				
				// Check that the configmap doesn't have the annotation
				_, hasAnnotation := cm2.Annotations[roTestAnnotationKey]
				return !hasAnnotation
			}, consistentlyDuration, consistentlyInterval).Should(BeTrue(), 
				"Configmap in namespace %s on cluster %s should not have override annotations", namespace2, memberCluster.ClusterName)
		}
	})
})
