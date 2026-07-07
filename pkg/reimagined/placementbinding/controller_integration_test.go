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

package placementbinding

import (
	"encoding/json"
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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	experimentalv1beta1 "github.com/kubefleet-dev/kubefleet/apis/experimental/v1beta1"
	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

const (
	// The member cluster name used across integration tests.
	memberClusterName = "bravelion"

	// The name of the placement policy used across integration tests.
	placementName = "my-placement"

	// Eventually polling interval and timeout.
	eventuallyInterval = 500 * time.Millisecond
	eventuallyDuration = 10 * time.Second
)

// rawJSON returns the JSON encoding of v as a runtime.RawExtension.
// It panics on marshaling failure to keep test helpers concise.
func rawJSON(v any) runtime.RawExtension {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal object to JSON: %v", err))
	}
	return runtime.RawExtension{Raw: data}
}

var _ = Describe("binding operations", Ordered, func() {
	Context("when a new binding is created", Ordered, func() {
		bindingName := "test-binding"
		snapshotName := "test-snapshot-rev-1"

		var deploy *appsv1.Deployment
		var cm *corev1.ConfigMap

		var deployRawJSON, cmRawJSON runtime.RawExtension

		BeforeAll(func() {
			// Create the PlacementResourceSnapshot that the binding will reference.
			// It captures the Deployment and the ConfigMap as its resources.
			deploy = &appsv1.Deployment{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
				},
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
			cm = &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "ConfigMap",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "app-config",
					Namespace: workNSName,
				},
				Data: map[string]string{
					"key": "value",
				},
			}

			deployRawJSON = rawJSON(deploy)
			cmRawJSON = rawJSON(cm)
			snapshot := &experimentalv1beta1.PlacementResourceSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name:      snapshotName,
					Namespace: workNSName,
				},
				Spec: experimentalv1beta1.PlacementResourceSnapshotSpec{
					Resources: []experimentalv1beta1.ResourceContent{
						{
							Identifier: experimentalv1beta1.SameNamespacedObjectReference{
								Name:       "app",
								APIVersion: "apps/v1",
								Kind:       "Deployment",
								Resource:   "deployments",
							},
							Manifest: deployRawJSON,
						},
						{
							Identifier: experimentalv1beta1.SameNamespacedObjectReference{
								Name:       "app-config",
								APIVersion: "v1",
								Kind:       "ConfigMap",
								Resource:   "configmaps",
							},
							Manifest: cmRawJSON,
						},
					},
				},
			}
			Expect(hubClient.Create(ctx, snapshot)).To(Succeed())

			// Create the binding.
			binding := &experimentalv1beta1.PlacementBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      bindingName,
					Namespace: workNSName,
				},
				Spec: experimentalv1beta1.PlacementBindingSpec{
					PlacementPolicyName:  placementName,
					ClusterName:          memberClusterName,
					ResourceSnapshotName: ptr.To(snapshotName),
				},
			}
			Expect(hubClient.Create(ctx, binding)).To(Succeed())
		})

		AfterAll(func() {
			// Issue the delete (idempotent — ignore not-found if already gone), then wait for
			// the controller to drop its finalizer and fully remove the object.
			Eventually(func() error {
				binding := &experimentalv1beta1.PlacementBinding{}
				if err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: bindingName}, binding); err != nil {
					if apierrors.IsNotFound(err) {
						return nil
					}
					return err
				}

				if binding.DeletionTimestamp.IsZero() {
					if err := hubClient.Delete(ctx, binding); err != nil {
						return err
					}
				}
				return fmt.Errorf("binding still exists")
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "binding should be deleted by the controller")

			// Remove the snapshot and wait for it to be fully deleted.
			Eventually(func() error {
				snapshot := &experimentalv1beta1.PlacementResourceSnapshot{}
				if err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: snapshotName}, snapshot); err != nil {
					if apierrors.IsNotFound(err) {
						return nil
					}
					return err
				}
				if err := hubClient.Delete(ctx, snapshot); err != nil {
					return err
				}
				return fmt.Errorf("snapshot still exists")
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "snapshot should be fully deleted")

			// The controller deletes owned Work objects as part of its cleanup before removing
			// the binding finalizer, so by the time the binding is gone the Work objects should
			// already be gone too. Wait to confirm, filtering to only those owned by this binding.
			Eventually(func() (int, error) {
				workList := &placementv1beta1.WorkList{}
				if err := hubClient.List(ctx, workList,
					client.InNamespace(memberClusterReservedNSName),
					client.MatchingLabels{
						experimentalv1beta1.WorkOwnedByPlacementBindingLabelKey: bindingName,
						experimentalv1beta1.WorkOwnerNamespaceLabelKey:          workNSName,
					},
				); err != nil {
					return 0, err
				}
				return len(workList.Items), nil
			}, eventuallyDuration, eventuallyInterval).Should(BeZero(), "all Work objects owned by the binding should be cleaned up")
		})

		It("should create a Work object in the member cluster's hub namespace", func() {
			wantWorkName := fmt.Sprintf("%s-0", bindingName)
			wantWork := &placementv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Name:      wantWorkName,
					Namespace: memberClusterReservedNSName,
					Labels: map[string]string{
						experimentalv1beta1.WorkOwnedByPlacementBindingLabelKey: bindingName,
						experimentalv1beta1.WorkOwnerNamespaceLabelKey:          workNSName,
						experimentalv1beta1.WorkOwnedByPlacementPolicyLabelKey:  placementName,
					},
					Annotations: map[string]string{
						experimentalv1beta1.WorkDerivedFromResourceSnapshotAnnotationKey: snapshotName,
					},
				},
				Spec: placementv1beta1.WorkSpec{
					Workload: placementv1beta1.WorkloadTemplate{
						Manifests: []placementv1beta1.Manifest{
							{RawExtension: deployRawJSON},
							{RawExtension: cmRawJSON},
						},
					},
				},
			}

			By("waiting for the Work object to be created")
			work := &placementv1beta1.Work{}
			Eventually(func() error {
				return hubClient.Get(ctx, types.NamespacedName{
					Namespace: memberClusterReservedNSName,
					Name:      wantWorkName,
				}, work)
			}, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Work object should be created in the member namespace")

			By("verifying the Work object matches the expected state")
			if diff := cmp.Diff(work, wantWork,
				cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion", "UID", "CreationTimestamp", "ManagedFields", "Generation"),
				cmpopts.IgnoreFields(placementv1beta1.WorkloadTemplate{}, "Manifests"),
			); diff != "" {
				Fail(fmt.Sprintf("Work object mismatch (-got, +want):\n%s", diff))
			}

			By("verifying that the cleanup finalizer is added to the binding")
			binding := &experimentalv1beta1.PlacementBinding{}
			Expect(hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: bindingName}, binding)).To(Succeed())
			Expect(binding.Finalizers).To(ContainElement(placementBindingCleanupFinalizer))
		})

		It("should update the binding status based on the Work object's status", func() {
			// In the test environment there is no work applier, so the Work object will have an
			// empty status. The controller should reflect this back on the binding status.
			wantConditions := []metav1.Condition{
				{
					Type:   experimentalv1beta1.PlacementBindingCondTypeAllResourcesAvailable,
					Status: metav1.ConditionFalse,
					Reason: "NotAllResourcesAvailable",
				},
				{
					Type:   experimentalv1beta1.PlacementBindingCondTypeSynchronized,
					Status: metav1.ConditionFalse,
					Reason: "NotAllResourcesApplied",
				},
			}

			By("waiting for the binding status conditions to be populated")
			binding := &experimentalv1beta1.PlacementBinding{}
			Eventually(func() ([]metav1.Condition, error) {
				if err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: bindingName}, binding); err != nil {
					return nil, err
				}
				return binding.Status.Conditions, nil
			}, eventuallyDuration, eventuallyInterval).Should(HaveLen(2), "binding should have two status conditions")

			By("verifying the binding status conditions match the expected state")
			if diff := cmp.Diff(binding.Status.Conditions, wantConditions,
				cmpopts.IgnoreFields(metav1.Condition{}, "ObservedGeneration", "LastTransitionTime", "Message"),
				cmpopts.SortSlices(func(a, b metav1.Condition) bool { return a.Type < b.Type }),
			); diff != "" {
				Fail(fmt.Sprintf("binding status conditions mismatch (-got, +want):\n%s", diff))
			}
		})

		It("should reflect Applied=True and Available=True on the binding after the Work status is updated", func() {
			wantWorkName := fmt.Sprintf("%s-0", bindingName)

			By("fetching the Work object")
			work := &placementv1beta1.Work{}
			Expect(hubClient.Get(ctx, types.NamespacedName{
				Namespace: memberClusterReservedNSName,
				Name:      wantWorkName,
			}, work)).To(Succeed())

			By("patching the Work status with Applied=True and Available=True")
			updatedWork := work.DeepCopy()
			updatedWork.Status.Conditions = []metav1.Condition{
				{
					Type:               string(placementv1beta1.WorkConditionTypeApplied),
					Status:             metav1.ConditionTrue,
					Reason:             "AllManifestsApplied",
					ObservedGeneration: work.Generation,
					LastTransitionTime: metav1.Now(),
				},
				{
					Type:               string(placementv1beta1.WorkConditionTypeAvailable),
					Status:             metav1.ConditionTrue,
					Reason:             "AllManifestsAvailable",
					ObservedGeneration: work.Generation,
					LastTransitionTime: metav1.Now(),
				},
			}
			Expect(hubClient.Status().Update(ctx, updatedWork)).To(Succeed())

			By("waiting for the binding status to reflect Synchronized=True and AllResourcesAvailable=True")
			wantConditions := []metav1.Condition{
				{
					Type:   experimentalv1beta1.PlacementBindingCondTypeAllResourcesAvailable,
					Status: metav1.ConditionTrue,
					Reason: "AllResourcesAvailable",
				},
				{
					Type:   experimentalv1beta1.PlacementBindingCondTypeSynchronized,
					Status: metav1.ConditionTrue,
					Reason: "AllResourcesApplied",
				},
			}
			binding := &experimentalv1beta1.PlacementBinding{}
			Eventually(func() ([]metav1.Condition, error) {
				if err := hubClient.Get(ctx, types.NamespacedName{Namespace: workNSName, Name: bindingName}, binding); err != nil {
					return nil, err
				}
				return binding.Status.Conditions, nil
			}, eventuallyDuration, eventuallyInterval).Should(SatisfyAll(
				HaveLen(2),
				ContainElement(HaveField("Status", metav1.ConditionTrue)),
			), "binding should have two True status conditions")

			if diff := cmp.Diff(binding.Status.Conditions, wantConditions,
				cmpopts.IgnoreFields(metav1.Condition{}, "ObservedGeneration", "LastTransitionTime", "Message"),
				cmpopts.SortSlices(func(a, b metav1.Condition) bool { return a.Type < b.Type }),
			); diff != "" {
				Fail(fmt.Sprintf("binding status conditions mismatch (-got, +want):\n%s", diff))
			}
		})
	})
})
