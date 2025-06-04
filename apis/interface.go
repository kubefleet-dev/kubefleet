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

// Package apis contains API interfaces for the fleet API group.
package apis

import (
	"github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// A Conditioned may have conditions set or retrieved. Conditions typically
// indicate the status of both a resource and its reconciliation process.
// +kubebuilder:object:generate=false
type Conditioned interface {
	SetConditions(...metav1.Condition)
	GetCondition(string) *metav1.Condition
}

// A ConditionedObj is for kubernetes resource with conditions.
// +kubebuilder:object:generate=false
type ConditionedObj interface {
	client.Object
	Conditioned
}

// A PlacementSpec contains placementSpec
// +kubebuilder:object:generate=false
type PlacementSpec interface {
	GetPlacementSpec() *v1beta1.PlacementSpec
	SetPlacementSpec(*v1beta1.PlacementSpec)
}

// A PlacementStatus contains placementStatus
// +kubebuilder:object:generate=false
type PlacementStatus interface {
	GetPlacementStatus() *v1beta1.PlacementStatus
	SetPlacementStatus(*v1beta1.PlacementStatus)
}

// A PlacementObj is for kubernetes resource placement object.
// +kubebuilder:object:generate=false
type PlacementObj interface {
	client.Object
	PlacementSpec
	PlacementStatus
}

// A BindingSpec contains bindingSpec
// +kubebuilder:object:generate=false
type BindingSpec interface {
	GetBindingSpec() *v1beta1.ResourceBindingSpec
	SetBindingSpec(*v1beta1.ResourceBindingSpec)
}

// A BindingStatus contains bindingStatus
// +kubebuilder:object:generate=false
type BindingStatus interface {
	GetBindingStatus() *v1beta1.ResourceBindingStatus
	SetBindingStatus(*v1beta1.ResourceBindingStatus)
}

// A BindingObj is for kubernetes resource binding object.
// +kubebuilder:object:generate=false
type BindingObj interface {
	client.Object
	BindingSpec
	BindingStatus
}

// A PolicySnapshotSpec contains policy snapshot spec
// +kubebuilder:object:generate=false
type PolicySnapshotSpec interface {
	GetPolicySnapshotSpec() *v1beta1.SchedulingPolicySnapshotSpec
	SetPolicySnapshotSpec(*v1beta1.SchedulingPolicySnapshotSpec)
}

// A PolicySnapshotStatus contains policy snapshot status
// +kubebuilder:object:generate=false
type PolicySnapshotStatus interface {
	GetPolicySnapshotStatus() *v1beta1.SchedulingPolicySnapshotStatus
	SetPolicySnapshotStatus(*v1beta1.SchedulingPolicySnapshotStatus)
}

// A PolicySnapshotObj is for kubernetes policy snapshot object.
// +kubebuilder:object:generate=false
type PolicySnapshotObj interface {
	client.Object
	PolicySnapshotSpec
	PolicySnapshotStatus
}

// A ResourceSnapshotSpec contains resource snapshot spec
// +kubebuilder:object:generate=false
type ResourceSnapshotSpec interface {
	GetResourceSnapshotSpec() *v1beta1.ResourceSnapshotSpec
	SetResourceSnapshotSpec(*v1beta1.ResourceSnapshotSpec)
}

// A ResourceSnapshotStatus contains resource snapshot status
// +kubebuilder:object:generate=false
type ResourceSnapshotStatus interface {
	GetResourceSnapshotStatus() *v1beta1.ResourceSnapshotStatus
	SetResourceSnapshotStatus(*v1beta1.ResourceSnapshotStatus)
}

// A ResourceSnapshotObj is for kubernetes resource snapshot object.
// +kubebuilder:object:generate=false
type ResourceSnapshotObj interface {
	client.Object
	ResourceSnapshotSpec
	ResourceSnapshotStatus
}
