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

const (
	// The Kinds of API resource types in this package.
	ClusterClaimKind                     = "ClusterClaim"
	ClusterProviderClassKind             = "ClusterProviderClass"
	PlacementPolicyKind                  = "PlacementPolicy"
	ClusterPlacementPolicyKind           = "ClusterPlacementPolicy"
	PlacementBindingKind                 = "PlacementBinding"
	ClusterPlacementBindingKind          = "ClusterPlacementBinding"
	PlacementResourceSnapshotKind        = "PlacementResourceSnapshot"
	ClusterPlacementResourceSnapshotKind = "ClusterPlacementResourceSnapshot"
	WorkKind                             = "Work"
)

// The annotation and label keys that KubeFleet reserves for the placement APIs.
//
// Note that these keys carry the KubeFleet domain itself rather than the API group; they are
// set on arbitrary Kubernetes objects, not only on the objects of this group.
const (
	// KubeFleetPrefix is the domain that prefixes every reserved annotation and label key below,
	// per the Kubernetes convention that reserves unprefixed keys for end users.
	KubeFleetPrefix = "kubefleet.dev/"

	// ClusterSelectorsAnnotation is the annotation that requests annotation-based placement for the
	// resource it is set on. Its value is a semicolon-separated list of cluster selectors, each of
	// which is a comma-separated list of LABEL_KEY=LABEL_VALUE label matchers with an optional
	// count=N|All directive, e.g.:
	//
	//     kubefleet.dev/cluster-selectors: "env=staging,count=All;env=canary,region=eastus,count=1"
	//
	// KubeFleet keeps a PlacementPolicy (or ClusterPlacementPolicy) object in sync with the
	// annotation for as long as it is present.
	//
	// The key names what the value holds rather than what KubeFleet does with it, matching the
	// clusterSelectors field of the generated policy: the annotation is one way to write that field,
	// and the two are read together often enough that they should not have to be translated.
	//
	// Within the annotation, `region` may be used in place of the well-known
	// topology.kubernetes.io/region label key, and `alias` in place of the cluster alias label
	// (placement/v1beta1's ClusterAliasLabel).
	ClusterSelectorsAnnotation = KubeFleetPrefix + "cluster-selectors"

	// FulfillerFinalizer is the finalizer a provider places on a cluster claim when it accepts it,
	// so that a withdrawn claim is not removed before the provider has cancelled or cleaned up the
	// provisioning it started. KubeFleet never removes it.
	FulfillerFinalizer = KubeFleetPrefix + "fulfiller"

	// FulfilledClaimNameLabel and FulfilledClaimUIDLabel are set by a provider on the member cluster it
	// provisions for a cluster claim, so that the provider can find the clusters it owns for a claim
	// (by name, which KubeFleet keeps stable across re-issues of the same claim) and tell apart the
	// one it provisioned for this very claim (by UID). The name label is shortened when the claim
	// name does not fit in a label value; the UID always fits.
	FulfilledClaimNameLabel = KubeFleetPrefix + "fulfilled-claim-name"
	FulfilledClaimUIDLabel  = KubeFleetPrefix + "fulfilled-claim-uid"
)

// The labels that record, on a placement policy KubeFleet generated from an annotation, the
// resource whose annotation caused it to exist.
//
// These keys carry the API group rather than the bare KubeFleet domain, since unlike the keys
// above they are set on objects of this group.
//
// The labels exist so that a policy can be found with a List when its generated name is not known;
// the owner reference on the policy remains the authoritative record of where it came from. Note
// that ParentNameLabel is lossy: the name of a resource can run to 253 bytes while a label value
// stops at 63, so for a longer name the label holds a prefix and a hash instead, and selecting on
// it with the resource's own name matches nothing.
const (
	// ParentAPIGroupLabel holds the API group of the resource a policy was generated from. It is
	// the empty string for resources in the core API group, and is always present, so that
	// core-group resources can be selected as readily as any other.
	ParentAPIGroupLabel = "placement.kubefleet.dev/parent-api-group"

	// ParentKindLabel holds the kind of the resource a policy was generated from, spelled as the
	// kind itself is (Deployment, not deployment).
	ParentKindLabel = "placement.kubefleet.dev/parent-kind"

	// ParentNameLabel holds the name of the resource a policy was generated from, shortened if it
	// does not fit in a label value.
	ParentNameLabel = "placement.kubefleet.dev/parent-name"

	// IsDefaultClusterProviderClassAnnotation marks the ClusterProviderClass that placement policies
	// use when they name none, in the way the storage.kubernetes.io/is-default-class annotation marks
	// a StorageClass. Its value must be "true". When more than one class carries it, no class is the
	// default and a policy that names none issues no cluster claims.
	IsDefaultClusterProviderClassAnnotation = "placement.kubefleet.dev/is-default-class"

	// ProvisionedClusterNameAnnotation records on a cluster claim, before the provider creates anything,
	// the name of the cluster it will provision for the claim, so that a provider restarted mid-flight
	// resumes with the same cluster instead of creating a second one.
	ProvisionedClusterNameAnnotation = "placement.kubefleet.dev/provisioned-cluster-name"
)

type ObjectReference struct {
	// The namespace of the referenced object.
	//
	// If the object is cluster-scoped, this field should be left empty.
	//
	// +kubebuilder:validation:Optional
	Namespace string `json:"namespace,omitempty"`

	// The name of the referenced object.
	//
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// The API group, version, and kind of the referenced object.

	// +kubebuilder:validation:Optional
	APIGroup string `json:"apiGroup,omitempty"`

	// +kubebuilder:validation:Required
	APIVersion string `json:"apiVersion,omitempty"`

	// +kubebuilder:validation:Required
	Kind string `json:"kind,omitempty"`
}
