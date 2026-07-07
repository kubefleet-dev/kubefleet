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

package placementpolicy

import (
	"context"
	"math/rand/v2"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	experimentalv1beta1 "github.com/kubefleet-dev/kubefleet/apis/experimental/v1beta1"
	bindingmanagertools "github.com/kubefleet-dev/kubefleet/pkg/reimagined/utils/bindingmanager"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/errors"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/resource"
)

func prepareClusterSelectorsForScheduling(selectors []experimentalv1beta1.ClusterSelector) ([]string, map[string]sets.Set[int], error) {
	selectorHashes := make([]string, len(selectors))

	for i, selector := range selectors {
		// The count field is excluded from the hash calculation.
		selectorCopy := selector.DeepCopy()
		selectorCopy.Count = nil

		selectorHash, err := resource.HashOf(selectorCopy)
		if err != nil {
			wrappedErr := errors.NewUnexpectedError(err, "failed to calculate hash for a cluster selector", "clusterSelector", selector)
			klog.ErrorS(wrappedErr, "Failed to calculate hash for a cluster selector", errors.Args(wrappedErr)...)
			return nil, nil, wrappedErr
		}
		selectorHashes[i] = selectorHash
	}

	selectorSetByHash := make(map[string]sets.Set[int])
	for idx := range selectorHashes {
		selectorHash := selectorHashes[idx]

		selectorSet := selectorSetByHash[selectorHash]
		if selectorSet == nil {
			selectorSet = sets.New[int]()
		}
		selectorSet.Insert(idx)
		selectorSetByHash[selectorHash] = selectorSet
	}

	return selectorHashes, selectorSetByHash, nil
}

func crossReferenceClusterSelectorsAndBindings(
	selectorSetByHash map[string]sets.Set[int],
	bindings []experimentalv1beta1.PlacementBinding,
) []experimentalv1beta1.PlacementBinding {
	danglingBindings := make([]experimentalv1beta1.PlacementBinding, 0)
	for idx := range bindings {
		binding := bindings[idx]

		bindingClusterSelectorHash := binding.Spec.ClusterSelectorHash
		selectorSet, found := selectorSetByHash[bindingClusterSelectorHash]
		if !found {
			danglingBindings = append(danglingBindings, binding)
			continue
		}

		_, popped := selectorSet.PopAny()
		if !popped {
			// The hash matches but there is no selector that corresponds to the binding. Mark the
			// binding as dangling as well.
			danglingBindings = append(danglingBindings, binding)
			continue
		}
	}

	return danglingBindings
}

func needsScheduling(selectorSetByHash map[string]sets.Set[int], danglingBindings []experimentalv1beta1.PlacementBinding) bool {
	if len(danglingBindings) > 0 {
		return true
	}

	for _, selectorSet := range selectorSetByHash {
		if len(selectorSet) > 0 {
			return true
		}
	}
	return false
}

func (r *Reconciler) scheduleOnce(
	ctx context.Context,
	placement *experimentalv1beta1.PlacementPolicy,
	latestResourceSnapshot *experimentalv1beta1.PlacementResourceSnapshot,
	selectors []experimentalv1beta1.ClusterSelector,
	selectorSetByHash map[string]sets.Set[int],
	allBindings, danglingBindings []experimentalv1beta1.PlacementBinding,
) (bool, error) {
	// Scheduling is needed; claim the role of binding manager on the placement.
	claimed, err := bindingmanagertools.ClaimAsBindingManager(ctx, r.HubClient, placement, wantBindingManager)
	if err != nil {
		wrappedErr := errors.Wraps(err, "", "placementPolicy", klog.KObj(placement))
		klog.ErrorS(wrappedErr, "Failed to claim as the binding manager for the placement policy", errors.Args(wrappedErr)...)

		return false, wrappedErr
	}
	if !claimed {
		// Another controller has already claimed the binding manager role; requeue after some time.
		klog.V(2).InfoS("A different binding manager has claimed the placement",
			"bindingManager", placement.Status.BindingManager, "placementPolicy", klog.KObj(placement))
		return true, nil
	}

	// Delete all the dangling bindings.
	for idx := range danglingBindings {
		danglingBinding := danglingBindings[idx]
		if err := r.HubClient.Delete(ctx, &danglingBinding); err != nil && !apierrors.IsNotFound(err) {
			wrappedErr := errors.NewAPIServerError(err, "", false,
				"placementBinding", klog.KObj(&danglingBinding), "placementPolicy", klog.KObj(placement))
			klog.ErrorS(wrappedErr, "Failed to delete a dangling placement binding", errors.Args(wrappedErr)...)
			return false, wrappedErr
		}
	}
	if len(danglingBindings) > 0 {
		klog.V(2).InfoS("Deleted dangling bindings; requeue until all the deleted bindings are fully removed",
			"placementPolicy", klog.KObj(placement))
		return true, nil
	}

	alreadySelectedClusters := sets.New[string]()
	for idx := range allBindings {
		binding := allBindings[idx]
		alreadySelectedClusters.Insert(binding.Spec.ClusterName)
	}

	// List all member clusters.
	memberClusterList := &clusterv1beta1.MemberClusterList{}
	if err := r.HubClient.List(ctx, memberClusterList); err != nil {
		wrappedErr := errors.NewAPIServerError(err, "", false, "placementPolicy", klog.KObj(placement), "controller", controllerName)
		klog.ErrorS(wrappedErr, "Failed to list member clusters", errors.Args(wrappedErr)...)
		return false, wrappedErr
	}
	memberClusters := memberClusterList.Items

	// Find out the clusters that can match with the selectors with no bindings, and the selectors
	// that still cannot be associated with a member cluster.
	selectedClusters, matchedSelectors, selectorHashForSelectedClusters, unmatchedSelectors, unmatchedSelectorHashes := selectClusters(selectors, selectorSetByHash, memberClusters, alreadySelectedClusters, klog.KObj(placement))

	// Create a new binding for each selected member cluster.
	resourceSnapshotRevisionNamePtr := defaultResourceSnapshotRevisionName(placement, latestResourceSnapshot)
	if err := r.createBindingsFor(
		ctx, placement, selectedClusters, matchedSelectors, selectorHashForSelectedClusters, resourceSnapshotRevisionNamePtr,
	); err != nil {
		wrappedErr := errors.Wraps(err, "", "placementPolicy", klog.KObj(placement))
		klog.ErrorS(wrappedErr, "Failed to create placement bindings for the selected member clusters", errors.Args(wrappedErr)...)
		return false, wrappedErr
	}

	// Submit a cluster request if there are unmatched selectors.
	shouldRequeue, err := r.submitClusterRequestIfNeeded(ctx, placement, memberClusters, unmatchedSelectors, unmatchedSelectorHashes)
	if err != nil {
		wrappedErr := errors.Wraps(err, "", "placementPolicy", klog.KObj(placement))
		klog.ErrorS(wrappedErr, "Failed to submit cluster request for the unmatched selectors", errors.Args(wrappedErr)...)
		return false, wrappedErr
	}
	return shouldRequeue, nil
}

