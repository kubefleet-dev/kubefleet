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

package workapplier

import (
	"context"
	"fmt"

	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	fleetv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/condition"
)

// createWorkWithManifest creates a work given a manifest
func createWorkWithManifest(manifest runtime.Object) *fleetv1beta1.Work {
	manifestCopy := manifest.DeepCopyObject()
	newWork := fleetv1beta1.Work{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "work-" + utilrand.String(5),
			Namespace: memberReservedNSName,
		},
		Spec: fleetv1beta1.WorkSpec{
			Workload: fleetv1beta1.WorkloadTemplate{
				Manifests: []fleetv1beta1.Manifest{
					{
						RawExtension: runtime.RawExtension{Object: manifestCopy},
					},
				},
			},
		},
	}
	return &newWork
}

// verifyAppliedConfigMap verifies that the applied CM is the same as the CM we want to apply
func verifyAppliedConfigMap(cm *corev1.ConfigMap) *corev1.ConfigMap {
	var appliedCM corev1.ConfigMap
	Expect(memberClient.Get(context.Background(), types.NamespacedName{Name: cm.GetName(), Namespace: cm.GetNamespace()}, &appliedCM)).Should(Succeed())

	By("Check the config map label")
	Expect(cmp.Diff(appliedCM.Labels, cm.Labels)).Should(BeEmpty())

	By("Check the config map annotation value")
	Expect(len(appliedCM.Annotations)).Should(Equal(len(cm.Annotations) + 2)) // we added 2 more annotations
	for key := range cm.Annotations {
		Expect(appliedCM.Annotations[key]).Should(Equal(cm.Annotations[key]))
	}
	Expect(appliedCM.Annotations[fleetv1beta1.ManifestHashAnnotation]).ShouldNot(BeEmpty())
	Expect(appliedCM.Annotations[fleetv1beta1.LastAppliedConfigAnnotation]).ShouldNot(BeEmpty())

	By("Check the config map data")
	Expect(cmp.Diff(appliedCM.Data, cm.Data)).Should(BeEmpty())
	return &appliedCM
}

// waitForWorkToApply waits for a work to be applied
func waitForWorkToApply(workName string) *fleetv1beta1.Work {
	var resultWork fleetv1beta1.Work
	Eventually(func() bool {
		err := hubClient.Get(context.Background(), types.NamespacedName{Name: workName, Namespace: memberReservedNSName}, &resultWork)
		if err != nil {
			return false
		}
		applyCond := meta.FindStatusCondition(resultWork.Status.Conditions, fleetv1beta1.WorkConditionTypeApplied)
		if applyCond == nil || applyCond.Status != metav1.ConditionTrue || applyCond.ObservedGeneration != resultWork.Generation {
			By(fmt.Sprintf("applyCond not true: %v", applyCond))
			return false
		}
		for _, manifestCondition := range resultWork.Status.ManifestConditions {
			if !meta.IsStatusConditionTrue(manifestCondition.Conditions, fleetv1beta1.WorkConditionTypeApplied) {
				By(fmt.Sprintf("manifest applyCond not true %v : %v", manifestCondition.Identifier, manifestCondition.Conditions))
				return false
			}
		}
		return true
	}, eventuallyDuration, eventuallyInterval).Should(BeTrue())
	return &resultWork
}

// waitForWorkToAvailable waits for a work to have an available condition to be true
func waitForWorkToBeAvailable(workName string) *fleetv1beta1.Work {
	var resultWork fleetv1beta1.Work
	Eventually(func() bool {
		err := hubClient.Get(context.Background(), types.NamespacedName{Name: workName, Namespace: memberReservedNSName}, &resultWork)
		if err != nil {
			return false
		}
		availCond := meta.FindStatusCondition(resultWork.Status.Conditions, fleetv1beta1.WorkConditionTypeAvailable)
		if !condition.IsConditionStatusTrue(availCond, resultWork.Generation) {
			By(fmt.Sprintf("availCond not true: %v", availCond))
			return false
		}
		for _, manifestCondition := range resultWork.Status.ManifestConditions {
			if !meta.IsStatusConditionTrue(manifestCondition.Conditions, fleetv1beta1.WorkConditionTypeAvailable) {
				By(fmt.Sprintf("manifest availCond not true %v : %v", manifestCondition.Identifier, manifestCondition.Conditions))
				return false
			}
		}
		return true
	}, eventuallyDuration, eventuallyInterval).Should(BeTrue())
	return &resultWork
}

// waitForWorkToBeHandled waits for a work to have a finalizer
func waitForWorkToBeHandled(workName, workNS string) *fleetv1beta1.Work {
	var resultWork fleetv1beta1.Work
	Eventually(func() bool {
		err := hubClient.Get(context.Background(), types.NamespacedName{Name: workName, Namespace: workNS}, &resultWork)
		if err != nil {
			return false
		}
		return controllerutil.ContainsFinalizer(&resultWork, fleetv1beta1.WorkFinalizer)
	}, eventuallyDuration, eventuallyInterval).Should(BeTrue())
	return &resultWork
}

