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

package v1beta1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:resource:scope=Cluster,categories={fleet,fleet-placement}
// +kubebuilder:object:root=true
// +kubebuilder:storageversion
type DynamicResourceClass struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec is the desired configuration of a DynamicResourceClass.
	// +kubebuilder:validation:Required
	// +required
	Spec DynamicResourceClassSpec `json:"spec,omitempty"`
}

type DynamicResourceClassSpec struct {
	// Selector is a DynamicResourceClassSelector that help select resources
	// belonging to this DynamicResourceClass.
	Selector DynamicResourceClassSelector `json:"selector,omitempty"`

	// TO-DO (chenyu1): add support for extensible configuration data (see KEP 4381's DeviceClass API).
}

type DynamicResourceClassSelector struct {
	// MatchAttributes is a list of string-based key/value pairs that are used to match against
	// resource attributes.
	// TO-DO (chenyu1): switch to CEL expressions for better flexibility (see KEP 4381's DeviceClass API).
	MatchAttributes map[string]string `json:"matchAttributes,omitempty"`
}

// +kubebuilder:object:root=true
type DynamicResourceClassList struct {
	metav1.TypeMeta `json:",inline"`
	// Standard list metadata.
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`
	// The list of DynamicResourceClasses.
	// +listType=set
	Items []DynamicResourceClass `json:"items"`
}

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:resource:scope=Cluster,categories={fleet,fleet-placement}
// +kubebuilder:object:root=true
// +kubebuilder:storageversion
type DynamicResourceSlice struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec is the desired configuration of a DynamicResourceSlice.
	// +kubebuilder:validation:Required
	// +required
	Spec DynamicResourceSliceSpec `json:"spec,omitempty"`
}

type DynamicResourceSliceSpec struct {
	// Provisioner is the name of the source that prepares this resource slice.
	// +kubebuilder:validation:Required
	// +required
	Provisioner string `json:"provisioner,omitempty"`

	// ClusterName is the name of the cluster where the resource slice is available.
	// TO-DO (chenyu1): for simplicity reasons, at this stage the system assumes resources are always
	// cluster-local; switch to a more flexible model that allows non-local resources as needed.
	// +kubebuilder:validation:Required
	// +required
	ClusterName string `json:"clusterName,omitempty"`

	// Resources are the list of dynamic resources specified by this slice.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=100
	Resources []DynamicResource `json:"resources,omitempty"`
}

type QualifiedName string

type DynamicResource struct {
	// Name is the name of the dynamic resource. It must be a DNS label.
	// +kubebuilder:validation:Required
	// +required
	Name string `json:"name,omitempty"`

	// +optional
	Attributes map[QualifiedName]ResourceAttribute `json:"attributes,omitempty"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinProperties=1
	// +kubebuilder:validation:MaxProperties=100
	Capacity map[QualifiedName]ResourceCapacity `json:"capacity,omitempty"`
}

type ResourceAttribute struct {
	// +kubebuilder:validation:Required
	// +required
	Value string `json:"value,omitempty"`
	// +kubebuilder:validation:Required
	// +required
	// +kubebuilder:default=String
	// +kubebuilder:validation:Enum=String
	TypeHint string `json:"typeHint,omitempty"`
}

type ResourceCapacity struct {
	Quantity resource.Quantity `json:"quantity,omitempty"`
}

