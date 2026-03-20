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

package v1

const (
	// FleetPrefix is the prefix used for official fleet labels/annotations.
	// Unprefixed labels/annotations are reserved for end-users
	// See https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#label-selector-and-annotation-conventions
	FleetPrefix = "kubernetes-fleet.io/"

	// IsLatestSnapshotLabel indicates if the snapshot is the latest one.
	IsLatestSnapshotLabel = FleetPrefix + "is-latest-snapshot"

	// ParentClusterResourceOverrideSnapshotHashAnnotation is the annotation to work that contains the hash of the parent cluster resource override snapshot list.
	ParentClusterResourceOverrideSnapshotHashAnnotation = FleetPrefix + "parent-cluster-resource-override-snapshot-hash"

	// ParentResourceOverrideSnapshotHashAnnotation is the annotation to work that contains the hash of the parent resource override snapshot list.
	ParentResourceOverrideSnapshotHashAnnotation = FleetPrefix + "parent-resource-override-snapshot-hash"
)

var (
	// ClusterResourceOverrideKind is the kind of the ClusterResourceOverride.
	ClusterResourceOverrideKind = "ClusterResourceOverride"

	// ClusterResourceOverrideSnapshotKind is the kind of the ClusterResourceOverrideSnapshot.
	ClusterResourceOverrideSnapshotKind = "ClusterResourceOverrideSnapshot"

	// ResourceOverrideKind is the kind of the ResourceOverride.
	ResourceOverrideKind = "ResourceOverride"

	// ResourceOverrideSnapshotKind is the kind of the ResourceOverrideSnapshot.
	ResourceOverrideSnapshotKind = "ResourceOverrideSnapshot"

	// OverrideClusterNameVariable is the reserved variable in the override value that will be replaced by the actual cluster name.
	OverrideClusterNameVariable = "${MEMBER-CLUSTER-NAME}"

	// OverrideClusterLabelKeyVariablePrefix is a reserved variable in the override expression.
	// We use this variable to find the associated key following the prefix.
	// The key name ends with a "}" character (but not include it).
	// The key name must be a valid Kubernetes label name and case-sensitive.
	// The content of the string containing this variable will be replaced by the actual label value on the member cluster.
	// For example, if the string is "${MEMBER-CLUSTER-LABEL-KEY-kube-fleet.io/region}" then the key name is "kube-fleet.io/region".
	// If there is a label "kube-fleet.io/region": "us-west-1" on the member cluster, this string will be replaced by "us-west-1".
	OverrideClusterLabelKeyVariablePrefix = "${MEMBER-CLUSTER-LABEL-KEY-"
)

// NamespacedName comprises a resource name, with a mandatory namespace.
type NamespacedName struct {
	// Name is the name of the namespaced scope resource.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace is namespace of the namespaced scope resource.
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`
}
