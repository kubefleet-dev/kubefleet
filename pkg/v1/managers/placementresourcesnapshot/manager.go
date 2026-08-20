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
	"hash/fnv"
	"sync"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	placementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	errors "github.com/kubefleet-dev/kubefleet/pkg/utils/errors"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/informer"
)

const (
	managerName = "placementresourcesnapshot"
)

const (
	// The two custom fields that are used to index placement resource snapshots in the cache.
	//
	// Important: the placement resource snapshot manager runs under the assumption that proper custom fields
	// have been added and indexed in the cache when running. Failure to complete such prior setup **before
	// the manager starts** will result in unexpected behaviors. Make sure that the manager uses the client
	// provided by the hub controller manager, and `SetupWithManager` is called before the manager starts.

	// ownedByAndSubIndexedCustomFieldName is the name of the custom field that indexes placement resource
	// snapshots by their owner placement policies and their sub-indices.
	//
	// This is added to help the manager retrieve all primary placement resource snapshots (i.e., those
	// with a sub-index of 0) associated with a placement policy.
	ownedByAndSubIndexedCustomFieldName = "ownedByWithSubIndex"
	// ownedByAndIndexedCustomFieldName is the name of the custom field that indexes placement resource snapshots by
	// their owner placement policies and their indices.
	//
	// This is added to help the manager retrieve all placement resource snapshots of a specific index
	// associated with a placement policy.
	ownedByAndIndexedCustomFieldName = "ownedByWithIndex"

	// The format of the custom field values for the two custom fields above.
	//
	// Note that slashes are used to avoid unexpected collisions.
	ownedByAndSubIndexedCustomFieldFmt = "%s/%s"
	ownedByAndIndexedCustomFieldFmt    = "%s/%s"
)

const (
	// The format of the key used to find the mutex for a placement policy in the mutex array.
	//
	// Note that slashes are used to avoid unexpected collisions.
	placementPolicyKeyFmt = "%s/%s"

	minSlotCnt = 16
)

type Manager struct {
	hubClient                 client.Client
	hubDynamicClient          dynamic.Interface
	hubDynamicInformerManager informer.Manager

	restMapper meta.RESTMapper

	mus       []sync.Mutex
	muSlotCnt int32
}

// New returns a new Manager.
func New(mgr ctrl.Manager,
	hubDynamicClient dynamic.Interface,
	hubDynamicInformerManager informer.Manager,
	restMapper meta.RESTMapper,
	muSlotCnt int32,
) (*Manager, error) {
	if muSlotCnt < minSlotCnt {
		return nil, errors.NewUserError(nil, "mu slot size must be greater than or equal to the minimum limit",
			"manager", managerName, "limit", minSlotCnt, "actual", muSlotCnt)
	}

	return &Manager{
		hubClient:                 mgr.GetClient(),
		hubDynamicClient:          hubDynamicClient,
		hubDynamicInformerManager: hubDynamicInformerManager,
		restMapper:                restMapper,
		mus:                       make([]sync.Mutex, muSlotCnt),
		muSlotCnt:                 muSlotCnt,
	}, nil
}

// SetupWithManager sets up the indices the placement resource snapshot manager needs to run properly.
// It must be called before the manager starts.
func (m *Manager) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	fieldIdxer := mgr.GetFieldIndexer()

	// Index placement resource snapshots by their owner placement policies and their sub-indices.
	if err := fieldIdxer.IndexField(ctx, &placementv1alpha1.PlacementResourceSnapshot{}, ownedByAndSubIndexedCustomFieldName, func(rawObj client.Object) []string {
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

		v := fmt.Sprintf(ownedByAndSubIndexedCustomFieldFmt, ownedBy, subIndex)
		return []string{v}
	}); err != nil {
		return errors.NewUnexpectedError(err, "failed to index placement resource snapshots by owner and sub-index", "manager", managerName)
	}

	// Index placement resource snapshots by their owner placement policies and their indices.
	if err := fieldIdxer.IndexField(ctx, &placementv1alpha1.PlacementResourceSnapshot{}, ownedByAndIndexedCustomFieldName, func(rawObj client.Object) []string {
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

		v := fmt.Sprintf(ownedByAndIndexedCustomFieldFmt, ownedBy, index)
		return []string{v}
	}); err != nil {
		return errors.NewUnexpectedError(err, "failed to index placement resource snapshots by owner and index", "manager", managerName)
	}

	// Index cluster placement resource snapshots by their owner placement policies and their sub-indices.
	if err := fieldIdxer.IndexField(ctx, &placementv1alpha1.ClusterPlacementResourceSnapshot{}, ownedByAndSubIndexedCustomFieldName, func(rawObj client.Object) []string {
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

		v := fmt.Sprintf(ownedByAndSubIndexedCustomFieldFmt, ownedBy, subIndex)
		return []string{v}
	}); err != nil {
		return errors.NewUnexpectedError(err, "failed to index cluster placement resource snapshots by owner and sub-index", "manager", managerName)
	}

	// Index cluster placement resource snapshots by their owner placement policies and their indices.
	if err := fieldIdxer.IndexField(ctx, &placementv1alpha1.ClusterPlacementResourceSnapshot{}, ownedByAndIndexedCustomFieldName, func(rawObj client.Object) []string {
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

		v := fmt.Sprintf(ownedByAndIndexedCustomFieldFmt, ownedBy, index)
		return []string{v}
	}); err != nil {
		return errors.NewUnexpectedError(err, "failed to index cluster placement resource snapshots by owner and index", "manager", managerName)
	}

	return nil
}

func (m *Manager) acquireLock(placementPolicy placementv1alpha1.PlacementPolicyAccessor) {
	placementPolicyKey := fmt.Sprintf(placementPolicyKeyFmt, placementPolicy.GetNamespace(), placementPolicy.GetName())

	hasher := fnv.New32a()
	hasher.Write([]byte(placementPolicyKey))

	slot := int(hasher.Sum32() % uint32(m.muSlotCnt))
	m.mus[slot].Lock()
}

func (m *Manager) releaseLock(placementPolicy placementv1alpha1.PlacementPolicyAccessor) {
	placementPolicyKey := fmt.Sprintf(placementPolicyKeyFmt, placementPolicy.GetNamespace(), placementPolicy.GetName())

	hasher := fnv.New32a()
	hasher.Write([]byte(placementPolicyKey))

	slot := int(hasher.Sum32() % uint32(m.muSlotCnt))
	m.mus[slot].Unlock()
}
