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

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	PlacementBindingOwnedByLabelKey               = "experimental.kubefleet.dev/binding-owned-by"
	PlacementBindingCreatedForMigrationRequestKey = "experimental.kubefleet.dev/binding-created-for-migration-request"
	PlacementBindingMigratedFromKey               = "experimental.kubefleet.dev/binding-migrated-from"
)

const (
	PlacementBindingCondTypeSynchronized          = "Synchronized"
	PlacementBindingCondTypeAllResourcesAvailable = "AllResourcesAvailable"
)

// PlacementBinding is the KubeFleet API that binds a placement to a specific member cluster.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope="Namespaced",categories={kubefleet, kubefleet-experimental}
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type PlacementBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// The specification of the binding.
	Spec PlacementBindingSpec `json:"spec,omitempty"`

	// The observed status of the binding.
	Status PlacementBindingStatus `json:"status,omitempty"`
}

type PlacementBindingSpec struct {
	// The name of the placement policy that this binding is associated with.
	//
	// +kubebuilder:validation:Required
	PlacementPolicyName string `json:"placementPolicyName"`

	// The hash of the cluster selector associated with this binding.
	//
	// +kubebuilder:validation:Required
	ClusterSelectorHash string `json:"clusterSelectorHash"`

	// The cluster selector associated with this binding.
	//
	// This field is added for informational purposes only.
	//
	// +kubebuilder:validation:Required
	ClusterSelector ClusterSelector `json:"clusterSelector"`

	// The name of the member cluster that this binding is associated with.
	//
	// +kubebuilder:validation:Required
	ClusterName string `json:"clusterName"`

	// The name of the resource snapshot that this binding is associated with.
	//
	// +kubebuilder:validation:Optional
	ResourceSnapshotName *string `json:"resourceSnapshotName,omitempty"`

	// Whether the binding is suspended. If true, KubeFleet will remove resources
	// from the associated cluster.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=false
	Suspended bool `json:"suspended,omitempty"`
}

type PlacementBindingStatus struct {
	// A list of observed conditions about the binding.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// The name of the last synced resource snapshot.
	LastSyncedResourceSnapshotName *string `json:"lastSyncedResourceSnapshotName,omitempty"`

	// The number of resources that are included in the last synced resource snapshot.
	SelectedResources *int32 `json:"selectedResources,omitempty"`
	// The number of resources that cannot be synced to the target cluster.
	FailedToSyncResources *int32 `json:"failedToSyncResources,omitempty"`
	// The number of resources that are not yet available in the target cluster.
	UnavailableResources *int32 `json:"unavailableResources,omitempty"`
}

// PlacementBindingList contains a list of PlacementBinding.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope="Namespaced"
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type PlacementBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PlacementBinding `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PlacementBinding{}, &PlacementBindingList{})
}
