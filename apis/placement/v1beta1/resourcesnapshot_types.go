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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	// ResourceIndexLabel is the label that indicate the resource snapshot index of a cluster resource snapshot.
	ResourceIndexLabel = fleetPrefix + "resource-index"

	// ResourceGroupHashAnnotation is the annotation that contains the value of the sha-256 hash
	// value of all the snapshots belong to the same snapshot index.
	ResourceGroupHashAnnotation = fleetPrefix + "resource-hash"

	// NumberOfEnvelopedObjectsAnnotation is the annotation that contains the number of the enveloped objects in the resource snapshot group.
	NumberOfEnvelopedObjectsAnnotation = fleetPrefix + "number-of-enveloped-object"

	// NumberOfResourceSnapshotsAnnotation is the annotation that contains the total number of resource snapshots.
	NumberOfResourceSnapshotsAnnotation = fleetPrefix + "number-of-resource-snapshots"

	// SubindexOfResourceSnapshotAnnotation is the annotation to store the subindex of resource snapshot in the group.
	SubindexOfResourceSnapshotAnnotation = fleetPrefix + "subindex-of-resource-snapshot"

	// ResourceSnapshotNameFmt is resourcePolicySnapshot name format: {CRPName}-{resourceIndex}-snapshot.
	ResourceSnapshotNameFmt = "%s-%d-snapshot"

	// ResourceSnapshotNameWithSubindexFmt is resourcePolicySnapshot name with subindex format: {CRPName}-{resourceIndex}-{subindex}.
	ResourceSnapshotNameWithSubindexFmt = "%s-%d-%d"
)

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope="Cluster",shortName=crs,categories={fleet,fleet-placement}
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:JSONPath=`.metadata.generation`,name="Gen",type=string
// +kubebuilder:printcolumn:JSONPath=`.metadata.creationTimestamp`,name="Age",type=date
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ClusterResourceSnapshot is used to store a snapshot of selected resources by a resource placement policy.
// Its spec is immutable.
// We may need to produce more than one resourceSnapshot for all the resources a ResourcePlacement selected to get around the 1MB size limit of k8s objects.
// We assign an ever-increasing index for each such group of resourceSnapshots.
// The naming convention of a clusterResourceSnapshot is {CRPName}-{resourceIndex}-{subindex}
// where the name of the first snapshot of a group has no subindex part so its name is {CRPName}-{resourceIndex}-snapshot.
// resourceIndex will begin with 0.
// Each snapshot MUST have the following labels:
//   - `CRPTrackingLabel` which points to its owner CRP.
//   - `ResourceIndexLabel` which is the index  of the snapshot group.
//   - `IsLatestSnapshotLabel` which indicates whether the snapshot is the latest one.
//
// All the snapshots within the same index group must have the same ResourceIndexLabel.
//
// The first snapshot of the index group MUST have the following annotations:
//   - `NumberOfResourceSnapshotsAnnotation` to store the total number of resource snapshots in the index group.
//   - `ResourceGroupHashAnnotation` whose value is the sha-256 hash of all the snapshots belong to the same snapshot index.
//
// Each snapshot (excluding the first snapshot) MUST have the following annotations:
//   - `SubindexOfResourceSnapshotAnnotation` to store the subindex of resource snapshot in the group.
type ClusterResourceSnapshot struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// The desired state of ResourceSnapshot.
	// +required
	Spec ResourceSnapshotSpec `json:"spec"`

	// The observed status of ResourceSnapshot.
	// +optional
	Status ResourceSnapshotStatus `json:"status,omitempty"`
}

// ResourceSnapshotSpec	defines the desired state of ResourceSnapshot.
type ResourceSnapshotSpec struct {
	// SelectedResources contains a list of resources selected by ResourceSelectors.
	// +required
	SelectedResources []ResourceContent `json:"selectedResources"`
}