// verifyConfigMapExists verifies that the configmap exists in member cluster
func verifyConfigMapExists(cm *corev1.ConfigMap, resourceNamespace string) {
	Consistently(func() bool {
		var configMap corev1.ConfigMap
		err := memberClient.Get(context.Background(), types.NamespacedName{Name: cm.Name, Namespace: resourceNamespace}, &configMap)
		return !apierrors.IsNotFound(err)
	}, consistentlyDuration, consistentlyInterval).Should(BeTrue(), fmt.Sprintf("ConfigMap %s should not be deleted", cm.Name))
}

// verifyConfigmapIsRemoved verifies that the configmap is removed from member cluster
func verifyConfigmapIsRemoved(cm *corev1.ConfigMap, ns string) {
	Eventually(func() bool {
		var configMap corev1.ConfigMap
		err := memberClient.Get(context.Background(), types.NamespacedName{Name: cm.Name, Namespace: ns}, &configMap)
		return apierrors.IsNotFound(err)
	}, eventuallyDuration*5, eventuallyInterval).Should(BeTrue(), fmt.Sprintf("ConfigMap %s should be deleted", cm.Name))
}

// verifyWorkIsDeleted verifies that the work is deleted from the hub cluster
func verifyWorkIsDeleted(work *fleetv1beta1.Work) {
	Eventually(func() bool {
		var currentWork fleetv1beta1.Work
		return apierrors.IsNotFound(hubClient.Get(context.Background(), types.NamespacedName{Name: work.Name, Namespace: memberReservedNSName}, &currentWork))
	}, eventuallyDuration, eventuallyInterval).Should(BeTrue(), "Work should be deleted")
}

// verifyAppliedWorkIsDeleted verifies that the applied work is deleted from the hub cluster
func verifyAppliedWorkIsDeleted(name string) {
	Eventually(func() bool {
		var currentAppliedWork fleetv1beta1.AppliedWork
		return apierrors.IsNotFound(hubClient.Get(context.Background(), types.NamespacedName{Name: name}, &currentAppliedWork))
	}, eventuallyDuration, eventuallyInterval).Should(BeTrue(), "AppliedWork should be deleted")
}

// verifyNamespaceIsDeleted verifies that the namespace is deleted from the hub cluster
func verifyNamespaceIsDeleted(nsName string) {
	Eventually(func() bool {
		var ns corev1.Namespace
		return apierrors.IsNotFound(hubClient.Get(context.Background(), types.NamespacedName{Name: nsName}, &ns))
	}, eventuallyDuration, eventuallyInterval).Should(BeTrue(), "Namespace should be deleted")
}

// removeFinalizersFromResource removes the finalizers from the configmap to delete
func removeFinalizersFromResource(cm *corev1.ConfigMap) {
	var configMap corev1.ConfigMap
	Expect(memberClient.Get(context.Background(), types.NamespacedName{Name: cm.Name, Namespace: cm.Namespace}, &configMap)).Should(Succeed(), "Failed to get configmap")
	controllerutil.RemoveFinalizer(&configMap, "example.com/finalizer")
	Expect(memberClient.Update(context.Background(), &configMap)).Should(Succeed(), "Failed to remove finalizers from configmap")
}

func addFinalizerToConfigMap(cm *corev1.ConfigMap) {
	// Add finalizer to the configmap
	var configMap corev1.ConfigMap
	Expect(memberClient.Get(context.Background(), types.NamespacedName{Name: cm.Name, Namespace: cm.Namespace}, &configMap)).Should(Succeed())
	controllerutil.AddFinalizer(&configMap, "example.com/finalizer")
	Expect(memberClient.Update(context.Background(), &configMap)).Should(Succeed())
	By("Added finalizer to configmap")
}

func cleanupResources(cm, cm2 *corev1.ConfigMap, ns *corev1.Namespace, work *fleetv1beta1.Work) {
	// Remove finalizers from the configmap
	removeFinalizersFromResource(cm)
	removeFinalizersFromResource(cm2)

	// Delete the configmap
	Expect(memberClient.Delete(context.Background(), cm)).Should(Succeed())
	Expect(memberClient.Delete(context.Background(), cm2)).Should(Succeed())

	// Verify the configmap is deleted
	verifyConfigmapIsRemoved(cm, cm.Namespace)
	verifyConfigmapIsRemoved(cm2, cm2.Namespace)

	// Delete the namespace
	Expect(memberClient.Delete(context.Background(), ns)).Should(Succeed())
	verifyNamespaceIsDeleted(ns.Name)

	// Remove finalizers from the applied work. Needed as there
	var currentAppliedWork fleetv1beta1.AppliedWork
	Expect(memberClient.Get(context.Background(), types.NamespacedName{Name: work.Name}, &currentAppliedWork)).Should(Succeed(), "Failed to get applied work")
	controllerutil.RemoveFinalizer(&currentAppliedWork, metav1.FinalizerDeleteDependents)
	Expect(memberClient.Update(context.Background(), &currentAppliedWork)).Should(Succeed(), "Failed to remove finalizers from applied work")

	// verify the applied work is deleted
	verifyAppliedWorkIsDeleted(work.Name)

	// verify the work is deleted
	verifyWorkIsDeleted(work)
}
