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

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// KustomizeRelease is the KubeFleet API that represents a Kustomize application from an
// OCI artifact for placement.
//
// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={kubefleet, kubefleet-experimental}
// +kubebuilder:storageversion
type KustomizeRelease struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// The specification of the kustomize release.
	Spec KustomizeReleaseSpec `json:"spec,omitempty"`
	// The observed status of the kustomize release.
	Status KustomizeReleaseStatus `json:"status,omitempty"`
}

type KustomizeReleaseSpec struct {
	// The reference to the OCI artifact that contains the Kustomize application.
	//
	// +kubebuilder:Validation:Required
	KustomizeArtifactRef SameNamespacedOCIArtifactReference `json:"kustomizeArtifactRef,omitempty"`

	// The path to the directory within the OCI artifact (after extraction) that contains the
	// Kustomize configuration file, `kustomization.yaml`, `kustomization.yml`, or `kustomization`.
	//
	// The default is `.` (the root of the extracted OCI artifact).
	//
	// +kubebuilder:Validation:Required
	// +kubebuilder:Default:="."
	Path string `json:"path,omitempty"`

	// The options for processing the Kustomize application from the OCI artifact.
	//
	// KubeFleet will merge the setup here into the Kustomize configuration file and build the manifests
	// from the merged configuration. If the same value is specified in both the Kustomize configuration file
	// and the setup here, the value here will take precedence.
	//
	// +kubebuilder:Validation:Optional
	KustomizeConfigOpts KustomizeConfigurationOptions `json:"options,omitempty"`
}

