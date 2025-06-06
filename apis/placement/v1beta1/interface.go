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

// Package v1beta1 contains API Schema definitions for the fleet placement v1beta1 API group

// +kubebuilder:object:generate=false
// +k8s:deepcopy-gen=package,register
// +groupName=placement.kubernetes-fleet.io
package v1beta1

import "sigs.k8s.io/controller-runtime/pkg/client"

// PlacementSpecGetSetter offers the functionality to work with the placementSpec.
// +kubebuilder:object:generate=false
type PlacementSpecGetSetter interface {
	GetPlacementSpec() *PlacementSpec
	SetPlacementSpec(*PlacementSpec)
}

// PlacementStatusGetSetter offers the functionality to work with the PlacementStatusGetSetter.
// +kubebuilder:object:generate=false
type PlacementStatusGetSetter interface {
	GetPlacementStatus() *PlacementStatus
	SetPlacementStatus(*PlacementStatus)
}

var _ PlacementObj = &ClusterResourcePlacement{}
var _ PlacementObj = &ResourcePlacement{}

// PlacementObj offers the functionality to work with kubernetes resource placement object.
// +kubebuilder:object:generate=false
type PlacementObj interface {
	client.Object
	PlacementSpecGetSetter
	PlacementStatusGetSetter
}

// A BindingSpecGetSetter contains bindingSpec
// +kubebuilder:object:generate=false
type BindingSpecGetSetter interface {
	GetBindingSpec() *ResourceBindingSpec
	SetBindingSpec(*ResourceBindingSpec)
}

// A BindingStatusGetSetter contains bindingStatus
// +kubebuilder:object:generate=false
type BindingStatusGetSetter interface {
	GetBindingStatus() *ResourceBindingStatus
	SetBindingStatus(*ResourceBindingStatus)
}

var _ BindingObj = &ClusterResourceBinding{}
var _ BindingObj = &ResourceBinding{}

// A BindingObj is for kubernetes resource binding object.
// +kubebuilder:object:generate=false
type BindingObj interface {
	client.Object
	BindingSpecGetSetter
	BindingStatusGetSetter
}

// A PolicySnapshotSpecGetSetter contains policy snapshot spec
// +kubebuilder:object:generate=false
type PolicySnapshotSpecGetSetter interface {
	GetPolicySnapshotSpec() *SchedulingPolicySnapshotSpec
	SetPolicySnapshotSpec(*SchedulingPolicySnapshotSpec)
}

// A PolicySnapshotStatusGetSetter contains policy snapshot status
// +kubebuilder:object:generate=false
type PolicySnapshotStatusGetSetter interface {
	GetPolicySnapshotStatus() *SchedulingPolicySnapshotStatus
	SetPolicySnapshotStatus(*SchedulingPolicySnapshotStatus)
}

// A PolicySnapshotObj is for kubernetes policy snapshot object.
// +kubebuilder:object:generate=false
type PolicySnapshotObj interface {
	client.Object
	PolicySnapshotSpecGetSetter
	PolicySnapshotStatusGetSetter
}

// A PolicySnapshotSpec contains policy snapshot spec
// +kubebuilder:object:generate=false
type PolicySnapshotListItemGetter interface {
	GetPolicySnapshotObjs() []PolicySnapshotObj
}

// A PolicySnapshotList is for kubernetes policy snapshot list object.
// +kubebuilder:object:generate=false
type PolicySnapshotList interface {
	client.ObjectList
	PolicySnapshotListItemGetter
}

// A ResourceSnapshotSpecGetSettter contains resource snapshot spec
// +kubebuilder:object:generate=false
type ResourceSnapshotSpecGetSettter interface {
	GetResourceSnapshotSpec() *ResourceSnapshotSpec
	SetResourceSnapshotSpec(*ResourceSnapshotSpec)
}

// A ResourceSnapshotStatusGetSetter contains resource snapshot status
// +kubebuilder:object:generate=false
type ResourceSnapshotStatusGetSetter interface {
	GetResourceSnapshotStatus() *ResourceSnapshotStatus
	SetResourceSnapshotStatus(*ResourceSnapshotStatus)
}

var _ ResourceSnapshotObj = &ClusterResourceSnapshot{}
var _ ResourceSnapshotObj = &ResourceSnapshot{}

// A ResourceSnapshotObj is for kubernetes resource snapshot object.
// +kubebuilder:object:generate=false
type ResourceSnapshotObj interface {
	client.Object
	ResourceSnapshotSpecGetSettter
	ResourceSnapshotStatusGetSetter
}
