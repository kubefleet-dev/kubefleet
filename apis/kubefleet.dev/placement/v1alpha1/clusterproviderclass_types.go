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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterClaimApprovalMode decides whether a cluster claim must be approved by hand before a
// provider may act on it.
// +enum
type ClusterClaimApprovalMode string

const (
	// ClusterClaimApprovalModeManual requires an approver to set the Approved condition on each
	// cluster claim of the class before any provider may fulfill it.
	ClusterClaimApprovalModeManual ClusterClaimApprovalMode = "Manual"
	// ClusterClaimApprovalModeAutomatic has KubeFleet approve each cluster claim of the class as it
	// is issued.
	ClusterClaimApprovalModeAutomatic ClusterClaimApprovalMode = "Automatic"
)

// ClusterClaimFailureAction decides what KubeFleet does with a cluster claim that has reached a
// terminal state (failed, expired, or denied).
// +enum
type ClusterClaimFailureAction string

const (
	// ClusterClaimFailureActionHold keeps the terminal cluster claim, as the record of what happened,
	// until the cluster selector or the class of its placement policy changes or an administrator
	// deletes it. No new claim is issued for the selector in the meantime.
	ClusterClaimFailureActionHold ClusterClaimFailureAction = "Hold"
	// ClusterClaimFailureActionRetry withdraws the terminal cluster claim once retryAfter has
	// elapsed and issues a fresh one for the selector. A denied claim is always held regardless.
	ClusterClaimFailureActionRetry ClusterClaimFailureAction = "Retry"
)

// ClusterProviderClass is a KubeFleet API that names the provider which fulfills cluster claims,
// the selector terms that provider understands, and how claims of the class are approved and how
// long they may stand. Placement policies reference a class by name; the class is cluster-scoped
// and owned by the platform administrator, in the way a StorageClass or GatewayClass is.
//
// A class references the platform's own blueprint (such as a Cluster API ClusterClass) through its
// parameters; KubeFleet never interprets them.
//
// Do not delete a class while claims it governs are outstanding: a provider needs the class to
// release a claim it accepted, so such claims wait, finalizer and all, until the class is
// recreated.
//
// +genclient
// +genclient:nonNamespaced
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,categories={kubefleet, kubefleet-placement}
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:JSONPath=`.spec.provisionerName`,name="Provisioner",type=string
// +kubebuilder:printcolumn:JSONPath=`.spec.approval`,name="Approval",type=string
// +kubebuilder:printcolumn:JSONPath=`.metadata.creationTimestamp`,name="Age",type=date
type ClusterProviderClass struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// The specification of the cluster provider class.
	//
	// +kubebuilder:validation:Required
	Spec ClusterProviderClassSpec `json:"spec"`
}

// ClusterProviderClassSpec is the specification of a cluster provider class.
//
// +kubebuilder:validation:XValidation:rule="self.provisionerName == oldSelf.provisionerName",message="the provisionerName field is immutable"
type ClusterProviderClassSpec struct {
	// The name of the provider that fulfills cluster claims of this class, which the provider's
	// fulfiller uses to recognize the claims meant for it. A domain-qualified name, such as
	// capi.kubefleet.dev, is recommended.
	//
	// This field is immutable: in-flight claims are held by the fulfiller that accepted them, and
	// re-pointing a class would orphan them.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	ProvisionerName string `json:"provisionerName"`

	// Opaque parameters passed to the provider with every claim of this class, such as the name of
	// the platform blueprint to provision from. KubeFleet does not interpret them.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxProperties=64
	Parameters map[string]string `json:"parameters,omitempty"`

	// The selector terms the provider understands. A placement policy whose unfulfilled cluster
	// selector uses a key, value, or property outside this vocabulary gets no cluster claim for
	// that selector; the policy reports why. When left unset, no selector term is claimable except
	// a selector with no terms at all.
	//
	// +kubebuilder:validation:Optional
	SelectorVocabulary *SelectorVocabulary `json:"selectorVocabulary,omitempty"`

	// How claims of this class are approved. Defaults to Manual.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=Manual;Automatic
	// +kubebuilder:default=Manual
	Approval ClusterClaimApprovalMode `json:"approval,omitempty"`

	// How long an approved claim may wait for a provider to accept it before KubeFleet marks it
	// expired. Counted from the moment the claim is approved; an unapproved claim never expires.
	// Defaults to 30 minutes.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="duration(self) > duration('0s')",message="must be a positive duration"
	PendingClaimTTL *metav1.Duration `json:"pendingClaimTTL,omitempty"`

	// How long a provider may work on an accepted claim before its transient errors become
	// permanent and the claim fails. Counted from acceptance. Defaults to 60 minutes.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="duration(self) > duration('0s')",message="must be a positive duration"
	MaxProvisionDuration *metav1.Duration `json:"maxProvisionDuration,omitempty"`

	// How long a fulfilled claim may wait for its cluster to become eligible for scheduling before
	// KubeFleet marks it expired; the provider then deprovisions the cluster if it never joined.
	// Counted from completion. Defaults to 30 minutes.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="duration(self) > duration('0s')",message="must be a positive duration"
	JoinTimeout *metav1.Duration `json:"joinTimeout,omitempty"`

	// What KubeFleet does with a claim of this class once it has failed or expired. Defaults to
	// Hold.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=Hold;Retry
	// +kubebuilder:default=Hold
	OnFailure ClusterClaimFailureAction `json:"onFailure,omitempty"`

	// How long a failed or expired claim is kept before it is withdrawn and a fresh claim is
	// issued, when onFailure is Retry. Defaults to 15 minutes.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="duration(self) > duration('0s')",message="must be a positive duration"
	RetryAfter *metav1.Duration `json:"retryAfter,omitempty"`
}

// SelectorVocabulary lists the selector terms a provider understands.
type SelectorVocabulary struct {
	// The label keys a cluster selector may match on, each optionally restricted to a set of
	// values.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=64
	// +listType=map
	// +listMapKey=key
	LabelKeys []LabelKeyRule `json:"labelKeys,omitempty"`

	// The cluster property keys a cluster selector may use in a property expression, each
	// optionally bounded.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=64
	// +listType=map
	// +listMapKey=key
	PropertyKeys []PropertyKeyRule `json:"propertyKeys,omitempty"`
}

// LabelKeyRule admits a label key, and optionally only some of its values, into a provider's
// vocabulary.
type LabelKeyRule struct {
	// The label key.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=317
	Key string `json:"key"`

	// The values the provider accepts for the key. When empty, any value is accepted.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=64
	Values []string `json:"values,omitempty"`
}

// PropertyKeyRule admits a cluster property key, optionally within bounds, into a provider's
// vocabulary.
type PropertyKeyRule struct {
	// The property key, e.g. kubernetes-fleet.io/node-count or
	// resources.kubernetes-fleet.io/allocatable-cpu.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=317
	Key string `json:"key"`

	// The smallest value, as a Kubernetes quantity, the provider can deliver for the property.
	//
	// +kubebuilder:validation:Optional
	Min *string `json:"min,omitempty"`

	// The largest value, as a Kubernetes quantity, the provider can deliver for the property.
	//
	// +kubebuilder:validation:Optional
	Max *string `json:"max,omitempty"`
}

// ClusterProviderClassList contains a list of ClusterProviderClass.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope="Cluster"
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ClusterProviderClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterProviderClass `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterProviderClass{}, &ClusterProviderClassList{})
}
