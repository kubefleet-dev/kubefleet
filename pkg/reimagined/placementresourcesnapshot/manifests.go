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
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	experimentalv1beta1 "github.com/kubefleet-dev/kubefleet/apis/experimental/v1beta1"
	errors "github.com/kubefleet-dev/kubefleet/pkg/utils/errors"
)

func (m *Manager) retrieveResourceContentsFrom(
	ctx context.Context, placementPolicy *experimentalv1beta1.PlacementPolicy,
) ([]experimentalv1beta1.ResourceContent, error) {
	var resourceContents []experimentalv1beta1.ResourceContent
	for idx := range placementPolicy.Spec.ResourceSelectors {
		additionalResRef := placementPolicy.Spec.ResourceSelectors[idx]
		additionalResGVR := &schema.GroupVersionResource{
			Group:    additionalResRef.APIGroup,
			Version:  additionalResRef.APIVersion,
			Resource: additionalResRef.Resource,
		}

		additionalResObj, err := m.dynamicClient.
			Resource(*additionalResGVR).
			Namespace(placementPolicy.Namespace).
			Get(ctx, additionalResRef.Name, metav1.GetOptions{})
		if err != nil {
			wrappedErr := errors.NewAPIServerError(err,
				"failed to get additional resource object",
				true,
				"sourcekind", additionalResRef.Kind, "sourceName", additionalResRef.Name, "sourceNamespace", placementPolicy.Namespace)
			return nil, wrappedErr
		}
		cleanupManagedFields(additionalResObj)
		unstructured.RemoveNestedField(additionalResObj.Object, "status")

		additionalResObjJSON, err := additionalResObj.MarshalJSON()
		if err != nil {
			wrappedErr := errors.NewUnexpectedError(err,
				"failed to marshal additional resource object into JSON",
				"sourcekind", additionalResRef.Kind, "sourceName", additionalResRef.Name, "sourceNamespace", placementPolicy.Namespace)
			return nil, wrappedErr
		}

		resourceContents = append(resourceContents, experimentalv1beta1.ResourceContent{
			Identifier: experimentalv1beta1.SameNamespacedObjectReference{
				APIGroup:   additionalResGVR.Group,
				APIVersion: additionalResGVR.Version,
				Kind:       additionalResRef.Kind,
				Resource:   additionalResGVR.Resource,
				Name:       additionalResRef.Name,
			},
			Manifest: runtime.RawExtension{Raw: additionalResObjJSON},
		})
	}
	return resourceContents, nil
}

func cleanupManagedFields(obj metav1.Object) {
	obj.SetCreationTimestamp(metav1.Time{})
	obj.SetDeletionTimestamp(nil)
	obj.SetGeneration(0)
	obj.SetManagedFields(nil)
	obj.SetResourceVersion("")
	obj.SetUID("")
	obj.SetFinalizers(nil)
	obj.SetGenerateName("")
	obj.SetOwnerReferences(nil)
	obj.SetSelfLink("")
}
