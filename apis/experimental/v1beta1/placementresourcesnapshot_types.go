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
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	ResourceSnapshotOwnedByLabelKey  = "experimental.kubefleet.dev/resource-snapshot-owned-by"
	ResourceSnapshotRevisionLabelKey = "experimental.kubefleet.dev/resource-snapshot-revision"
)

const (
	PlacementResourceSnapshotRequestCondTypeCompleted = "Completed"

	PlacementResourceSnapshotRequestCompletedReasonSuccess                 = "Success"
	PlacementResourceSnapshotRequestCompletedReasonErred                   = "Erred"
	PlacementResourceSnapshotRequestCompletedReasonPlacementPolicyNotFound = "PlacementPolicyNotFound"
)

// PlacementResourceSnapshot is the KubeFleet API that captures the resources
// (e.g., pod templates, configmaps, secrets, etc.) selected by a placement policy as seen on the hub cluster
// at a specific point in time. It can be referenced by other KubeFleet APIs
// to enable consistent rollouts of resources across multiple member clusters in the fleet.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,categories={kubefleet, kubefleet-experimental}
// +kubebuilder:storageversion
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type PlacementResourceSnapshot struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// The spec of a placement resource snapshot.
	Spec PlacementResourceSnapshotSpec `json:"spec,omitempty"`
}

type PlacementResourceSnapshotSpec struct {
	// The manifests of the resources selected by owner placement policy at the time of snapshot creation.
	// +kubebuilder:validation:Optional
	Resources []ResourceContent `json:"resources,omitempty"`
}

type ResourceContent struct {
	// The identifier of the resource.
	// +kubebuilder:validation:Required
	Identifier SameNamespacedObjectReference `json:"identifier,omitempty"`

	// The manifest of the resource. It should be a Kubernetes object in YAML or JSON format.
	// +kubebuilder:validation:Required
	Manifest runtime.RawExtension `json:"manifest,omitempty"`

	// The name of the data bundle that is associated with the resource, if any.
	// +kubebuilder:validation:Optional
	DataBundleName *string `json:"dataBundleName,omitempty"`
}

// PlacementResourceSnapshotList is a list of PlacementResourceSnapshot objects.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,categories={kubefleet, kubefleet-experimental}
// +kubebuilder:storageversion
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type PlacementResourceSnapshotList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []PlacementResourceSnapshot `json:"items"`
}

// PlacementResourceSnapshotRequest is the KubeFleet API that represents a request to
// create a PlacementResourceSnapshot, or in other words, a new snapshot of resources
// for a placement policy.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,categories={kubefleet, kubefleet-experimental}
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type PlacementResourceSnapshotRequest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// The spec of a placement resource snapshot request.
	Spec PlacementResourceSnapshotRequestSpec `json:"spec,omitempty"`

	// The status of a placement resource snapshot request.
	Status PlacementResourceSnapshotRequestStatus `json:"status,omitempty"`
}

type PlacementResourceSnapshotRequestSpec struct {
	// The reference to the placement policy for which a resource snapshot is requested.
	// +kubebuilder:validation:Required
	PlacementPolicyRef SameNamespacedObjectReference `json:"placementPolicyRef,omitempty"`

	// The TTL (time to live) of the request. KubeFleet will GC old requests that have outlived their TTL.
	//
	// The default is 10 minutes (600 seconds).
	//
	// Demo-only note: the field is not in use at the moment. No requests will be GC'd in the demo.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=600
	// +kubebuilder:validation:Minimum=60
	// +kubebuilder:validation:Maximum=86400
	TTLSeconds *int32 `json:"ttl,omitempty"`
}

type PlacementResourceSnapshotRequestStatus struct {
	// A list of observed conditions of this placement resource snapshot request.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// The name of the PlacementResourceSnapshot object created for this request.
	PlacementResourceSnapshotName string `json:"placementResourceSnapshotName,omitempty"`
}

// PlacementResourceSnapshotRequestList is a list of PlacementResourceSnapshotRequest objects.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,categories={kubefleet, kubefleet-experimental}
// +kubebuilder:storageversion
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type PlacementResourceSnapshotRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	// A list of placement resource snapshot requests.
	Items []PlacementResourceSnapshotRequest `json:"items"`
}

func init() {
	SchemeBuilder.Register(
		&PlacementResourceSnapshot{}, &PlacementResourceSnapshotList{},
		&PlacementResourceSnapshotRequest{}, &PlacementResourceSnapshotRequestList{},
	)
}
