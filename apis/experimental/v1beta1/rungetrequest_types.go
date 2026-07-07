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
	RunGetRequestCondTypeCompleted = "Completed"

	RunGetRequestCompletedCondReasonSucceeded = "Succeeded"
	RunGetRequestCompletedCondReasonFailed    = "Failed"
)

// RunGetRequest is a KubeFleet API that allows users to run a GET request
// on a member cluster.
//
// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={kubefleet, kubefleet-experimental}
// +kubebuilder:storageversion
type RunGetRequest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// The spec of a RunGetRequest object.
	Spec RunGetRequestSpec `json:"spec,omitempty"`

	// The status of a RunGetRequest object.
	Status RunGetRequestStatus `json:"status,omitempty"`
}

type RunGetRequestSpec struct {
	// The API group of the resource to get.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=""
	APIGroup string `json:"apiGroup,omitempty"`

	// The API version of the resource to get.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default="v1"
	APIVersion string `json:"apiVersion,omitempty"`

	// The resource to get.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=pods
	// +kubebuilder:default=pods
	Resource string `json:"resource"`

	// The subresource to get.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=""
	// +kubebuilder:validation:Enum=logs;""
	SubResource string `json:"subResource,omitempty"`

	// The namespace of the resource to get.
	//
	// +kubebuilder:validation:Optional
	Namespace string `json:"namespace,omitempty"`

	// The name of the resource to get.
	//
	// +kubebuilder:validation:Optional
	Name string `json:"name"`

	// A list of label matchers to select the resource to get.
	//
	// +kubebuilder:validation:Optional
	LabelMatchers map[string]string `json:"labelMatchers,omitempty"`
}

type RunGetRequestStatus struct {
	Result []byte `json:"result,omitempty"`

	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// RunGetRequestList contains a list of RunGetRequest.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope="Namespaced"
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type RunGetRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RunGetRequest `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RunGetRequest{}, &RunGetRequestList{})
}
