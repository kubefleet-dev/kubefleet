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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope="Cluster",categories={fleet,fleet-placement}
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ClusterResourceEnvelope wraps cluster-scoped resources for placement.
type ClusterResourceEnvelope struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// The desired state of ClusterResourceEnvelope.
	// +kubebuilder:validation:Required
	Spec EnvelopeSpec `json:"spec"`
}

// EnvelopeSpec helps wrap resources for placement.
type EnvelopeSpec struct {
	// A map of wrapped manifests.
	//
	// Each manifest is uniquely identified by a string key, typically a filename that represents
	// the manifest.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinProperties=1
	// +kubebuilder:validation:MaxProperties=50
	Manifests map[string]Manifest `json:"manifests"`
}

// Manifest is a wrapped resource.
type Manifest struct {
	// The resource data.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:EmbeddedResource
	// +kubebuilder:pruning:PreserveUnknownFields
	Data runtime.RawExtension `json:"data"`
}

// +genclient
// +genclient:Namespaced
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope="Namespaced",categories={fleet,fleet-placement}
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ResourceEnvelope struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// The desired state of ResourceEnvelope.
	// +kubebuilder:validation:Required
	Spec EnvelopeSpec `json:"spec"`
}

type EnvelopeReader interface {
	// GetManifests returns the manifests in the envelope.
	GetManifests() map[string]Manifest

	// GetEnvelopeObjRef returns a klog object reference to the envelope.
	GetEnvelopeObjRef() klog.ObjectRef

	// GetNamespace returns the namespace of the envelope.
	GetNamespace() string

	// GetName returns the name of the envelope.
	GetName() string

	// GetEnvelopeType returns the type of the envelope.
	GetEnvelopeType() string
}

// Ensure that both ClusterResourceEnvelope and ResourceEnvelope implement the
// EnvelopeReader interface at compile time.
var (
	_ EnvelopeReader = &ClusterResourceEnvelope{}
	_ EnvelopeReader = &ResourceEnvelope{}
)

// Implements the EnvelopeReader interface for ClusterResourceEnvelope.

func (e *ClusterResourceEnvelope) GetManifests() map[string]Manifest {
	return e.Spec.Manifests
}

func (e *ClusterResourceEnvelope) GetEnvelopeObjRef() klog.ObjectRef {
	return klog.KObj(e)
}

func (e *ClusterResourceEnvelope) GetEnvelopeType() string {
	return string(placementv1beta1.ClusterResourceEnvelopeType)
}

// Implements the EnvelopeReader interface for ResourceEnvelope.

func (e *ResourceEnvelope) GetManifests() map[string]Manifest {
	return e.Spec.Manifests
}

func (e *ResourceEnvelope) GetEnvelopeObjRef() klog.ObjectRef {
	return klog.KObj(e)
}

func (e *ResourceEnvelope) GetEnvelopeType() string {
	return string(placementv1beta1.ResourceEnvelopeType)
}