// ResourceContent contains the content of a resource
type ResourceContent struct {
	// +kubebuilder:validation:EmbeddedResource
	// +kubebuilder:pruning:PreserveUnknownFields
	runtime.RawExtension `json:"-,inline"`
}

type ResourceSnapshotStatus struct {
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type

	// Conditions is an array of current observed conditions for ResourceSnapshot.
	// +optional
	Conditions []metav1.Condition `json:"conditions"`
}

// ClusterResourceSnapshotList contains a list of ClusterResourceSnapshot.
// +kubebuilder:resource:scope="Cluster"
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ClusterResourceSnapshotList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterResourceSnapshot `json:"items"`
}

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope="Namespaced",shortName=rs,categories={fleet,fleet-placement}
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:JSONPath=`.metadata.generation`,name="Gen",type=string
// +kubebuilder:printcolumn:JSONPath=`.metadata.creationTimestamp`,name="Age",type=date
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ResourceSnapshot is used to store a snapshot of selected resources by a resource placement policy.
// Its spec is immutable.
// We may need to produce more than one resourceSnapshot for all the resources a ResourcePlacement selected to get around the 1MB size limit of k8s objects.
// We assign an ever-increasing index for each such group of resourceSnapshots.
// The naming convention of a resourceSnapshot is {RPName}-{resourceIndex}-{subindex}
// where the name of the first snapshot of a group has no subindex part so its name is {RPName}-{resourceIndex}-snapshot.
// resourceIndex will begin with 0.
// Each snapshot MUST have the following labels:
//   - `CRPTrackingLabel` which points to its owner resource placement.
//   - `ResourceIndexLabel` which is the index  of the snapshot group.
//   - `IsLatestSnapshotLabel` which indicates whether the snapshot is the latest one.
//
// All the snapshots within the same index group must have the same ResourceIndexLabel.
//
// The first snapshot of the index group MUST have the following annotations:
//   - `NumberOfResourceSnapshotsAnnotation` to store the total number of resource snapshots in the index group.
//   - `ResourceGroupHashAnnotation` whose value is the sha-256 hash of all the snapshots belong to the same snapshot index.
//
// Each snapshot (excluding the first snapshot) MUST have the following annotations:
//   - `SubindexOfResourceSnapshotAnnotation` to store the subindex of resource snapshot in the group.
type ResourceSnapshot struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// The desired state of ResourceSnapshot.
	// +required
	Spec ResourceSnapshotSpec `json:"spec"`

	// The observed status of ResourceSnapshot.
	// +optional
	Status ResourceSnapshotStatus `json:"status,omitempty"`
}

// ResourceSnapshotList contains a list of ResourceSnapshot.
// +kubebuilder:resource:scope="Namespaced"
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ResourceSnapshotList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ResourceSnapshot `json:"items"`
}

// SetConditions sets the conditions for a ClusterResourceSnapshot.
func (m *ClusterResourceSnapshot) SetConditions(conditions ...metav1.Condition) {
	for _, c := range conditions {
		meta.SetStatusCondition(&m.Status.Conditions, c)
	}
}

// GetCondition gets the condition for a ClusterResourceSnapshot.
func (m *ClusterResourceSnapshot) GetCondition(conditionType string) *metav1.Condition {
	return meta.FindStatusCondition(m.Status.Conditions, conditionType)
}

// SetConditions sets the conditions for a ResourceSnapshot.
func (m *ResourceSnapshot) SetConditions(conditions ...metav1.Condition) {
	for _, c := range conditions {
		meta.SetStatusCondition(&m.Status.Conditions, c)
	}
}

// GetCondition gets the condition for a ResourceSnapshot.
func (m *ResourceSnapshot) GetCondition(conditionType string) *metav1.Condition {
	return meta.FindStatusCondition(m.Status.Conditions, conditionType)
}

func init() {
	SchemeBuilder.Register(&ClusterResourceSnapshot{}, &ClusterResourceSnapshotList{})
	SchemeBuilder.Register(&ResourceSnapshot{}, &ResourceSnapshotList{})
}
