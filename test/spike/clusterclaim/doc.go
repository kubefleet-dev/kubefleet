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

// Package clusterclaim is a SPIKE for kubefleet-dev/kubefleet#791 ([FEP-0001]
// Verify the cluster claim workflow). It is NOT production code and is not
// wired into any build target.
//
// The spike demonstrates, against the merged v1alpha1 API types
// (ClusterRequest; renamed ClusterClaim in PR #803), the full claim
// lifecycle with the two controllers the FEP-0001 contract implies:
//
//   - FakeProvisioner plays the platform/cloud-provider role (the part
//     Meridian's ClusterRequest reconciler or a CAPI-based controller would
//     play in production): it watches claims and, per test policy, fulfills
//     them (creates a matching MemberCluster, sets Completed=True and
//     provisionedClusterName), fails them, or ignores them.
//   - Withdrawer prototypes the KubeFleet placement-policy-controller slice
//     under verification (issue #786 will implement it for real): it watches
//     MemberClusters, matches them against each claim's clusterSelectorTerms,
//     withdraws (deletes) fulfilled claims regardless of their status, and
//     refreshes lastObservedMostRecentClusterCreationTimestamp on claims that
//     remain unfulfilled.
//
// The test suites double as the seed of the #791 verification matrix:
// validation_test.go pins the CEL/schema contract; workflow_test.go pins the
// behavioral contract (fulfillment by the provisioned cluster, fulfillment by
// an unrelated cluster, provisioning failure, staleness refresh).
package clusterclaim
