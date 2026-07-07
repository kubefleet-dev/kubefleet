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

package placementpolicy

import (
	"fmt"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	experimentalv1beta1 "github.com/kubefleet-dev/kubefleet/apis/experimental/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/resource"
)

const (
	// Eventually polling interval and timeout.
	eventuallyInterval = 500 * time.Millisecond
	eventuallyDuration = 10 * time.Second
)

var _ = Describe("placement policy ops", func() {
	Context("creating a new placement policy that targets existing clusters", Ordered, func() {
		BeforeAll(func() {
			By("creating a Deployment")
			deploy := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "app",
					Namespace: workNSName,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: ptr.To(int32(1)),
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": "app"},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{"app": "app"},
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

			By("creating a ConfigMap")
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "app-config",
					Namespace: workNSName,
				},
				Data: map[string]string{
					"key": "value",
				},
			}
			Expect(hubClient.Create(ctx, cm)).To(Succeed())

			By("creating 3 member clusters, one per region")
			clusters := []struct {
				name   string
				region string
			}{
				{"useast", "useast"},
				{"chinanorth", "chinanorth"},
				{"uksouth", "uksouth"},
			}
			for _, c := range clusters {
				mc := &clusterv1beta1.MemberCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: c.name,
						Labels: map[string]string{
							"region": c.region,
						},
					},
					Spec: clusterv1beta1.MemberClusterSpec{
						Identity: rbacv1.Subject{
							Kind: rbacv1.ServiceAccountKind,
							Name: "hub-access",
						},
					},
				}
				Expect(hubClient.Create(ctx, mc)).To(Succeed())
			}

			By("creating the PlacementPolicy")
			placement := &experimentalv1beta1.PlacementPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-placement",
					Namespace: workNSName,
				},
				Spec: experimentalv1beta1.PlacementPolicySpec{
					ClusterSelectors: []experimentalv1beta1.ClusterSelector{
						{Terms: []experimentalv1beta1.LabelAndClusterPropertySelectorTerm{{MatchLabels: map[string]string{"region": "useast"}}}},
						{Terms: []experimentalv1beta1.LabelAndClusterPropertySelectorTerm{{MatchLabels: map[string]string{"region": "chinanorth"}}}},
						{Terms: []experimentalv1beta1.LabelAndClusterPropertySelectorTerm{{MatchLabels: map[string]string{"region": "uksouth"}}}},
					},
					ResourceSelectors: []experimentalv1beta1.SameNamespacedObjectReference{
						{
							Name:       "app",
							APIGroup:   "apps",
							APIVersion: "v1",
							Kind:       "Deployment",
							Resource:   "deployments",
						},
						{
							Name:       "app-config",
							APIGroup:   "",
							APIVersion: "v1",
							Kind:       "ConfigMap",
							Resource:   "configmaps",
						},
					},
				},
			}
			Expect(hubClient.Create(ctx, placement)).To(Succeed())
		})

		It("should add the cleanup finalizer to the placement policy", func() {
			placement := &experimentalv1beta1.PlacementPolicy{}
			Eventually(func() ([]string, error) {
				if err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: "my-placement"}, placement); err != nil {
					return nil, err
				}
				return placement.Finalizers, nil
			}, eventuallyDuration, eventuallyInterval).Should(ContainElement(placementPolicyCleanupFinalizer),
				"placement should have the cleanup finalizer")

			wantFinalizers := []string{placementPolicyCleanupFinalizer}
			if diff := cmp.Diff(placement.Finalizers, wantFinalizers); diff != "" {
				Fail(fmt.Sprintf("placement finalizers mismatch (-got, +want):\n%s", diff))
			}
		})

		It("should create a resource snapshot for the placement policy", func() {
			wantSnapshotName := "my-placement-1"
			wantSnapshot := &experimentalv1beta1.PlacementResourceSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name:      wantSnapshotName,
					Namespace: workNSName,
					Labels: map[string]string{
						experimentalv1beta1.ResourceSnapshotOwnedByLabelKey:  "my-placement",
						experimentalv1beta1.ResourceSnapshotRevisionLabelKey: "1",
					},
				},
				Spec: experimentalv1beta1.PlacementResourceSnapshotSpec{
					Resources: []experimentalv1beta1.ResourceContent{
						{
							Identifier: experimentalv1beta1.SameNamespacedObjectReference{
								APIGroup:   "apps",
								APIVersion: "v1",
								Kind:       "Deployment",
								Resource:   "deployments",
								Name:       "app",
							},
						},
						{
							Identifier: experimentalv1beta1.SameNamespacedObjectReference{
								APIGroup:   "",
								APIVersion: "v1",
								Kind:       "ConfigMap",
								Resource:   "configmaps",
								Name:       "app-config",
							},
						},
					},
				},
			}

			By("waiting for the resource snapshot to be created")
			snapshot := &experimentalv1beta1.PlacementResourceSnapshot{}
			Eventually(func() error {
				return hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: wantSnapshotName}, snapshot)
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "PlacementResourceSnapshot should be created")

			By("verifying the resource snapshot matches the expected state")
			if diff := cmp.Diff(snapshot, wantSnapshot,
				cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion", "UID", "CreationTimestamp", "ManagedFields", "Generation", "OwnerReferences"),
				cmpopts.IgnoreFields(experimentalv1beta1.ResourceContent{}, "Manifest"),
			); diff != "" {
				Fail(fmt.Sprintf("resource snapshot mismatch (-got, +want):\n%s", diff))
			}
		})

		It("should create 3 placement bindings for the placement policy", func() {
			By("waiting for 3 placement bindings to be created")
			bindingList := &experimentalv1beta1.PlacementBindingList{}
			Eventually(func() (int, error) {
				if err := hubClient.List(ctx, bindingList,
					client.InNamespace(workNSName),
					client.MatchingLabels{
						experimentalv1beta1.PlacementBindingOwnedByLabelKey: "my-placement",
					},
				); err != nil {
					return 0, err
				}
				return len(bindingList.Items), nil
			}, eventuallyDuration, eventuallyInterval).Should(Equal(3), "3 placement bindings should be created")

			By("computing expected cluster selector hashes")
			useastHash, err := resource.HashOf(&experimentalv1beta1.ClusterSelector{Terms: []experimentalv1beta1.LabelAndClusterPropertySelectorTerm{{MatchLabels: map[string]string{"region": "useast"}}}})
			Expect(err).NotTo(HaveOccurred())
			chinanorthHash, err := resource.HashOf(&experimentalv1beta1.ClusterSelector{Terms: []experimentalv1beta1.LabelAndClusterPropertySelectorTerm{{MatchLabels: map[string]string{"region": "chinanorth"}}}})
			Expect(err).NotTo(HaveOccurred())
			uksouthHash, err := resource.HashOf(&experimentalv1beta1.ClusterSelector{Terms: []experimentalv1beta1.LabelAndClusterPropertySelectorTerm{{MatchLabels: map[string]string{"region": "uksouth"}}}})
			Expect(err).NotTo(HaveOccurred())

			snapshotRevision := ptr.To("my-placement-1")
			wantBindings := []experimentalv1beta1.PlacementBinding{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-placement-useast",
						Namespace: workNSName,
						Labels: map[string]string{
							experimentalv1beta1.PlacementBindingOwnedByLabelKey: "my-placement",
						},
					},
					Spec: experimentalv1beta1.PlacementBindingSpec{
						PlacementPolicyName:  "my-placement",
						ClusterSelectorHash:  useastHash,
						ClusterName:          "useast",
						ResourceSnapshotName: snapshotRevision,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-placement-chinanorth",
						Namespace: workNSName,
						Labels: map[string]string{
							experimentalv1beta1.PlacementBindingOwnedByLabelKey: "my-placement",
						},
					},
					Spec: experimentalv1beta1.PlacementBindingSpec{
						PlacementPolicyName:  "my-placement",
						ClusterSelectorHash:  chinanorthHash,
						ClusterName:          "chinanorth",
						ResourceSnapshotName: snapshotRevision,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-placement-uksouth",
						Namespace: workNSName,
						Labels: map[string]string{
							experimentalv1beta1.PlacementBindingOwnedByLabelKey: "my-placement",
						},
					},
					Spec: experimentalv1beta1.PlacementBindingSpec{
						PlacementPolicyName:  "my-placement",
						ClusterSelectorHash:  uksouthHash,
						ClusterName:          "uksouth",
						ResourceSnapshotName: snapshotRevision,
					},
				},
			}

			By("verifying the placement bindings match the expected state")
			if diff := cmp.Diff(bindingList.Items, wantBindings,
				cmpopts.IgnoreFields(metav1.TypeMeta{}, "Kind", "APIVersion"),
				cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion", "UID", "CreationTimestamp", "ManagedFields", "Generation", "OwnerReferences"),
				cmpopts.IgnoreFields(experimentalv1beta1.PlacementBindingStatus{}, "Conditions"),
				cmpopts.IgnoreFields(experimentalv1beta1.PlacementBindingSpec{}, "ClusterSelector"),
				cmpopts.SortSlices(func(a, b experimentalv1beta1.PlacementBinding) bool {
					return a.Name < b.Name
				}),
			); diff != "" {
				Fail(fmt.Sprintf("placement bindings mismatch (-got, +want):\n%s", diff))
			}
		})

		It("should set the placement policy status correctly", func() {
			wantStatus := experimentalv1beta1.PlacementPolicyStatus{
				LatestResourceRevisionName: ptr.To("my-placement-1"),
				Conditions: []metav1.Condition{
					{
						Type:   experimentalv1beta1.PlacementPolicyCondTypeScheduled,
						Status: metav1.ConditionTrue,
						Reason: "FoundClustersForAllSelectors",
					},
					{
						Type:   experimentalv1beta1.PlacementPolicyCondTypeSynchronized,
						Status: metav1.ConditionFalse,
						Reason: "NotAllBindingsHaveSynchronizedResources",
					},
					{
						Type:   experimentalv1beta1.PlacementPolicyCondTypeAvailable,
						Status: metav1.ConditionFalse,
						Reason: "NotAllBindingsHaveResourcesAvailable",
					},
				},
			}

			By("waiting for all 3 status conditions to be populated")
			placement := &experimentalv1beta1.PlacementPolicy{}
			Eventually(func() ([]metav1.Condition, error) {
				if err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: "my-placement"}, placement); err != nil {
					return nil, err
				}
				return placement.Status.Conditions, nil
			}, eventuallyDuration, eventuallyInterval).Should(HaveLen(3),
				"placement should have 3 status conditions")

			By("verifying the placement policy status matches the expected state")
			if diff := cmp.Diff(placement.Status, wantStatus,
				cmpopts.IgnoreFields(experimentalv1beta1.PlacementPolicyStatus{}, "BindingManager"),
				cmpopts.IgnoreFields(metav1.Condition{}, "ObservedGeneration", "LastTransitionTime", "Message"),
				cmpopts.SortSlices(func(a, b metav1.Condition) bool { return a.Type < b.Type }),
			); diff != "" {
				Fail(fmt.Sprintf("placement policy status mismatch (-got, +want):\n%s", diff))
			}
		})

		It("should patch all 3 binding statuses with Synchronized=True and AllResourcesAvailable=True", func() {
			bindingNames := []string{
				"my-placement-useast",
				"my-placement-chinanorth",
				"my-placement-uksouth",
			}
			for _, name := range bindingNames {
				By("patching binding " + name)
				binding := &experimentalv1beta1.PlacementBinding{}
				Expect(hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: name}, binding)).To(Succeed())

				updatedBinding := binding.DeepCopy()
				updatedBinding.Status.Conditions = []metav1.Condition{
					{
						Type:               experimentalv1beta1.PlacementBindingCondTypeSynchronized,
						Status:             metav1.ConditionTrue,
						Reason:             "AllResourcesApplied",
						ObservedGeneration: binding.Generation,
						LastTransitionTime: metav1.Now(),
					},
					{
						Type:               experimentalv1beta1.PlacementBindingCondTypeAllResourcesAvailable,
						Status:             metav1.ConditionTrue,
						Reason:             "AllResourcesAvailable",
						ObservedGeneration: binding.Generation,
						LastTransitionTime: metav1.Now(),
					},
				}
				Expect(hubClient.Status().Update(ctx, updatedBinding)).To(Succeed())
			}
		})

		It("should reflect Synchronized=True and AllResourcesAvailable=True on the placement status", func() {
			wantStatus := experimentalv1beta1.PlacementPolicyStatus{
				LatestResourceRevisionName: ptr.To("my-placement-1"),
				Conditions: []metav1.Condition{
					{
						Type:   experimentalv1beta1.PlacementPolicyCondTypeScheduled,
						Status: metav1.ConditionTrue,
						Reason: "FoundClustersForAllSelectors",
					},
					{
						Type:   experimentalv1beta1.PlacementPolicyCondTypeSynchronized,
						Status: metav1.ConditionTrue,
						Reason: "AllBindingsHaveUpToDateSnapshot",
					},
					{
						Type:   experimentalv1beta1.PlacementPolicyCondTypeAvailable,
						Status: metav1.ConditionTrue,
						Reason: "AllBindingsHaveResourcesAvailable",
					},
				},
			}

			By("waiting for the placement status to reflect Synchronized=True and AllResourcesAvailable=True")
			placement := &experimentalv1beta1.PlacementPolicy{}
			Eventually(func() string {
				if err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: "my-placement"}, placement); err != nil {
					return err.Error()
				}
				return cmp.Diff(placement.Status, wantStatus,
					cmpopts.IgnoreFields(experimentalv1beta1.PlacementPolicyStatus{}, "BindingManager"),
					cmpopts.IgnoreFields(metav1.Condition{}, "ObservedGeneration", "LastTransitionTime", "Message"),
					cmpopts.SortSlices(func(a, b metav1.Condition) bool { return a.Type < b.Type }),
				)
			}, eventuallyDuration, eventuallyInterval).Should(BeEmpty(),
				"placement policy status should reflect Synchronized=True and AllResourcesAvailable=True")
		})

		AfterAll(func() {
			By("deleting the PlacementPolicy")
			placement := &experimentalv1beta1.PlacementPolicy{}
			Expect(hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: "my-placement"}, placement)).To(Succeed())
			Expect(hubClient.Delete(ctx, placement)).To(Succeed())

			By("waiting for the PlacementPolicy to be fully removed")
			Eventually(func() error {
				err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: "my-placement"}, placement)
				if apierrors.IsNotFound(err) {
					return nil
				}
				if err != nil {
					return err
				}
				return fmt.Errorf("placement still exists")
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "PlacementPolicy should be fully removed")

			By("verifying all resource snapshots owned by the placement are gone")
			Eventually(func() (int, error) {
				snapshotList := &experimentalv1beta1.PlacementResourceSnapshotList{}
				if err := hubClient.List(ctx, snapshotList,
					client.InNamespace(workNSName),
					client.MatchingLabels{experimentalv1beta1.ResourceSnapshotOwnedByLabelKey: "my-placement"},
				); err != nil {
					return 0, err
				}
				for i := range snapshotList.Items {
					if err := client.IgnoreNotFound(hubClient.Delete(ctx, &snapshotList.Items[i])); err != nil {
						return len(snapshotList.Items), err
					}
				}
				return len(snapshotList.Items), nil
			}, eventuallyDuration, eventuallyInterval).Should(BeZero(), "all resource snapshots should be cleaned up")

			By("verifying all placement bindings owned by the placement are gone")
			Eventually(func() (int, error) {
				bindingList := &experimentalv1beta1.PlacementBindingList{}
				if err := hubClient.List(ctx, bindingList,
					client.InNamespace(workNSName),
					client.MatchingLabels{experimentalv1beta1.PlacementBindingOwnedByLabelKey: "my-placement"},
				); err != nil {
					return 0, err
				}
				for i := range bindingList.Items {
					if err := client.IgnoreNotFound(hubClient.Delete(ctx, &bindingList.Items[i])); err != nil {
						return len(bindingList.Items), err
					}
				}
				return len(bindingList.Items), nil
			}, eventuallyDuration, eventuallyInterval).Should(BeZero(), "all placement bindings should be cleaned up")

			By("deleting the Deployment and waiting for it to disappear")
			deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: workNSName}}
			Expect(client.IgnoreNotFound(hubClient.Delete(ctx, deploy))).To(Succeed())
			Eventually(func() error {
				err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: "app"}, deploy)
				if apierrors.IsNotFound(err) {
					return nil
				}
				if err != nil {
					return err
				}
				return fmt.Errorf("Deployment still exists")
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Deployment should be fully removed")

			By("deleting the ConfigMap and waiting for it to disappear")
			cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "app-config", Namespace: workNSName}}
			Expect(client.IgnoreNotFound(hubClient.Delete(ctx, cm))).To(Succeed())
			Eventually(func() error {
				err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: "app-config"}, cm)
				if apierrors.IsNotFound(err) {
					return nil
				}
				if err != nil {
					return err
				}
				return fmt.Errorf("ConfigMap still exists")
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "ConfigMap should be fully removed")

			By("deleting the useast, chinanorth, and uksouth member clusters and waiting for them to disappear")
			for _, name := range []string{"useast", "chinanorth", "uksouth"} {
				mc := &clusterv1beta1.MemberCluster{ObjectMeta: metav1.ObjectMeta{Name: name}}
				Expect(client.IgnoreNotFound(hubClient.Delete(ctx, mc))).To(Succeed())
				Eventually(func() error {
					err := hubClient.Get(ctx, types.NamespacedName{Name: name}, mc)
					if apierrors.IsNotFound(err) {
						return nil
					}
					if err != nil {
						return err
					}
					return fmt.Errorf("MemberCluster %s still exists", name)
				}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "MemberCluster "+name+" should be fully removed")
			}
		})
	})

	Context("creating a new placement policy that targets non-existent clusters", Ordered, func() {
		BeforeAll(func() {
			By("creating a Deployment")
			deploy := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "app2",
					Namespace: workNSName,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: ptr.To(int32(1)),
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": "app2"},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{"app": "app2"},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "app2",
									Image: "nginx:latest",
								},
							},
						},
					},
				},
			}
			Expect(hubClient.Create(ctx, deploy)).To(Succeed())

			By("creating a ConfigMap")
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "app2-config",
					Namespace: workNSName,
				},
				Data: map[string]string{
					"key": "value",
				},
			}
			Expect(hubClient.Create(ctx, cm)).To(Succeed())

			By("creating the PlacementPolicy targeting australiaeast")
			placement := &experimentalv1beta1.PlacementPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-placement-2",
					Namespace: workNSName,
				},
				Spec: experimentalv1beta1.PlacementPolicySpec{
					ClusterSelectors: []experimentalv1beta1.ClusterSelector{
						{Terms: []experimentalv1beta1.LabelAndClusterPropertySelectorTerm{{MatchLabels: map[string]string{"region": "australiaeast"}}}},
					},
					ResourceSelectors: []experimentalv1beta1.SameNamespacedObjectReference{
						{
							Name:       "app2",
							APIGroup:   "apps",
							APIVersion: "v1",
							Kind:       "Deployment",
							Resource:   "deployments",
						},
						{
							Name:       "app2-config",
							APIGroup:   "",
							APIVersion: "v1",
							Kind:       "ConfigMap",
							Resource:   "configmaps",
						},
					},
				},
			}
			Expect(hubClient.Create(ctx, placement)).To(Succeed())
		})

		It("should add the cleanup finalizer to the placement policy", func() {
			placement := &experimentalv1beta1.PlacementPolicy{}
			Eventually(func() ([]string, error) {
				if err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: "my-placement-2"}, placement); err != nil {
					return nil, err
				}
				return placement.Finalizers, nil
			}, eventuallyDuration, eventuallyInterval).Should(ContainElement(placementPolicyCleanupFinalizer),
				"placement should have the cleanup finalizer")

			wantFinalizers := []string{placementPolicyCleanupFinalizer}
			if diff := cmp.Diff(placement.Finalizers, wantFinalizers); diff != "" {
				Fail(fmt.Sprintf("placement finalizers mismatch (-got, +want):\n%s", diff))
			}
		})

		It("should create a resource snapshot for the placement policy", func() {
			wantSnapshotName := "my-placement-2-1"
			wantSnapshot := &experimentalv1beta1.PlacementResourceSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name:      wantSnapshotName,
					Namespace: workNSName,
					Labels: map[string]string{
						experimentalv1beta1.ResourceSnapshotOwnedByLabelKey:  "my-placement-2",
						experimentalv1beta1.ResourceSnapshotRevisionLabelKey: "1",
					},
				},
				Spec: experimentalv1beta1.PlacementResourceSnapshotSpec{
					Resources: []experimentalv1beta1.ResourceContent{
						{
							Identifier: experimentalv1beta1.SameNamespacedObjectReference{
								APIGroup:   "apps",
								APIVersion: "v1",
								Kind:       "Deployment",
								Resource:   "deployments",
								Name:       "app2",
							},
						},
						{
							Identifier: experimentalv1beta1.SameNamespacedObjectReference{
								APIGroup:   "",
								APIVersion: "v1",
								Kind:       "ConfigMap",
								Resource:   "configmaps",
								Name:       "app2-config",
							},
						},
					},
				},
			}

			By("waiting for the resource snapshot to be created")
			snapshot := &experimentalv1beta1.PlacementResourceSnapshot{}
			Eventually(func() error {
				return hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: wantSnapshotName}, snapshot)
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "PlacementResourceSnapshot should be created")

			By("verifying the resource snapshot matches the expected state")
			if diff := cmp.Diff(snapshot, wantSnapshot,
				cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion", "UID", "CreationTimestamp", "ManagedFields", "Generation", "OwnerReferences"),
				cmpopts.IgnoreFields(experimentalv1beta1.ResourceContent{}, "Manifest"),
			); diff != "" {
				Fail(fmt.Sprintf("resource snapshot mismatch (-got, +want):\n%s", diff))
			}
		})

		It("should not create any placement bindings for the placement policy", func() {
			// Give the controller time to reconcile; a binding should never appear
			// because no cluster with region=australiaeast exists.
			Consistently(func() (int, error) {
				bindingList := &experimentalv1beta1.PlacementBindingList{}
				if err := hubClient.List(ctx, bindingList,
					client.InNamespace(workNSName),
					client.MatchingLabels{
						experimentalv1beta1.PlacementBindingOwnedByLabelKey: "my-placement-2",
					},
				); err != nil {
					return 0, err
				}
				return len(bindingList.Items), nil
			}, eventuallyDuration, eventuallyInterval).Should(BeZero(),
				"no placement bindings should be created when no matching cluster exists")
		})

		It("should create a cluster request for the australiaeast selector", func() {
			australiaeastHash, err := resource.HashOf(&experimentalv1beta1.ClusterSelector{Terms: []experimentalv1beta1.LabelAndClusterPropertySelectorTerm{{MatchLabels: map[string]string{"region": "australiaeast"}}}})
			Expect(err).NotTo(HaveOccurred())

			wantReq := &experimentalv1beta1.ClusterRequest{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-placement-2",
					Namespace: workNSName,
				},
				Spec: experimentalv1beta1.ClusterRequestSpec{
					ClusterSelectorHash: australiaeastHash,
				},
			}

			By("waiting for the ClusterRequest to be created")
			req := &experimentalv1beta1.ClusterRequest{}
			Eventually(func() error {
				return hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: "my-placement-2"}, req)
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "ClusterRequest should be created")

			By("verifying the ClusterRequest matches the expected state")
			if diff := cmp.Diff(req, wantReq,
				cmpopts.IgnoreFields(metav1.TypeMeta{}, "Kind", "APIVersion"),
				cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion", "UID", "CreationTimestamp", "ManagedFields", "Generation", "OwnerReferences"),
				cmpopts.IgnoreFields(experimentalv1beta1.ClusterRequestStatus{}, "Conditions", "LatestObservedClusterCreationTimestamp", "ProvisionedClusterName"),
				cmpopts.IgnoreFields(experimentalv1beta1.ClusterRequestSpec{}, "ClusterSelector"),
			); diff != "" {
				Fail(fmt.Sprintf("cluster request mismatch (-got, +want):\n%s", diff))
			}
		})

		It("should create a member cluster in the australiaeast region", func() {
			By("creating a member cluster with region=australiaeast")
			mc := &clusterv1beta1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "australiaeast",
					Labels: map[string]string{
						"region": "australiaeast",
					},
				},
				Spec: clusterv1beta1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Kind: rbacv1.ServiceAccountKind,
						Name: "hub-access",
					},
				},
			}
			Expect(hubClient.Create(ctx, mc)).To(Succeed())
		})

		It("should create a binding to the australiaeast member cluster", func() {
			australiaeastHash, err := resource.HashOf(&experimentalv1beta1.ClusterSelector{Terms: []experimentalv1beta1.LabelAndClusterPropertySelectorTerm{{MatchLabels: map[string]string{"region": "australiaeast"}}}})
			Expect(err).NotTo(HaveOccurred())

			snapshotRevision := ptr.To("my-placement-2-1")
			wantBinding := experimentalv1beta1.PlacementBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-placement-2-australiaeast",
					Namespace: workNSName,
					Labels: map[string]string{
						experimentalv1beta1.PlacementBindingOwnedByLabelKey: "my-placement-2",
					},
				},
				Spec: experimentalv1beta1.PlacementBindingSpec{
					PlacementPolicyName:  "my-placement-2",
					ClusterSelectorHash:  australiaeastHash,
					ClusterName:          "australiaeast",
					ResourceSnapshotName: snapshotRevision,
				},
			}

			By("waiting for the binding to be created")
			binding := &experimentalv1beta1.PlacementBinding{}
			Eventually(func() error {
				return hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: "my-placement-2-australiaeast"}, binding)
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "binding to australiaeast should be created")

			By("verifying the binding matches the expected state")
			if diff := cmp.Diff(*binding, wantBinding,
				cmpopts.IgnoreFields(metav1.TypeMeta{}, "Kind", "APIVersion"),
				cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion", "UID", "CreationTimestamp", "ManagedFields", "Generation", "OwnerReferences"),
				cmpopts.IgnoreFields(experimentalv1beta1.PlacementBindingStatus{}, "Conditions"),
				cmpopts.IgnoreFields(experimentalv1beta1.PlacementBindingSpec{}, "ClusterSelector"),
			); diff != "" {
				Fail(fmt.Sprintf("binding mismatch (-got, +want):\n%s", diff))
			}
		})

		It("should update the placement status after the australiaeast cluster is found", func() {
			wantStatus := experimentalv1beta1.PlacementPolicyStatus{
				LatestResourceRevisionName: ptr.To("my-placement-2-1"),
				Conditions: []metav1.Condition{
					{
						Type:   experimentalv1beta1.PlacementPolicyCondTypeScheduled,
						Status: metav1.ConditionTrue,
						Reason: "FoundClustersForAllSelectors",
					},
					{
						Type:   experimentalv1beta1.PlacementPolicyCondTypeSynchronized,
						Status: metav1.ConditionFalse,
						Reason: "NotAllBindingsHaveSynchronizedResources",
					},
					{
						Type:   experimentalv1beta1.PlacementPolicyCondTypeAvailable,
						Status: metav1.ConditionFalse,
						Reason: "NotAllBindingsHaveResourcesAvailable",
					},
				},
			}

			placement := &experimentalv1beta1.PlacementPolicy{}
			Eventually(func() string {
				if err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: "my-placement-2"}, placement); err != nil {
					return err.Error()
				}
				return cmp.Diff(placement.Status, wantStatus,
					cmpopts.IgnoreFields(experimentalv1beta1.PlacementPolicyStatus{}, "BindingManager"),
					cmpopts.IgnoreFields(metav1.Condition{}, "ObservedGeneration", "LastTransitionTime", "Message"),
					cmpopts.SortSlices(func(a, b metav1.Condition) bool { return a.Type < b.Type }),
				)
			}, eventuallyDuration, eventuallyInterval).Should(BeEmpty(),
				"placement status should reflect Scheduled=True after australiaeast cluster is found")
		})

		It("should delete the cluster request once the selector is fulfilled", func() {
			Eventually(func() error {
				req := &experimentalv1beta1.ClusterRequest{}
				err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: "my-placement-2"}, req)
				if apierrors.IsNotFound(err) {
					return nil
				}
				if err != nil {
					return err
				}
				return fmt.Errorf("cluster request still exists")
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(),
				"cluster request should be deleted once all selectors are fulfilled")
		})

		AfterAll(func() {
			By("deleting the PlacementPolicy")
			placement := &experimentalv1beta1.PlacementPolicy{}
			Expect(hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: "my-placement-2"}, placement)).To(Succeed())
			Expect(hubClient.Delete(ctx, placement)).To(Succeed())

			By("waiting for the PlacementPolicy to be fully removed")
			Eventually(func() error {
				err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: "my-placement-2"}, placement)
				if apierrors.IsNotFound(err) {
					return nil
				}
				if err != nil {
					return err
				}
				return fmt.Errorf("placement still exists")
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "PlacementPolicy should be fully removed")

			By("verifying all resource snapshots owned by the placement are gone")
			Eventually(func() (int, error) {
				snapshotList := &experimentalv1beta1.PlacementResourceSnapshotList{}
				if err := hubClient.List(ctx, snapshotList,
					client.InNamespace(workNSName),
					client.MatchingLabels{experimentalv1beta1.ResourceSnapshotOwnedByLabelKey: "my-placement-2"},
				); err != nil {
					return 0, err
				}
				for i := range snapshotList.Items {
					if err := client.IgnoreNotFound(hubClient.Delete(ctx, &snapshotList.Items[i])); err != nil {
						return len(snapshotList.Items), err
					}
				}
				return len(snapshotList.Items), nil
			}, eventuallyDuration, eventuallyInterval).Should(BeZero(), "all resource snapshots should be cleaned up")

			By("verifying all placement bindings owned by the placement are gone")
			Eventually(func() (int, error) {
				bindingList := &experimentalv1beta1.PlacementBindingList{}
				if err := hubClient.List(ctx, bindingList,
					client.InNamespace(workNSName),
					client.MatchingLabels{experimentalv1beta1.PlacementBindingOwnedByLabelKey: "my-placement-2"},
				); err != nil {
					return 0, err
				}
				for i := range bindingList.Items {
					if err := client.IgnoreNotFound(hubClient.Delete(ctx, &bindingList.Items[i])); err != nil {
						return len(bindingList.Items), err
					}
				}
				return len(bindingList.Items), nil
			}, eventuallyDuration, eventuallyInterval).Should(BeZero(), "all placement bindings should be cleaned up")

			By("deleting the Deployment and waiting for it to disappear")
			deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "app2", Namespace: workNSName}}
			Expect(client.IgnoreNotFound(hubClient.Delete(ctx, deploy))).To(Succeed())
			Eventually(func() error {
				err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: "app2"}, deploy)
				if apierrors.IsNotFound(err) {
					return nil
				}
				if err != nil {
					return err
				}
				return fmt.Errorf("Deployment still exists")
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Deployment should be fully removed")

			By("deleting the ConfigMap and waiting for it to disappear")
			cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "app2-config", Namespace: workNSName}}
			Expect(client.IgnoreNotFound(hubClient.Delete(ctx, cm))).To(Succeed())
			Eventually(func() error {
				err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: "app2-config"}, cm)
				if apierrors.IsNotFound(err) {
					return nil
				}
				if err != nil {
					return err
				}
				return fmt.Errorf("ConfigMap still exists")
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "ConfigMap should be fully removed")

			By("deleting the australiaeast member cluster and waiting for it to disappear")
			mc := &clusterv1beta1.MemberCluster{ObjectMeta: metav1.ObjectMeta{Name: "australiaeast"}}
			Expect(client.IgnoreNotFound(hubClient.Delete(ctx, mc))).To(Succeed())
			Eventually(func() error {
				err := hubClient.Get(ctx, types.NamespacedName{Name: "australiaeast"}, mc)
				if apierrors.IsNotFound(err) {
					return nil
				}
				if err != nil {
					return err
				}
				return fmt.Errorf("MemberCluster still exists")
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "MemberCluster should be fully removed")
		})
	})

	Context("updating a placement policy to target different clusters", Ordered, func() {
		BeforeAll(func() {
			By("creating a Deployment")
			deploy := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "app3",
					Namespace: workNSName,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: ptr.To(int32(1)),
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": "app3"},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{"app": "app3"},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "app3",
									Image: "nginx:latest",
								},
							},
						},
					},
				},
			}
			Expect(hubClient.Create(ctx, deploy)).To(Succeed())

			By("creating a ConfigMap")
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "app3-config",
					Namespace: workNSName,
				},
				Data: map[string]string{
					"key": "value",
				},
			}
			Expect(hubClient.Create(ctx, cm)).To(Succeed())

			By("creating 2 member clusters in useast and uswest")
			clusters := []struct {
				name   string
				region string
			}{
				{"useast2", "useast"},
				{"uswest", "uswest"},
			}
			for _, c := range clusters {
				mc := &clusterv1beta1.MemberCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: c.name,
						Labels: map[string]string{
							"region": c.region,
						},
					},
					Spec: clusterv1beta1.MemberClusterSpec{
						Identity: rbacv1.Subject{
							Kind: rbacv1.ServiceAccountKind,
							Name: "hub-access",
						},
					},
				}
				Expect(hubClient.Create(ctx, mc)).To(Succeed())
			}

			By("creating the PlacementPolicy targeting useast and uswest")
			placement := &experimentalv1beta1.PlacementPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-placement-3",
					Namespace: workNSName,
				},
				Spec: experimentalv1beta1.PlacementPolicySpec{
					ClusterSelectors: []experimentalv1beta1.ClusterSelector{
						{Terms: []experimentalv1beta1.LabelAndClusterPropertySelectorTerm{{MatchLabels: map[string]string{"region": "useast"}}}},
						{Terms: []experimentalv1beta1.LabelAndClusterPropertySelectorTerm{{MatchLabels: map[string]string{"region": "uswest"}}}},
					},
					ResourceSelectors: []experimentalv1beta1.SameNamespacedObjectReference{
						{
							Name:       "app3",
							APIGroup:   "apps",
							APIVersion: "v1",
							Kind:       "Deployment",
							Resource:   "deployments",
						},
						{
							Name:       "app3-config",
							APIGroup:   "",
							APIVersion: "v1",
							Kind:       "ConfigMap",
							Resource:   "configmaps",
						},
					},
				},
			}
			Expect(hubClient.Create(ctx, placement)).To(Succeed())
		})

		It("should create 2 placement bindings for the placement policy", func() {
			By("waiting for 2 placement bindings to be created")
			bindingList := &experimentalv1beta1.PlacementBindingList{}
			Eventually(func() (int, error) {
				if err := hubClient.List(ctx, bindingList,
					client.InNamespace(workNSName),
					client.MatchingLabels{
						experimentalv1beta1.PlacementBindingOwnedByLabelKey: "my-placement-3",
					},
				); err != nil {
					return 0, err
				}
				return len(bindingList.Items), nil
			}, eventuallyDuration, eventuallyInterval).Should(Equal(2), "2 placement bindings should be created")

			By("computing expected cluster selector hashes")
			useastHash, err := resource.HashOf(&experimentalv1beta1.ClusterSelector{Terms: []experimentalv1beta1.LabelAndClusterPropertySelectorTerm{{MatchLabels: map[string]string{"region": "useast"}}}})
			Expect(err).NotTo(HaveOccurred())
			uswestHash, err := resource.HashOf(&experimentalv1beta1.ClusterSelector{Terms: []experimentalv1beta1.LabelAndClusterPropertySelectorTerm{{MatchLabels: map[string]string{"region": "uswest"}}}})
			Expect(err).NotTo(HaveOccurred())

			snapshotRevision := ptr.To("my-placement-3-1")
			wantBindings := []experimentalv1beta1.PlacementBinding{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-placement-3-useast2",
						Namespace: workNSName,
						Labels: map[string]string{
							experimentalv1beta1.PlacementBindingOwnedByLabelKey: "my-placement-3",
						},
					},
					Spec: experimentalv1beta1.PlacementBindingSpec{
						PlacementPolicyName:  "my-placement-3",
						ClusterSelectorHash:  useastHash,
						ClusterName:          "useast2",
						ResourceSnapshotName: snapshotRevision,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-placement-3-uswest",
						Namespace: workNSName,
						Labels: map[string]string{
							experimentalv1beta1.PlacementBindingOwnedByLabelKey: "my-placement-3",
						},
					},
					Spec: experimentalv1beta1.PlacementBindingSpec{
						PlacementPolicyName:  "my-placement-3",
						ClusterSelectorHash:  uswestHash,
						ClusterName:          "uswest",
						ResourceSnapshotName: snapshotRevision,
					},
				},
			}

			By("verifying the placement bindings match the expected state")
			if diff := cmp.Diff(bindingList.Items, wantBindings,
				cmpopts.IgnoreFields(metav1.TypeMeta{}, "Kind", "APIVersion"),
				cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion", "UID", "CreationTimestamp", "ManagedFields", "Generation", "OwnerReferences"),
				cmpopts.IgnoreFields(experimentalv1beta1.PlacementBindingStatus{}, "Conditions"),
				cmpopts.IgnoreFields(experimentalv1beta1.PlacementBindingSpec{}, "ClusterSelector"),
				cmpopts.SortSlices(func(a, b experimentalv1beta1.PlacementBinding) bool {
					return a.Name < b.Name
				}),
			); diff != "" {
				Fail(fmt.Sprintf("placement bindings mismatch (-got, +want):\n%s", diff))
			}
		})

		It("should set the placement policy status correctly", func() {
			wantStatus := experimentalv1beta1.PlacementPolicyStatus{
				LatestResourceRevisionName: ptr.To("my-placement-3-1"),
				Conditions: []metav1.Condition{
					{
						Type:   experimentalv1beta1.PlacementPolicyCondTypeScheduled,
						Status: metav1.ConditionTrue,
						Reason: "FoundClustersForAllSelectors",
					},
					{
						Type:   experimentalv1beta1.PlacementPolicyCondTypeSynchronized,
						Status: metav1.ConditionFalse,
						Reason: "NotAllBindingsHaveSynchronizedResources",
					},
					{
						Type:   experimentalv1beta1.PlacementPolicyCondTypeAvailable,
						Status: metav1.ConditionFalse,
						Reason: "NotAllBindingsHaveResourcesAvailable",
					},
				},
			}

			placement := &experimentalv1beta1.PlacementPolicy{}
			Eventually(func() string {
				if err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: "my-placement-3"}, placement); err != nil {
					return err.Error()
				}
				return cmp.Diff(placement.Status, wantStatus,
					cmpopts.IgnoreFields(experimentalv1beta1.PlacementPolicyStatus{}, "BindingManager"),
					cmpopts.IgnoreFields(metav1.Condition{}, "ObservedGeneration", "LastTransitionTime", "Message"),
					cmpopts.SortSlices(func(a, b metav1.Condition) bool { return a.Type < b.Type }),
				)
			}, eventuallyDuration, eventuallyInterval).Should(BeEmpty(),
				"placement status should be set correctly")
		})

		It("should update the placement to select uscentral and uswest", func() {
			By("fetching the current placement")
			placement := &experimentalv1beta1.PlacementPolicy{}
			Expect(hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: "my-placement-3"}, placement)).To(Succeed())

			By("updating the cluster selectors to uscentral and uswest")
			updatedPlacement := placement.DeepCopy()
			updatedPlacement.Spec.ClusterSelectors = []experimentalv1beta1.ClusterSelector{
				{Terms: []experimentalv1beta1.LabelAndClusterPropertySelectorTerm{{MatchLabels: map[string]string{"region": "uscentral"}}}},
				{Terms: []experimentalv1beta1.LabelAndClusterPropertySelectorTerm{{MatchLabels: map[string]string{"region": "uswest"}}}},
			}
			Expect(hubClient.Update(ctx, updatedPlacement)).To(Succeed())
		})

		It("should delete the stale useast binding and retain the uswest binding", func() {
			uswestHash, err := resource.HashOf(&experimentalv1beta1.ClusterSelector{Terms: []experimentalv1beta1.LabelAndClusterPropertySelectorTerm{{MatchLabels: map[string]string{"region": "uswest"}}}})
			Expect(err).NotTo(HaveOccurred())

			wantBindings := []experimentalv1beta1.PlacementBinding{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-placement-3-uswest",
						Namespace: workNSName,
						Labels: map[string]string{
							experimentalv1beta1.PlacementBindingOwnedByLabelKey: "my-placement-3",
						},
					},
					Spec: experimentalv1beta1.PlacementBindingSpec{
						PlacementPolicyName:  "my-placement-3",
						ClusterSelectorHash:  uswestHash,
						ClusterName:          "uswest",
						ResourceSnapshotName: ptr.To("my-placement-3-1"),
					},
				},
			}

			bindingList := &experimentalv1beta1.PlacementBindingList{}
			Eventually(func() string {
				if err := hubClient.List(ctx, bindingList,
					client.InNamespace(workNSName),
					client.MatchingLabels{
						experimentalv1beta1.PlacementBindingOwnedByLabelKey: "my-placement-3",
					},
				); err != nil {
					return err.Error()
				}
				return cmp.Diff(bindingList.Items, wantBindings,
					cmpopts.IgnoreFields(metav1.TypeMeta{}, "Kind", "APIVersion"),
					cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion", "UID", "CreationTimestamp", "ManagedFields", "Generation", "OwnerReferences"),
					cmpopts.IgnoreFields(experimentalv1beta1.PlacementBindingStatus{}, "Conditions"),
					cmpopts.IgnoreFields(experimentalv1beta1.PlacementBindingSpec{}, "ClusterSelector"),
				)
			}, eventuallyDuration, eventuallyInterval).Should(BeEmpty(),
				"only the uswest binding should remain after the selector update")
		})

		It("should create a cluster request for the uscentral selector", func() {
			uscentralHash, err := resource.HashOf(&experimentalv1beta1.ClusterSelector{Terms: []experimentalv1beta1.LabelAndClusterPropertySelectorTerm{{MatchLabels: map[string]string{"region": "uscentral"}}}})
			Expect(err).NotTo(HaveOccurred())

			wantReq := &experimentalv1beta1.ClusterRequest{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-placement-3",
					Namespace: workNSName,
				},
				Spec: experimentalv1beta1.ClusterRequestSpec{
					ClusterSelectorHash: uscentralHash,
				},
			}

			By("waiting for the ClusterRequest to be created")
			req := &experimentalv1beta1.ClusterRequest{}
			Eventually(func() error {
				return hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: "my-placement-3"}, req)
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "ClusterRequest should be created")

			By("verifying the ClusterRequest matches the expected state")
			if diff := cmp.Diff(req, wantReq,
				cmpopts.IgnoreFields(metav1.TypeMeta{}, "Kind", "APIVersion"),
				cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion", "UID", "CreationTimestamp", "ManagedFields", "Generation", "OwnerReferences"),
				cmpopts.IgnoreFields(experimentalv1beta1.ClusterRequestStatus{}, "Conditions", "LatestObservedClusterCreationTimestamp", "ProvisionedClusterName"),
				cmpopts.IgnoreFields(experimentalv1beta1.ClusterRequestSpec{}, "ClusterSelector"),
			); diff != "" {
				Fail(fmt.Sprintf("cluster request mismatch (-got, +want):\n%s", diff))
			}
		})

		AfterAll(func() {
			By("deleting the PlacementPolicy")
			placement := &experimentalv1beta1.PlacementPolicy{}
			Expect(hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: "my-placement-3"}, placement)).To(Succeed())
			Expect(hubClient.Delete(ctx, placement)).To(Succeed())

			By("waiting for the PlacementPolicy to be fully removed")
			Eventually(func() error {
				err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: "my-placement-3"}, placement)
				if apierrors.IsNotFound(err) {
					return nil
				}
				if err != nil {
					return err
				}
				return fmt.Errorf("placement still exists")
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "PlacementPolicy should be fully removed")

			By("verifying all resource snapshots owned by the placement are gone")
			Eventually(func() (int, error) {
				snapshotList := &experimentalv1beta1.PlacementResourceSnapshotList{}
				if err := hubClient.List(ctx, snapshotList,
					client.InNamespace(workNSName),
					client.MatchingLabels{experimentalv1beta1.ResourceSnapshotOwnedByLabelKey: "my-placement-3"},
				); err != nil {
					return 0, err
				}
				for i := range snapshotList.Items {
					if err := client.IgnoreNotFound(hubClient.Delete(ctx, &snapshotList.Items[i])); err != nil {
						return len(snapshotList.Items), err
					}
				}
				return len(snapshotList.Items), nil
			}, eventuallyDuration, eventuallyInterval).Should(BeZero(), "all resource snapshots should be cleaned up")

			By("verifying all placement bindings owned by the placement are gone")
			Eventually(func() (int, error) {
				bindingList := &experimentalv1beta1.PlacementBindingList{}
				if err := hubClient.List(ctx, bindingList,
					client.InNamespace(workNSName),
					client.MatchingLabels{experimentalv1beta1.PlacementBindingOwnedByLabelKey: "my-placement-3"},
				); err != nil {
					return 0, err
				}
				for i := range bindingList.Items {
					if err := client.IgnoreNotFound(hubClient.Delete(ctx, &bindingList.Items[i])); err != nil {
						return len(bindingList.Items), err
					}
				}
				return len(bindingList.Items), nil
			}, eventuallyDuration, eventuallyInterval).Should(BeZero(), "all placement bindings should be cleaned up")

			By("deleting the Deployment and waiting for it to disappear")
			deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "app3", Namespace: workNSName}}
			Expect(client.IgnoreNotFound(hubClient.Delete(ctx, deploy))).To(Succeed())
			Eventually(func() error {
				err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: "app3"}, deploy)
				if apierrors.IsNotFound(err) {
					return nil
				}
				if err != nil {
					return err
				}
				return fmt.Errorf("Deployment still exists")
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Deployment should be fully removed")

			By("deleting the ConfigMap and waiting for it to disappear")
			cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "app3-config", Namespace: workNSName}}
			Expect(client.IgnoreNotFound(hubClient.Delete(ctx, cm))).To(Succeed())
			Eventually(func() error {
				err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: "app3-config"}, cm)
				if apierrors.IsNotFound(err) {
					return nil
				}
				if err != nil {
					return err
				}
				return fmt.Errorf("ConfigMap still exists")
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "ConfigMap should be fully removed")

			By("deleting the useast2 and uswest member clusters and waiting for them to disappear")
			for _, name := range []string{"useast2", "uswest"} {
				mc := &clusterv1beta1.MemberCluster{ObjectMeta: metav1.ObjectMeta{Name: name}}
				Expect(client.IgnoreNotFound(hubClient.Delete(ctx, mc))).To(Succeed())
				Eventually(func() error {
					err := hubClient.Get(ctx, types.NamespacedName{Name: name}, mc)
					if apierrors.IsNotFound(err) {
						return nil
					}
					if err != nil {
						return err
					}
					return fmt.Errorf("MemberCluster %s still exists", name)
				}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "MemberCluster "+name+" should be fully removed")
			}
		})
	})
})
