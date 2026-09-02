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

package fieldindexers

import (
	"context"
	"fmt"

	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	placementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/errors"
)

const (
	// The field-based indexes set up for KubeFleet API objects.
	//
	// Important: many KubeFleet components run under the assumption that proper custom fields
	// have been added and indexed in the cache when running. Failure to complete such prior setup **before
	// the manager starts** will result in unexpected behaviors. Make sure that all applicable components
	// are properly set up using the client provided by the hub controller manager, and `SetupWithManager` is
	// called before the manager starts.

	// PlacementResourceSnapshotOwnedByAndSubIndexedCustomFieldName is the name of the custom field that indexes
	// placement resource snapshots by their owner placement policies and their sub-indices.
	//
	// This is added to help the placement resource snapshot manager retrieve all primary placement resource
	// snapshots (i.e., those with a sub-index of 0) associated with a placement policy.
	PlacementResourceSnapshotOwnedByAndSubIndexedCustomFieldName = "ownedByWithSubIndex"

	// PlacementResourceSnapshotOwnedByAndIndexedCustomFieldName is the name of the custom field that indexes
	// placement resource snapshots by their owner placement policies and their indices.
	//
	// This is added to help the placement resource snapshot manager retrieve all placement resource snapshots of a
	// specific index associated with a placement policy.
	PlacementResourceSnapshotOwnedByAndIndexedCustomFieldName = "ownedByWithIndex"
)

const (
	// The format of the custom field values for the field-based indexes defined above.

	// PlacementResourceSnapshotOwnedByAndSubIndexedCustomFieldValFmt is used to format the value for the custom field,
	// `PlacementResourceSnapshotOwnedByAndSubIndexedCustomFieldName`.
	//
	// Note that slashes are used to avoid unexpected collisions.
	PlacementResourceSnapshotOwnedByAndSubIndexedCustomFieldValFmt = "%s/%s"

	// PlacementResourceSnapshotOwnedByAndIndexedCustomFieldValFmt is used to format the value for the custom field,
	// `PlacementResourceSnapshotOwnedByAndIndexedCustomFieldName`.
	//
	// Note that slashes are used to avoid unexpected collisions.
	PlacementResourceSnapshotOwnedByAndIndexedCustomFieldValFmt = "%s/%s"
)

// IndexPlacementResourceSnapshotOwnedByAndSubIndexedField indexes placement resource snapshots by their
// owner label value and sub-index label value.
func IndexPlacementResourceSnapshotOwnedByAndSubIndexedField(ctx context.Context, fieldIdxer client.FieldIndexer) error {
	if err := fieldIdxer.IndexField(ctx, &placementv1alpha1.PlacementResourceSnapshot{}, PlacementResourceSnapshotOwnedByAndSubIndexedCustomFieldName, func(rawObj client.Object) []string {
		snapshot, ok := rawObj.(*placementv1alpha1.PlacementResourceSnapshot)
		if !ok {
			wrappedErr := errors.NewUnexpectedError(nil, "failed to convert object to placement resource snapshot",
				"object", klog.KObj(rawObj))
			klog.ErrorS(wrappedErr, "failed to index placement resource snapshot by owner and sub-index", errors.Args(wrappedErr)...)
			return nil
		}

		ownedBy := snapshot.GetLabels()[placementv1alpha1.PlacementResourceSnapshotOwnedByLabelKey]
		subIndex := snapshot.GetLabels()[placementv1alpha1.PlacementResourceSnapshotSubIndexLabelKey]
		if ownedBy == "" || subIndex == "" {
			wrappedErr := errors.NewUnexpectedError(nil, "placement resource snapshot is missing required labels",
				"placementResourceSnapshot", klog.KObj(snapshot))
			klog.ErrorS(wrappedErr, "failed to index placement resource snapshot by owner and sub-index", errors.Args(wrappedErr)...)
			return nil
		}

		v := fmt.Sprintf(PlacementResourceSnapshotOwnedByAndSubIndexedCustomFieldValFmt, ownedBy, subIndex)
		return []string{v}
	}); err != nil {
		return errors.NewUnexpectedError(err, "failed to index placement resource snapshots by owner and sub-index")
	}
	return nil
}

// IndexPlacementResourceSnapshotOwnedByAndIndexedField indexes placement resource snapshots by their
// owner label value and index label value.
func IndexPlacementResourceSnapshotOwnedByAndIndexedField(ctx context.Context, fieldIdxer client.FieldIndexer) error {
	if err := fieldIdxer.IndexField(ctx, &placementv1alpha1.PlacementResourceSnapshot{}, PlacementResourceSnapshotOwnedByAndIndexedCustomFieldName, func(rawObj client.Object) []string {
		snapshot, ok := rawObj.(*placementv1alpha1.PlacementResourceSnapshot)
		if !ok {
			wrappedErr := errors.NewUnexpectedError(nil, "failed to convert object to placement resource snapshot",
				"object", klog.KObj(rawObj))
			klog.ErrorS(wrappedErr, "failed to index placement resource snapshot by owner and index", errors.Args(wrappedErr)...)
			return nil
		}

		ownedBy := snapshot.GetLabels()[placementv1alpha1.PlacementResourceSnapshotOwnedByLabelKey]
		index := snapshot.GetLabels()[placementv1alpha1.PlacementResourceSnapshotIndexLabelKey]
		if ownedBy == "" || index == "" {
			wrappedErr := errors.NewUnexpectedError(nil, "placement resource snapshot is missing required labels",
				"placementResourceSnapshot", klog.KObj(snapshot))
			klog.ErrorS(wrappedErr, "failed to index placement resource snapshot by owner and index", errors.Args(wrappedErr)...)
			return nil
		}

		v := fmt.Sprintf(PlacementResourceSnapshotOwnedByAndIndexedCustomFieldValFmt, ownedBy, index)
		return []string{v}
	}); err != nil {
		return errors.NewUnexpectedError(err, "failed to index placement resource snapshots by owner and index")
	}
	return nil
}