// +kubebuilder:object:root=true
type DynamicResourceSliceList struct {
	metav1.TypeMeta `json:",inline"`
	// Standard list metadata.
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`
	// The list of DynamicResourceClasses.
	// +listType=set
	Items []DynamicResourceSlice `json:"items"`
}

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:resource:scope="Cluster",categories={fleet,fleet-placement}
// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
type ClusterResourcePlacementSchedulingContext struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	// +required
	Spec PlacementSchedulingContextSpec `json:"spec,omitempty"`
	// +optional
	Status PlacementSchedulingContextStatus `json:"status,omitempty"`
}

func (c *ClusterResourcePlacementSchedulingContext) GetPlacementSchedulingContextSpec() *PlacementSchedulingContextSpec {
	return &c.Spec
}

func (c *ClusterResourcePlacementSchedulingContext) SetPlacementSchedulingContextSpec(spec *PlacementSchedulingContextSpec) {
	c.Spec = *spec
}

func (c *ClusterResourcePlacementSchedulingContext) GetPlacementSchedulingContextStatus() *PlacementSchedulingContextStatus {
	return &c.Status
}

func (c *ClusterResourcePlacementSchedulingContext) SetPlacementSchedulingContextStatus(status *PlacementSchedulingContextStatus) {
	c.Status = *status
}

// +genclient
// +genclient:Namespaced
// +kubebuilder:resource:scope="Namespaced",categories={fleet,fleet-placement}
// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
type ResourcePlacementSchedulingContext struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	// +required
	Spec PlacementSchedulingContextSpec `json:"spec,omitempty"`
	// +optional
	Status PlacementSchedulingContextStatus `json:"status,omitempty"`
}

func (r *ResourcePlacementSchedulingContext) GetPlacementSchedulingContextSpec() *PlacementSchedulingContextSpec {
	return &r.Spec
}

func (r *ResourcePlacementSchedulingContext) SetPlacementSchedulingContextSpec(spec *PlacementSchedulingContextSpec) {
	r.Spec = *spec
}

func (r *ResourcePlacementSchedulingContext) GetPlacementSchedulingContextStatus() *PlacementSchedulingContextStatus {
	return &r.Status
}

func (r *ResourcePlacementSchedulingContext) SetPlacementSchedulingContextStatus(status *PlacementSchedulingContextStatus) {
	r.Status = *status
}

type PlacementSchedulingContextSpec struct {
	// +optional
	PotentialClusters []string `json:"potentialClusters,omitempty"`
	// +required
	// +kubebuilder:validation:Required
	PlacementRef string `json:"placementRef,omitempty"`
}

type PlacementSchedulingContextStatus struct {
	// +optional
	DynamicResourceClaimStatus []DynamicResourceClaimStatus `json:"dynamicResourceClaimStatus,omitempty"`
}

type DynamicResourceClaimStatus struct {
	// +kubebuilder:validation:Required
	// +required
	ResourceName string `json:"resourceName,omitempty"`
	// +optional
	SuitableClusters map[string]int64 `json:"suiteableClusters,omitempty"`
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
type ResourcePlacementSchedulingContextList struct {
	metav1.TypeMeta `json:",inline"`
	// Standard list metadata.
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`
	// The list of DynamicResourceClasses.
	// +listType=set
	Items []ResourcePlacementSchedulingContext `json:"items"`
}

// +kubebuilder:object:root=true
type ClusterResourcePlacementSchedulingContextList struct {
	metav1.TypeMeta `json:",inline"`
	// Standard list metadata.
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`
	// The list of DynamicResourceClasses.
	// +listType=set
	Items []ClusterResourcePlacementSchedulingContext `json:"items"`
}

var _ PlacementSchedulingContextObj = &ClusterResourcePlacementSchedulingContext{}
var _ PlacementSchedulingContextObj = &ResourcePlacementSchedulingContext{}

// +kubebuilder:object:generate=false
type PlacementSchedulingContextSpecGetterSetter interface {
	GetPlacementSchedulingContextSpec() *PlacementSchedulingContextSpec
	SetPlacementSchedulingContextSpec(spec *PlacementSchedulingContextSpec)
}

// +kubebuilder:object:generate=false
type PlacementSchedulingContextStatusGetterSetter interface {
	GetPlacementSchedulingContextStatus() *PlacementSchedulingContextStatus
	SetPlacementSchedulingContextStatus(status *PlacementSchedulingContextStatus)
}

// +kubebuilder:object:generate=false
type PlacementSchedulingContextObj interface {
	client.Object
	PlacementSchedulingContextSpecGetterSetter
	PlacementSchedulingContextStatusGetterSetter
}

func init() {
	SchemeBuilder.Register(
		&DynamicResourceClass{}, &DynamicResourceClassList{},
		&DynamicResourceSlice{}, &DynamicResourceSliceList{},
		&ClusterResourcePlacementSchedulingContext{}, &ClusterResourcePlacementSchedulingContextList{},
		&ResourcePlacementSchedulingContext{}, &ResourcePlacementSchedulingContextList{},
	)
}
