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
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	PlacementPolicyCondTypeScheduled    = "Scheduled"
	PlacementPolicyCondTypeSynchronized = "Synchronized"
	PlacementPolicyCondTypeAvailable    = "Available"
)

// PlacementPolicy is a KubeFleet API that allows users to place workloads across
// member clusters.
//
// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={kubefleet, kubefleet-experimental}
// +kubebuilder:storageversion
type PlacementPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// The specification of the placement policy.
	// +kubebuilder:validation:Required
	Spec PlacementPolicySpec `json:"spec,omitempty"`

	// The observed status of the placement policy.
	// +kubebuilder:validation:Optional
	Status PlacementPolicyStatus `json:"status,omitempty"`
}

type PlacementPolicySpec struct {
	// A list of cluster selectors that specifies the target clusters where KubeFleet should place
	// the resources. A cluster selector consists of a list of label and cluster property selectors
	// and a count; and for each cluster selector, KubeFleet will pick `count` number of clusters
	// that match the given selectors for placing the resources.
	//
	// If not specified, KubeFleet will place the resources to all available member clusters.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=5
	ClusterSelectors []ClusterSelector `json:"clusterSelectors,omitempty"`

	// A list of resource selectors that specifies the resources that KubeFleet should place across
	// the target clusters.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=20
	ResourceSelectors []SameNamespacedObjectReference `json:"resourceSelectors,omitempty"`

	// The resource revision history limit for this placement policy.
	//
	// KubeFleet will snapshot the resources selected by a placement policy; when the resources are
	// updated and a rollout is triggered on the placement policy, KubeFleet will create a new
	// revision of the selected resources (in the form of a resource snapshot), which tracks the state of
	// the selected resources at that point of time. These revisions are kept for auditing and
	// failure recovery purposes; one can inspect them to see the past state of the selected resources,
	// or roll back to a previous revision if the latest revision is not working as expected.
	//
	// It is also possible to manually request a new resource revision to be created.
	//
	// The default value is 3.
	//
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=20
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=3
	ResourceRevisionHistoryLimit *int32 `json:"resourceRevisionHistoryLimit,omitempty"`

	// The strategy that KubeFleet uses to synchronize the selected resources to the target clusters.
	// Set the strategy to configure how selected resources are applied to a target cluster, how to handle
	// drifts/conflicts when applying the resources, what to do with placed resources when the placement policy
	// is deleted, and many more.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="false",message="syncStrategy is not supported and must not be set"
	SyncStrategy *SyncStrategy `json:"syncStrategy,omitempty"`

	// The tolerations which allows KubeFleet to synchronize selected resources to tainted target
	// clusters.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="size(self) == 0",message="tolerations are not supported and must be empty"
	Tolerations []Toleration `json:"tolerations,omitempty"`
}

type ClusterSelector struct {
	// A list of terms that form the selector. The terms are ORed, i.e., a cluster would match the selector
	// if it matches any of the terms.
	//
	// If not specified, the selector will match all clusters.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=5
	// +kubebuilder:validation:XValidation:rule="size(self) <= 1 && (size(self) == 0 || (!has(self[0].matchLabelExpressions) && !has(self[0].matchClusterPropertyExpressions)))",message="terms must contain at most one item and only matchLabels may be set"
	Terms []LabelAndClusterPropertySelectorTerm `json:"terms,omitempty"`

	// The number of clusters that KubeFleet should select from the clusters that match the given selectors.
	//
	// The default value is 1. To select all clusters that match the given selectors, use the value "All".
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:XIntOrString
	// +kubebuilder:validation:Pattern="^([1-9][0-9]{0,2}|All)$"
	// +kubebuilder:validation:XValidation:rule="(type(self) == int && self == 1) || (type(self) == string && self == '1')",message="count must be unset or set to 1"
	Count *intstr.IntOrString `json:"count,omitempty"`
}

