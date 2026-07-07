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

package placementresourcesnapshot

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	experimentalv1beta1 "github.com/kubefleet-dev/kubefleet/apis/experimental/v1beta1"
	errors "github.com/kubefleet-dev/kubefleet/pkg/utils/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	placementResSnapshotNameFmt = "%s-%d"

	startIdx = 1
)

// Demo-only note: this is a simple, naive implementation of snapshotting that is only meant for
// demo purposes.
func (m *Manager) createNewResourceSnapshot(
	ctx context.Context,
	placementPolicy *experimentalv1beta1.PlacementPolicy,
	resourceContents []experimentalv1beta1.ResourceContent,
) (*experimentalv1beta1.PlacementResourceSnapshot, error) {
	snapshotList := &experimentalv1beta1.PlacementResourceSnapshotList{}
	labelSelector := client.MatchingLabels{
		experimentalv1beta1.ResourceSnapshotOwnedByLabelKey: placementPolicy.Name,
	}
	if err := m.client.List(ctx, snapshotList, labelSelector, client.InNamespace(placementPolicy.Namespace)); err != nil {
		return nil, errors.NewAPIServerError(err, "failed to list placement resource snapshots", false)
	}

	if len(snapshotList.Items) == 0 {
		// No snapshot exists for this placement policy. Create a new one.
		klog.V(2).InfoS("No existing placement resource snapshot found for the placement policy, creating a new one",
			"placementPolicy", klog.KObj(placementPolicy))
		newSnapshot := &experimentalv1beta1.PlacementResourceSnapshot{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf(placementResSnapshotNameFmt, placementPolicy.Name, startIdx),
				Namespace: placementPolicy.Namespace,
				Labels: map[string]string{
					experimentalv1beta1.ResourceSnapshotOwnedByLabelKey:  placementPolicy.Name,
					experimentalv1beta1.ResourceSnapshotRevisionLabelKey: fmt.Sprintf("%d", startIdx),
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
			Spec: experimentalv1beta1.PlacementResourceSnapshotSpec{
				Resources: resourceContents,
			},
		}
		if err := m.client.Create(ctx, newSnapshot); err != nil {
			return nil, errors.NewAPIServerError(err, "failed to create placement resource snapshot", true, "snapshotRevision", startIdx)
		}

		klog.V(2).InfoS("Successfully created placement resource snapshot for the placement policy",
			"snapshot", klog.KObj(newSnapshot), "snapshotRevision", startIdx,
			"placementPolicy", klog.KObj(placementPolicy))
		return newSnapshot, nil
	}

	// Some snapshots already exist for the placement policy.
	klog.V(2).InfoS("Found existing placement resource snapshots for the placement policy",
		"snapshotCount", len(snapshotList.Items), "placementPolicy", klog.KObj(placementPolicy))

	// Sort the existing snapshots by their revision numbers in descending order and get the one with the highest revision number.
	var sortErrs []error
	sort.Slice(snapshotList.Items, func(i, j int) bool {
		iRevisionStr := snapshotList.Items[i].Labels[experimentalv1beta1.ResourceSnapshotRevisionLabelKey]
		jRevisionStr := snapshotList.Items[j].Labels[experimentalv1beta1.ResourceSnapshotRevisionLabelKey]
		iRevision, err := strconv.Atoi(iRevisionStr)
		if err != nil {
			sortErrs = append(sortErrs, fmt.Errorf("failed to parse revision label on snapshot %s: %w", snapshotList.Items[i].Name, err))
		}
		jRevision, err := strconv.Atoi(jRevisionStr)
		if err != nil {
			sortErrs = append(sortErrs, fmt.Errorf("failed to parse revision label on snapshot %s: %w", snapshotList.Items[j].Name, err))
		}
		return iRevision > jRevision
	})
	if len(sortErrs) > 0 {
		return nil, errors.NewUnexpectedError(sortErrs[0], "failed to sort placement resource snapshots by revision number")
	}

	latestSnapshot := snapshotList.Items[0]
	latestRevisionStr := latestSnapshot.Labels[experimentalv1beta1.ResourceSnapshotRevisionLabelKey]
	latestRevision, err := strconv.Atoi(latestRevisionStr)
	if err != nil {
		return nil, errors.NewUnexpectedError(err, "failed to parse revision label on the latest snapshot", "snapshotName", latestSnapshot.Name, "revisionLabelValue", latestRevisionStr)
	}

	// Create a new snapshot with the revision number incremented by 1.
	newRevision := latestRevision + 1
	newSnapshot := &experimentalv1beta1.PlacementResourceSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf(placementResSnapshotNameFmt, placementPolicy.Name, newRevision),
			Namespace: placementPolicy.Namespace,
			Labels: map[string]string{
				experimentalv1beta1.ResourceSnapshotOwnedByLabelKey:  placementPolicy.Name,
				experimentalv1beta1.ResourceSnapshotRevisionLabelKey: fmt.Sprintf("%d", newRevision),
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: experimentalv1beta1.GroupVersion.String(),
					Kind:       "PlacementPolicy",
					Name:       placementPolicy.Name,
					UID:        placementPolicy.UID,
					Controller: ptr.To(false),
				},
			},
		},
		Spec: experimentalv1beta1.PlacementResourceSnapshotSpec{
			Resources: resourceContents,
		},
	}
	if err := m.client.Create(ctx, newSnapshot); err != nil {
		return nil, errors.NewAPIServerError(err, "failed to create placement resource snapshot", true, "snapshotRevision", newRevision)
	}

	klog.V(2).InfoS("Successfully created placement resource snapshot for the placement policy",
		"snapshot", klog.KObj(newSnapshot), "snapshotRevision", newRevision,
		"placementPolicy", klog.KObj(placementPolicy))
	return newSnapshot, nil
}
