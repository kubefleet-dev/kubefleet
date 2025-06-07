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