// IndexClusterPlacementResourceSnapshotOwnedByAndSubIndexedField indexes cluster placement resource snapshots
// by their owner label value and sub-index label value.
func IndexClusterPlacementResourceSnapshotOwnedByAndSubIndexedField(ctx context.Context, fieldIdxer client.FieldIndexer) error {
	if err := fieldIdxer.IndexField(ctx, &placementv1alpha1.ClusterPlacementResourceSnapshot{}, PlacementResourceSnapshotOwnedByAndSubIndexedCustomFieldName, func(rawObj client.Object) []string {
		snapshot, ok := rawObj.(*placementv1alpha1.ClusterPlacementResourceSnapshot)
		if !ok {
			wrappedErr := errors.NewUnexpectedError(nil, "failed to convert object to cluster placement resource snapshot",
				"object", klog.KObj(rawObj))
			klog.ErrorS(wrappedErr, "failed to index cluster placement resource snapshot by owner and sub-index", errors.Args(wrappedErr)...)
			return nil
		}

		ownedBy := snapshot.GetLabels()[placementv1alpha1.PlacementResourceSnapshotOwnedByLabelKey]
		subIndex := snapshot.GetLabels()[placementv1alpha1.PlacementResourceSnapshotSubIndexLabelKey]
		if ownedBy == "" || subIndex == "" {
			wrappedErr := errors.NewUnexpectedError(nil, "cluster placement resource snapshot is missing required labels",
				"clusterPlacementResourceSnapshot", klog.KObj(snapshot))
			klog.ErrorS(wrappedErr, "failed to index cluster placement resource snapshot by owner and sub-index", errors.Args(wrappedErr)...)
			return nil
		}

		v := fmt.Sprintf(PlacementResourceSnapshotOwnedByAndSubIndexedCustomFieldValFmt, ownedBy, subIndex)
		return []string{v}
	}); err != nil {
		return errors.NewUnexpectedError(err, "failed to index cluster placement resource snapshots by owner and sub-index")
	}
	return nil
}

// IndexClusterPlacementResourceSnapshotOwnedByAndIndexedField indexes cluster placement resource snapshots
// by their owner label value and index label value.
func IndexClusterPlacementResourceSnapshotOwnedByAndIndexedField(ctx context.Context, fieldIdxer client.FieldIndexer) error {
	if err := fieldIdxer.IndexField(ctx, &placementv1alpha1.ClusterPlacementResourceSnapshot{}, PlacementResourceSnapshotOwnedByAndIndexedCustomFieldName, func(rawObj client.Object) []string {
		snapshot, ok := rawObj.(*placementv1alpha1.ClusterPlacementResourceSnapshot)
		if !ok {
			wrappedErr := errors.NewUnexpectedError(nil, "failed to convert object to cluster placement resource snapshot",
				"object", klog.KObj(rawObj))
			klog.ErrorS(wrappedErr, "failed to index cluster placement resource snapshot by owner and index", errors.Args(wrappedErr)...)
			return nil
		}

		ownedBy := snapshot.GetLabels()[placementv1alpha1.PlacementResourceSnapshotOwnedByLabelKey]
		index := snapshot.GetLabels()[placementv1alpha1.PlacementResourceSnapshotIndexLabelKey]
		if ownedBy == "" || index == "" {
			wrappedErr := errors.NewUnexpectedError(nil, "cluster placement resource snapshot is missing required labels",
				"clusterPlacementResourceSnapshot", klog.KObj(snapshot))
			klog.ErrorS(wrappedErr, "failed to index cluster placement resource snapshot by owner and index", errors.Args(wrappedErr)...)
			return nil
		}

		v := fmt.Sprintf(PlacementResourceSnapshotOwnedByAndIndexedCustomFieldValFmt, ownedBy, index)
		return []string{v}
	}); err != nil {
		return errors.NewUnexpectedError(err, "failed to index cluster placement resource snapshots by owner and index")
	}
	return nil
}

// SetupWithManager sets up the indices the placement resource snapshot manager needs to run properly.
// It must be called before the manager starts.
func SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	fieldIdxer := mgr.GetFieldIndexer()

	// Index placement resource snapshots by their owner placement policies and their sub-indices.
	if err := IndexPlacementResourceSnapshotOwnedByAndSubIndexedField(ctx, fieldIdxer); err != nil {
		return errors.Wraps(err, "failed to set up placement resource snapshot owner and sub-index field index")
	}

	// Index placement resource snapshots by their owner placement policies and their indices.
	if err := IndexPlacementResourceSnapshotOwnedByAndIndexedField(ctx, fieldIdxer); err != nil {
		return errors.Wraps(err, "failed to set up placement resource snapshot owner and index field index")
	}

	// Index cluster placement resource snapshots by their owner placement policies and their sub-indices.
	if err := IndexClusterPlacementResourceSnapshotOwnedByAndSubIndexedField(ctx, fieldIdxer); err != nil {
		return errors.Wraps(err, "failed to set up cluster placement resource snapshot owner and sub-index field index")
	}

	// Index cluster placement resource snapshots by their owner placement policies and their indices.
	if err := IndexClusterPlacementResourceSnapshotOwnedByAndIndexedField(ctx, fieldIdxer); err != nil {
		return errors.Wraps(err, "failed to set up cluster placement resource snapshot owner and index field index")
	}

	return nil
}
