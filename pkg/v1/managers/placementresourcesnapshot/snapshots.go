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
	"strconv"

	placementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	errors "github.com/kubefleet-dev/kubefleet/pkg/utils/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func primaryPlacementResourceSnapshot(
	namespace, name string,
	ownerPlacementPolicy placementv1alpha1.PlacementPolicyAccessor,
	idx int,
	resources []placementv1alpha1.SnapshottedResource,
	resourceHash string,
	snapshotCount int,
	scheme *runtime.Scheme,
) (placementv1alpha1.PlacementResourceSnapshotAccessor, error) {
	labels := map[string]string{
		placementv1alpha1.PlacementResourceSnapshotOwnedByLabelKey:         ownerPlacementPolicy.GetName(),
		placementv1alpha1.PlacementResourceSnapshotIndexLabelKey:           strconv.Itoa(idx),
		placementv1alpha1.PlacementResourceSnapshotSubIndexLabelKey:        "0",
		placementv1alpha1.SubIndexedPlacementResourceSnapshotCountLabelKey: strconv.Itoa(snapshotCount),
	}
	annotations := map[string]string{
		placementv1alpha1.PlacementResourceSnapshotContentsHashAnnotationKey: resourceHash,
	}

	var primarySnapshot placementv1alpha1.PlacementResourceSnapshotAccessor
	if namespace == "" {
		primarySnapshot = &placementv1alpha1.ClusterPlacementResourceSnapshot{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Labels:      labels,
				Annotations: annotations,
			},
			Spec: placementv1alpha1.PlacementResourceSnapshotSpec{
				Resources: resources,
			},
		}
	} else {
		primarySnapshot = &placementv1alpha1.PlacementResourceSnapshot{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   namespace,
				Labels:      labels,
				Annotations: annotations,
			},
			Spec: placementv1alpha1.PlacementResourceSnapshotSpec{
				Resources: resources,
			},
		}
	}

	if err := controllerutil.SetControllerReference(ownerPlacementPolicy, primarySnapshot, scheme); err != nil {
		return nil, errors.NewUnexpectedError(err, "failed to set controller reference on the primary placement resource snapshot",
			"manager", managerName, "primaryPlacementResourceSnapshot", klog.KObj(primarySnapshot))
	}
	return primarySnapshot, nil
}

func secondaryPlacementResourceSnapshot(
	namespace, name string,
	ownerPlacementPolicy placementv1alpha1.PlacementPolicyAccessor,
	idx, subIdx int,
	resources []placementv1alpha1.SnapshottedResource,
	resourceHash string,
	scheme *runtime.Scheme,
) (placementv1alpha1.PlacementResourceSnapshotAccessor, error) {
	labels := map[string]string{
		placementv1alpha1.PlacementResourceSnapshotOwnedByLabelKey:  ownerPlacementPolicy.GetName(),
		placementv1alpha1.PlacementResourceSnapshotIndexLabelKey:    strconv.Itoa(idx),
		placementv1alpha1.PlacementResourceSnapshotSubIndexLabelKey: strconv.Itoa(subIdx),
	}
	annotations := map[string]string{
		placementv1alpha1.PlacementResourceSnapshotContentsHashAnnotationKey: resourceHash,
	}

	var secondarySnapshot placementv1alpha1.PlacementResourceSnapshotAccessor
	if namespace == "" {
		secondarySnapshot = &placementv1alpha1.ClusterPlacementResourceSnapshot{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Labels:      labels,
				Annotations: annotations,
			},
			Spec: placementv1alpha1.PlacementResourceSnapshotSpec{
				Resources: resources,
			},
		}
	} else {
		secondarySnapshot = &placementv1alpha1.PlacementResourceSnapshot{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   namespace,
				Labels:      labels,
				Annotations: annotations,
			},
			Spec: placementv1alpha1.PlacementResourceSnapshotSpec{
				Resources: resources,
			},
		}
	}

	if err := controllerutil.SetControllerReference(ownerPlacementPolicy, secondarySnapshot, scheme); err != nil {
		return nil, errors.NewUnexpectedError(err, "failed to set controller reference on a secondary placement resource snapshot",
			"manager", managerName, "secondaryPlacementResourceSnapshot", klog.KObj(secondarySnapshot))
	}
	return secondarySnapshot, nil
}
