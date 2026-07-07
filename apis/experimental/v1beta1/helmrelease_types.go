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
	"k8s.io/apimachinery/pkg/runtime"
)

// HelmRelease is the KubeFleet API that represents a Helm chart for placement, as extracted from an
// OCI artifact.
//
// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={kubefleet, kubefleet-experimental}
// +kubebuilder:storageversion
type HelmRelease struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// The specification of the Helm release.
	Spec HelmReleaseSpec `json:"spec,omitempty"`

	// The observed status of the Helm release.
	Status HelmReleaseStatus `json:"status,omitempty"`
}

type HelmReleaseSpec struct {
	// The reference to the OCI artifact that contains the Helm chart.
	//
	// +kubebuilder:Validation:Required
	ChartRef SameNamespacedOCIArtifactReference `json:"chartRef,omitempty"`

	// The options for rendering the manifests from the Helm chart via the Helm Go SDK.
	//
	// KubeFleet installs/upgrades a Helm chart by rendering the manifests from the chart (enabled by the Helm Go SDK
	// in the same way as how `helm template` works) rather directly invoking the Helm framework to install/upgrade
	// chart. This is a design choice so as to avoid the complication of distributing charts across cluster binaries
	// and to keep the placement process neutral to manifests from all sources. In other words, the lifecycle of
	// all manifests, installed from a Helm chart via KubeFleet, is managed by KubeFleet and not Helm.
	// As a result, functionalities that are specific to the Helm-managed releases might not be available when a
	// chart is placed via KubeFleet. Refer to the KubeFleet documentations for specifics on such limitations and
	// how KubeFleet features can be used as workarounds.
	//
	// If not specified, the default options will be used.
	//
	// +kubebuilder:Validation:Optional
	HelmOptions *HelmOptions `json:"helmOptions,omitempty"`

	// The Helm chart values.
	//
	// You may also set values via ConfigMap or Secret objects by setting the `ValuesFrom` field below.
	// KubeFleet will merge the values from all sources and apply them when templating the Helm chart. If a value
	// is set in multiple sources, the value explicitly set here in the `Values` field will take precedence
	// over those set in the `ValuesFrom` field.
	//
	// +kubebuilder:Validation:Optional
	Values runtime.RawExtension `json:"values,omitempty"`

	// The Helm chart values, set via ConfigMap or Secret objects.
	//
	// You may also set values directly via the `Values` field above. KubeFleet will merge the values from all sources
	// and apply them when templating the Helm chart. If a value is set in multiple sources, the value explicitly set
	// in the `Values` field will take precedence over those set in this `ValuesFrom` field. If a value is set
	// in multiple sources from this `ValuesFrom` field, the value from the last source in the list will take
	// precedence over those from the previous sources in the list.
	//
	// +kubebuilder:Validation:Optional
	ValuesFrom []HelmValuesFromObjectReference `json:"valuesFrom,omitempty"`
}

type SameNamespacedOCIArtifactReference struct {
	// The name of the OCI artifact. The object must be in the same namespace as the helm release object.
	//
	// +kubebuilder:Validation:Required
	Name string `json:"name,omitempty"`
}

type HelmOptions struct {
	// The name of the Helm release. This is equivalent to the `--release-name` option in the
	// `helm template` command. See the Helm documentation for more information.
	//
	// The default value is `[NAMESPACE-NAME]`, where `[NAMESPACE]` is the namespace of the
	// HelmRelease object and `[NAME]` is the name of the HelmRelease object.
	//
	// +kubebuilder:Validation:Optional
	ReleaseName string `json:"releaseName,omitempty"`

	// The namespace scope of the Helm release. This is equivalent to the `--namespace` option in the
	// `helm template` command. See the Helm documentation for more information.
	//
	// The default value is the namespace of the HelmRelease object.
	//
	// +kubebuilder:Validation:Optional
	Namespace string `json:"namespace,omitempty"`

	// The API versions set in Helm chart capabilities when rendering the manifests. This is equivalent
	// to the `--api-versions` option in the `helm template` command. See the Helm documentation for more information.
	//
	// +kubebuilder:Validation:Optional
	APIVersions []string `json:"apiVersions,omitempty"`

	// The Kubernetes version set in Helm chart capabilities when rendering the manifests. This is equivalent
	// to the `--kube-version` option in the `helm template` command. See the Helm documentation for more information.
	//
	// +kubebuilder:Validation:Optional
	KubeVersion string `json:"kubeVersion,omitempty"`

	// Whether to skip CRDs when rendering the manifests. This is equivalent to the `--skip-crds` option
	// in the `helm template` command. See the Helm documentation for more information.
	//
	// +kubebuilder:Validation:Optional
	SkipCRDs bool `json:"skipCRDs,omitempty"`

	// Whether to skip JSON schema validation when rendering the manifests. This is equivalent to the
	// `--skip-schema-validation` option in the `helm template` command. See the Helm documentation for more information.
	//
	// +kubebuilder:Validation:Optional
	SkipSchemaValidation bool `json:"skipSchemaValidation,omitempty"`

	// Whether to skip test resources when rendering the manifests. This is equivalent to the
	// `--skip-tests` option in the `helm template` command. See the Helm documentation for more information.
	//
	// +kubebuilder:Validation:Optional
	SkipTests bool `json:"skipTests,omitempty"`
}

type HelmValuesFromObjectReference struct {
	// The kind of the object that contains the Helm chart values.
	// This can be either `ConfigMap` or `Secret`.
	//
	// +kubebuilder:Validation:Required
	// +kubebuilder:validation:Enum=ConfigMap;Secret
	Kind string `json:"kind,omitempty"`

	// The name of the object that contains the Helm chart values.
	//
	// +kubebuilder:Validation:Required
	Name string `json:"name,omitempty"`

	// The key in the `.data` field of the object that contains the Helm chart values.
	// If not specified, the default value is `values.yaml`.
	//
	// +kubebuilder:Validation:Optional
	// +kubebuilder:default="values.yaml"
	DataKey string `json:"dataKey,omitempty"`

	// The path in the values where the extracted data will be merged. The path should be a valid Go YAML path;
	// see https://pkg.go.dev/github.com/goccy/go-yaml#PathString for more information.
	//
	// If not specified, the data will merged at the root level.
	//
	// +kubebuilder:Validation:Optional
	ValuesPath string `json:"valuesPath,omitempty"`

	// Whether to treat the extracted data as a literal value. If set to `true`, the extracted data will be
	// merged as a literal string value, rather than as an YAML object of its own.
	//
	// +kubebuilder:Validation:Optional
	SetAsLiteral bool `json:"setAsLiteral,omitempty"`
}

type HelmReleaseStatus struct {
	// A list of observed conditions of the Helm release.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// The hash of the Helm chart values, merged from all sources. This is a field kept for tracking
	// changes in the Helm chart values.
	ValuesHash string `json:"valuesHash,omitempty"`

	// The path where the Helm chart values is kept, after merging the inputs from all sources.
	ValuesPath string `json:"valuesPath,omitempty"`
}
