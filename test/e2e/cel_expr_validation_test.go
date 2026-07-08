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
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

const (
	nonExistentNSName = "non-existent-namespace"
	nonExistentCMName = "non-existent-configmap"
	rpNamespace       = "default"
)

var _ = Describe("CEL expression based validation for CRPs", func() {
	AfterEach(func() {
		// Clean up the CRP created during each test case.
		crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())

		Eventually(func() error {
			crp := &placementv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
			}
			if err := hubClient.Delete(ctx, crp); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete CRP: %w", err)
			}

			if err := hubClient.Get(ctx, client.ObjectKey{Name: crpName}, &placementv1beta1.ClusterResourcePlacement{}); !apierrors.IsNotFound(err) {
				return fmt.Errorf("CRP still exists after deletion attempt (error: %w)", err)
			}
			return nil
		}, eventuallyDuration, eventuallyInterval).Should(Succeed())
	})

	It("cannot set name and label selector in a resource selector at the same time", func() {
		crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())

		crp := &placementv1beta1.ClusterResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name: crpName,
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
					{
						Group:   "",
						Version: "v1",
						Kind:    "Namespace",
						Name:    nonExistentNSName,
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"foo": "bar"},
						},
					},
				},
			},
		}

		err := hubClient.Create(ctx, crp)
		Expect(err).To(HaveOccurred(), "Expected error when creating CRP with both name and label selector in a resource selector")
		var statusErr *apierrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), "The returned error is not a StatusError")
		Expect(statusErr.Status().Message).Should(ContainSubstring("name and labelSelector are mutually exclusive in a resource selector"))
	})

	It("cannot set numberOfClusters when placementType is PickFixed", func() {
		crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())

		var numberOfClusters int32 = 1
		crp := &placementv1beta1.ClusterResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name: crpName,
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
					{
						Group:   "",
						Version: "v1",
						Kind:    "Namespace",
						Name:    nonExistentNSName,
					},
				},
				Policy: &placementv1beta1.PlacementPolicy{
					PlacementType:    placementv1beta1.PickFixedPlacementType,
					ClusterNames:     []string{memberCluster1EastProdName},
					NumberOfClusters: &numberOfClusters,
				},
			},
		}

		err := hubClient.Create(ctx, crp)
		Expect(err).To(HaveOccurred(), "Expected error when creating CRP with numberOfClusters set for PickFixed placementType")
		var statusErr *apierrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), "The returned error is not a StatusError")
		Expect(statusErr.Status().Message).Should(ContainSubstring("affinity, numberOfClusters, topologySpreadConstraints, and tolerations cannot be set when placementType is PickFixed"))
	})

	It("cannot set affinity when placementType is PickFixed", func() {
		crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())

		crp := &placementv1beta1.ClusterResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name: crpName,
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
					{
						Group:   "",
						Version: "v1",
						Kind:    "Namespace",
						Name:    nonExistentNSName,
					},
				},
				Policy: &placementv1beta1.PlacementPolicy{
					PlacementType: placementv1beta1.PickFixedPlacementType,
					ClusterNames:  []string{memberCluster1EastProdName},
					Affinity: &placementv1beta1.Affinity{
						ClusterAffinity: &placementv1beta1.ClusterAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &placementv1beta1.ClusterSelector{
								ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
									{
										LabelSelector: &metav1.LabelSelector{
											MatchLabels: map[string]string{"foo": "bar"},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		err := hubClient.Create(ctx, crp)
		Expect(err).To(HaveOccurred(), "Expected error when creating CRP with affinity set for PickFixed placementType")
		var statusErr *apierrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), "The returned error is not a StatusError")
		Expect(statusErr.Status().Message).Should(ContainSubstring("affinity, numberOfClusters, topologySpreadConstraints, and tolerations cannot be set when placementType is PickFixed"))
	})

	It("cannot set topologySpreadConstraints when placementType is PickFixed", func() {
		crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())

		crp := &placementv1beta1.ClusterResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name: crpName,
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
					{
						Group:   "",
						Version: "v1",
						Kind:    "Namespace",
						Name:    nonExistentNSName,
					},
				},
				Policy: &placementv1beta1.PlacementPolicy{
					PlacementType: placementv1beta1.PickFixedPlacementType,
					ClusterNames:  []string{memberCluster1EastProdName},
					TopologySpreadConstraints: []placementv1beta1.TopologySpreadConstraint{
						{
							TopologyKey: "topology.kubernetes.io/zone",
						},
					},
				},
			},
		}

		err := hubClient.Create(ctx, crp)
		Expect(err).To(HaveOccurred(), "Expected error when creating CRP with topologySpreadConstraints set for PickFixed placementType")
		var statusErr *apierrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), "The returned error is not a StatusError")
		Expect(statusErr.Status().Message).Should(ContainSubstring("affinity, numberOfClusters, topologySpreadConstraints, and tolerations cannot be set when placementType is PickFixed"))
	})

	It("cannot set tolerations when placementType is PickFixed", func() {
		crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())

		crp := &placementv1beta1.ClusterResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name: crpName,
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
					{
						Group:   "",
						Version: "v1",
						Kind:    "Namespace",
						Name:    nonExistentNSName,
					},
				},
				Policy: &placementv1beta1.PlacementPolicy{
					PlacementType: placementv1beta1.PickFixedPlacementType,
					ClusterNames:  []string{memberCluster1EastProdName},
					Tolerations: []placementv1beta1.Toleration{
						{
							Key:      "foo",
							Operator: corev1.TolerationOpEqual,
							Value:    "bar",
							Effect:   corev1.TaintEffectNoSchedule,
						},
					},
				},
			},
		}

		err := hubClient.Create(ctx, crp)
		Expect(err).To(HaveOccurred(), "Expected error when creating CRP with tolerations set for PickFixed placementType")
		var statusErr *apierrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), "The returned error is not a StatusError")
		Expect(statusErr.Status().Message).Should(ContainSubstring("affinity, numberOfClusters, topologySpreadConstraints, and tolerations cannot be set when placementType is PickFixed"))
	})

	It("cannot set clusterNames when placementType is PickAll", func() {
		crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())

		crp := &placementv1beta1.ClusterResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name: crpName,
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
					{
						Group:   "",
						Version: "v1",
						Kind:    "Namespace",
						Name:    nonExistentNSName,
					},
				},
				Policy: &placementv1beta1.PlacementPolicy{
					PlacementType: placementv1beta1.PickAllPlacementType,
					ClusterNames:  []string{memberCluster1EastProdName},
				},
			},
		}

		err := hubClient.Create(ctx, crp)
		Expect(err).To(HaveOccurred(), "Expected error when creating CRP with clusterNames set for PickAll placementType")
		var statusErr *apierrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), "The returned error is not a StatusError")
		Expect(statusErr.Status().Message).Should(ContainSubstring("clusterNames, numberOfClusters, and topologySpreadConstraints cannot be set when placementType is PickAll"))
	})

	It("cannot set numberOfClusters when placementType is PickAll", func() {
		crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())

		var numberOfClusters int32 = 1
		crp := &placementv1beta1.ClusterResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name: crpName,
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
					{
						Group:   "",
						Version: "v1",
						Kind:    "Namespace",
						Name:    nonExistentNSName,
					},
				},
				Policy: &placementv1beta1.PlacementPolicy{
					PlacementType:    placementv1beta1.PickAllPlacementType,
					NumberOfClusters: &numberOfClusters,
				},
			},
		}

		err := hubClient.Create(ctx, crp)
		Expect(err).To(HaveOccurred(), "Expected error when creating CRP with numberOfClusters set for PickAll placementType")
		var statusErr *apierrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), "The returned error is not a StatusError")
		Expect(statusErr.Status().Message).Should(ContainSubstring("clusterNames, numberOfClusters, and topologySpreadConstraints cannot be set when placementType is PickAll"))
	})

	It("cannot set topologySpreadConstraints when placementType is PickAll", func() {
		crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())

		crp := &placementv1beta1.ClusterResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name: crpName,
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
					{
						Group:   "",
						Version: "v1",
						Kind:    "Namespace",
						Name:    nonExistentNSName,
					},
				},
				Policy: &placementv1beta1.PlacementPolicy{
					PlacementType: placementv1beta1.PickAllPlacementType,
					TopologySpreadConstraints: []placementv1beta1.TopologySpreadConstraint{
						{
							TopologyKey: "topology.kubernetes.io/zone",
						},
					},
				},
			},
		}

		err := hubClient.Create(ctx, crp)
		Expect(err).To(HaveOccurred(), "Expected error when creating CRP with topologySpreadConstraints set for PickAll placementType")
		var statusErr *apierrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), "The returned error is not a StatusError")
		Expect(statusErr.Status().Message).Should(ContainSubstring("clusterNames, numberOfClusters, and topologySpreadConstraints cannot be set when placementType is PickAll"))
	})

	It("cannot set clusterNames when placementType is PickN", func() {
		crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())

		var numberOfClusters int32 = 1
		crp := &placementv1beta1.ClusterResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name: crpName,
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
					{
						Group:   "",
						Version: "v1",
						Kind:    "Namespace",
						Name:    nonExistentNSName,
					},
				},
				Policy: &placementv1beta1.PlacementPolicy{
					PlacementType:    placementv1beta1.PickNPlacementType,
					ClusterNames:     []string{memberCluster1EastProdName},
					NumberOfClusters: &numberOfClusters,
				},
			},
		}

		err := hubClient.Create(ctx, crp)
		Expect(err).To(HaveOccurred(), "Expected error when creating CRP with clusterNames set for PickN placementType")
		var statusErr *apierrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), "The returned error is not a StatusError")
		Expect(statusErr.Status().Message).Should(ContainSubstring("clusterNames cannot be set and numberOfClusters must be set when placementType is PickN"))
	})

	It("must set numberOfClusters when placementType is PickN", func() {
		crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())

		crp := &placementv1beta1.ClusterResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name: crpName,
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
					{
						Group:   "",
						Version: "v1",
						Kind:    "Namespace",
						Name:    nonExistentNSName,
					},
				},
				Policy: &placementv1beta1.PlacementPolicy{
					PlacementType: placementv1beta1.PickNPlacementType,
				},
			},
		}

		err := hubClient.Create(ctx, crp)
		Expect(err).To(HaveOccurred(), "Expected error when creating CRP without numberOfClusters set for PickN placementType")
		var statusErr *apierrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), "The returned error is not a StatusError")
		Expect(statusErr.Status().Message).Should(ContainSubstring("clusterNames cannot be set and numberOfClusters must be set when placementType is PickN"))
	})

	It("cannot set a toleration value when its operator is Exists", func() {
		crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())

		crp := &placementv1beta1.ClusterResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name: crpName,
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
					{
						Group:   "",
						Version: "v1",
						Kind:    "Namespace",
						Name:    nonExistentNSName,
					},
				},
				Policy: &placementv1beta1.PlacementPolicy{
					PlacementType: placementv1beta1.PickAllPlacementType,
					Tolerations: []placementv1beta1.Toleration{
						{
							Key:      "foo",
							Operator: corev1.TolerationOpExists,
							Value:    "bar",
							Effect:   corev1.TaintEffectNoSchedule,
						},
					},
				},
			},
		}

		err := hubClient.Create(ctx, crp)
		Expect(err).To(HaveOccurred(), "Expected error when creating CRP with a toleration value set for operator Exists")
		var statusErr *apierrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), "The returned error is not a StatusError")
		Expect(statusErr.Status().Message).Should(ContainSubstring("value must be empty when operator is Exists"))
	})

	It("must set a toleration operator to Exists when its key is empty", func() {
		crpName := fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess())

		crp := &placementv1beta1.ClusterResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name: crpName,
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
					{
						Group:   "",
						Version: "v1",
						Kind:    "Namespace",
						Name:    nonExistentNSName,
					},
				},
				Policy: &placementv1beta1.PlacementPolicy{
					PlacementType: placementv1beta1.PickAllPlacementType,
					Tolerations: []placementv1beta1.Toleration{
						{
							Operator: corev1.TolerationOpEqual,
							Value:    "bar",
							Effect:   corev1.TaintEffectNoSchedule,
						},
					},
				},
			},
		}

		err := hubClient.Create(ctx, crp)
		Expect(err).To(HaveOccurred(), "Expected error when creating CRP with an empty toleration key and operator Equal")
		var statusErr *apierrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), "The returned error is not a StatusError")
		Expect(statusErr.Status().Message).Should(ContainSubstring("operator must be Exists when key is empty"))
	})
})

