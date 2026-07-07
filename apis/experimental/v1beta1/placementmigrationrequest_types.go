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
)

const (
	PlacementMigrationRequestCondTypeInitialized = "Initialized"

	PlacementMigrationRequestCondTypeCompleted             = "Completed"
	PlacementMigrationRequestCompletedCondReasonSucceeded  = "Succeeded"
	PlacementMigrationRequestCompletedCondReasonFailed     = "Failed"
	PlacementMigrationRequestCompletedCondReasonInProgress = "InProgress"

	PlacementMigrationRequestCondTypeRolledBack             = "RolledBack"
	PlacementMigrationRequestRolledBackCondReasonSucceeded  = "Succeeded"
	PlacementMigrationRequestRolledBackCondReasonFailed     = "Failed"
	PlacementMigrationRequestRolledBackCondReasonInProgress = "InProgress"
)

const (
	PlacementMigrationAttemptCondTypeCompleted            = "Completed"
	PlacementMigrationAttemptCompletedCondReasonSucceeded = "Succeeded"
	PlacementMigrationAttemptCompletedCondReasonFailed    = "Failed"
	PlacementMigrationAttemptCompletedCondReasonSkipped   = "Skipped"

	PlacementMigrationAttemptCondTypeRolledBack            = "RolledBack"
	PlacementMigrationAttemptRolledBackCondReasonSucceeded = "Succeeded"
	PlacementMigrationAttemptRolledBackCondReasonFailed    = "Failed"
	PlacementMigrationAttemptRolledBackCondReasonSkipped   = "Skipped"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories={kubefleet, kubefleet-experimental}
// +kubebuilder:storageversion
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type PlacementMigrationRequest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// The spec of a placement migration request.
	Spec PlacementMigrationRequestSpec `json:"spec,omitempty"`

	// The status of a placement migration request.
	Status PlacementMigrationRequestStatus `json:"status,omitempty"`
}

// PlacementMigrationRequestSpec is the spec of a placement migration request.
type PlacementMigrationRequestSpec struct {
	// A list of label matchers that select the placements to migrate; KubeFleet will migrate
	// them if they are placed on the from clusters as selected by the fromClusterSelectors field
	// below.
	//
	// The label matchers are ORed. A placement will be migrated if it matches any of the label matchers in this list.
	//
	// If the list is empty, all placements on the from cluster are selected. This is best used to
	// migrate all placements from one cluster to another, so as to, for example, decommission a cluster.
	//
	// This field is immutable after creation.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=5
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="the placementPolicySelectors field is immutable"
	PlacementPolicySelectors []map[string]string `json:"placementPolicySelectors,omitempty"`

	// A label matcher that select the clusters to migrate placements from. For each selected from
	// cluster, KubeFleet will migrate the placements, as selected by the placementPolicySelectors field above,
	// to a corresponding to cluster, as selected by the toClusterSelectors field below. KubeFleet
	// may request to create a new cluster if an appropriate target to cluster cannot be found.
	//
	// This field is immutable after creation.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="the fromClusterSelector field is immutable"
	FromClusterSelector map[string]string `json:"fromClusterSelector,omitempty"`

	// A cluster selector that selects the clusters to which placements are migrated. KubeFleet may
	// use this selector to request a new cluster if an appropriate to cluster cannot be found.
	//
	// If the selector is unset, any cluster can be a target.
	//
	// This field is immutable after creation.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="the toClusterSelector field is immutable"
	ToClusterSelector ClusterSelector `json:"toClusterSelector,omitempty"`

	// How KubeFleet should handle this migration.
	//
	// The available options are:
	// * SurgeFirst: for each placement to migrate, KubeFleet will add the placement first to a to cluster,
	//   wait until it becomes available, then remove it from the from cluster. If a to cluster cannot
	//   be found, KubeFleet will send a cluster request and wait until a to cluster becomes available before
	//   proceeding with the migration; during this period the placement will continue to be active on the from cluster.
	// * DrainFirst: for each placement to migrate, KubeFleet will first remove the placement from the from
	//   cluster, then add it to a to cluster. If a to cluster cannot be found, KubeFleet will send a cluster
	//   request; during this period the from cluster will not have the placement anymore.
	//
	// The default mode is SurgeFirst.
	//
	// This field is immutable after creation.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=SurgeFirst;DrainFirst
	// +kubebuilder:default=SurgeFirst
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="the mode field is immutable"
	Mode string `json:"mode,omitempty"`

	// The maximum number of placements to migrate in parallel.
	//
	// The default value is 1.
	//
	// This field is immutable after creation.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=1
	// +kubebuilder:validation:Maximum=10
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="the maxConcurrency field is immutable"
	MaxConcurrency *int32 `json:"maxConcurrency,omitempty"`

	// Whether to cancel the migration and roll back the changes. If set to true, KubeFleet will move placements
	// back from the to clusters to the from clusters. Once the rollback starts, it cannot be
	// cancelled; to start the migration again, delete and re-create a placement migration request as
	// needed.
	//
	// During the rollback process, KubeFleet will use the same mode and maxConcurrency settings as
	// specified in this request; however, the failure policy will cease to apply. KubeFleet will
	// move back as many placements as possible, even if the from clusters fail to run them
	// for some reason.
	//
	// The field always starts with false and can only be set to true once. After that it cannot be changed anymore.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=false
	// +kubebuilder:validation:XValidation:rule="!oldSelf || self",message="the rollback field cannot be reset to false once set to true"
	Rollback bool `json:"rollback,omitempty"`

	// The failure policy of this migration request. It helps KubeFleet to stop the migration
	// when there are too many failures, so as to keep the impact radius within control.
	//
	// This field is immutable after creation.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="the failurePolicy field is immutable"
	FailurePolicy PlacementMigrationFailurePolicy `json:"failurePolicy,omitempty"`
}

