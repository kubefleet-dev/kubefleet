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

// OCIArtifact is the KubeFleet API that represents an ORAS (OCI Registry as Storage)
// artifact (e.g., a Helm chart, or a bundle of Kubernetes manifests), which can be deployed to
// member clusters via KubeFleet's placement APIs.
//
// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={kubefleet, kubefleet-experimental}
// +kubebuilder:storageversion
type OCIArtifact struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// The specification of the OCI artifact.
	Spec OCIArtifactSpec `json:"spec,omitempty"`

	// The observed status of the OCI artifact.
	Status OCIArtifactStatus `json:"status,omitempty"`
}

type OCIArtifactSpec struct {
	// The authentication provider to use when connecting to the OCI registry for artifact retrieval.
	//
	// If unset, KubeFleet will connect to the OCI registry directly (i.e., the registry is a public one).
	//
	// +kubebuilder:validation:Optional
	AuthProvider *OCIArtifactAuthProvider `json:"authProvider,omitempty"`

	// The retrieval policy for the OCI artifact. Set this field to control how often KubeFleet should
	// check for updates to the artifact in the OCI registry, how long to wait for a response from the registry,
	// and how to process the layers of the artifact.
	//
	// If unset, KubeFleet will use the default retrieval policy.
	//
	// +kubebuilder:validation:Optional
	RetrievalPolicy *OCIArtifactRetrievalPolicy `json:"retrievalPolicy,omitempty"`

	// The URL of the OCI artifact. It should be of the format `[REGISTRY]/[REPOSITORY]/[ARTIFACT]`,
	// where `[REGISTRY]` is the OCI registry (e.g., `rpdstars.azurecr.io`), `[REPOSITORY]` is the repository in the
	// registry (e.g., `alphateam`), and `[ARTIFACT]` is the artifact name (e.g., `webapp`).
	//
	// To retrieve a specific version of the artifact, set the `Ref` field below. By default KubeFleet
	// will attempt to retrieve the artifact with the `latest` tag.
	//
	// +kubebuilder:validation:Required
	URL string `json:"url,omitempty"`

	// The version reference of the OCI artifact. KubeFleet can:
	//
	// * retrieve the artifact with a specific tag (e.g., `production`, `experimental`), or
	// * retrieve the artifact with a tag that satisifies a semantic versioning (semver) constraint,
	//   e.g., `1.2 - 1.4`, `2.0.x`, `>=1.2.3, <2.0.0`. See the KubeFleet documentation for more details on the
	//   semver constraint syntax.
	// * retrieve the artifact with a specific digest (e.g., `sha256:abcdef1234567890`).
	//
	// If unset, KubeFleet will attempt to retrieve the artifact with the `latest` tag.
	//
	// +kubebuilder:validation:Optional
	Ref *OCIArtifactReference `json:"ref,omitempty"`

	// Whether to suspend the retrieval of the OCI artifact. If set to true, KubeFleet will no longer
	// attempt to check for updates to the artifact in the OCI registry; all placements that reference this artifact
	// will use the last retrieved version of the artifact.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=false
	Suspend bool `json:"suspend,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="self.authProviderType != 'Generic' || has(self.secretRef)",message="secretRef must be set when authProviderType is Generic"
type OCIArtifactAuthProvider struct {
	// The type of the authentication provider.
	//
	// Available options are:
	// * None: Connect to the OCI registry directly with no authentication (i.e., the registry is a public one).
	// * Generic: Connect to the OCI registry with an image pull secret. Set the `SecretRef` field below to
	//   specify the secret to use.
	// * Azure: Connect to the registry with default Azure credentials. This option works only when KubeFleet is
	//   deployed in an Azure environment (e.g., Azure Kubernetes Service) and the environment has properly
	//   configured Azure credentials.
	//
	// The default value is `None`.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=None
	Type AuthProviderType `json:"authProviderType,omitempty"`

	// An object reference to the Secret object that contains the credentials to use when connecting
	// to the OCI registry. The Secret object must be an image pull secret; see the Kubernetes documentation
	// for more information.
	//
	// +kubebuilder:validation:Optional
	SecretRef *SameNamespacedObjectReference `json:"secretRef,omitempty"`
}

type AuthProviderType string

const (
	AuthProviderTypeNone    AuthProviderType = "None"
	AuthProviderTypeGeneric AuthProviderType = "Generic"
	AuthProviderTypeAzure   AuthProviderType = "Azure"
)

type OCIArtifactRetrievalPolicy struct {
	// How often KubeFleet should check for updates to the OCI artifact in the registry, in seconds.
	//
	// If unset, KubeFleet will check for updates every 30 minutes (1800 seconds).
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=1800
	IntervalSeconds int32 `json:"intervalSeconds,omitempty"`

	// The maximum amount of time, in seconds, that KubeFleet should wait for a response from the OCI registry
	// when checking for updates to the artifact.
	//
	// If unset, KubeFleet will use a default timeout of 60 seconds.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=60
	TimeoutSeconds int32 `json:"timeoutSeconds,omitempty"`

	// The layer to process from the OCI artifact. KubeFleet will untar (and unzip, if applicable)
	// the selected layer and save the contents for placement.
	//
	// In many cases KubeFleet can auto-process OCI artifacts with no additional instructions needed.
	// If the OCI artifact is:
	//
	// * a single-layer artifact packaged with Helm that contains a Helm chart (using the media type
	// `application/vnd.cncf.helm.chart.content.v1.tar+gzip`); or
	// * a single- or multi-layer artifact packaged with ORAS that contains a bundle of Kubernetes manifests
	//   (possibly organized in a hierarchy of directories) with proper media types (e.g.,
	//   `application/vnd.oci.image.layer.v1.tar`) and annotations (e.g., `org.opencontainers.image.title`),
	//
	// No configuration is needed for this field. However, if the you package your artifact with
	// a custom tool that sets different media types/annotations, you might need to explicitly specify
	// which layer to pick and process by setting this field.
	//
	// If this field is unset and KubeFleet cannot recognize the layers, an error will be raised.
	//
	// If there are multiple matching layers, the first one (as ordered in the OCI artifact manifest)
	// will be selected.
	//
	// +kubebuilder:validation:Optional
	LayerSelectors OCIArtifactLayerSelector `json:"layerSelectors,omitempty"`
}

type OCIArtifactLayerSelector struct {
	// The media type of the layer to select. The layer must be a tarball (possibly gzipped).
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self.endsWith('.tar') || self.endsWith('.tar+gzip')",message="mediaType must end with '.tar' or '.tar+gzip'"
	MediaType string `json:"mediaType,omitempty"`

	// The (relative) path where the contents of the layer will be extracted.
	// It must be a single-level directory or file name.
	//
	// The default value is `app`.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:default="app"
	// +kubebuilder:validation:XValidation:rule="self.matches('^[a-zA-Z0-9_-]+([.][a-zA-Z0-9]+)?$')",message="path must be a single-level directory or file name using only alphanumeric characters, underscores, or dashes, e.g., 'app', 'deploy.yaml'"
	Path string `json:"path,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="!(has(self.tag) && has(self.digest))",message="tag and digest are mutually exclusive; only one of them can be set"
type OCIArtifactReference struct {
	// The tag of the OCI artifact.
	//
	// The `tag`, `semVer`, and `digest` fields are mutually exclusive. You can set only one of them.
	//
	// +kubebuilder:validation:Optional
	Tag string `json:"tag,omitempty"`

	// The semantic versioning (semver) constraint of the OCI artifact. This only
	// applies if the artifact is tagged with a semantic version (e.g., `1.2.3`).
	//
	// A constraint is a semver comparison string, or a group of semver comparison strings connected by logical OR
	// operators (`||`). A semver comparison string is a list of space- or comma-separated semver comparisons.
	//
	// For example, the constraints below are valid:
	// * `>= 1.2.3` (one single semver comparison string with one comparison)
	// * `>= 1.2.3 < 2.0.0` (one single semver comparison string with two comparisons)
	// * `>= 1.2.3 < 2.0.0 || >= 3.0.0` (two semver comparison strings connected by a logical OR operator)
	//
	// The following comparisons are supported: `=`, `!=`, `>`, `<`, `>=`, `<=`.
	//
	// You can also use the following shorthand notations, which are translated to the corresponding semver comparisons:
	// * Hyphen ranges, e.g., `1.2 - 1.4` (equivalent to `>= 1.2.0 <= 1.4.0`)
	// * Wildcards, e.g., `1.2.x` (equivalent to `>= 1.2.0 < 1.3.0`)
	// * Tilde ranges, e.g., `~1.2.3` (equivalent to `>= 1.2.3 < 1.3.0`)
	// * Caret ranges, e.g., `^1.2.3` (equivalent to `>= 1.2.3 < 2.0.0`)
	//
	// Pre-release versions come before their associated releases, e.g., `1.2.3-alpha` comes before `1.2.3`.
	// These versions, if included in the comparison, are sorted in their ASCII order.
	//
	// KubeFleet will ignore leading characters (e.g., `v` in `v1.2.3`) when comparing versions. There is no
	// need to include the leading character in the semver constraint.
	//
	// +kubebuilder:validation:Optional
	//SemVer string `json:"semVer,omitempty"`

	// The digest of the OCI artifact.
	//
	// The `tag`, `semVer`, and `digest` fields are mutually exclusive. You can set only one of them.
	//
	// +kubebuilder:validation:Optional
	Digest string `json:"digest,omitempty"`
}

type OCIArtifactStatus struct {
	// A list of observed conditions about the OCI artifact.
	//
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// The details about the retrieved OCI artifact (if any).
	//
	// +kubebuilder:validation:Optional
	ArtifactDetails OCIArtifactDetails `json:"artifact,omitempty"`
}

type OCIArtifactDetails struct {
	// The full URL of the OCI artifact.
	URL string `json:"url,omitempty"`
	// The tag of the OCI artifact.
	Tag string `json:"tag,omitempty"`
	// The digest of the OCI artifact.
	Digest string `json:"digest,omitempty"`
	// The annotations of the OCI artifact.
	Annotations map[string]string `json:"metadata,omitempty"`
	// The path where the contents of the artifact are extracted.
	Path string `json:"path,omitempty"`
	// The size of the OCI artifact in bytes.
	SizeBytes int64 `json:"size,omitempty"`
	// The media type of the OCI artifact.
	MediaType string `json:"mediaType,omitempty"`
	// The type of the OCI artifact.
	ArtifactType string `json:"artifactType,omitempty"`

	// The details about individual layers of the OCI artifact. If a layer selector
	// has been specified, only the selected layer will be included in this list.
	Layers []OCIArtifactLayerDetails `json:"layers,omitempty"`
}

type OCIArtifactLayerDetails struct {
	// The media type of the layer.
	MediaType string `json:"mediaType,omitempty"`
	// The size of the layer in bytes.
	SizeBytes int64 `json:"size,omitempty"`
	// The digest of the layer.
	Digest string `json:"digest,omitempty"`
	// The annotations of the layer.
	Annotations map[string]string `json:"metadata,omitempty"`
	// The path where the contents of the layer are extracted.
	Path string `json:"path,omitempty"`
}