func selectClusters(
	selectors []experimentalv1beta1.ClusterSelector,
	selectorSetByHash map[string]sets.Set[int],
	clusters []clusterv1beta1.MemberCluster,
	alreadySelectedClusters sets.Set[string],
	placementPolicyRef klog.ObjectRef,
) (
	[]clusterv1beta1.MemberCluster,
	[]experimentalv1beta1.ClusterSelector,
	[]string,
	[]experimentalv1beta1.ClusterSelector,
	[]string,
) {
	selectedClusters := make([]clusterv1beta1.MemberCluster, 0)
	matchedSelectors := make([]experimentalv1beta1.ClusterSelector, 0)
	selectorHashForSelectedClusters := make([]string, 0)
	unmatchedSelectors := make([]experimentalv1beta1.ClusterSelector, 0)
	unmatchedSelectorHashes := make([]string, 0)

	for h := range selectorSetByHash {
		selectorSet := selectorSetByHash[h]
		if len(selectorSet) == 0 {
			// All selectors with this hash already have bindings; no further action is needed.
			continue
		}

		for sIdx := range selectorSet {
			selector := selectors[sIdx]

			// Find the clusters that match this selector.
			matchedClusters := make([]clusterv1beta1.MemberCluster, 0)
			for cidx := range clusters {
				cluster := clusters[cidx]

				if !alreadySelectedClusters.Has(cluster.Name) && clusterMatchesSelector(cluster, selector) {
					matchedClusters = append(matchedClusters, cluster)
				}
			}

			if len(matchedClusters) == 0 {
				// No cluster matches this selector; no binding can be created for this selector. Just skip it.
				klog.V(2).InfoS("No member cluster matches the cluster selector; skip creating binding for this selector, and request a new cluster if applicable",
					"clusterSelector", selector, "placementPolicy", placementPolicyRef)
				unmatchedSelectors = append(unmatchedSelectors, selector)
				unmatchedSelectorHashes = append(unmatchedSelectorHashes, h)
				continue
			}

			// Just shuffle the selected clusters.
			rand.Shuffle(len(matchedClusters), func(i, j int) {
				matchedClusters[i], matchedClusters[j] = matchedClusters[j], matchedClusters[i]
			})

			selectedCluster := matchedClusters[0]
			selectedClusters = append(selectedClusters, selectedCluster)
			matchedSelectors = append(matchedSelectors, selector)
			selectorHashForSelectedClusters = append(selectorHashForSelectedClusters, h)
			alreadySelectedClusters.Insert(selectedCluster.Name)
		}
	}

	return selectedClusters, matchedSelectors, selectorHashForSelectedClusters, unmatchedSelectors, unmatchedSelectorHashes
}

func clusterMatchesSelector(cluster clusterv1beta1.MemberCluster, selector experimentalv1beta1.ClusterSelector) bool {
	if len(selector.Terms) == 0 || len(selector.Terms[0].MatchLabels) == 0 {
		// If the selector has no terms or no match labels, it matches all clusters.
		return true
	}

	for k, v := range selector.Terms[0].MatchLabels {
		if gotV, found := cluster.Labels[k]; !found || gotV != v {
			return false
		}
	}
	return true
}
