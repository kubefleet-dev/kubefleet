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

package overrider

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/controller"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/labels"
)

const (
	eventuallyTimeout    = time.Second * 10
	consistentlyDuration = time.Second * 5
	interval             = time.Millisecond * 250

	overrideNamespace = "test-app"
)

var _ = Describe("Test ClusterResourceOverride common logic", func() {
	var cro *placementv1beta1.ClusterResourceOverride
	croNameBase := "test-cro-common"
	var testCROName string

	BeforeEach(func() {
		testCROName = fmt.Sprintf("%s-%s", croNameBase, utils.RandStr())
		// we cannot apply the CRO to the cluster as it will trigger the real reconcile loop.
		cro = getClusterResourceOverride(testCROName)
		By("Creating five clusterResourceOverrideSnapshot")
		for i := 0; i < 5; i++ {
			snapshot := getClusterResourceOverrideSnapshot(testCROName, i)
			Expect(k8sClient.Create(ctx, snapshot)).Should(Succeed())
		}
	})

	AfterEach(func() {
		By("Deleting five clusterResourceOverrideSnapshot")
		for i := 0; i < 5; i++ {
			snapshot := getClusterResourceOverrideSnapshot(testCROName, i)
			Expect(k8sClient.Delete(ctx, snapshot)).Should(SatisfyAny(Succeed(), &utils.NotFoundMatcher{}))
		}
	})

	Context("Test handle override deleting", func() {
		It("Should not do anything if there is no finalizer", func() {
			Expect(commonReconciler.handleOverrideDeleting(ctx, nil, cro)).Should(Succeed())
		})

		It("Should not fail if there is no snapshots associated with the cro yet", func() {
			By("Adding the overrideFinalizer")
			controllerutil.AddFinalizer(cro, placementv1beta1.OverrideFinalizer)

			By("verifying that it handles no snapshot cases")
			cro.Name = "another-cro" //there is no snapshot associated with this CRO
			// we cannot apply the CRO to the cluster as it will trigger the real reconcile loop so the update can only return APIServerError
			Expect(errors.Is(commonReconciler.handleOverrideDeleting(context.Background(), getClusterResourceOverrideSnapshot(testCROName, 0), cro), controller.ErrAPIServerError)).Should(BeTrue())
			// make sure that we don't delete the original CRO's snapshot
			for i := 0; i < 5; i++ {
				snapshot := getClusterResourceOverrideSnapshot(testCROName, i)
				Consistently(func() error {
					return k8sClient.Get(ctx, types.NamespacedName{Name: snapshot.Name}, snapshot)
				}, consistentlyDuration, interval).Should(Succeed(), "snapshot should not be deleted")
			}
		})

		It("Should delete all the snapshots if there is finalizer", func() {
			By("Adding the overrideFinalizer")
			controllerutil.AddFinalizer(cro, placementv1beta1.OverrideFinalizer)
			By("verifying that all snapshots are deleted")
			// we cannot apply the CRO to the cluster as it will trigger the real reconcile loop so the update can only return APIServerError
			Expect(errors.Is(commonReconciler.handleOverrideDeleting(context.Background(), getClusterResourceOverrideSnapshot(testCROName, 0), cro), controller.ErrAPIServerError)).Should(BeTrue())
			for i := 0; i < 5; i++ {
				snapshot := getClusterResourceOverrideSnapshot(testCROName, i)
				Eventually(func() bool {
					return apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: snapshot.Name}, snapshot))
				}, eventuallyTimeout, interval).Should(BeTrue(), "snapshot should be deleted")
			}
		})
	})

	Context("Test list sorted override snapshots", func() {
		It("Should list all the snapshots associated with the override", func() {
			snapshotList, err := commonReconciler.listSortedOverrideSnapshots(ctx, cro)
			Expect(err).Should(Succeed())
			By("verifying that all snapshots are listed and sorted")
			Expect(snapshotList.Items).Should(HaveLen(5))
			index := -1
			for i := 0; i < 5; i++ {
				snapshot := snapshotList.Items[i]
				newIndex, err := labels.ExtractIndex(&snapshot, placementv1beta1.OverrideIndexLabel)
				Expect(err).Should(Succeed())
				Expect(newIndex == index+1).Should(BeTrue())
				index = newIndex
			}
		})
	})

	Context("Test remove extra cluster override snapshots", func() {
		It("Should not remove any snapshots if we no snapshot", func() {
			snapshotList := &unstructured.UnstructuredList{
				Items: []unstructured.Unstructured{},
			}
			// we have 0 snapshots, and the limit is 1, so we should not remove any
			err := commonReconciler.removeExtraSnapshot(ctx, snapshotList, 1)
			Expect(err).Should(Succeed())
		})

		It("Should not remove any snapshots if we have not reached the limit", func() {
			snapshotList, err := commonReconciler.listSortedOverrideSnapshots(ctx, cro)
			Expect(err).Should(Succeed())
			// we have 5 snapshots, and the limit is 6, so we should not remove any
			err = commonReconciler.removeExtraSnapshot(ctx, snapshotList, 6)
			Expect(err).Should(Succeed())
			By("verifying that all the snapshots remain")
			for i := 0; i < 5; i++ {
				snapshot := getClusterResourceOverrideSnapshot(testCROName, i)
				Eventually(func() error {
					return k8sClient.Get(ctx, types.NamespacedName{Name: snapshot.Name}, snapshot)
				}, eventuallyTimeout, interval).Should(Succeed(), "snapshot should not be deleted")
			}
		})

		It("Should remove 1 extra snapshots if we just reach the limit", func() {
			snapshotList, err := commonReconciler.listSortedOverrideSnapshots(ctx, cro)
			Expect(err).Should(Succeed())
			// we have 5 snapshots, and the limit is 5, so we should remove one. This is the base case.
			err = commonReconciler.removeExtraSnapshot(ctx, snapshotList, 5)
			Expect(err).Should(Succeed())
			By("verifying that the oldest snapshot is removed")
			snapshot := getClusterResourceOverrideSnapshot(testCROName, 0)
			Eventually(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: snapshot.Name}, snapshot))
			}, eventuallyTimeout, interval).Should(BeTrue(), "snapshot should be deleted")
			By("verifying that only the oldest snapshot is removed")
			for i := 1; i < 5; i++ {
				snapshot := getClusterResourceOverrideSnapshot(testCROName, i)
				Eventually(func() error {
					return k8sClient.Get(ctx, types.NamespacedName{Name: snapshot.Name}, snapshot)
				}, eventuallyTimeout, interval).Should(Succeed(), "snapshot should not be deleted")
			}
		})

		It("Should remove all extra snapshots if we overshoot the limit", func() {
			snapshotList, err := commonReconciler.listSortedOverrideSnapshots(ctx, cro)
			Expect(err).Should(Succeed())
			// we have 5 snapshots, and the limit is 2, so we should remove 4
			err = commonReconciler.removeExtraSnapshot(ctx, snapshotList, 2)
			Expect(err).Should(Succeed())
			By("verifying that the older snapshots are removed")
			for i := 0; i < 4; i++ {
				snapshot := getClusterResourceOverrideSnapshot(testCROName, 0)
				Eventually(func() bool {
					return apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: snapshot.Name}, snapshot))
				}, eventuallyTimeout, interval).Should(BeTrue(), "snapshot should be deleted")
			}
			By("verifying that only the latest snapshot is kept")
			Consistently(func() error {
				snapshot := getClusterResourceOverrideSnapshot(testCROName, 4)
				return k8sClient.Get(ctx, types.NamespacedName{Name: snapshot.Name}, snapshot)
			}, consistentlyDuration, interval).Should(Succeed(), "snapshot should not be deleted")
		})
	})

	Context("Test remove extra override snapshots", func() {
		It("Should keep the latest label as true if it's already true", func() {
			snapshot := getClusterResourceOverrideSnapshot(testCROName, 0)
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: snapshot.GetName()}, snapshot)).Should(Succeed())
			Expect(commonReconciler.ensureSnapshotLatest(ctx, snapshot)).Should(Succeed())
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: snapshot.GetName()}, snapshot)).Should(Succeed())
			diff := cmp.Diff(map[string]string{
				placementv1beta1.OverrideIndexLabel:    strconv.Itoa(0),
				placementv1beta1.IsLatestSnapshotLabel: "true",
				placementv1beta1.OverrideTrackingLabel: testCROName,
			}, snapshot.GetLabels())
			Expect(diff).Should(BeEmpty(), diff)
		})

		It("Should update the latest label as true if it was false", func() {
			By("update a snapshot to be not latest")
			snapshot := getClusterResourceOverrideSnapshot(testCROName, 0)
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: snapshot.GetName()}, snapshot)).Should(Succeed())
			snapshot.SetLabels(map[string]string{
				placementv1beta1.OverrideIndexLabel:    strconv.Itoa(0),
				placementv1beta1.IsLatestSnapshotLabel: "false",
				placementv1beta1.OverrideTrackingLabel: testCROName,
			})
			Expect(k8sClient.Update(ctx, snapshot)).Should(Succeed())
			Expect(commonReconciler.ensureSnapshotLatest(ctx, snapshot)).Should(Succeed())
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: snapshot.GetName()}, snapshot)).Should(Succeed())
			diff := cmp.Diff(map[string]string{
				placementv1beta1.OverrideIndexLabel:    strconv.Itoa(0),
				placementv1beta1.IsLatestSnapshotLabel: "true",
				placementv1beta1.OverrideTrackingLabel: testCROName,
			}, snapshot.GetLabels())
			Expect(diff).Should(BeEmpty(), diff)
		})
	})
})

