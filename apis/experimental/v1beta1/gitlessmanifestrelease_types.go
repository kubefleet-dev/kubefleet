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

// ManifestRelease is the KubeFleet API that represents a collection of manifests, as read
// from an OCI artifact, for placement.
//
// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={kubefleet, kubefleet-experimental}
// +kubebuilder:storageversion
type ManifestRelease struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// The specification of the manifest release.
	Spec ManifestReleaseSpec `json:"spec,omitempty"`
	// The observed status of the manifest release.
	Status ManifestReleaseStatus `json:"status,omitempty"`
}

type ManifestReleaseSpec struct {
	// The reference to the OCI artifact that contains the manifests.
	//
	// +kubebuilder:Validation:Required
	ManifestArtifactRef SameNamespacedOCIArtifactReference `json:"manifestArtifactRef,omitempty"`

	// The path to a file or a directory within the OCI artifact (after extraction) that contains the manifest(s)
	// to be placed. If the path is a directory, all the manifests (YAML files) under the directory will be placed.
	//
	// The default is `.` (the root of the extracted OCI artifact).
	//
	// +kubebuilder:Validation:Required
	// +kubebuilder:Default:="."
	Path string `json:"path,omitempty"`

	// The options for processing manifests from the OCI artifact.
	//
	// +kubebuilder:Validation:Optional
	Options ManifestReleaseOptions `json:"options,omitempty"`
}

type ManifestReleaseOptions struct {
	// Whether to find and place manifests under the specified path recursively if the path is a directory.
	//
	// +kubebuilder:Validation:Optional
	Recursive bool `json:"recursive,omitempty"`

	// A list of paths to ignore when processing manifests from the OCI artifact. You may use single asterisk
	// (`*`) to match any sequence of characters in a path segment.
	//
	// For example, the setup [`foo/`, `bar/*-test.yaml`] will set KubeFleet to ignore all manifests under
	// the `foo/` directory and any manifest file that ends with `-test.yaml` under the `bar/` directory.
	//
	// +kubebuilder:Validation:Optional
	IgnorePaths []string `json:"ignorePaths,omitempty"`
}

type ManifestReleaseStatus struct {
	// A list of observed conditions of the manifest release.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}
