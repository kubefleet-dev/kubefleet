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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MemberClusterProperties is a collection of cluster properties observed for a member cluster.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type MemberClusterProperties struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	// Properties is a collection of non-resource properties observed for the member cluster.
	// +optional
	Properties map[PropertyName]PropertyValue `json:"properties,omitempty" protobuf:"bytes,2,opt,name=properties"`

	// The current observed resource usage of the member cluster.
	// +optional
	ResourceUsage ResourceUsage `json:"resourceUsage,omitempty" protobuf:"bytes,3,opt,name=resourceUsage"`
}

// PropertyName is the name of a cluster property.
//
// A valid name should be a string of one or more segments, separated by slashes (/) if applicable.
//
// Each segment must be 63 characters or less, start and end with an alphanumeric character,
// and can include dashes (-), underscores (_), dots (.), and alphanumerics in between.
//
// Optionally, the property name can have a prefix, which must be a DNS subdomain up to 253 characters,
// followed by a slash (/).
//
// Examples include:
// - "avg-resource-pressure"
// - "kubernetes-fleet.io/node-count"
// - "kubernetes.azure.com/vm-sizes/Standard_D2s_v3/count"
type PropertyName string

// PropertyValue is the value of a cluster property.
type PropertyValue struct {
	// Value is the value of the cluster property.
	//
	// Currently, it should be a valid Kubernetes quantity.
	// For more information, see
	// https://pkg.go.dev/k8s.io/apimachinery/pkg/api/resource#Quantity.
	//
	// +required
	Value string `json:"value"`

	// ObservationTime is when the cluster property is observed.
	// +required
	ObservationTime metav1.Time `json:"observationTime"`
}

// ResourceUsage contains the observed resource usage of a member cluster.
type ResourceUsage struct {
	// Capacity represents the total resource capacity of all the nodes on a member cluster.
	//
	// A node's total capacity is the amount of resource installed on the node.
	// +optional
	Capacity corev1.ResourceList `json:"capacity,omitempty"`

	// Allocatable represents the total allocatable resources of all the nodes on a member cluster.
	//
	// A node's allocatable capacity is the amount of resource that can actually be used
	// for user workloads, i.e.,
	// allocatable capacity = total capacity - capacities reserved for the OS, kubelet, etc.
	//
	// For more information, see
	// https://kubernetes.io/docs/tasks/administer-cluster/reserve-compute-resources/.
	// +optional
	Allocatable corev1.ResourceList `json:"allocatable,omitempty"`

	// Available represents the total available resources of all the nodes on a member cluster.
	//
	// A node's available capacity is the amount of resource that has not been used yet, i.e.,
	// available capacity = allocatable capacity - capacity that has been requested by workloads.
	//
	// This field is beta-level; it is for the property-based scheduling feature and is only
	// populated when a property provider is enabled in the deployment.
	// +optional
	Available corev1.ResourceList `json:"available,omitempty"`

	// When the resource usage is observed.
	// +optional
	ObservationTime metav1.Time `json:"observationTime,omitempty"`
}

// MemberClusterPropertiesList contains a list of MemberClusterProperties.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type MemberClusterPropertiesList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []MemberClusterProperties `json:"items"`
}
