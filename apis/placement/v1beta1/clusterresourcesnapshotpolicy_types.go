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

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope="Cluster",shortName=crsp,categories={fleet,fleet-placement}
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:JSONPath=`.spec.updateStrategy`,name="Strategy",type=string
// +kubebuilder:printcolumn:JSONPath=`.spec.targetSnapshot`,name="Target-Snapshot",type=string
// +kubebuilder:printcolumn:JSONPath=`.status.currentSnapshot`,name="Current-Snapshot",type=string
// +kubebuilder:printcolumn:JSONPath=`.metadata.creationTimestamp`,name="Age",type=date
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ClusterResourceSnapshotPolicy controls the snapshot creation and selection strategy
// for a ClusterResourcePlacement. It allows users to decouple snapshot management
// from the CRP itself, providing more granular control over when snapshots are created.
//
// The name of this object must match the name of the corresponding ClusterResourcePlacement.
type ClusterResourceSnapshotPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the desired snapshot policy.
	// +kubebuilder:validation:Required
	Spec ResourceSnapshotPolicySpec `json:"spec"`

	// Status reports the observed state of the snapshot policy.
	// +kubebuilder:validation:Optional
	Status ResourceSnapshotPolicyStatus `json:"status,omitempty"`
}

// ResourceSnapshotPolicySpec defines the desired snapshot management behavior.
type ResourceSnapshotPolicySpec struct {
	// UpdateStrategy controls when new resource snapshots are created.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Automatic;OnDemand
	CreationStrategy SnapshotCreationStrategyType `json:"createStrategy"`

	// ResourceSnapshotIndex specifies which snapshot index to use for creation.
	// Only valid when UpdateStrategy is "OnDemand".
	// The requested index must be greater than the latest existing snapshot index if exists.
	// +kubebuilder:validation:Optional
	ResourceSnapshotIndex *uint32 `json:"resourceSnapshotIndex,omitempty"`

	// SnapshotRetentionPolicy controls how snapshots are retained.
	// If not specified, inherits from CRP's RevisionHistoryLimit.
	// +kubebuilder:validation:Optional
	SnapshotRetentionPolicy *SnapshotRetentionPolicy `json:"snapshotRetentionPolicy,omitempty"`
}

// SnapshotUpdateStrategyType describes when new snapshots are created.
// +enum
type SnapshotCreationStrategyType string

const (
	// SnapshotUpdateStrategyAutomatic automatically creates new snapshots when selected
	// resources change. This is the default behavior when no policy exists.
	SnapshotUpdateStrategyAutomatic SnapshotCreationStrategyType = "Automatic"

	// SnapshotUpdateStrategyOnDemand creates snapshots only when explicitly requested by the user.
	SnapshotUpdateStrategyOnDemand SnapshotCreationStrategyType = "OnDemand"
)

// SnapshotRetentionPolicy controls snapshot retention behavior.
type SnapshotRetentionPolicy struct {
	// The number of old ResourceSnapshot resources to retain.
	// Defaults to 10.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1000
	// +kubebuilder:default=10
	// +kubebuilder:validation:Optional
	// RevisionHistoryLimit wins over CRP's RevisionHistoryLimit if both are set.
	RevisionHistoryLimit *int32 `json:"revisionHistoryLimit,omitempty"`
}

// ResourceSnapshotPolicyStatus reports the observed state.
type ResourceSnapshotPolicyStatus struct {
	// LatestSnapshot is the most recently created snapshot index.
	// +kubebuilder:validation:Optional
	LatestSnapshot *SnapshotGroup `json:"latestSnapshot,omitempty"`

	// Conditions represents the current state of the snapshot policy.
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ResourceSnapshotPolicyConditionType defines a specific condition of a resource snapshot policy object.
// +enum
type ResourceSnapshotPolicyConditionType string

const (
	// ResourceSnapshotPolicyConditionTypeValid indicates whether the policy configuration is valid.
	// This condition will be set when the strategy type is onDemand.
	ResourceSnapshotPolicyConditionTypeValid ResourceSnapshotPolicyConditionType = "Valid"

	// ResourceSnapshotPolicyConditionTypeSnapshotReady indicates whether the target snapshot is ready.
	ResourceSnapshotPolicyConditionTypeSnapshotReady ResourceSnapshotPolicyConditionType = "SnapshotReady"
)

// SnapshotGroup contains metadata about a group of snapshots using the same index.
type SnapshotGroup struct {
	// Index is the snapshot index.
	// +kubebuilder:validation:Optional
	Index string `json:"index,omitempty"`

	// NumberOfSnapshots is the count of snapshots in this group.
	// +kubebuilder:validation:Optional
	NumberOfSnapshots int32 `json:"numberOfSnapshots,omitempty"`
}