var _ = Describe("CEL expression based validation for RPs", func() {
	AfterEach(func() {
		// Clean up the RP created during each test case.
		rpName := fmt.Sprintf(rpNameTemplate, GinkgoParallelProcess())

		Eventually(func() error {
			rp := &placementv1beta1.ResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name:      rpName,
					Namespace: rpNamespace,
				},
			}
			if err := hubClient.Delete(ctx, rp); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete RP: %w", err)
			}

			if err := hubClient.Get(ctx, client.ObjectKey{Name: rpName, Namespace: rpNamespace}, &placementv1beta1.ResourcePlacement{}); !apierrors.IsNotFound(err) {
				return fmt.Errorf("RP still exists after deletion attempt (error: %w)", err)
			}
			return nil
		}, eventuallyDuration, eventuallyInterval).Should(Succeed())
	})

	It("cannot set name and label selector in a resource selector at the same time", func() {
		rpName := fmt.Sprintf(rpNameTemplate, GinkgoParallelProcess())

		rp := &placementv1beta1.ResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rpName,
				Namespace: rpNamespace,
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
					{
						Group:   "",
						Version: "v1",
						Kind:    "ConfigMap",
						Name:    nonExistentCMName,
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"foo": "bar"},
						},
					},
				},
			},
		}

		err := hubClient.Create(ctx, rp)
		Expect(err).To(HaveOccurred(), "Expected error when creating RP with both name and label selector in a resource selector")
		var statusErr *apierrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), "The returned error is not a StatusError")
		Expect(statusErr.Status().Message).Should(ContainSubstring("name and labelSelector are mutually exclusive in a resource selector"))
	})

	It("cannot set numberOfClusters when placementType is PickFixed", func() {
		rpName := fmt.Sprintf(rpNameTemplate, GinkgoParallelProcess())

		var numberOfClusters int32 = 1
		rp := &placementv1beta1.ResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rpName,
				Namespace: rpNamespace,
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
					{
						Group:   "",
						Version: "v1",
						Kind:    "ConfigMap",
						Name:    nonExistentCMName,
					},
				},
				Policy: &placementv1beta1.PlacementPolicy{
					PlacementType:    placementv1beta1.PickFixedPlacementType,
					ClusterNames:     []string{memberCluster1EastProdName},
					NumberOfClusters: &numberOfClusters,
				},
			},
		}

		err := hubClient.Create(ctx, rp)
		Expect(err).To(HaveOccurred(), "Expected error when creating RP with numberOfClusters set for PickFixed placementType")
		var statusErr *apierrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), "The returned error is not a StatusError")
		Expect(statusErr.Status().Message).Should(ContainSubstring("affinity, numberOfClusters, topologySpreadConstraints, and tolerations cannot be set when placementType is PickFixed"))
	})

	It("cannot set affinity when placementType is PickFixed", func() {
		rpName := fmt.Sprintf(rpNameTemplate, GinkgoParallelProcess())

		rp := &placementv1beta1.ResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rpName,
				Namespace: rpNamespace,
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
					{
						Group:   "",
						Version: "v1",
						Kind:    "ConfigMap",
						Name:    nonExistentCMName,
					},
				},
				Policy: &placementv1beta1.PlacementPolicy{
					PlacementType: placementv1beta1.PickFixedPlacementType,
					ClusterNames:  []string{memberCluster1EastProdName},
					Affinity: &placementv1beta1.Affinity{
						ClusterAffinity: &placementv1beta1.ClusterAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &placementv1beta1.ClusterSelector{
								ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
									{
										LabelSelector: &metav1.LabelSelector{
											MatchLabels: map[string]string{"foo": "bar"},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		err := hubClient.Create(ctx, rp)
		Expect(err).To(HaveOccurred(), "Expected error when creating RP with affinity set for PickFixed placementType")
		var statusErr *apierrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), "The returned error is not a StatusError")
		Expect(statusErr.Status().Message).Should(ContainSubstring("affinity, numberOfClusters, topologySpreadConstraints, and tolerations cannot be set when placementType is PickFixed"))
	})

	It("cannot set topologySpreadConstraints when placementType is PickFixed", func() {
		rpName := fmt.Sprintf(rpNameTemplate, GinkgoParallelProcess())

		rp := &placementv1beta1.ResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rpName,
				Namespace: rpNamespace,
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
					{
						Group:   "",
						Version: "v1",
						Kind:    "ConfigMap",
						Name:    nonExistentCMName,
					},
				},
				Policy: &placementv1beta1.PlacementPolicy{
					PlacementType: placementv1beta1.PickFixedPlacementType,
					ClusterNames:  []string{memberCluster1EastProdName},
					TopologySpreadConstraints: []placementv1beta1.TopologySpreadConstraint{
						{
							TopologyKey: "topology.kubernetes.io/zone",
						},
					},
				},
			},
		}

		err := hubClient.Create(ctx, rp)
		Expect(err).To(HaveOccurred(), "Expected error when creating RP with topologySpreadConstraints set for PickFixed placementType")
		var statusErr *apierrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), "The returned error is not a StatusError")
		Expect(statusErr.Status().Message).Should(ContainSubstring("affinity, numberOfClusters, topologySpreadConstraints, and tolerations cannot be set when placementType is PickFixed"))
	})

	It("cannot set tolerations when placementType is PickFixed", func() {
		rpName := fmt.Sprintf(rpNameTemplate, GinkgoParallelProcess())

		rp := &placementv1beta1.ResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rpName,
				Namespace: rpNamespace,
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
					{
						Group:   "",
						Version: "v1",
						Kind:    "ConfigMap",
						Name:    nonExistentCMName,
					},
				},
				Policy: &placementv1beta1.PlacementPolicy{
					PlacementType: placementv1beta1.PickFixedPlacementType,
					ClusterNames:  []string{memberCluster1EastProdName},
					Tolerations: []placementv1beta1.Toleration{
						{
							Key:      "foo",
							Operator: corev1.TolerationOpEqual,
							Value:    "bar",
							Effect:   corev1.TaintEffectNoSchedule,
						},
					},
				},
			},
		}

		err := hubClient.Create(ctx, rp)
		Expect(err).To(HaveOccurred(), "Expected error when creating RP with tolerations set for PickFixed placementType")
		var statusErr *apierrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), "The returned error is not a StatusError")
		Expect(statusErr.Status().Message).Should(ContainSubstring("affinity, numberOfClusters, topologySpreadConstraints, and tolerations cannot be set when placementType is PickFixed"))
	})

	It("cannot set clusterNames when placementType is PickAll", func() {
		rpName := fmt.Sprintf(rpNameTemplate, GinkgoParallelProcess())

		rp := &placementv1beta1.ResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rpName,
				Namespace: rpNamespace,
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
					{
						Group:   "",
						Version: "v1",
						Kind:    "ConfigMap",
						Name:    nonExistentCMName,
					},
				},
				Policy: &placementv1beta1.PlacementPolicy{
					PlacementType: placementv1beta1.PickAllPlacementType,
					ClusterNames:  []string{memberCluster1EastProdName},
				},
			},
		}

		err := hubClient.Create(ctx, rp)
		Expect(err).To(HaveOccurred(), "Expected error when creating RP with clusterNames set for PickAll placementType")
		var statusErr *apierrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), "The returned error is not a StatusError")
		Expect(statusErr.Status().Message).Should(ContainSubstring("clusterNames, numberOfClusters, and topologySpreadConstraints cannot be set when placementType is PickAll"))
	})

	It("cannot set numberOfClusters when placementType is PickAll", func() {
		rpName := fmt.Sprintf(rpNameTemplate, GinkgoParallelProcess())

		var numberOfClusters int32 = 1
		rp := &placementv1beta1.ResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rpName,
				Namespace: rpNamespace,
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
					{
						Group:   "",
						Version: "v1",
						Kind:    "ConfigMap",
						Name:    nonExistentCMName,
					},
				},
				Policy: &placementv1beta1.PlacementPolicy{
					PlacementType:    placementv1beta1.PickAllPlacementType,
					NumberOfClusters: &numberOfClusters,
				},
			},
		}

		err := hubClient.Create(ctx, rp)
		Expect(err).To(HaveOccurred(), "Expected error when creating RP with numberOfClusters set for PickAll placementType")
		var statusErr *apierrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), "The returned error is not a StatusError")
		Expect(statusErr.Status().Message).Should(ContainSubstring("clusterNames, numberOfClusters, and topologySpreadConstraints cannot be set when placementType is PickAll"))
	})

	It("cannot set topologySpreadConstraints when placementType is PickAll", func() {
		rpName := fmt.Sprintf(rpNameTemplate, GinkgoParallelProcess())

		rp := &placementv1beta1.ResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rpName,
				Namespace: rpNamespace,
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
					{
						Group:   "",
						Version: "v1",
						Kind:    "ConfigMap",
						Name:    nonExistentCMName,
					},
				},
				Policy: &placementv1beta1.PlacementPolicy{
					PlacementType: placementv1beta1.PickAllPlacementType,
					TopologySpreadConstraints: []placementv1beta1.TopologySpreadConstraint{
						{
							TopologyKey: "topology.kubernetes.io/zone",
						},
					},
				},
			},
		}

		err := hubClient.Create(ctx, rp)
		Expect(err).To(HaveOccurred(), "Expected error when creating RP with topologySpreadConstraints set for PickAll placementType")
		var statusErr *apierrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), "The returned error is not a StatusError")
		Expect(statusErr.Status().Message).Should(ContainSubstring("clusterNames, numberOfClusters, and topologySpreadConstraints cannot be set when placementType is PickAll"))
	})

	It("cannot set clusterNames when placementType is PickN", func() {
		rpName := fmt.Sprintf(rpNameTemplate, GinkgoParallelProcess())

		var numberOfClusters int32 = 1
		rp := &placementv1beta1.ResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rpName,
				Namespace: rpNamespace,
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
					{
						Group:   "",
						Version: "v1",
						Kind:    "ConfigMap",
						Name:    nonExistentCMName,
					},
				},
				Policy: &placementv1beta1.PlacementPolicy{
					PlacementType:    placementv1beta1.PickNPlacementType,
					ClusterNames:     []string{memberCluster1EastProdName},
					NumberOfClusters: &numberOfClusters,
				},
			},
		}

		err := hubClient.Create(ctx, rp)
		Expect(err).To(HaveOccurred(), "Expected error when creating RP with clusterNames set for PickN placementType")
		var statusErr *apierrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), "The returned error is not a StatusError")
		Expect(statusErr.Status().Message).Should(ContainSubstring("clusterNames cannot be set and numberOfClusters must be set when placementType is PickN"))
	})

	It("must set numberOfClusters when placementType is PickN", func() {
		rpName := fmt.Sprintf(rpNameTemplate, GinkgoParallelProcess())

		rp := &placementv1beta1.ResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rpName,
				Namespace: rpNamespace,
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
					{
						Group:   "",
						Version: "v1",
						Kind:    "ConfigMap",
						Name:    nonExistentCMName,
					},
				},
				Policy: &placementv1beta1.PlacementPolicy{
					PlacementType: placementv1beta1.PickNPlacementType,
				},
			},
		}

		err := hubClient.Create(ctx, rp)
		Expect(err).To(HaveOccurred(), "Expected error when creating RP without numberOfClusters set for PickN placementType")
		var statusErr *apierrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), "The returned error is not a StatusError")
		Expect(statusErr.Status().Message).Should(ContainSubstring("clusterNames cannot be set and numberOfClusters must be set when placementType is PickN"))
	})

	It("cannot set a toleration value when its operator is Exists", func() {
		rpName := fmt.Sprintf(rpNameTemplate, GinkgoParallelProcess())

		rp := &placementv1beta1.ResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rpName,
				Namespace: rpNamespace,
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
					{
						Group:   "",
						Version: "v1",
						Kind:    "ConfigMap",
						Name:    nonExistentCMName,
					},
				},
				Policy: &placementv1beta1.PlacementPolicy{
					PlacementType: placementv1beta1.PickAllPlacementType,
					Tolerations: []placementv1beta1.Toleration{
						{
							Key:      "foo",
							Operator: corev1.TolerationOpExists,
							Value:    "bar",
							Effect:   corev1.TaintEffectNoSchedule,
						},
					},
				},
			},
		}

		err := hubClient.Create(ctx, rp)
		Expect(err).To(HaveOccurred(), "Expected error when creating RP with a toleration value set for operator Exists")
		var statusErr *apierrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), "The returned error is not a StatusError")
		Expect(statusErr.Status().Message).Should(ContainSubstring("value must be empty when operator is Exists"))
	})

	It("must set a toleration operator to Exists when its key is empty", func() {
		rpName := fmt.Sprintf(rpNameTemplate, GinkgoParallelProcess())

		rp := &placementv1beta1.ResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rpName,
				Namespace: rpNamespace,
			},
			Spec: placementv1beta1.PlacementSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
					{
						Group:   "",
						Version: "v1",
						Kind:    "ConfigMap",
						Name:    nonExistentCMName,
					},
				},
				Policy: &placementv1beta1.PlacementPolicy{
					PlacementType: placementv1beta1.PickAllPlacementType,
					Tolerations: []placementv1beta1.Toleration{
						{
							Operator: corev1.TolerationOpEqual,
							Value:    "bar",
							Effect:   corev1.TaintEffectNoSchedule,
						},
					},
				},
			},
		}

		err := hubClient.Create(ctx, rp)
		Expect(err).To(HaveOccurred(), "Expected error when creating RP with an empty toleration key and operator Equal")
		var statusErr *apierrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), "The returned error is not a StatusError")
		Expect(statusErr.Status().Message).Should(ContainSubstring("operator must be Exists when key is empty"))
	})
})
