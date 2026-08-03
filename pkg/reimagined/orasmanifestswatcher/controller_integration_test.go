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

package orasmanifestswatcher

import (
	"fmt"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

	// A short duration used to assert that something does NOT happen.
	consistentlyDuration = 3 * time.Second
	consistentlyInterval = 500 * time.Millisecond
)

var _ = Describe("ORASManifests watcher operations", Ordered, func() {
	Context("ORASManifests without a digest in status should not trigger PlacementPolicy creation until the digest is set", Ordered, func() {
		orasName := "my-oras-no-digest"

		BeforeAll(func() {
			oras := &experimentalv1beta1.ORASManifests{
				ObjectMeta: metav1.ObjectMeta{
					Name:      orasName,
					Namespace: workNamespace,
					Annotations: map[string]string{
						orasManifestsForPlacementAnnotationKey: "useast,eastasia",
					},
				},
				Spec: experimentalv1beta1.ORASManifestsSpec{
					OCIArtifact: &experimentalv1beta1.OCIArtifact{
						URL: "registry.example.com/myrepo",
					},
					Path: ".",
				},
			}
			Expect(hubClient.Create(ctx, oras)).To(Succeed())
		})

		AfterAll(func() {
			// Forcefully strip the finalizer and delete the ORASManifests.
			Eventually(func() error {
				oras := &experimentalv1beta1.ORASManifests{}
				if err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNamespace, Name: orasName}, oras); err != nil {
					if apierrors.IsNotFound(err) {
						return nil
					}
					return err
				}
				if controllerutil.ContainsFinalizer(oras, orasManifestsForPlacementFinalizer) {
					controllerutil.RemoveFinalizer(oras, orasManifestsForPlacementFinalizer)
					if err := hubClient.Update(ctx, oras); err != nil {
						return err
					}
				}
				if oras.DeletionTimestamp.IsZero() {
					if err := hubClient.Delete(ctx, oras); err != nil {
						return err
					}
				}
				return fmt.Errorf("ORASManifests still exists")
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "ORASManifests should be fully deleted")

			// Clean up the PlacementPolicy if it was unexpectedly created.
			Eventually(func() error {
				placement := &experimentalv1beta1.PlacementPolicy{}
				if err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNamespace, Name: orasName}, placement); err != nil {
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

		It("should not create a PlacementPolicy while the artifact digest is absent", func() {
			Consistently(func() error {
				placement := &experimentalv1beta1.PlacementPolicy{}
				err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNamespace, Name: orasName}, placement)
				if apierrors.IsNotFound(err) {
					return nil
				}
				if err != nil {
					return err
				}
				return fmt.Errorf("PlacementPolicy unexpectedly exists")
			}, consistentlyDuration, consistentlyInterval).Should(Succeed(), "PlacementPolicy should not be created before the digest is available")
		})

		It("should add the finalizer and create the PlacementPolicy once the artifact digest is set in status", func() {
			By("patching the ORASManifests status to include an artifact digest")
			oras := &experimentalv1beta1.ORASManifests{}
			Expect(hubClient.Get(ctx, types.NamespacedName{Namespace: workNamespace, Name: orasName}, oras)).To(Succeed())
			updatedOras := oras.DeepCopy()
			updatedOras.Status.OCIArtifactDetails = &experimentalv1beta1.OCIArtifactDetails{
				Digest: "sha256:abc123def456",
			}
			Expect(hubClient.Status().Update(ctx, updatedOras)).To(Succeed())

			By("waiting for the finalizer to be added to the ORASManifests")
			Eventually(func() ([]string, error) {
				o := &experimentalv1beta1.ORASManifests{}
				if err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNamespace, Name: orasName}, o); err != nil {
					return nil, err
				}
				return o.Finalizers, nil
			}, eventuallyDuration, eventuallyInterval).Should(ContainElement(orasManifestsForPlacementFinalizer), "ORASManifests should have the cleanup finalizer")

			By("waiting for the PlacementPolicy to be created")
			placement := &experimentalv1beta1.PlacementPolicy{}
			Eventually(func() error {
				return hubClient.Get(ctx, types.NamespacedName{Namespace: workNamespace, Name: orasName}, placement)
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "PlacementPolicy should be created")

			By("verifying the PlacementPolicy spec matches the expected state")
			wantPlacement := &experimentalv1beta1.PlacementPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      orasName,
					Namespace: workNamespace,
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
								{MatchLabels: map[string]string{"topology.kubernetes.io/region": "useast"}},
							},
							Count: ptr.To(intstr.FromInt(1)),
						},
					},
					ResourceSelectors: []experimentalv1beta1.SameNamespacedObjectReference{
						{
							Kind:       "ORASManifests",
							APIGroup:   "experimental.kubefleet.dev",
							APIVersion: "v1beta1",
							Name:       orasName,
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
	})

	Context("creating a new ORASManifests with annotation, updating the annotation, and removing the annotation", Ordered, func() {
		orasName := "my-oras-app"

		BeforeAll(func() {
			oras := &experimentalv1beta1.ORASManifests{
				ObjectMeta: metav1.ObjectMeta{
					Name:      orasName,
					Namespace: workNamespace,
					Annotations: map[string]string{
						orasManifestsForPlacementAnnotationKey: "useast,eastasia,uksouth",
					},
				},
				Spec: experimentalv1beta1.ORASManifestsSpec{
					OCIArtifact: &experimentalv1beta1.OCIArtifact{
						URL: "registry.example.com/myrepo",
					},
					Path: ".",
				},
			}
			Expect(hubClient.Create(ctx, oras)).To(Succeed())

			// Set a digest in the status so the controller will proceed.
			updatedOras := oras.DeepCopy()
			updatedOras.Status.OCIArtifactDetails = &experimentalv1beta1.OCIArtifactDetails{
				Digest: "sha256:deadbeef1234",
			}
			Expect(hubClient.Status().Update(ctx, updatedOras)).To(Succeed())
		})

		AfterAll(func() {
			// Forcefully strip the finalizer and delete the ORASManifests.
			Eventually(func() error {
				oras := &experimentalv1beta1.ORASManifests{}
				if err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNamespace, Name: orasName}, oras); err != nil {
					if apierrors.IsNotFound(err) {
						return nil
					}
					return err
				}
				if controllerutil.ContainsFinalizer(oras, orasManifestsForPlacementFinalizer) {
					controllerutil.RemoveFinalizer(oras, orasManifestsForPlacementFinalizer)
					if err := hubClient.Update(ctx, oras); err != nil {
						return err
					}
				}
				if oras.DeletionTimestamp.IsZero() {
					if err := hubClient.Delete(ctx, oras); err != nil {
						return err
					}
				}
				return fmt.Errorf("ORASManifests still exists")
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "ORASManifests should be fully deleted")

			// Always issue a Delete on the PlacementPolicy (idempotent — ignore not-found),
			// then wait for it to be fully gone.
			Eventually(func() error {
				placement := &experimentalv1beta1.PlacementPolicy{}
				if err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNamespace, Name: orasName}, placement); err != nil {
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

		It("should add the finalizer to the ORASManifests and create the PlacementPolicy", func() {
			By("waiting for the finalizer to be added to the ORASManifests")
			oras := &experimentalv1beta1.ORASManifests{}
			Eventually(func() ([]string, error) {
				if err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNamespace, Name: orasName}, oras); err != nil {
					return nil, err
				}
				return oras.Finalizers, nil
			}, eventuallyDuration, eventuallyInterval).Should(ContainElement(orasManifestsForPlacementFinalizer), "ORASManifests should have the cleanup finalizer")

			By("waiting for the PlacementPolicy to be created")
			placement := &experimentalv1beta1.PlacementPolicy{}
			Eventually(func() error {
				return hubClient.Get(ctx, types.NamespacedName{Namespace: workNamespace, Name: orasName}, placement)
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "PlacementPolicy should be created")

			By("verifying the PlacementPolicy spec matches the expected state")
			// The controller sorts the target regions alphabetically before building selectors.
			wantPlacement := &experimentalv1beta1.PlacementPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      orasName,
					Namespace: workNamespace,
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
							Kind:       "ORASManifests",
							APIGroup:   "experimental.kubefleet.dev",
							APIVersion: "v1beta1",
							Name:       orasName,
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
			By("updating the ORASManifests annotation to drop useast and uksouth and add uscentral")
			oras := &experimentalv1beta1.ORASManifests{}
			Expect(hubClient.Get(ctx, types.NamespacedName{Namespace: workNamespace, Name: orasName}, oras)).To(Succeed())
			updatedOras := oras.DeepCopy()
			updatedOras.Annotations[orasManifestsForPlacementAnnotationKey] = "eastasia,uscentral"
			Expect(hubClient.Update(ctx, updatedOras)).To(Succeed())

			By("waiting for the PlacementPolicy to be updated with the new regions")
			// The controller sorts the target regions alphabetically: eastasia, uscentral.
			wantPlacement := &experimentalv1beta1.PlacementPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      orasName,
					Namespace: workNamespace,
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
							Kind:       "ORASManifests",
							APIGroup:   "experimental.kubefleet.dev",
							APIVersion: "v1beta1",
							Name:       orasName,
						},
					},
				},
			}
			placement := &experimentalv1beta1.PlacementPolicy{}
			Eventually(func() ([]experimentalv1beta1.ClusterSelector, error) {
				if err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNamespace, Name: orasName}, placement); err != nil {
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
			By("removing the place-to annotation from the ORASManifests")
			oras := &experimentalv1beta1.ORASManifests{}
			Expect(hubClient.Get(ctx, types.NamespacedName{Namespace: workNamespace, Name: orasName}, oras)).To(Succeed())
			updatedOras := oras.DeepCopy()
			delete(updatedOras.Annotations, orasManifestsForPlacementAnnotationKey)
			Expect(hubClient.Update(ctx, updatedOras)).To(Succeed())

			By("waiting for the PlacementPolicy to be deleted")
			Eventually(func() error {
				placement := &experimentalv1beta1.PlacementPolicy{}
				err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNamespace, Name: orasName}, placement)
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

	Context("creating an ORASManifests with annotation then deleting it", Ordered, func() {
		orasName := "my-oras-delete"

		BeforeAll(func() {
			oras := &experimentalv1beta1.ORASManifests{
				ObjectMeta: metav1.ObjectMeta{
					Name:      orasName,
					Namespace: workNamespace,
					Annotations: map[string]string{
						orasManifestsForPlacementAnnotationKey: "useast,eastasia",
					},
				},
				Spec: experimentalv1beta1.ORASManifestsSpec{
					OCIArtifact: &experimentalv1beta1.OCIArtifact{
						URL: "registry.example.com/myrepo",
					},
					Path: ".",
				},
			}
			Expect(hubClient.Create(ctx, oras)).To(Succeed())

			// Set a digest so the controller proceeds.
			updatedOras := oras.DeepCopy()
			updatedOras.Status.OCIArtifactDetails = &experimentalv1beta1.OCIArtifactDetails{
				Digest: "sha256:cafebabe5678",
			}
			Expect(hubClient.Status().Update(ctx, updatedOras)).To(Succeed())
		})

		AfterAll(func() {
			// Forcefully strip the finalizer and delete the ORASManifests in case an earlier It node failed.
			Eventually(func() error {
				oras := &experimentalv1beta1.ORASManifests{}
				if err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNamespace, Name: orasName}, oras); err != nil {
					if apierrors.IsNotFound(err) {
						return nil
					}
					return err
				}
				if controllerutil.ContainsFinalizer(oras, orasManifestsForPlacementFinalizer) {
					controllerutil.RemoveFinalizer(oras, orasManifestsForPlacementFinalizer)
					if err := hubClient.Update(ctx, oras); err != nil {
						return err
					}
				}
				if oras.DeletionTimestamp.IsZero() {
					if err := hubClient.Delete(ctx, oras); err != nil {
						return err
					}
				}
				return fmt.Errorf("ORASManifests still exists")
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "ORASManifests should be fully deleted")

			// Always issue a Delete on the PlacementPolicy (idempotent — ignore not-found),
			// then wait for it to be fully gone.
			Eventually(func() error {
				placement := &experimentalv1beta1.PlacementPolicy{}
				if err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNamespace, Name: orasName}, placement); err != nil {
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

		It("should add the finalizer to the ORASManifests and create the PlacementPolicy", func() {
			By("waiting for the finalizer to be added to the ORASManifests")
			oras := &experimentalv1beta1.ORASManifests{}
			Eventually(func() ([]string, error) {
				if err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNamespace, Name: orasName}, oras); err != nil {
					return nil, err
				}
				return oras.Finalizers, nil
			}, eventuallyDuration, eventuallyInterval).Should(ContainElement(orasManifestsForPlacementFinalizer), "ORASManifests should have the cleanup finalizer")

			By("waiting for the PlacementPolicy to be created")
			placement := &experimentalv1beta1.PlacementPolicy{}
			Eventually(func() error {
				return hubClient.Get(ctx, types.NamespacedName{Namespace: workNamespace, Name: orasName}, placement)
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "PlacementPolicy should be created")
		})

		It("should delete the PlacementPolicy and remove the finalizer when the ORASManifests is deleted", func() {
			By("deleting the ORASManifests")
			oras := &experimentalv1beta1.ORASManifests{}
			Expect(hubClient.Get(ctx, types.NamespacedName{Namespace: workNamespace, Name: orasName}, oras)).To(Succeed())
			Expect(hubClient.Delete(ctx, oras)).To(Succeed())

			By("waiting for the controller to delete the PlacementPolicy")
			Eventually(func() error {
				placement := &experimentalv1beta1.PlacementPolicy{}
				err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNamespace, Name: orasName}, placement)
				if apierrors.IsNotFound(err) {
					return nil
				}
				if err != nil {
					return err
				}
				return fmt.Errorf("PlacementPolicy still exists")
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "PlacementPolicy should be deleted by the controller")

			By("waiting for the controller to remove the finalizer and fully delete the ORASManifests")
			Eventually(func() error {
				o := &experimentalv1beta1.ORASManifests{}
				err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNamespace, Name: orasName}, o)
				if apierrors.IsNotFound(err) {
					return nil
				}
				if err != nil {
					return err
				}
				return fmt.Errorf("ORASManifests still exists")
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "ORASManifests should be fully deleted after the controller removes its finalizer")
		})
	})
})
