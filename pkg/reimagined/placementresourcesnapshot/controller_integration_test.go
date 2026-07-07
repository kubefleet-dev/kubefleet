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

package placementresourcesnapshot

import (
	"fmt"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	experimentalv1beta1 "github.com/kubefleet-dev/kubefleet/apis/experimental/v1beta1"
)

const (
	// Eventually polling interval and timeout.
	eventuallyInterval = 500 * time.Millisecond
	eventuallyDuration = 10 * time.Second
)

var _ = Describe("processing placement resource snapshot requests", Ordered, func() {
	Context("can fulfill a new placement resource snapshot request", Ordered, func() {
		deployName := "web-app"
		cmName := "app-config"
		placementName := "web-app"

		BeforeAll(func() {
			By("creating the Deployment")
			deploy := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      deployName,
					Namespace: workNSName,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: ptr.To(int32(1)),
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": deployName},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{"app": deployName},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "app",
									Image: "nginx:latest",
								},
							},
						},
					},
				},
			}
			Expect(hubClient.Create(ctx, deploy)).To(Succeed())

			By("creating the ConfigMap")
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cmName,
					Namespace: workNSName,
				},
				Data: map[string]string{
					"key": "value",
				},
			}
			Expect(hubClient.Create(ctx, cm)).To(Succeed())

			By("creating the PlacementPolicy referencing the Deployment and the ConfigMap")
			placement := &experimentalv1beta1.PlacementPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      placementName,
					Namespace: workNSName,
				},
				Spec: experimentalv1beta1.PlacementPolicySpec{
					ClusterSelectors: []experimentalv1beta1.ClusterSelector{
						{
							Terms: []experimentalv1beta1.LabelAndClusterPropertySelectorTerm{
								{
									MatchLabels: map[string]string{"topology.kubernetes.io/region": "useast"},
								},
							},
						},
					},
					ResourceSelectors: []experimentalv1beta1.SameNamespacedObjectReference{
						{
							Kind:       "Deployment",
							APIGroup:   "apps",
							APIVersion: "v1",
							Resource:   "deployments",
							Name:       deployName,
						},
						{
							Kind:       "ConfigMap",
							APIGroup:   "",
							APIVersion: "v1",
							Resource:   "configmaps",
							Name:       cmName,
						},
					},
				},
			}
			Expect(hubClient.Create(ctx, placement)).To(Succeed())
		})

		AfterAll(func() {
			By("deleting the PlacementResourceSnapshotRequest")
			req := &experimentalv1beta1.PlacementResourceSnapshotRequest{
				ObjectMeta: metav1.ObjectMeta{Name: "snapshot-req-1", Namespace: workNSName},
			}
			Expect(client.IgnoreNotFound(hubClient.Delete(ctx, req))).To(Succeed())
			Eventually(func() bool {
				return apierrors.IsNotFound(hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: "snapshot-req-1"}, req))
			}, eventuallyDuration, eventuallyInterval).Should(BeTrue(), "PlacementResourceSnapshotRequest should be deleted")

			By("deleting the PlacementResourceSnapshot")
			snapshot := &experimentalv1beta1.PlacementResourceSnapshot{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-1", placementName), Namespace: workNSName},
			}
			Expect(client.IgnoreNotFound(hubClient.Delete(ctx, snapshot))).To(Succeed())
			Eventually(func() bool {
				return apierrors.IsNotFound(hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: fmt.Sprintf("%s-1", placementName)}, snapshot))
			}, eventuallyDuration, eventuallyInterval).Should(BeTrue(), "PlacementResourceSnapshot should be deleted")

			By("deleting the PlacementPolicy")
			placement := &experimentalv1beta1.PlacementPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: placementName, Namespace: workNSName},
			}
			Expect(client.IgnoreNotFound(hubClient.Delete(ctx, placement))).To(Succeed())
			Eventually(func() bool {
				return apierrors.IsNotFound(hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: placementName}, placement))
			}, eventuallyDuration, eventuallyInterval).Should(BeTrue(), "PlacementPolicy should be deleted")

			By("deleting the ConfigMap")
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: cmName, Namespace: workNSName},
			}
			Expect(client.IgnoreNotFound(hubClient.Delete(ctx, cm))).To(Succeed())
			Eventually(func() bool {
				return apierrors.IsNotFound(hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: cmName}, cm))
			}, eventuallyDuration, eventuallyInterval).Should(BeTrue(), "ConfigMap should be deleted")

			By("deleting the Deployment")
			deploy := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: deployName, Namespace: workNSName},
			}
			Expect(client.IgnoreNotFound(hubClient.Delete(ctx, deploy))).To(Succeed())
			Eventually(func() bool {
				return apierrors.IsNotFound(hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: deployName}, deploy))
			}, eventuallyDuration, eventuallyInterval).Should(BeTrue(), "Deployment should be deleted")
		})

		It("should complete the request and set the Completed condition to True", func() {
			reqName := "snapshot-req-1"

			By("creating the PlacementResourceSnapshotRequest")
			req := &experimentalv1beta1.PlacementResourceSnapshotRequest{
				ObjectMeta: metav1.ObjectMeta{
					Name:      reqName,
					Namespace: workNSName,
				},
				Spec: experimentalv1beta1.PlacementResourceSnapshotRequestSpec{
					PlacementPolicyRef: experimentalv1beta1.SameNamespacedObjectReference{
						Kind:       "PlacementPolicy",
						APIGroup:   "experimental.kubefleet.dev",
						APIVersion: "v1beta1",
						Resource:   "placementpolicies",
						Name:       placementName,
					},
				},
			}
			Expect(hubClient.Create(ctx, req)).To(Succeed())

			By("waiting for the request to be completed")
			wantConditions := []metav1.Condition{
				{
					Type:               experimentalv1beta1.PlacementResourceSnapshotRequestCondTypeCompleted,
					Status:             metav1.ConditionTrue,
					ObservedGeneration: 1,
					Reason:             experimentalv1beta1.PlacementResourceSnapshotRequestCompletedReasonSuccess,
					Message:            "Successfully added new snapshot for the placement policy",
				},
			}
			Eventually(func() string {
				if err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: reqName}, req); err != nil {
					return fmt.Sprintf("failed to get PlacementResourceSnapshotRequest: %v", err)
				}
				return cmp.Diff(wantConditions, req.Status.Conditions, cmpopts.IgnoreFields(metav1.Condition{}, "LastTransitionTime"))
			}, eventuallyDuration, eventuallyInterval).Should(BeEmpty(), "request status conditions mismatch (-want, +got)")
		})

		It("should have created exactly one PlacementResourceSnapshot with the expected content", func() {
			By("listing PlacementResourceSnapshots for the placement policy")
			snapshotList := &experimentalv1beta1.PlacementResourceSnapshotList{}
			Expect(hubClient.List(ctx, snapshotList,
				client.MatchingLabels{experimentalv1beta1.ResourceSnapshotOwnedByLabelKey: placementName},
				client.InNamespace(workNSName),
			)).To(Succeed())
			Expect(snapshotList.Items).To(HaveLen(1), "expected exactly one PlacementResourceSnapshot")

			By("verifying the content of the snapshot")
			wantSnapshot := experimentalv1beta1.PlacementResourceSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s-1", placementName),
					Namespace: workNSName,
					Labels: map[string]string{
						experimentalv1beta1.ResourceSnapshotOwnedByLabelKey:  placementName,
						experimentalv1beta1.ResourceSnapshotRevisionLabelKey: "1",
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: experimentalv1beta1.GroupVersion.String(),
							Kind:       "PlacementPolicy",
							Name:       placementName,
							Controller: ptr.To(true),
						},
					},
				},
				Spec: experimentalv1beta1.PlacementResourceSnapshotSpec{
					Resources: []experimentalv1beta1.ResourceContent{
						{
							Identifier: experimentalv1beta1.SameNamespacedObjectReference{
								Kind:       "Deployment",
								APIGroup:   "apps",
								APIVersion: "v1",
								Resource:   "deployments",
								Name:       deployName,
							},
						},
						{
							Identifier: experimentalv1beta1.SameNamespacedObjectReference{
								Kind:       "ConfigMap",
								APIGroup:   "",
								APIVersion: "v1",
								Resource:   "configmaps",
								Name:       cmName,
							},
						},
					},
				},
			}
			if diff := cmp.Diff(wantSnapshot, snapshotList.Items[0],
				cmpopts.IgnoreFields(metav1.ObjectMeta{}, "UID", "ResourceVersion", "Generation", "CreationTimestamp", "ManagedFields"),
				cmpopts.IgnoreFields(metav1.OwnerReference{}, "UID"),
				cmpopts.IgnoreFields(experimentalv1beta1.ResourceContent{}, "Manifest"),
				cmpopts.IgnoreTypes(metav1.TypeMeta{}),
			); diff != "" {
				Fail(fmt.Sprintf("PlacementResourceSnapshot mismatch (-want, +got):\n%s", diff))
			}
		})

		It("should confirm the latest snapshot is up to date using the manager methods", func() {
			By("fetching the PlacementPolicy")
			placement := &experimentalv1beta1.PlacementPolicy{}
			Expect(hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: placementName}, placement)).To(Succeed())

			By("retrieving the latest resource snapshot via RetrieveLatestResourceSnapshot")
			snapshot, err := snapshotMgr.RetrieveLatestResourceSnapshot(ctx, placement)
			Expect(err).NotTo(HaveOccurred())
			Expect(snapshot).NotTo(BeNil())
			Expect(snapshot.Name).To(Equal(fmt.Sprintf("%s-1", placementName)))

			By("verifying the snapshot is up to date via IsResourceSnapshotUpToDate")
			upToDate, err := snapshotMgr.IsResourceSnapshotUpToDate(ctx, placement, snapshot)
			Expect(err).NotTo(HaveOccurred())
			Expect(upToDate).To(BeTrue())
		})
	})
})
