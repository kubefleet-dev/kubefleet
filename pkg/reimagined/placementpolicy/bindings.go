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
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	experimentalv1beta1 "github.com/kubefleet-dev/kubefleet/apis/experimental/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/errors"
)

func defaultResourceSnapshotRevisionName(
	placementPolicy *experimentalv1beta1.PlacementPolicy,
	latestResourceSnapshot *experimentalv1beta1.PlacementResourceSnapshot,
) *string {
	res := (*string)(nil)

	scheduledCond := meta.FindStatusCondition(placementPolicy.Status.Conditions, experimentalv1beta1.PlacementPolicyCondTypeScheduled)
	if scheduledCond == nil {
		// The placement is just created; for the first round of scheduling, assign the current latest
		// resource snapshot revision to each binding.
		res = ptr.To(latestResourceSnapshot.Name)
	}
	return res
}

func (r *Reconciler) createBindingsFor(
	ctx context.Context,
	placementPolicy *experimentalv1beta1.PlacementPolicy,
	clusters []clusterv1beta1.MemberCluster,
	selectorForSelectedClusters []experimentalv1beta1.ClusterSelector,
	selectorHashForSelectedClusters []string,
	resourceSnapshotNamePtr *string,
) error {
	for idx := range clusters {
		cluster := clusters[idx]

		binding := &experimentalv1beta1.PlacementBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf(placementBindingNameFmt, placementPolicy.Name, cluster.Name),
				Namespace: placementPolicy.Namespace,
				Labels: map[string]string{
					experimentalv1beta1.PlacementBindingOwnedByLabelKey: placementPolicy.Name,
				},
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
			Spec: experimentalv1beta1.PlacementBindingSpec{
				PlacementPolicyName:  placementPolicy.Name,
				ClusterSelectorHash:  selectorHashForSelectedClusters[idx],
				ClusterSelector:      selectorForSelectedClusters[idx],
				ClusterName:          cluster.Name,
				ResourceSnapshotName: resourceSnapshotNamePtr,
			},
		}
		if err := r.HubClient.Create(ctx, binding); err != nil {
			wrappedErr := errors.NewAPIServerError(err, "", false,
				"placementBinding", klog.KObj(binding), "placementPolicy", klog.KObj(placementPolicy))
			klog.ErrorS(wrappedErr, "Failed to create placement binding for the selected member cluster", errors.Args(wrappedErr)...)
			return wrappedErr
		}
	}

	return nil
}