type LabelAndClusterPropertySelectorTerm struct {
	// One can mix and match `MatchLabels`, `MatchLabelExpressions`, and `MatchClusterPropertyExpressions`
	// in a selector term as needed. The requirements/constraints will be ANDed.
	//
	// If none of the fields are specified, the selector term will match all clusters.

	// A list of label key-value pairs that a cluster must have to match this selector term.
	//
	// +kubebuilder:validation:Optional
	MatchLabels map[string]string `json:"matchLabels,omitempty"`

	// A list of label expressions that a cluster must all satisfy to match this selector term.
	//
	// +kubebuilder:validation:Optional
	MatchLabelExpressions []LabelClusterPropertyExpression `json:"matchLabelExpressions,omitempty"`

	// A list of cluster property expressions that a cluster must all satisfy to match this selector term.
	//
	// +kubebuilder:validation:Optional
	MatchClusterPropertyExpressions []LabelClusterPropertyExpression `json:"matchClusterPropertyExpressions,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="(self.operator == 'In' || self.operator == 'NotIn') ? (has(self.values) && size(self.values) > 0) : true",message="values must be non-empty when operator is In or NotIn"
// +kubebuilder:validation:XValidation:rule="(self.operator == 'Exists' || self.operator == 'DoesNotExist') ? (!has(self.values) || size(self.values) == 0) : true",message="values must be empty when operator is Exists or DoesNotExist"
// +kubebuilder:validation:XValidation:rule="(self.operator == 'Gt' || self.operator == 'Lt' || self.operator == 'Ge' || self.operator == 'Le' || self.operator == 'Eq' || self.operator == 'Ne') ? (has(self.values) && size(self.values) == 1) : true",message="values must contain exactly one element when operator is Gt, Lt, Ge, Le, Eq, or Ne"
type LabelClusterPropertyExpression struct {
	// The key of the label or cluster property that selector applies to.
	// +kubebuilder:validation:Required
	Key string `json:"key"`

	// The operator that specifies the relational between the current value under the key and the given values.
	//
	// If the operation is In, NotIn, Exists, or DoesNotExist, the key must be one referring to a label, or to a string-based
	// cluster property.
	// If the operation is Gt, Lt, Ge, Le, Eq, or Ne, the key must be one referring to a numeric-based cluster property.
	// Applying an unsupported operator to a key will cause an error at the scheduling phase.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=In;NotIn;Exists;DoesNotExist;Gt;Lt;Ge;Le;Eq;Ne
	Operator LabelClusterPropertyExpressionOperator `json:"operator"`

	// The values that are used in conjunction with the operator to determine if a selector matches.
	//
	// If the operator is In or NotIn, the values array must be non-empty.
	// If the operator is Exists or DoesNotExist, the values array must be empty.
	// If the operator is Gt, Lt, Ge, Le, Eq, or Ne, the values array must contain exactly one element.
	// +kubebuilder:validation:Optional
	Values []string `json:"values,omitempty"`
}

type LabelClusterPropertyExpressionOperator string

const (
	// The operators applicable to labels and string-based cluster properties.
	LabelClusterPropertyExpressionOperatorIn           LabelClusterPropertyExpressionOperator = "In"
	LabelClusterPropertyExpressionOperatorNotIn        LabelClusterPropertyExpressionOperator = "NotIn"
	LabelClusterPropertyExpressionOperatorExists       LabelClusterPropertyExpressionOperator = "Exists"
	LabelClusterPropertyExpressionOperatorDoesNotExist LabelClusterPropertyExpressionOperator = "DoesNotExist"

	// The operators applicable to numeric-based cluster properties.
	LabelClusterPropertyExpressionOperatorGt LabelClusterPropertyExpressionOperator = "Gt"
	LabelClusterPropertyExpressionOperatorLt LabelClusterPropertyExpressionOperator = "Lt"
	LabelClusterPropertyExpressionOperatorGe LabelClusterPropertyExpressionOperator = "Ge"
	LabelClusterPropertyExpressionOperatorLe LabelClusterPropertyExpressionOperator = "Le"
	LabelClusterPropertyExpressionOperatorEq LabelClusterPropertyExpressionOperator = "Eq"
	LabelClusterPropertyExpressionOperatorNe LabelClusterPropertyExpressionOperator = "Ne"
)

type SyncStrategy struct {
	// The method KubeFleet uses to apply resources to target clusters.
	//
	// Available options are:
	// * ClientSideApply: KubeFleet applies resources to a target cluster using three-way merge patch, similar
	//   to how the Kubernetes CLI performs a client-side apply.
	// * ServerSideApply: KubeFleet applies resources to a target cluster using server-side apply, which allows
	//   the API server to manage conflicts and merge changes.
	//
	// The default value is ClientSideApply.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=ClientSideApply;ServerSideApply
	// +kubebuilder:default=ClientSideApply
	ApplyMethod ApplyMethod `json:"applyMethod"`

	// The options for running server-side apply ops. This field takes effect only if the apply method is
	// set to ServerSideApply.
	//
	// +kubebuilder:validation:Optional
	ServerSideApplyOptions *ServerSideApplyOptions `json:"serverSideApplyOptions,omitempty"`

	// How to handle resource co-ownership. This is most relevant when KubeFleet must manage resources that
	// are already (or expected to be) owned by other non-KubeFleet controllers in target clusters.
	//
	// Available options are:
	// * ShareOwnership: KubeFleet registers itself as a co-owner of the resource.
	// * ReportError: KubeFleet reports an error when a resource to be placed is already owned by other controllers.
	//
	// The default value is ReportError.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=ShareOwnership;ReportError
	// +kubebuilder:default=ReportError
	WhenOwnedByOthers WhenOwnedByOthersOption `json:"whenOwnedByOthers,omitempty"`

	// The action to take when a resource on the target cluster side has drifted from its desired state as controlled
	// by the placement. A drift can occur when a user or a controller on the target cluster makes an inadvertent change
	// to a KubeFleet-managed resource.
	//
	// Available options are:
	// * ApplyAnyway: KubeFleet applies the desired state, which might overwrite the drift.
	// * ReportError: KubeFleet reports an error and leaves the drift as is.
	//
	// The default value is ApplyAnyway.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=ApplyAnyway;ReportError
	// +kubebuilder:default=ApplyAnyway
	WhenDrifted WhenDriftedOption `json:"whenDrifted,omitempty"`

	// The action to take when a resource to be placed already exists on the target cluster side and is not managed
	// by KubeFleet.
	//
	// Available options are:
	// * AlwaysTakeOver: KubeFleet takes over the resource by registering itself as an owner of the resource (if
	//   the resource has no owner or co-ownership is allowed). This enables KubeFleet to adopt the existing resource for
	//   centralized management.
	// * TakeOverIfNoDiff: KubeFleet takes over the resource only if the existing resource reads the same as the desired state
	//   specified on the hub cluster side.
	// * ReportError: KubeFleet reports an error and leaves the existing resource as is.
	//
	// The default value is ReportError.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=AlwaysTakeOver;TakeOverIfNoDiff;ReportError
	// +kubebuilder:default=ReportError
	WhenAlreadyExists WhenAlreadyExistsOption `json:"whenAlreadyExists,omitempty"`

	// The action to take on resources managed by a KubeFleet placement when the placement itself is deleted.
	//
	// Available options are:
	// * CleanUpResources: KubeFleet deletes all the resources managed by the placement.
	// * OrphanResources: KubeFleet relinquishes ownership of such resources and leaves them as they are on target clusters.
	//
	// The default value is CleanUpResources.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=CleanUpResources;OrphanResources
	// +kubebuilder:default=CleanUpResources
	WhenPlacementDeleted WhenPlacementDeletedOption `json:"whenPlacementDeleted,omitempty"`

	// The action to take when a resource to be placed is namespaced but its namespace does not exist on a target cluster.
	//
	// Available options are:
	// * CreateNamespace: KubeFleet creates the namespace on the target cluster. Note that the namespace itself will not be
	//   managed by KubeFleet, and thus will not be deleted even if the placement itself has been deleted.
	// * ReportError: KubeFleet reports an error and does not place the resource to the target cluster.
	//
	// The default value is CreateNamespace.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=CreateNamespace;ReportError
	// +kubebuilder:default=CreateNamespace
	WhenNamespaceDoesNotExist WhenNamespaceDoesNotExistOption `json:"whenNamespaceDoesNotExist,omitempty"`

	// How to compare the states between the target cluster side and the hub cluster side, when calculating drifts
	// or diffs.
	//
	// Available options are:
	// * PartialComparison: KubeFleet compares only the resource fields that have been explicitly specified on the hub cluster
	//   side.
	// * FullComparison: KubeFleet compares all the fields of a resource, including those that are not specified on
	//   the hub cluster side.
	//
	// The default value is PartialComparison.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=PartialComparison;FullComparison
	// +kubebuilder:default=PartialComparison
	ComparisonOption ComparisonOption `json:"comparisonOption,omitempty"`
}

type ApplyMethod string

const (
	ApplyMethodServerSideApply ApplyMethod = "ServerSideApply"
	ApplyMethodClientSideApply ApplyMethod = "ClientSideApply"
)

type ServerSideApplyOptions struct {
	ForceConflicts bool `json:"forceConflicts,omitempty"`
}

type WhenOwnedByOthersOption string

const (
	WhenOwnedByOthersOptionShareOwnership WhenOwnedByOthersOption = "ShareOwnership"
	WhenOwnedByOthersOptionReportError    WhenOwnedByOthersOption = "ReportError"
)

type WhenDriftedOption string

const (
	WhenDriftedOptionApplyAnyway WhenDriftedOption = "ApplyAnyway"
	WhenDriftedOptionReportError WhenDriftedOption = "ReportError"
)

type WhenAlreadyExistsOption string

const (
	WhenAlreadyExistsOptionAlwaysTakeOver   WhenAlreadyExistsOption = "AlwaysTakeOver"
	WhenAlreadyExistsOptionTakeOverIfNoDiff WhenAlreadyExistsOption = "TakeOverIfNoDiff"
	WhenAlreadyExistsOptionReportError      WhenAlreadyExistsOption = "ReportError"
)

type WhenPlacementDeletedOption string

const (
	WhenPlacementDeletedOptionCleanUpResources WhenPlacementDeletedOption = "CleanUpResources"
	WhenPlacementDeletedOptionOrphanResources  WhenPlacementDeletedOption = "OrphanResources"
)

type ComparisonOption string

const (
	ComparisonOptionPartialComparison ComparisonOption = "PartialComparison"
	ComparisonOptionFullComparison    ComparisonOption = "FullComparison"
)

type WhenNamespaceDoesNotExistOption string

const (
	WhenNamespaceDoesNotExistOptionCreateNamespace WhenNamespaceDoesNotExistOption = "CreateNamespace"
	WhenNamespaceDoesNotExistOptionReportError     WhenNamespaceDoesNotExistOption = "ReportError"
)

type Toleration struct{}

type PlacementPolicyStatus struct {
	// A list of conditions that describe the workload placement.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// The name of the latest revision of the resources selected by this placement policy, in the form of a resource snapshot.
	// +kubebuilder:validation:Optional
	LatestResourceRevisionName *string `json:"latestResourceRevisionName,omitempty"`

	// The number of clusters that are expected to be selected by this placement.
	DesiredClusters *int32 `json:"desiredClusters,omitempty"`
	// The number of clusters that should be but have not yet been selected by this placement.
	NotYetScheduledClusters *int32 `json:"notYetScheduledClusters,omitempty"`
	// The number of clusters that have resources out of sync with the desired state on the hub cluster side.
	ResourcesOutOfSyncClusters *int32 `json:"resourcesOutOfSyncClusters,omitempty"`
	// The number of clusters that have resources failed the KubeFleet availability check.
	ResourcesUnavailableClusters *int32 `json:"resourcesUnavailableClusters,omitempty"`

	// The number of ongoing cluster requests that have been submitted by this placement.
	OngoingClusterRequests *int32 `json:"ongoingClusterRequests,omitempty"`

	// The binding manager that is currently managing the bindings for this placement.
	// +kubebuilder:validation:Optional
	BindingManager *BindingManager `json:"bindingManager,omitempty"`
}

type BindingManager struct {
	// A name of the controller that manages the bindings for this placement.
	//
	// +kubebuilder:validation:Required
	ControllerName string `json:"controllerName"`

	// A list of references to the objects that are currently managing the bindings for this placement,
	// under the reconciliation of the specified controller.
	//
	// +kubebuilder:validation:Optional
	ObjectRefs []SameNamespacedObjectReference `json:"objectRefs,omitempty"`
}

// PlacementPolicyList contains a list of PlacementPolicy.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope="Namespaced"
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type PlacementPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []PlacementPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PlacementPolicy{}, &PlacementPolicyList{})
}