type PlacementMigrationFailurePolicy struct {
	// The maximum number of placements that have failed to be migrated before KubeFleet stops the migration.
	//
	// If this threshold is reached, KubeFleet will suspend the migration process. Delete the request
	// to accept the current state, or roll back the request to restore to the original state.
	//
	// The default value is 1.
	//
	// This field is immutable after creation.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="the maxFailureCount field is immutable"
	MaxFailureCount int32 `json:"maxFailureCount,omitempty"`

	// The maximum time in minutes that KubeFleet will wait for a placement to migrate. If the time limit
	// is reached, KubeFleet will consider the placement to have failed the migration.
	//
	// Note that setting this field to be of a value that is too low may lead to unexpected failures.
	//
	// The default value is 30 minutes.
	//
	// This field is immutable after creation.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=5
	// +kubebuilder:default=30
	// +kubebuilder:validation:Maximum=1440
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="the maxWaitTimePerPlacementMinutes field is immutable"
	MaxWaitTimePerPlacementMinutes int32 `json:"maxWaitTimePerPlacementMinutes,omitempty"`
}

type PlacementMigrationRequestStatus struct {
	// A list of placements that KubeFleet will migrate given the migration request, plus their individual migration status.
	//
	// +kubebuilder:validation:Optional
	PlacementsToMigrate []PerPlacementMigrationStatus `json:"placementsToMigrate,omitempty"`

	// A list of observed conditions of this placement migration request.
	//
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type PerPlacementMigrationStatus struct {
	// The reference to the placement binding involved in this migration attempt.
	PlacementBindingRef CrossNamespaceObjectReference `json:"placementBindingRef,omitempty"`

	// The reference to the placement policy involved in this migration attempt.
	PlacementPolicyRef CrossNamespaceObjectReference `json:"placementPolicyRef,omitempty"`

	// The from cluster of the migration.
	FromClusterName string `json:"fromClusterName,omitempty"`

	// The to cluster of the migration. If unset, the to cluster is not determined yet,
	// and KubeFleet may have to request a new cluster to fulfill the migration attempt.
	ToClusterName *string `json:"toClusterName,omitempty"`

	// The cluster request created for the migration, if any.
	ToClusterRequestName *string `json:"toClusterRequestName,omitempty"`

	// A list of observed conditions of the migration.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// PlacementMigrationRequestList contains a list of PlacementMigrationRequest objects.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope="Cluster"
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type PlacementMigrationRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []PlacementMigrationRequest `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PlacementMigrationRequest{}, &PlacementMigrationRequestList{})
}
