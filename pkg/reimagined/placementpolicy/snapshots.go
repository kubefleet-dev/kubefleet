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

	"github.com/kubefleet-dev/kubefleet/pkg/utils/errors"
	"k8s.io/klog/v2"

	experimentalv1beta1 "github.com/kubefleet-dev/kubefleet/apis/experimental/v1beta1"
)

func (r *Reconciler) retrieveLatestResourceSnapshot(
	ctx context.Context,
	placementPolicy *experimentalv1beta1.PlacementPolicy,
) (*experimentalv1beta1.PlacementResourceSnapshot, bool, error) {
	latestResourceSnapshot, err := r.PlacementResourceSnapshotManager.RetrieveLatestResourceSnapshot(ctx, placementPolicy)
	if err != nil {
		klog.ErrorS(err, "Failed to retrieve latest resource snapshot", errors.Args(err)...)
		return nil, false, err
	}

	if latestResourceSnapshot == nil {
		klog.V(2).InfoS("No resource snapshot found for the placement policy, creating a new one", "placementPolicy", klog.KObj(placementPolicy))

		latestResourceSnapshot, err = r.PlacementResourceSnapshotManager.RequestAndWaitForNewSnapshot(ctx, placementPolicy, r.MaxSnapshotCreationWaitTime)
		if err != nil {
			klog.ErrorS(err, "Failed to create new resource snapshot for the placement policy", errors.Args(err)...)
			return nil, false, err
		}

		return latestResourceSnapshot, true, nil
	}

	isLatestResourceSnapshotUpToDate, err := r.PlacementResourceSnapshotManager.IsResourceSnapshotUpToDate(ctx, placementPolicy, latestResourceSnapshot)
	if err != nil {
		klog.ErrorS(err, "Failed to check if the latest resource snapshot is up-to-date", errors.Args(err)...)
		return nil, false, err
	}

	return latestResourceSnapshot, isLatestResourceSnapshotUpToDate, nil
}
