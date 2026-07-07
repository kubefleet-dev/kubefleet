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

package deploymentwatcher

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
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	experimentalv1beta1 "github.com/kubefleet-dev/kubefleet/apis/experimental/v1beta1"
)

const (
	// Eventually polling interval and timeout.
	eventuallyInterval = 500 * time.Millisecond
	eventuallyDuration = 10 * time.Second
)

var _ = Describe("deployment operations", Ordered, func() {
	Context("creating a new deployment with annotation, updating the annotation, and removing the annotation", Ordered, func() {
		deployName := "my-app"

		BeforeAll(func() {
			deploy := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      deployName,
					Namespace: workNameName,
					Annotations: map[string]string{
						deploymentForPlacementAnnotationKey: "useast,eastasia,uksouth",
					},
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
		})

		AfterAll(func() {
			// Forcefully strip the finalizer and delete the deployment, in case the controller
			// did not remove it (e.g., if an earlier It node failed before removing the annotation).
			Eventually(func() error {
				deploy := &appsv1.Deployment{}
				if err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNameName, Name: deployName}, deploy); err != nil {
					if apierrors.IsNotFound(err) {
						return nil
					}
					return err
				}
				// Strip the finalizer so the API server can honor the deletion.
				if controllerutil.ContainsFinalizer(deploy, deploymentForPlacementFinalizer) {
					controllerutil.RemoveFinalizer(deploy, deploymentForPlacementFinalizer)
					if err := hubClient.Update(ctx, deploy); err != nil {
						return err
					}
				}
				if deploy.DeletionTimestamp.IsZero() {
					if err := hubClient.Delete(ctx, deploy); err != nil {
						return err
					}
				}
				return fmt.Errorf("deployment still exists")
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "deployment should be fully deleted")

			// Always issue a Delete on the PlacementPolicy (idempotent — ignore not-found),
			// then wait for it to be fully gone.
			Eventually(func() error {
				placement := &experimentalv1beta1.PlacementPolicy{}
				if err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNameName, Name: deployName}, placement); err != nil {
					if apierrors.IsNotFound(err) {
						return nil
					}
					return err
				}
				if err := hubClient.Delete(ctx, placement); err != nil && !apierrors.IsNotFound(err) {
					return err
				}
				return fmt.Errorf("PlacementPolicy still exists")
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "PlacementPolicy should be fully deleted")
		})

		It("should add the finalizer to the deployment and create the PlacementPolicy", func() {
			By("waiting for the finalizer to be added to the deployment")
			deploy := &appsv1.Deployment{}
			Eventually(func() ([]string, error) {
				if err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNameName, Name: deployName}, deploy); err != nil {
					return nil, err
				}
				return deploy.Finalizers, nil
			}, eventuallyDuration, eventuallyInterval).Should(ContainElement(deploymentForPlacementFinalizer), "deployment should have the cleanup finalizer")

			By("waiting for the PlacementPolicy to be created")
			placement := &experimentalv1beta1.PlacementPolicy{}
			Eventually(func() error {
				return hubClient.Get(ctx, types.NamespacedName{Namespace: workNameName, Name: deployName}, placement)
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "PlacementPolicy should be created")

			By("verifying the PlacementPolicy spec matches the expected state")
			// The controller sorts the target regions alphabetically before building selectors.
			wantPlacement := &experimentalv1beta1.PlacementPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      deployName,
					Namespace: workNameName,
				},
				Spec: experimentalv1beta1.PlacementPolicySpec{
					ClusterSelectors: []experimentalv1beta1.ClusterSelector{
						{
							Terms: []experimentalv1beta1.LabelAndClusterPropertySelectorTerm{
								{MatchLabels: map[string]string{"topology.kubernetes.io/region": "eastasia"}},
							},
							Count: ptr.To(intstr.FromInt(1)),
						},
						{
							Terms: []experimentalv1beta1.LabelAndClusterPropertySelectorTerm{
								{MatchLabels: map[string]string{"topology.kubernetes.io/region": "uksouth"}},
							},
							Count: ptr.To(intstr.FromInt(1)),
						},
						{
							Terms: []experimentalv1beta1.LabelAndClusterPropertySelectorTerm{
								{MatchLabels: map[string]string{"topology.kubernetes.io/region": "useast"}},
							},
							Count: ptr.To(intstr.FromInt(1)),
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
					},
				},
			}
			if diff := cmp.Diff(placement, wantPlacement,
				cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion", "UID", "CreationTimestamp", "ManagedFields", "Generation"),
				cmpopts.IgnoreFields(experimentalv1beta1.PlacementPolicySpec{}, "ResourceRevisionHistoryLimit", "SyncStrategy", "Tolerations"),
				cmpopts.IgnoreFields(experimentalv1beta1.PlacementPolicy{}, "Status"),
				cmpopts.IgnoreTypes(metav1.TypeMeta{}),
			); diff != "" {
				Fail(fmt.Sprintf("PlacementPolicy mismatch (-got, +want):\n%s", diff))
			}
		})

		It("should update the PlacementPolicy when the annotation changes", func() {
			By("updating the deployment annotation to drop useast and uksouth and add uscentral")
			deploy := &appsv1.Deployment{}
			Expect(hubClient.Get(ctx, types.NamespacedName{Namespace: workNameName, Name: deployName}, deploy)).To(Succeed())
			updatedDeploy := deploy.DeepCopy()
			updatedDeploy.Annotations[deploymentForPlacementAnnotationKey] = "eastasia,uscentral"
			Expect(hubClient.Update(ctx, updatedDeploy)).To(Succeed())

			By("waiting for the PlacementPolicy to be updated with the new regions")
			// The controller sorts the target regions alphabetically: eastasia, uscentral.
			wantPlacement := &experimentalv1beta1.PlacementPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      deployName,
					Namespace: workNameName,
				},
				Spec: experimentalv1beta1.PlacementPolicySpec{
					ClusterSelectors: []experimentalv1beta1.ClusterSelector{
						{
							Terms: []experimentalv1beta1.LabelAndClusterPropertySelectorTerm{
								{MatchLabels: map[string]string{"topology.kubernetes.io/region": "eastasia"}},
							},
							Count: ptr.To(intstr.FromInt(1)),
						},
						{
							Terms: []experimentalv1beta1.LabelAndClusterPropertySelectorTerm{
								{MatchLabels: map[string]string{"topology.kubernetes.io/region": "uscentral"}},
							},
							Count: ptr.To(intstr.FromInt(1)),
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
					},
				},
			}
			placement := &experimentalv1beta1.PlacementPolicy{}
			Eventually(func() ([]experimentalv1beta1.ClusterSelector, error) {
				if err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNameName, Name: deployName}, placement); err != nil {
					return nil, err
				}
				return placement.Spec.ClusterSelectors, nil
			}, eventuallyDuration, eventuallyInterval).Should(HaveLen(2), "PlacementPolicy should have exactly two cluster selectors")

			if diff := cmp.Diff(placement, wantPlacement,
				cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion", "UID", "CreationTimestamp", "ManagedFields", "Generation"),
				cmpopts.IgnoreFields(experimentalv1beta1.PlacementPolicySpec{}, "ResourceRevisionHistoryLimit", "SyncStrategy", "Tolerations"),
				cmpopts.IgnoreFields(experimentalv1beta1.PlacementPolicy{}, "Status"),
				cmpopts.IgnoreTypes(metav1.TypeMeta{}),
			); diff != "" {
				Fail(fmt.Sprintf("PlacementPolicy mismatch (-got, +want):\n%s", diff))
			}
		})

		It("should delete the PlacementPolicy when the annotation is removed", func() {
			By("removing the place-to annotation from the deployment")
			deploy := &appsv1.Deployment{}
			Expect(hubClient.Get(ctx, types.NamespacedName{Namespace: workNameName, Name: deployName}, deploy)).To(Succeed())
			updatedDeploy := deploy.DeepCopy()
			delete(updatedDeploy.Annotations, deploymentForPlacementAnnotationKey)
			Expect(hubClient.Update(ctx, updatedDeploy)).To(Succeed())

			By("waiting for the PlacementPolicy to be deleted")
			Eventually(func() error {
				placement := &experimentalv1beta1.PlacementPolicy{}
				err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNameName, Name: deployName}, placement)
				if apierrors.IsNotFound(err) {
					return nil
				}
				if err != nil {
					return err
				}
				return fmt.Errorf("PlacementPolicy still exists")
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "PlacementPolicy should be deleted")
		})
	})

	Context("creating a deployment with annotation then deleting the deployment", Ordered, func() {
		deployName := "my-app-delete"

		BeforeAll(func() {
			deploy := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      deployName,
					Namespace: workNameName,
					Annotations: map[string]string{
						deploymentForPlacementAnnotationKey: "useast,eastasia",
					},
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
		})

		AfterAll(func() {
			// Forcefully strip the finalizer and delete the deployment, in case the controller
			// did not remove it (e.g., if an It node failed before the deletion test ran).
			Eventually(func() error {
				deploy := &appsv1.Deployment{}
				if err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNameName, Name: deployName}, deploy); err != nil {
					if apierrors.IsNotFound(err) {
						return nil
					}
					return err
				}
				if controllerutil.ContainsFinalizer(deploy, deploymentForPlacementFinalizer) {
					controllerutil.RemoveFinalizer(deploy, deploymentForPlacementFinalizer)
					if err := hubClient.Update(ctx, deploy); err != nil {
						return err
					}
				}
				if deploy.DeletionTimestamp.IsZero() {
					if err := hubClient.Delete(ctx, deploy); err != nil {
						return err
					}
				}
				return fmt.Errorf("deployment still exists")
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "deployment should be fully deleted")

			// Always issue a Delete on the PlacementPolicy (idempotent — ignore not-found),
			// then wait for it to be fully gone.
			Eventually(func() error {
				placement := &experimentalv1beta1.PlacementPolicy{}
				if err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNameName, Name: deployName}, placement); err != nil {
					if apierrors.IsNotFound(err) {
						return nil
					}
					return err
				}
				if err := hubClient.Delete(ctx, placement); err != nil && !apierrors.IsNotFound(err) {
					return err
				}
				return fmt.Errorf("PlacementPolicy still exists")
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "PlacementPolicy should be fully deleted")
		})

		It("should add the finalizer to the deployment and create the PlacementPolicy", func() {
			By("waiting for the finalizer to be added to the deployment")
			deploy := &appsv1.Deployment{}
			Eventually(func() ([]string, error) {
				if err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNameName, Name: deployName}, deploy); err != nil {
					return nil, err
				}
				return deploy.Finalizers, nil
			}, eventuallyDuration, eventuallyInterval).Should(ContainElement(deploymentForPlacementFinalizer), "deployment should have the cleanup finalizer")

			By("waiting for the PlacementPolicy to be created")
			placement := &experimentalv1beta1.PlacementPolicy{}
			Eventually(func() error {
				return hubClient.Get(ctx, types.NamespacedName{Namespace: workNameName, Name: deployName}, placement)
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "PlacementPolicy should be created")
		})

		It("should delete the PlacementPolicy and remove the finalizer when the deployment is deleted", func() {
			By("deleting the deployment")
			deploy := &appsv1.Deployment{}
			Expect(hubClient.Get(ctx, types.NamespacedName{Namespace: workNameName, Name: deployName}, deploy)).To(Succeed())
			Expect(hubClient.Delete(ctx, deploy)).To(Succeed())

			By("waiting for the controller to delete the PlacementPolicy")
			Eventually(func() error {
				placement := &experimentalv1beta1.PlacementPolicy{}
				err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNameName, Name: deployName}, placement)
				if apierrors.IsNotFound(err) {
					return nil
				}
				if err != nil {
					return err
				}
				return fmt.Errorf("PlacementPolicy still exists")
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "PlacementPolicy should be deleted by the controller")

			By("waiting for the controller to remove the finalizer and fully delete the deployment")
			Eventually(func() error {
				deploy := &appsv1.Deployment{}
				err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNameName, Name: deployName}, deploy)
				if apierrors.IsNotFound(err) {
					return nil
				}
				if err != nil {
					return err
				}
				return fmt.Errorf("deployment still exists")
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "deployment should be fully deleted after the controller removes its finalizer")
		})
	})
})