var _ = Describe("Test ResourceOverride common logic", func() {
	var ro *placementv1beta1.ResourceOverride
	totalSnapshots := 7
	testROName := "test-ro-common"
	var namespaceName string

	BeforeEach(func() {
		namespaceName = fmt.Sprintf("%s-%s", overrideNamespace, utils.RandStr())
		namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespaceName,
			},
		}
		Expect(k8sClient.Create(ctx, namespace)).Should(Succeed())
		// we cannot apply the RO to the cluster as it will trigger the real reconcile loop.
		ro = getResourceOverride(testROName, namespaceName)
		By("Creating resourceOverrideSnapshot")
		for i := 0; i < totalSnapshots; i++ {
			snapshot := getResourceOverrideSnapshot(testROName, namespaceName, i)
			Expect(k8sClient.Create(ctx, snapshot)).Should(Succeed())
		}
	})

	AfterEach(func() {
		By("Deleting seven resourceOverrideSnapshots")
		for i := 0; i < totalSnapshots; i++ {
			snapshot := getResourceOverrideSnapshot(testROName, namespaceName, i)
			Expect(k8sClient.Delete(ctx, snapshot)).Should(SatisfyAny(Succeed(), &utils.NotFoundMatcher{}))
		}
		By("Deleting the namespace")
		namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespaceName,
			},
		}
		Expect(k8sClient.Delete(ctx, namespace)).Should(SatisfyAny(Succeed(), &utils.NotFoundMatcher{}))
	})

	Context("Test handle override deleting", func() {
		It("Should not do anything if there is no finalizer", func() {
			Expect(commonReconciler.handleOverrideDeleting(ctx, nil, ro)).Should(Succeed())
		})

		It("Should not fail if there is no snapshots associated with the ro yet", func() {
			By("Adding the overrideFinalizer")
			controllerutil.AddFinalizer(ro, placementv1beta1.OverrideFinalizer)

			By("verifying that it handles no snapshot cases")
			ro.Name = "another-ro" //there is no snapshot associated with this RO
			// we cannot apply the RO to the cluster as it will trigger the real reconcile loop so the update can only return APIServerError
			Expect(errors.Is(commonReconciler.handleOverrideDeleting(context.Background(), getResourceOverrideSnapshot(testROName, namespaceName, 0), ro), controller.ErrAPIServerError)).Should(BeTrue())
			// make sure that we don't delete the original RO's snapshot
			for i := 0; i < totalSnapshots; i++ {
				snapshot := getResourceOverrideSnapshot(testROName, namespaceName, i)
				Consistently(func() error {
					return k8sClient.Get(ctx, types.NamespacedName{Name: snapshot.Name, Namespace: namespaceName}, snapshot)
				}, consistentlyDuration, interval).Should(Succeed(), "snapshot should not be deleted")
			}
		})

		It("Should delete all the snapshots if there is finalizer", func() {
			By("Adding the overrideFinalizer")
			controllerutil.AddFinalizer(ro, placementv1beta1.OverrideFinalizer)
			By("verifying that all snapshots are deleted")
			// we cannot apply the RO to the cluster as it will trigger the real reconcile loop so the update can only return APIServerError
			Expect(errors.Is(commonReconciler.handleOverrideDeleting(context.Background(), getResourceOverrideSnapshot(testROName, namespaceName, 0), ro), controller.ErrAPIServerError)).Should(BeTrue())
			for i := 0; i < totalSnapshots; i++ {
				snapshot := getResourceOverrideSnapshot(testROName, namespaceName, i)
				Eventually(func() bool {
					return apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: snapshot.Name, Namespace: namespaceName}, snapshot))
				}, eventuallyTimeout, interval).Should(BeTrue(), "snapshot should be deleted")
			}
		})
	})

	Context("Test list sorted override snapshots", func() {
		It("Should list all the snapshots associated with the override", func() {
			snapshotList, err := commonReconciler.listSortedOverrideSnapshots(ctx, ro)
			Expect(err).Should(Succeed())
			By("verifying that all snapshots are listed and sorted")
			Expect(snapshotList.Items).Should(HaveLen(totalSnapshots))
			index := -1
			for i := 0; i < totalSnapshots; i++ {
				snapshot := snapshotList.Items[i]
				newIndex, err := labels.ExtractIndex(&snapshot, placementv1beta1.OverrideIndexLabel)
				Expect(err).Should(Succeed())
				Expect(newIndex == index+1).Should(BeTrue())
				index = newIndex
			}
		})
	})

	Context("Test remove extra cluster override snapshots", func() {
		It("Should not remove any snapshots if we no snapshot", func() {
			snapshotList := &unstructured.UnstructuredList{
				Items: []unstructured.Unstructured{},
			}
			// we have 0 snapshots, and the limit is 1, so we should not remove any
			err := commonReconciler.removeExtraSnapshot(ctx, snapshotList, 1)
			Expect(err).Should(Succeed())
		})

		It("Should not remove any snapshots if we have not reached the limit", func() {
			snapshotList, err := commonReconciler.listSortedOverrideSnapshots(ctx, ro)
			Expect(err).Should(Succeed())
			// we have less snapshots than limit so we should not remove any
			err = commonReconciler.removeExtraSnapshot(ctx, snapshotList, totalSnapshots+1)
			Expect(err).Should(Succeed())
			By("verifying that all the snapshots remain")
			for i := 0; i < totalSnapshots; i++ {
				snapshot := getResourceOverrideSnapshot(ro.Name, ro.Namespace, i)
				Eventually(func() error {
					return k8sClient.Get(ctx, types.NamespacedName{Name: snapshot.Name, Namespace: snapshot.Namespace}, snapshot)
				}, eventuallyTimeout, interval).Should(Succeed(), "snapshot should not be deleted")
			}
		})

		It("Should remove 1 extra snapshots if we just reach the limit", func() {
			snapshotList, err := commonReconciler.listSortedOverrideSnapshots(ctx, ro)
			Expect(err).Should(Succeed())
			// we have 7 snapshots, and the limit is 7, so we should remove one. This is the base case.
			err = commonReconciler.removeExtraSnapshot(ctx, snapshotList, totalSnapshots)
			Expect(err).Should(Succeed())
			By("verifying that the oldest snapshot is removed")
			snapshot := getClusterResourceOverrideSnapshot(testROName, 0)
			Eventually(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: snapshot.Name, Namespace: snapshot.Namespace}, snapshot))
			}, eventuallyTimeout, interval).Should(BeTrue(), "snapshot should be deleted")
			By("verifying that only the oldest snapshot is removed")
			for i := 1; i < totalSnapshots; i++ {
				snapshot := getResourceOverrideSnapshot(testROName, ro.Namespace, i)
				Eventually(func() error {
					return k8sClient.Get(ctx, types.NamespacedName{Name: snapshot.Name, Namespace: snapshot.Namespace}, snapshot)
				}, eventuallyTimeout, interval).Should(Succeed(), "snapshot should not be deleted")
			}
		})

		It("Should remove all extra snapshots if we overshoot the limit", func() {
			snapshotList, err := commonReconciler.listSortedOverrideSnapshots(ctx, ro)
			Expect(err).Should(Succeed())
			// we have 7 snapshots, and the limit is 2, so we should remove 6
			err = commonReconciler.removeExtraSnapshot(ctx, snapshotList, 2)
			Expect(err).Should(Succeed())
			By("verifying that the older snapshots are removed")
			for i := 0; i <= totalSnapshots-2; i++ {
				snapshot := getResourceOverrideSnapshot(testROName, ro.Namespace, i)
				Eventually(func() bool {
					return apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: snapshot.Name, Namespace: snapshot.Namespace}, snapshot))
				}, eventuallyTimeout, interval).Should(BeTrue(), "snapshot should be deleted")
			}
			By("verifying that only the latest snapshot is kept")
			Consistently(func() error {
				snapshot := getResourceOverrideSnapshot(testROName, ro.Namespace, totalSnapshots-1)
				return k8sClient.Get(ctx, types.NamespacedName{Name: snapshot.Name, Namespace: snapshot.Namespace}, snapshot)
			}, consistentlyDuration, interval).Should(Succeed(), "snapshot should not be deleted")
		})
	})

	Context("Test remove extra override snapshots", func() {
		It("Should keep the latest label as true if it's already true", func() {
			snapshot := getResourceOverrideSnapshot(testROName, ro.Namespace, 0)
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: snapshot.GetName(), Namespace: snapshot.Namespace}, snapshot)).Should(Succeed())
			Expect(commonReconciler.ensureSnapshotLatest(ctx, snapshot)).Should(Succeed())
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: snapshot.GetName(), Namespace: snapshot.Namespace}, snapshot)).Should(Succeed())
			diff := cmp.Diff(map[string]string{
				placementv1beta1.OverrideIndexLabel:    strconv.Itoa(0),
				placementv1beta1.IsLatestSnapshotLabel: "true",
				placementv1beta1.OverrideTrackingLabel: testROName,
			}, snapshot.GetLabels())
			Expect(diff).Should(BeEmpty(), diff)
		})

		It("Should update the latest label as true if it was false", func() {
			By("update a snapshot to be not latest")
			snapshot := getResourceOverrideSnapshot(testROName, ro.Namespace, 0)
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: snapshot.GetName(), Namespace: snapshot.Namespace}, snapshot)).Should(Succeed())
			snapshot.SetLabels(map[string]string{
				placementv1beta1.OverrideIndexLabel:    strconv.Itoa(0),
				placementv1beta1.IsLatestSnapshotLabel: "false",
				placementv1beta1.OverrideTrackingLabel: testROName,
			})
			Expect(k8sClient.Update(ctx, snapshot)).Should(Succeed())
			Expect(commonReconciler.ensureSnapshotLatest(ctx, snapshot)).Should(Succeed())
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: snapshot.GetName(), Namespace: snapshot.Namespace}, snapshot)).Should(Succeed())
			diff := cmp.Diff(map[string]string{
				placementv1beta1.OverrideIndexLabel:    strconv.Itoa(0),
				placementv1beta1.IsLatestSnapshotLabel: "true",
				placementv1beta1.OverrideTrackingLabel: testROName,
			}, snapshot.GetLabels())
			Expect(diff).Should(BeEmpty(), diff)
		})
	})
})
