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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	experimentalv1beta1 "github.com/kubefleet-dev/kubefleet/apis/experimental/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/errors"
)

func (r *Reconciler) submitClusterRequestIfNeeded(
	ctx context.Context,
	placementPolicy *experimentalv1beta1.PlacementPolicy,
	clusters []clusterv1beta1.MemberCluster,
	unmatchedSelectors []experimentalv1beta1.ClusterSelector,
	unmatchedSelectorHashes []string,
) (bool, error) {
	if len(unmatchedSelectors) == 0 {
		klog.V(2).InfoS("No unmatched cluster selector, no need to submit a cluster request", "placementPolicy", klog.KObj(placementPolicy))
		staleReq := &experimentalv1beta1.ClusterRequest{}
		staleReq.Name = placementPolicy.Name
		staleReq.Namespace = placementPolicy.Namespace
		if err := r.HubClient.Delete(ctx, staleReq); err != nil && !apierrors.IsNotFound(err) {
			wrappedErr := errors.NewAPIServerError(err, "", false,
				"clusterRequest", klog.KObj(staleReq), "placementPolicy", klog.KObj(placementPolicy))
			klog.ErrorS(wrappedErr, "Failed to delete stale cluster request for the placement policy", errors.Args(wrappedErr)...)
			return false, wrappedErr
		}
		return false, nil
	}

	klog.V(2).InfoS("Submitting cluster request for the unmatched cluster selectors", "placementPolicy", klog.KObj(placementPolicy), "unmatchedSelectors", unmatchedSelectors)
	curClusterRequest := &experimentalv1beta1.ClusterRequest{}
	if err := r.HubClient.Get(ctx, client.ObjectKey{Namespace: placementPolicy.Namespace, Name: placementPolicy.Name}, curClusterRequest); err != nil {
		if apierrors.IsNotFound(err) {
			// No cluster request exists for the placement.
			klog.V(2).InfoS("No existing cluster request found for the placement policy; need to create a new one", "placementPolicy", klog.KObj(placementPolicy))
			curClusterRequest = nil
		} else {
			wrappedErr := errors.NewAPIServerError(err, "", false,
				"clusterRequest", client.ObjectKey{Namespace: placementPolicy.Namespace, Name: placementPolicy.Name}, "placementPolicy", klog.KObj(placementPolicy))
			klog.ErrorS(wrappedErr, "Failed to get cluster request for the placement policy", errors.Args(wrappedErr)...)

			return false, wrappedErr
		}
	}

	latestObservedClusterCreationTimestamp := metav1.Time{}
	for cidx := range clusters {
		cluster := clusters[cidx]

		if cluster.CreationTimestamp.After(latestObservedClusterCreationTimestamp.Time) {
			latestObservedClusterCreationTimestamp = cluster.CreationTimestamp
		}
	}

	if curClusterRequest == nil {
		// No cluster request exists for the placement; need to create a new one.
		newClusterRequest := &experimentalv1beta1.ClusterRequest{
			ObjectMeta: metav1.ObjectMeta{
				Name:      placementPolicy.Name,
				Namespace: placementPolicy.Namespace,
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: experimentalv1beta1.GroupVersion.String(),
						Kind:       "PlacementPolicy",
						Name:       placementPolicy.Name,
						UID:        placementPolicy.UID,
						Controller: ptr.To(true),
					},
				},
			},
			Spec: experimentalv1beta1.ClusterRequestSpec{
				ClusterSelector:     unmatchedSelectors[0],
				ClusterSelectorHash: unmatchedSelectorHashes[0],
			},
		}

		if err := r.HubClient.Create(ctx, newClusterRequest); err != nil {
			wrappedErr := errors.NewAPIServerError(err, "", false,
				"clusterRequest", klog.KObj(newClusterRequest), "placementPolicy", klog.KObj(placementPolicy))
			klog.ErrorS(wrappedErr, "Failed to create cluster request for the placement policy", errors.Args(wrappedErr)...)
			return false, wrappedErr
		}

		// Add the latest observed cluster creation timestamp to the request.
		if !latestObservedClusterCreationTimestamp.IsZero() {
			newClusterRequest.Status.LatestObservedClusterCreationTimestamp = &latestObservedClusterCreationTimestamp
			if err := r.HubClient.Status().Update(ctx, newClusterRequest); err != nil {
				wrappedErr := errors.NewAPIServerError(err, "", false,
					"clusterRequest", klog.KObj(newClusterRequest), "placementPolicy", klog.KObj(placementPolicy))
				klog.ErrorS(wrappedErr, "Failed to update cluster request status for the placement policy", errors.Args(wrappedErr)...)
				return false, wrappedErr
			}
		}

		return true, nil
	}

	// A cluster request already exists for the placement; check to see if it needs updating or if it should
	// be deleted.
	curSelectorHash := curClusterRequest.Spec.ClusterSelectorHash
	if !sets.New(unmatchedSelectorHashes...).Has(curSelectorHash) {
		if err := r.HubClient.Delete(ctx, curClusterRequest); err != nil && !apierrors.IsNotFound(err) {
			wrappedErr := errors.NewAPIServerError(err, "", false,
				"clusterRequest", klog.KObj(curClusterRequest), "placementPolicy", klog.KObj(placementPolicy))
			klog.ErrorS(wrappedErr, "Failed to delete cluster request for the placement policy", errors.Args(wrappedErr)...)
			return false, wrappedErr
		}
		return true, nil
	}

	// Update the latest observed cluster creation timestamp on the request (if applicable).
	if curClusterRequest.DeletionTimestamp.IsZero() &&
		(curClusterRequest.Status.LatestObservedClusterCreationTimestamp == nil || latestObservedClusterCreationTimestamp.After(curClusterRequest.Status.LatestObservedClusterCreationTimestamp.Time)) {
		curClusterRequest.Status.LatestObservedClusterCreationTimestamp = &latestObservedClusterCreationTimestamp
		if err := r.HubClient.Status().Update(ctx, curClusterRequest); err != nil {
			wrappedErr := errors.NewAPIServerError(err, "", false,
				"clusterRequest", klog.KObj(curClusterRequest), "placementPolicy", klog.KObj(placementPolicy))
			klog.ErrorS(wrappedErr, "Failed to update cluster request status for the placement policy", errors.Args(wrappedErr)...)
			return false, wrappedErr
		}
	}
	return true, nil
}
