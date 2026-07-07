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
	ClusterRequestManagedByLabelKey = "experimental.kubefleet.dev/cluster-request-managed-by"
)

const (
	ClusterRequestCondTypeCompleted = "Completed"
	ClusterRequestCondTypeStarted   = "Started"
)

// ClusterRequest is a KubeFleet API that represents a request for a member cluster to be provisioned.
// It is created by KubeFleet when it cannot find any existing member cluster that matches the placement
// requirements specified in a WorkloadPlacement object.
//
// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={kubefleet, kubefleet-experimental}
// +kubebuilder:storageversion
type ClusterRequest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// The specification of the cluster request.
	// +kubebuilder:validation:Optional
	Spec ClusterRequestSpec `json:"spec,omitempty"`

	// The observed status of the cluster request.
	// +kubebuilder:validation:Optional
	Status ClusterRequestStatus `json:"status,omitempty"`
}

type ClusterRequestSpec struct {
	// The cluster selector the describes the requirements for the new member cluster to provision.
	//
	// If not specified, any member cluster can satisfy the request.
	//
	// This field is immutable after creation.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="the clusterSelector field is immutable"
	ClusterSelector ClusterSelector `json:"clusterSelector,omitempty"`

	// The hash of the cluster selector that describes the requirements for the new member cluster to provision.
	//
	// This field is immutable after creation.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="the clusterSelectorHash field is immutable"
	ClusterSelectorHash string `json:"clusterSelectorHash"`
}

type ClusterRequestStatus struct {
	// A list of observed conditions of the cluster request.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// The name of the cluster that has been provisioned for this cluster request, if any.
	// +kubebuilder:validation:Optional
	ProvisionedClusterName *string `json:"provisionedClusterName,omitempty"`

	// The latest observed creation timestamp across all the member clusters. This field is used
	// as a expedient solution to verify if a cluster request is still valid for consideration, i.e.,
	// if the current latest observed cluster creation timestamp is later than this timestamp in the
	// the status, a new member cluster must have been created after the cluster request was created,
	// and thus the cluster request should be considered stale and can be ignored.
	// +kubebuilder:validation:Optional
	LatestObservedClusterCreationTimestamp *metav1.Time `json:"latestObservedClusterCreationTimestamp,omitempty"`
}

// ClusterRequestList contains a list of ClusterRequest.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope="Cluster"
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ClusterRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []ClusterRequest `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterRequest{}, &ClusterRequestList{})
}
