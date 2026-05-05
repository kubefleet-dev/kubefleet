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
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/controller"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/labels"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/resource"
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
		It("Should not remove any snapshots if we have no snapshots", func() {
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

		It("Should remove 1 extra snapshot if we just reach the limit", func() {
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

	Context("Test ensureSnapshotLatest", func() {
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

	Context("Test cleanupStaleLatestSiblings on CRO snapshots", func() {
		It("Should be a no-op when only one snapshot is latest=true", func() {
			By("flipping all but the highest-index snapshot to latest=false")
			for i := range 4 {
				snapshot := getClusterResourceOverrideSnapshot(testCROName, i)
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: snapshot.GetName()}, snapshot)).Should(Succeed())
				labels := snapshot.GetLabels()
				labels[placementv1beta1.IsLatestSnapshotLabel] = "false"
				snapshot.SetLabels(labels)
				Expect(k8sClient.Update(ctx, snapshot)).Should(Succeed())
			}

			By("calling the audit on the freshly listed snapshots")
			snapshotList, err := commonReconciler.listSortedOverrideSnapshots(ctx, cro)
			Expect(err).Should(Succeed())
			Expect(commonReconciler.cleanupStaleLatestSiblings(ctx, snapshotList)).Should(Succeed())

			By("verifying that the highest-index snapshot is still latest=true")
			highest := getClusterResourceOverrideSnapshot(testCROName, 4)
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: highest.GetName()}, highest)).Should(Succeed())
			Expect(highest.GetLabels()[placementv1beta1.IsLatestSnapshotLabel]).Should(Equal("true"))

			By("verifying that the older snapshots remain latest=false")
			for i := range 4 {
				snapshot := getClusterResourceOverrideSnapshot(testCROName, i)
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: snapshot.GetName()}, snapshot)).Should(Succeed())
				Expect(snapshot.GetLabels()[placementv1beta1.IsLatestSnapshotLabel]).Should(Equal("false"))
			}
		})

		It("Should flip every stale latest=true sibling to false", func() {
			// BeforeEach creates 5 snapshots all with latest=true, simulating the post-crash
			// state where Create-first succeeded several times but the demote step failed.
			By("calling the audit on the freshly listed snapshots")
			snapshotList, err := commonReconciler.listSortedOverrideSnapshots(ctx, cro)
			Expect(err).Should(Succeed())
			Expect(commonReconciler.cleanupStaleLatestSiblings(ctx, snapshotList)).Should(Succeed())

			By("verifying that the highest-index snapshot keeps latest=true")
			highest := getClusterResourceOverrideSnapshot(testCROName, 4)
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: highest.GetName()}, highest)).Should(Succeed())
			Expect(highest.GetLabels()[placementv1beta1.IsLatestSnapshotLabel]).Should(Equal("true"))

			By("verifying that the older snapshots are flipped to latest=false")
			for i := range 4 {
				snapshot := getClusterResourceOverrideSnapshot(testCROName, i)
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: snapshot.GetName()}, snapshot)).Should(Succeed())
				Expect(snapshot.GetLabels()[placementv1beta1.IsLatestSnapshotLabel]).Should(Equal("false"))
			}
		})

		It("Should be a no-op for empty or single-item lists", func() {
			Expect(commonReconciler.cleanupStaleLatestSiblings(ctx, nil)).Should(Succeed())
			Expect(commonReconciler.cleanupStaleLatestSiblings(ctx, &unstructured.UnstructuredList{})).Should(Succeed())
		})
	})

	Context("Test ensureClusterResourceOverrideSnapshot AlreadyExists recovery", func() {
		var aeReconciler *ClusterResourceReconciler
		var aeCROName string
		var aeCRO *placementv1beta1.ClusterResourceOverride

		BeforeEach(func() {
			// Use a dedicated CRO name so this Context's snapshots don't collide with the parent
			// BeforeEach's snapshots (which all share testCROName at indices 0-4).
			aeCROName = fmt.Sprintf("test-cro-already-exists-%s", utils.RandStr())
			aeCRO = getClusterResourceOverride(aeCROName)
			// We don't apply the CRO to the cluster (would trigger the real reconcile loop), but
			// the snapshot Create inside ensureClusterResourceOverrideSnapshot adds an OwnerReference
			// whose UID must be non-empty for API-server validation.
			aeCRO.UID = "fake-uid-for-owner-ref"
			aeReconciler = &ClusterResourceReconciler{Reconciler: commonReconciler}
		})

		AfterEach(func() {
			// Clean up snapshots created in this Context. Both indices may or may not exist.
			for i := 0; i < 2; i++ {
				snap := getClusterResourceOverrideSnapshot(aeCROName, i)
				Expect(k8sClient.Delete(ctx, snap)).Should(SatisfyAny(Succeed(), &utils.NotFoundMatcher{}))
			}
		})

		It("Should treat AlreadyExists with matching hash as success and demote the previous snapshot", func() {
			// Pre-create snapshot 0 fully tracked: this is the "previous latest" the controller
			// will see via listSortedOverrideSnapshots.
			snapshot0 := getClusterResourceOverrideSnapshot(aeCROName, 0)
			snapshot0.Spec.OverrideHash = []byte("old-hash")
			Expect(k8sClient.Create(ctx, snapshot0)).Should(Succeed())

			// Pre-create snapshot 1 WITHOUT the OverrideTrackingLabel so listSortedOverrideSnapshots
			// (which filters by tracking label) does not see it. The controller will compute a new
			// snapshot at index 1 and hit AlreadyExists when it tries to Create.
			//
			// In production the real trigger for this branch is etcd restore from backup (a
			// snapshot that exists in etcd but is invisible to the controller's listing pass),
			// which envtest cannot simulate directly. Stripping the tracking label is the most
			// faithful approximation available in an integration test.
			intendedHash, err := resource.HashOf(aeCRO.Spec)
			Expect(err).Should(Succeed())
			invisibleSnapshot1 := getClusterResourceOverrideSnapshot(aeCROName, 1)
			delete(invisibleSnapshot1.Labels, placementv1beta1.OverrideTrackingLabel)
			invisibleSnapshot1.Spec.OverrideHash = []byte(intendedHash)
			invisibleSnapshot1.Spec.OverrideSpec = aeCRO.Spec
			Expect(k8sClient.Create(ctx, invisibleSnapshot1)).Should(Succeed())

			Expect(aeReconciler.ensureClusterResourceOverrideSnapshot(ctx, aeCRO, 10)).Should(Succeed(),
				"AlreadyExists with matching hash should be treated as success")

			By("Verifying snapshot 0 was demoted to latest=false")
			final0 := &placementv1beta1.ClusterResourceOverrideSnapshot{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: snapshot0.Name}, final0)).Should(Succeed())
			Expect(final0.Labels[placementv1beta1.IsLatestSnapshotLabel]).Should(Equal("false"))
		})

		It("Should return ExpectedBehaviorError and emit a Warning event when AlreadyExists target has a mismatched hash", func() {
			// Wire a fake recorder so we can assert the Warning event is actually emitted —
			// production controllers set this in SetupWithManager, but this test constructs the
			// reconciler manually.
			fakeRecorder := record.NewFakeRecorder(10)
			aeReconciler.recorder = fakeRecorder

			snapshot0 := getClusterResourceOverrideSnapshot(aeCROName, 0)
			snapshot0.Spec.OverrideHash = []byte("old-hash")
			Expect(k8sClient.Create(ctx, snapshot0)).Should(Succeed())

			// Pre-create snapshot 1 invisibly with a hash that does NOT match the CRO's spec —
			// simulating etcd restore from backup or a future hash-function change. The most
			// likely real-world trigger is an etcd restore where retry will eventually converge,
			// so the controller surfaces an expected-behavior error rather than a hard failure.
			invisibleSnapshot1 := getClusterResourceOverrideSnapshot(aeCROName, 1)
			delete(invisibleSnapshot1.Labels, placementv1beta1.OverrideTrackingLabel)
			invisibleSnapshot1.Spec.OverrideHash = []byte("stale-hash-from-backup")
			Expect(k8sClient.Create(ctx, invisibleSnapshot1)).Should(Succeed())

			err := aeReconciler.ensureClusterResourceOverrideSnapshot(ctx, aeCRO, 10)
			Expect(err).Should(HaveOccurred(), "mismatched hash should not silently succeed")
			Expect(errors.Is(err, controller.ErrExpectedBehavior)).Should(BeTrue(),
				"error should wrap ErrExpectedBehavior so retry runs without stack-trace floods, got %v", err)

			By("Verifying a Warning event with reason OverrideSnapshotHashMismatch was emitted on the parent CRO")
			select {
			case ev := <-fakeRecorder.Events:
				Expect(ev).Should(ContainSubstring("OverrideSnapshotHashMismatch"),
					"event = %q, want reason OverrideSnapshotHashMismatch", ev)
				Expect(ev).Should(ContainSubstring("Warning"),
					"event should be a Warning, got %q", ev)
			default:
				Fail("expected a Warning event with reason OverrideSnapshotHashMismatch, got none")
			}

			By("Verifying snapshot 0 was NOT demoted (we abort before the demote step)")
			final0 := &placementv1beta1.ClusterResourceOverrideSnapshot{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: snapshot0.Name}, final0)).Should(Succeed())
			Expect(final0.Labels[placementv1beta1.IsLatestSnapshotLabel]).Should(Equal("true"))
		})

		It("Should take the hash-match path and audit siblings when the latest snapshot's hash equals the CRO spec", func() {
			// Pre-create snapshot 0 with a hash that matches the CRO's current spec. When the
			// controller lists snapshots and inspects the highest-index one (this snapshot), the
			// hash check at the top of ensureClusterResourceOverrideSnapshot will short-circuit:
			// no new snapshot is created and ensureSnapshotLatest + cleanupStaleLatestSiblings run.
			intendedHash, err := resource.HashOf(aeCRO.Spec)
			Expect(err).Should(Succeed())
			snapshot0 := getClusterResourceOverrideSnapshot(aeCROName, 0)
			snapshot0.Spec.OverrideHash = []byte(intendedHash)
			snapshot0.Spec.OverrideSpec = aeCRO.Spec
			// ensureSnapshotLatest should flip this back to true on the hash-match path.
			snapshot0.Labels[placementv1beta1.IsLatestSnapshotLabel] = strconv.FormatBool(false)
			Expect(k8sClient.Create(ctx, snapshot0)).Should(Succeed())

			Expect(aeReconciler.ensureClusterResourceOverrideSnapshot(ctx, aeCRO, 10)).Should(Succeed())

			By("Verifying ensureSnapshotLatest flipped snapshot 0 back to latest=true")
			final0 := &placementv1beta1.ClusterResourceOverrideSnapshot{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: snapshot0.Name}, final0)).Should(Succeed())
			Expect(final0.Labels[placementv1beta1.IsLatestSnapshotLabel]).Should(Equal("true"))

			By("Verifying no new snapshot at index 1 was created")
			snapshot1 := getClusterResourceOverrideSnapshot(aeCROName, 1)
			Expect(apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: snapshot1.Name}, snapshot1))).Should(BeTrue(),
				"hash-match path must not create a new snapshot")
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
		It("Should not remove any snapshots if we have no snapshots", func() {
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

		It("Should remove 1 extra snapshot if we just reach the limit", func() {
			snapshotList, err := commonReconciler.listSortedOverrideSnapshots(ctx, ro)
			Expect(err).Should(Succeed())
			// we have 7 snapshots, and the limit is 7, so we should remove one. This is the base case.
			err = commonReconciler.removeExtraSnapshot(ctx, snapshotList, totalSnapshots)
			Expect(err).Should(Succeed())
			By("verifying that the oldest snapshot is removed")
			snapshot := getResourceOverrideSnapshot(testROName, ro.Namespace, 0)
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

	Context("Test ensureSnapshotLatest", func() {
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

	Context("Test cleanupStaleLatestSiblings on RO snapshots", func() {
		It("Should flip every stale latest=true sibling to false (post-crash recovery)", func() {
			// BeforeEach creates 7 snapshots all with latest=true, simulating the post-crash state.
			By("calling the audit on the freshly listed snapshots")
			snapshotList, err := commonReconciler.listSortedOverrideSnapshots(ctx, ro)
			Expect(err).Should(Succeed())
			Expect(commonReconciler.cleanupStaleLatestSiblings(ctx, snapshotList)).Should(Succeed())

			By("verifying that the highest-index snapshot keeps latest=true")
			highest := getResourceOverrideSnapshot(testROName, ro.Namespace, totalSnapshots-1)
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: highest.GetName(), Namespace: highest.Namespace}, highest)).Should(Succeed())
			Expect(highest.GetLabels()[placementv1beta1.IsLatestSnapshotLabel]).Should(Equal("true"))

			By("verifying that the older snapshots are flipped to latest=false")
			for i := 0; i < totalSnapshots-1; i++ {
				snapshot := getResourceOverrideSnapshot(testROName, ro.Namespace, i)
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: snapshot.GetName(), Namespace: snapshot.Namespace}, snapshot)).Should(Succeed())
				Expect(snapshot.GetLabels()[placementv1beta1.IsLatestSnapshotLabel]).Should(Equal("false"))
			}
		})
	})

	Context("Test ensureResourceOverrideSnapshot AlreadyExists recovery", func() {
		var aeReconciler *ResourceReconciler
		var aeROName string
		var aeRO *placementv1beta1.ResourceOverride

		BeforeEach(func() {
			// Use a dedicated RO so this Context's snapshots don't collide with the parent
			// BeforeEach's snapshots (testROName at indices 0..totalSnapshots-1).
			aeROName = fmt.Sprintf("test-ro-already-exists-%s", utils.RandStr())
			aeRO = getResourceOverride(aeROName, namespaceName)
			// Snapshot Create adds an OwnerReference whose UID must be non-empty for API-server
			// validation; we don't apply the RO to the cluster (would trigger the real reconcile
			// loop), so we synthesize a UID.
			aeRO.UID = "fake-uid-for-owner-ref"
			aeReconciler = &ResourceReconciler{Reconciler: commonReconciler}
		})

		AfterEach(func() {
			for i := 0; i < 2; i++ {
				snap := getResourceOverrideSnapshot(aeROName, namespaceName, i)
				Expect(k8sClient.Delete(ctx, snap)).Should(SatisfyAny(Succeed(), &utils.NotFoundMatcher{}))
			}
		})

		It("Should treat AlreadyExists with matching hash as success and demote the previous snapshot", func() {
			snapshot0 := getResourceOverrideSnapshot(aeROName, namespaceName, 0)
			snapshot0.Spec.OverrideHash = []byte("old-hash")
			Expect(k8sClient.Create(ctx, snapshot0)).Should(Succeed())

			// Pre-create snapshot 1 invisibly: stripping OverrideTrackingLabel hides it from
			// listSortedOverrideSnapshots so the controller computes newIndex=1 and Create races
			// into AlreadyExists. See the CRO test for the etcd-restore production rationale.
			intendedHash, err := resource.HashOf(aeRO.Spec)
			Expect(err).Should(Succeed())
			invisibleSnapshot1 := getResourceOverrideSnapshot(aeROName, namespaceName, 1)
			delete(invisibleSnapshot1.Labels, placementv1beta1.OverrideTrackingLabel)
			invisibleSnapshot1.Spec.OverrideHash = []byte(intendedHash)
			invisibleSnapshot1.Spec.OverrideSpec = aeRO.Spec
			Expect(k8sClient.Create(ctx, invisibleSnapshot1)).Should(Succeed())

			Expect(aeReconciler.ensureResourceOverrideSnapshot(ctx, aeRO, 10)).Should(Succeed(),
				"AlreadyExists with matching hash should be treated as success")

			By("Verifying snapshot 0 was demoted to latest=false")
			final0 := &placementv1beta1.ResourceOverrideSnapshot{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: snapshot0.Name, Namespace: snapshot0.Namespace}, final0)).Should(Succeed())
			Expect(final0.Labels[placementv1beta1.IsLatestSnapshotLabel]).Should(Equal("false"))
		})

		It("Should return ExpectedBehaviorError and emit a Warning event when AlreadyExists target has a mismatched hash", func() {
			fakeRecorder := record.NewFakeRecorder(10)
			aeReconciler.recorder = fakeRecorder

			snapshot0 := getResourceOverrideSnapshot(aeROName, namespaceName, 0)
			snapshot0.Spec.OverrideHash = []byte("old-hash")
			Expect(k8sClient.Create(ctx, snapshot0)).Should(Succeed())

			invisibleSnapshot1 := getResourceOverrideSnapshot(aeROName, namespaceName, 1)
			delete(invisibleSnapshot1.Labels, placementv1beta1.OverrideTrackingLabel)
			invisibleSnapshot1.Spec.OverrideHash = []byte("stale-hash-from-backup")
			Expect(k8sClient.Create(ctx, invisibleSnapshot1)).Should(Succeed())

			err := aeReconciler.ensureResourceOverrideSnapshot(ctx, aeRO, 10)
			Expect(err).Should(HaveOccurred(), "mismatched hash should not silently succeed")
			Expect(errors.Is(err, controller.ErrExpectedBehavior)).Should(BeTrue(),
				"error should wrap ErrExpectedBehavior, got %v", err)

			By("Verifying a Warning event with reason OverrideSnapshotHashMismatch was emitted on the parent RO")
			select {
			case ev := <-fakeRecorder.Events:
				Expect(ev).Should(ContainSubstring("OverrideSnapshotHashMismatch"),
					"event = %q, want reason OverrideSnapshotHashMismatch", ev)
				Expect(ev).Should(ContainSubstring("Warning"),
					"event should be a Warning, got %q", ev)
			default:
				Fail("expected a Warning event with reason OverrideSnapshotHashMismatch, got none")
			}

			By("Verifying snapshot 0 was NOT demoted (we abort before the demote step)")
			final0 := &placementv1beta1.ResourceOverrideSnapshot{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: snapshot0.Name, Namespace: snapshot0.Namespace}, final0)).Should(Succeed())
			Expect(final0.Labels[placementv1beta1.IsLatestSnapshotLabel]).Should(Equal("true"))
		})

		It("Should take the hash-match path and audit siblings when the latest snapshot's hash equals the RO spec", func() {
			// Mirror of the CRO hash-match test for the namespaced controller.
			intendedHash, err := resource.HashOf(aeRO.Spec)
			Expect(err).Should(Succeed())
			snapshot0 := getResourceOverrideSnapshot(aeROName, namespaceName, 0)
			snapshot0.Spec.OverrideHash = []byte(intendedHash)
			snapshot0.Spec.OverrideSpec = aeRO.Spec
			snapshot0.Labels[placementv1beta1.IsLatestSnapshotLabel] = strconv.FormatBool(false)
			Expect(k8sClient.Create(ctx, snapshot0)).Should(Succeed())

			Expect(aeReconciler.ensureResourceOverrideSnapshot(ctx, aeRO, 10)).Should(Succeed())

			By("Verifying ensureSnapshotLatest flipped snapshot 0 back to latest=true")
			final0 := &placementv1beta1.ResourceOverrideSnapshot{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: snapshot0.Name, Namespace: snapshot0.Namespace}, final0)).Should(Succeed())
			Expect(final0.Labels[placementv1beta1.IsLatestSnapshotLabel]).Should(Equal("true"))

			By("Verifying no new snapshot at index 1 was created")
			snapshot1 := getResourceOverrideSnapshot(aeROName, namespaceName, 1)
			Expect(apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: snapshot1.Name, Namespace: snapshot1.Namespace}, snapshot1))).Should(BeTrue(),
				"hash-match path must not create a new snapshot")
		})
	})
})
