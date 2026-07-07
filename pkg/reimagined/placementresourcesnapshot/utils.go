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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/kubectl/pkg/util/deployment"
)

func sanitizeManifestObject(manifestObj *unstructured.Unstructured) *unstructured.Unstructured {
	// Create a deep copy of the object.
	manifestObjCopy := manifestObj.DeepCopy()

	// Remove certain labels and annotations.
	if annotations := manifestObjCopy.GetAnnotations(); annotations != nil {
		// Remove the last applied configuration set by kubectl.
		delete(annotations, corev1.LastAppliedConfigAnnotation)

		// Remove the revision annotation set by deployment controller.
		delete(annotations, deployment.RevisionAnnotation)

		if len(annotations) == 0 {
			manifestObjCopy.SetAnnotations(nil)
		} else {
			manifestObjCopy.SetAnnotations(annotations)
		}
	}

	// Remove certain system-managed fields.
	manifestObjCopy.SetOwnerReferences(nil)
	manifestObjCopy.SetManagedFields(nil)

	// Remove the read-only fields.
	manifestObjCopy.SetCreationTimestamp(metav1.Time{})
	manifestObjCopy.SetDeletionTimestamp(nil)
	manifestObjCopy.SetDeletionGracePeriodSeconds(nil)
	manifestObjCopy.SetGeneration(0)
	manifestObjCopy.SetResourceVersion("")
	manifestObjCopy.SetSelfLink("")
	manifestObjCopy.SetUID("")

	// Remove the status field.
	unstructured.RemoveNestedField(manifestObjCopy.Object, "status")

	// Note: in the Fleet hub agent logic, the system also handles the Service and Job objects
	// in a special way, so as to remove certain fields that are set by the hub cluster API
	// server automatically; for the Fleet member agent logic here, however, Fleet assumes
	// that if these fields are set, users must have set them on purpose, and they should not
	// be removed. The difference comes to the fact that the Fleet member agent sanitization
	// logic concerns only the enveloped objects, which are free from any hub cluster API
	// server manipulation anyway.

	return manifestObjCopy
}