type KustomizeConfigurationOptions struct {
	// The prefix to add to the names of all resources listed in the Kustomize configuration file. This is equivalent
	// to the `namePrefix` field in the Kustomize configuration file. See the Kustomize documentation for more
	// information.
	//
	// +kubebuilder:Validation:Optional
	NamePrefix *string `json:"namePrefix,omitempty"`

	// The suffix to add to the names of all resources listed in the Kustomize configuration file. This is equivalent
	// to the `nameSuffix` field in the Kustomize configuration file. See the Kustomize documentation for more
	// information.
	//
	// +kubebuilder:Validation:Optional
	NameSuffix *string `json:"nameSuffix,omitempty"`

	// The image overrides to apply to the Kustomize application. This is equivalent to the `images` field in the Kustomize
	// configuration file. See the Kustomize documentation for more information.
	//
	// +kubebuilder:Validation:Optional
	Images []KustomizeImageOverrides `json:"images,omitempty"`

	// The replica overrides to apply to the Kustomize application. This is equivalent to the `replicas` field in the Kustomize
	// configuration file. See the Kustomize documentation for more information.
	//
	// +kubebuilder:Validation:Optional
	Replicas []KustomizeReplicaOverrides `json:"replicas,omitempty"`

	// The label overrides to apply to the Kustomize application. This is equivalent to the `labels` field in the Kustomize
	// configuration file. See the Kustomize documentation for more information.
	//
	// +kubebuilder:Validation:Optional
	Labels []KustomizeLabelOverrides `json:"labels,omitempty"`

	// The annotation overrides to apply to the Kustomize application. This is equivalent to the `commonAnnotations`
	// field in the Kustomize configuration file. See the Kustomize documentation for more information.
	//
	// +kubebuilder:Validation:Optional
	CommonAnnotations map[string]string `json:"annotations,omitempty"`

	// The namespace to apply to all resources listed in the Kustomize configuration file. This is equivalent
	// to the `namespace` field in the Kustomize configuration file. See the Kustomize documentation for more information.
	//
	// +kubebuilder:Validation:Optional
	Namespace string `json:"namespace,omitempty"`

	// The patches to apply to the Kustomize application. This is equivalent to the `patches` field in the Kustomize
	// configuration file. See the Kustomize documentation for more information.
	//
	// +kubebuilder:Validation:Optional
	Patches []KustomizePatch `json:"patches,omitempty"`

	// The components to enable in the Kustomize application. This is equivalent to the `components` field in the Kustomize
	// configuration file. See the Kustomize documentation for more information.
	//
	// +kubebuilder:Validation:Optional
	Components []string `json:"components,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="has(self.newTag) || has(self.newName)",message="at least one of newTag or newName must be set"
type KustomizeImageOverrides struct {
	// The name of the image to override. See the Kustomize documentation for more information.
	//
	// +kubebuilder:Validation:Required
	Name string `json:"name,omitempty"`

	// The tag to use for the image. See the Kustomize documentation for more information.
	//
	// +kubebuilder:Validation:Optional
	NewTag string `json:"newTag,omitempty"`

	// The new name to use for the image. See the Kustomize documentation for more information.
	//
	// +kubebuilder:Validation:Optional
	NewName string `json:"newName,omitempty"`
}

type KustomizeReplicaOverrides struct {
	// The name of the resource to override the replica count for. See the Kustomize documentation for more information.
	//
	// +kubebuilder:Validation:Required
	Name string `json:"name,omitempty"`

	// The new replica count to use for the resource. See the Kustomize documentation for more information.
	//
	// +kubebuilder:Validation:Required
	Count int32 `json:"count,omitempty"`
}

type KustomizeLabelOverrides struct {
	// The labels to apply. See the Kustomize documentation for more information.
	//
	// +kubebuilder:Validation:Required
	Pairs map[string]string `json:"pairs,omitempty"`

	// Whether to include the labels in the selector fields. See the Kustomize documentation for more information.
	//
	// +kubebuilder:Validation:Optional
	IncludeSelectors bool `json:"includeSelectors,omitempty"`

	// Whether to include the labels in the pod template metadata. See the Kustomize documentation for more information.
	//
	// +kubebuilder:Validation:Optional
	IncludeTemplates bool `json:"includeTemplates,omitempty"`

	// Whether to include the labels in the volume claim templates. See the Kustomize documentation for more information.
	//
	// +kubebuilder:Validation:Optional
	IncludeVolumeClaimTemplates bool `json:"includeVolumeClaimTemplates,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="has(self.path) || has(self.patch)",message="at least one of path or patch must be set"
type KustomizePatch struct {
	// The path to the a file within the OCI artifact (after extraction) that contains the patch to be applied.
	//
	// +kubebuilder:Validation:Optional
	Path string `json:"path,omitempty"`

	// The target resource(s) where the patch will be applied.
	//
	// +kubebuilder:Validation:Required
	Target KustomizePatchTarget `json:"target,omitempty"`

	// The patch data to apply.
	//
	// +kubebuilder:Validation:Optional
	Patch []byte `json:"patch,omitempty"`

	// The options for applying the patch.
	//
	// +kubebuilder:Validation:Optional
	Options KustomizePatchOptions `json:"options,omitempty"`
}

type KustomizePatchTarget struct {
	// KubeFleet will pass the target information to Kustomize as it is; no additional validation
	// is performed on the API level.

	// The API group of the resource(s).
	Group string `json:"group,omitempty"`

	// The API version of the resource(s).
	Version string `json:"version,omitempty"`

	// The kind of the resource(s).
	Kind string `json:"kind,omitempty"`

	// The name of the resource.
	Name string `json:"name,omitempty"`

	// The label selector to select the resource(s).
	LabelSelector string `json:"labelSelector,omitempty"`

	// The annotation selector to select the resource(s).
	AnnotationSelector string `json:"annotationSelector,omitempty"`
}

type KustomizePatchOptions struct {
	// Whether to allow the patch to modify the name of the resource(s).
	AllowNameChange bool `json:"allowNameChange,omitempty"`

	// Whether to allow the patch to modify the kind of the resource(s).
	AllowKindChange bool `json:"allowKindChange,omitempty"`
}

type KustomizeReleaseStatus struct {
	// A list of observed conditions of the manifest release.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}
