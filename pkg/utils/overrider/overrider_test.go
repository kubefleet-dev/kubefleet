/*
Copyright 2025 The KubeFleet Authors.

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

package overrider

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/controller"
	"github.com/kubefleet-dev/kubefleet/test/utils/informer"
	"github.com/kubefleet-dev/kubefleet/test/utils/resource"
)

var (
	crpName = "my-test-crp"
	rpName  = "my-test-rp"
)

// ensureSnapshotTrackingLabels populates the OverrideTrackingLabel and OverrideIndexLabel on
// snapshot fixtures that may have only declared the IsLatestSnapshotLabel. In production both
// controllers always set these labels at Create time; without them, read-time dedup correctly
// fails fast as malformed input.
func ensureSnapshotTrackingLabels(meta *metav1.ObjectMeta, parent string, index int) {
	if meta.Labels == nil {
		meta.Labels = map[string]string{}
	}
	if _, ok := meta.Labels[placementv1beta1.OverrideTrackingLabel]; !ok {
		meta.Labels[placementv1beta1.OverrideTrackingLabel] = parent
	}
	if _, ok := meta.Labels[placementv1beta1.OverrideIndexLabel]; !ok {
		meta.Labels[placementv1beta1.OverrideIndexLabel] = fmt.Sprintf("%d", index)
	}
}

func serviceScheme(t *testing.T) *runtime.Scheme {
	scheme := runtime.NewScheme()
	if err := placementv1beta1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add placement v1beta1 scheme: %v", err)
	}
	if err := clusterv1beta1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add cluster v1beta1 scheme: %v", err)
	}
	return scheme
}

func TestFetchAllMatchingOverridesForResourceSnapshot(t *testing.T) {
	fakeInformer := informer.FakeManager{
		APIResources: map[schema.GroupVersionKind]bool{
			{
				Group:   "",
				Version: "v1",
				Kind:    "Service",
			}: true,
			{
				Group:   "apps",
				Version: "v1",
				Kind:    "Deployment",
			}: true,
			{
				Group:   "",
				Version: "v1",
				Kind:    "Secret",
			}: true,
		},
		IsClusterScopedResource: false,
	}

	tests := []struct {
		name         string
		placementKey string
		master       placementv1beta1.ResourceSnapshotObj
		snapshots    []placementv1beta1.ResourceSnapshotObj
		croList      []placementv1beta1.ClusterResourceOverrideSnapshot
		roList       []placementv1beta1.ResourceOverrideSnapshot
		wantCRO      []*placementv1beta1.ClusterResourceOverrideSnapshot
		wantRO       []*placementv1beta1.ResourceOverrideSnapshot
	}{
		{
			name:         "single resource snapshot selecting empty resources",
			placementKey: crpName,
			master: &placementv1beta1.ClusterResourceSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf(placementv1beta1.ResourceSnapshotNameFmt, crpName, 0),
					Labels: map[string]string{
						placementv1beta1.ResourceIndexLabel:     "0",
						placementv1beta1.PlacementTrackingLabel: crpName,
					},
					Annotations: map[string]string{
						placementv1beta1.ResourceGroupHashAnnotation:         "abc",
						placementv1beta1.NumberOfResourceSnapshotsAnnotation: "1",
					},
				},
			},
			croList: []placementv1beta1.ClusterResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cro-1",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{},
				},
			},
			wantCRO: []*placementv1beta1.ClusterResourceOverrideSnapshot{},
			wantRO:  []*placementv1beta1.ResourceOverrideSnapshot{},
		},
		{
			name:         "single resource snapshot with no matched overrides",
			placementKey: crpName,
			master: &placementv1beta1.ClusterResourceSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf(placementv1beta1.ResourceSnapshotNameFmt, crpName, 0),
					Labels: map[string]string{
						placementv1beta1.ResourceIndexLabel:     "0",
						placementv1beta1.PlacementTrackingLabel: crpName,
					},
					Annotations: map[string]string{
						placementv1beta1.ResourceGroupHashAnnotation:         "abc",
						placementv1beta1.NumberOfResourceSnapshotsAnnotation: "1",
					},
				},
				Spec: placementv1beta1.ResourceSnapshotSpec{
					SelectedResources: []placementv1beta1.ResourceContent{
						*resource.ServiceResourceContentForTest(t),
					},
				},
			},
			croList: []placementv1beta1.ClusterResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cro-1",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ClusterResourceOverrideSpec{
							ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
								{
									Group:   "rbac.authorization.k8s.io",
									Version: "v1",
									Kind:    "ClusterRole",
									Name:    "test-cluster-role",
								},
							},
						},
					},
				},
			},
			roList: []placementv1beta1.ResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "ro-1",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{
									Group:   "rbac.authorization.k8s.io",
									Version: "v1",
									Kind:    "Role",
									Name:    "test-role",
								},
							},
						},
					},
				},
			},
			wantCRO: []*placementv1beta1.ClusterResourceOverrideSnapshot{},
			wantRO:  []*placementv1beta1.ResourceOverrideSnapshot{},
		},
		{
			name: "single resource snapshot with matched cro and ro",
			master: &placementv1beta1.ClusterResourceSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf(placementv1beta1.ResourceSnapshotNameFmt, crpName, 0),
					Labels: map[string]string{
						placementv1beta1.ResourceIndexLabel:     "0",
						placementv1beta1.PlacementTrackingLabel: crpName,
					},
					Annotations: map[string]string{
						placementv1beta1.ResourceGroupHashAnnotation:         "abc",
						placementv1beta1.NumberOfResourceSnapshotsAnnotation: "1",
					},
				},
				Spec: placementv1beta1.ResourceSnapshotSpec{
					SelectedResources: []placementv1beta1.ResourceContent{
						*resource.ServiceResourceContentForTest(t),
					},
				},
			},
			croList: []placementv1beta1.ClusterResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cro-1",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ClusterResourceOverrideSpec{
							ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
								{
									Group:   "",
									Version: "v1",
									Kind:    "Namespace",
									Name:    "svc-namespace",
								},
							},
						},
					},
				},
			},
			roList: []placementv1beta1.ResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-1",
						Namespace: "svc-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{
									Group:   "",
									Version: "v1",
									Kind:    "Service",
									Name:    "svc-name",
								},
							},
						},
					},
				},
			},
			wantCRO: []*placementv1beta1.ClusterResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cro-1",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ClusterResourceOverrideSpec{
							ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
								{
									Group:   "",
									Version: "v1",
									Kind:    "Namespace",
									Name:    "svc-namespace",
								},
							},
						},
					},
				},
			},
			wantRO: []*placementv1beta1.ResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-1",
						Namespace: "svc-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{
									Group:   "",
									Version: "v1",
									Kind:    "Service",
									Name:    "svc-name",
								},
							},
						},
					},
				},
			},
		},
		{
			name:         "single resource snapshot with matched stale cro and ro snapshot",
			placementKey: crpName,
			master: &placementv1beta1.ClusterResourceSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf(placementv1beta1.ResourceSnapshotNameFmt, crpName, 0),
					Labels: map[string]string{
						placementv1beta1.ResourceIndexLabel:     "0",
						placementv1beta1.PlacementTrackingLabel: crpName,
					},
					Annotations: map[string]string{
						placementv1beta1.ResourceGroupHashAnnotation:         "abc",
						placementv1beta1.NumberOfResourceSnapshotsAnnotation: "1",
					},
				},
				Spec: placementv1beta1.ResourceSnapshotSpec{
					SelectedResources: []placementv1beta1.ResourceContent{
						*resource.ServiceResourceContentForTest(t),
					},
				},
			},
			croList: []placementv1beta1.ClusterResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cro-1",
					},
					Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ClusterResourceOverrideSpec{
							ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
								{
									Group:   "",
									Version: "v1",
									Kind:    "Namespace",
									Name:    "svc-namespace",
								},
							},
						},
					},
				},
			},
			roList: []placementv1beta1.ResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-1",
						Namespace: "svc-namespace",
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{
									Group:   "",
									Version: "v1",
									Kind:    "Service",
									Name:    "svc-name",
								},
							},
						},
					},
				},
			},
			wantCRO: []*placementv1beta1.ClusterResourceOverrideSnapshot{},
			wantRO:  []*placementv1beta1.ResourceOverrideSnapshot{},
		},
		{
			name:         "multiple resource snapshots with matched cro and ro",
			placementKey: crpName,
			master: &placementv1beta1.ClusterResourceSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf(placementv1beta1.ResourceSnapshotNameFmt, crpName, 0),
					Labels: map[string]string{
						placementv1beta1.ResourceIndexLabel:     "0",
						placementv1beta1.PlacementTrackingLabel: crpName,
					},
					Annotations: map[string]string{
						placementv1beta1.ResourceGroupHashAnnotation:         "abc",
						placementv1beta1.NumberOfResourceSnapshotsAnnotation: "3",
					},
				},
				Spec: placementv1beta1.ResourceSnapshotSpec{
					SelectedResources: []placementv1beta1.ResourceContent{
						*resource.NamespaceResourceContentForTest(t),
						*resource.ServiceResourceContentForTest(t),
					},
				},
			},
			snapshots: []placementv1beta1.ResourceSnapshotObj{
				&placementv1beta1.ClusterResourceSnapshot{
					ObjectMeta: metav1.ObjectMeta{
						Name: fmt.Sprintf(placementv1beta1.ResourceSnapshotNameWithSubindexFmt, crpName, 0, 0),
						Labels: map[string]string{
							placementv1beta1.ResourceIndexLabel:     "0",
							placementv1beta1.PlacementTrackingLabel: crpName,
						},
						Annotations: map[string]string{
							placementv1beta1.SubindexOfResourceSnapshotAnnotation: "0",
						},
					},
					Spec: placementv1beta1.ResourceSnapshotSpec{
						SelectedResources: []placementv1beta1.ResourceContent{
							*resource.DeploymentResourceContentForTest(t),
						},
					},
				},
				&placementv1beta1.ClusterResourceSnapshot{
					ObjectMeta: metav1.ObjectMeta{
						Name: fmt.Sprintf(placementv1beta1.ResourceSnapshotNameWithSubindexFmt, crpName, 0, 1),
						Labels: map[string]string{
							placementv1beta1.ResourceIndexLabel:     "0",
							placementv1beta1.PlacementTrackingLabel: crpName,
						},
						Annotations: map[string]string{
							placementv1beta1.SubindexOfResourceSnapshotAnnotation: "1",
						},
					},
					Spec: placementv1beta1.ResourceSnapshotSpec{
						SelectedResources: []placementv1beta1.ResourceContent{
							*resource.ClusterRoleResourceContentForTest(t),
						},
					},
				},
			},
			croList: []placementv1beta1.ClusterResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cro-1",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ClusterResourceOverrideSpec{
							ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
								{
									Group:   "rbac.authorization.k8s.io",
									Version: "v1",
									Kind:    "ClusterRole",
									Name:    "not-exist",
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cro-2",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ClusterResourceOverrideSpec{
							ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
								{
									Group:   "rbac.authorization.k8s.io",
									Version: "v1",
									Kind:    "ClusterRole",
									Name:    "clusterrole-name",
								},
							},
						},
					},
				},
			},
			roList: []placementv1beta1.ResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-1",
						Namespace: "svc-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{
									Group:   "apps",
									Version: "v1",
									Kind:    "Deployment",
									Name:    "not-exist",
								},
								{
									Group:   "",
									Version: "v1",
									Kind:    "Service",
									Name:    "svc-name",
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-2",
						Namespace: "deployment-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{
									Group:   "apps",
									Version: "v1",
									Kind:    "Deployment",
									Name:    "deployment-name",
								},
							},
						},
					},
				},
			},
			wantCRO: []*placementv1beta1.ClusterResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cro-2",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ClusterResourceOverrideSpec{
							ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
								{
									Group:   "rbac.authorization.k8s.io",
									Version: "v1",
									Kind:    "ClusterRole",
									Name:    "clusterrole-name",
								},
							},
						},
					},
				},
			},
			wantRO: []*placementv1beta1.ResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-2",
						Namespace: "deployment-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{
									Group:   "apps",
									Version: "v1",
									Kind:    "Deployment",
									Name:    "deployment-name",
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-1",
						Namespace: "svc-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{
									Group:   "apps",
									Version: "v1",
									Kind:    "Deployment",
									Name:    "not-exist",
								},
								{
									Group:   "",
									Version: "v1",
									Kind:    "Service",
									Name:    "svc-name",
								},
							},
						},
					},
				},
			},
		},
		{
			name:         "multiple resource snapshots with matched cro and ro by specifying the placement name",
			placementKey: crpName,
			master: &placementv1beta1.ClusterResourceSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf(placementv1beta1.ResourceSnapshotNameFmt, crpName, 0),
					Labels: map[string]string{
						placementv1beta1.ResourceIndexLabel:     "0",
						placementv1beta1.PlacementTrackingLabel: crpName,
					},
					Annotations: map[string]string{
						placementv1beta1.ResourceGroupHashAnnotation:         "abc",
						placementv1beta1.NumberOfResourceSnapshotsAnnotation: "3",
					},
				},
				Spec: placementv1beta1.ResourceSnapshotSpec{
					SelectedResources: []placementv1beta1.ResourceContent{
						*resource.NamespaceResourceContentForTest(t),
						*resource.ServiceResourceContentForTest(t),
					},
				},
			},
			snapshots: []placementv1beta1.ResourceSnapshotObj{
				&placementv1beta1.ClusterResourceSnapshot{
					ObjectMeta: metav1.ObjectMeta{
						Name: fmt.Sprintf(placementv1beta1.ResourceSnapshotNameWithSubindexFmt, crpName, 0, 0),
						Labels: map[string]string{
							placementv1beta1.ResourceIndexLabel:     "0",
							placementv1beta1.PlacementTrackingLabel: crpName,
						},
						Annotations: map[string]string{
							placementv1beta1.SubindexOfResourceSnapshotAnnotation: "0",
						},
					},
					Spec: placementv1beta1.ResourceSnapshotSpec{
						SelectedResources: []placementv1beta1.ResourceContent{
							*resource.DeploymentResourceContentForTest(t),
						},
					},
				},
				&placementv1beta1.ClusterResourceSnapshot{
					ObjectMeta: metav1.ObjectMeta{
						Name: fmt.Sprintf(placementv1beta1.ResourceSnapshotNameWithSubindexFmt, crpName, 0, 1),
						Labels: map[string]string{
							placementv1beta1.ResourceIndexLabel:     "0",
							placementv1beta1.PlacementTrackingLabel: crpName,
						},
						Annotations: map[string]string{
							placementv1beta1.SubindexOfResourceSnapshotAnnotation: "1",
						},
					},
					Spec: placementv1beta1.ResourceSnapshotSpec{
						SelectedResources: []placementv1beta1.ResourceContent{
							*resource.ClusterRoleResourceContentForTest(t),
						},
					},
				},
			},
			croList: []placementv1beta1.ClusterResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cro-1",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ClusterResourceOverrideSpec{
							Placement: &placementv1beta1.PlacementRef{
								Name: crpName,
							},
							ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
								{
									Group:   "rbac.authorization.k8s.io",
									Version: "v1",
									Kind:    "ClusterRole",
									Name:    "not-exist",
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cro-2",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ClusterResourceOverrideSpec{
							ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
								{
									Group:   "rbac.authorization.k8s.io",
									Version: "v1",
									Kind:    "ClusterRole",
									Name:    "clusterrole-name",
								},
							},
						},
					},
				},
			},
			roList: []placementv1beta1.ResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-1",
						Namespace: "svc-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{
									Group:   "apps",
									Version: "v1",
									Kind:    "Deployment",
									Name:    "not-exist",
								},
								{
									Group:   "",
									Version: "v1",
									Kind:    "Service",
									Name:    "svc-name",
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-2",
						Namespace: "deployment-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							Placement: &placementv1beta1.PlacementRef{
								Name: crpName,
							},
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{
									Group:   "apps",
									Version: "v1",
									Kind:    "Deployment",
									Name:    "deployment-name",
								},
							},
						},
					},
				},
			},
			wantCRO: []*placementv1beta1.ClusterResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cro-2",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ClusterResourceOverrideSpec{
							ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
								{
									Group:   "rbac.authorization.k8s.io",
									Version: "v1",
									Kind:    "ClusterRole",
									Name:    "clusterrole-name",
								},
							},
						},
					},
				},
			},
			wantRO: []*placementv1beta1.ResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-2",
						Namespace: "deployment-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							Placement: &placementv1beta1.PlacementRef{
								Name: crpName,
							},
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{
									Group:   "apps",
									Version: "v1",
									Kind:    "Deployment",
									Name:    "deployment-name",
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-1",
						Namespace: "svc-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{
									Group:   "apps",
									Version: "v1",
									Kind:    "Deployment",
									Name:    "not-exist",
								},
								{
									Group:   "",
									Version: "v1",
									Kind:    "Service",
									Name:    "svc-name",
								},
							},
						},
					},
				},
			},
		},
		{
			// not supported in the first phase
			name:         "single resource snapshot with multiple matched cro and ro",
			placementKey: crpName,
			master: &placementv1beta1.ClusterResourceSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf(placementv1beta1.ResourceSnapshotNameFmt, crpName, 0),
					Labels: map[string]string{
						placementv1beta1.ResourceIndexLabel:     "0",
						placementv1beta1.PlacementTrackingLabel: crpName,
					},
					Annotations: map[string]string{
						placementv1beta1.ResourceGroupHashAnnotation:         "abc",
						placementv1beta1.NumberOfResourceSnapshotsAnnotation: "1",
					},
				},
				Spec: placementv1beta1.ResourceSnapshotSpec{
					SelectedResources: []placementv1beta1.ResourceContent{
						*resource.ServiceResourceContentForTest(t),
					},
				},
			},
			croList: []placementv1beta1.ClusterResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cro-1",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ClusterResourceOverrideSpec{
							ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
								{
									Group:   "",
									Version: "v1",
									Kind:    "Namespace",
									Name:    "svc-namespace",
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cro-2",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ClusterResourceOverrideSpec{
							ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
								{
									Group:   "",
									Version: "v1",
									Kind:    "Namespace",
									Name:    "svc-namespace",
								},
							},
						},
					},
				},
			},
			roList: []placementv1beta1.ResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-1",
						Namespace: "svc-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{
									Group:   "",
									Version: "v1",
									Kind:    "Service",
									Name:    "svc-name",
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-2",
						Namespace: "svc-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{
									Group:   "",
									Version: "v1",
									Kind:    "Service",
									Name:    "svc-name",
								},
							},
						},
					},
				},
			},
			wantCRO: []*placementv1beta1.ClusterResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cro-1",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ClusterResourceOverrideSpec{
							ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
								{
									Group:   "",
									Version: "v1",
									Kind:    "Namespace",
									Name:    "svc-namespace",
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cro-2",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ClusterResourceOverrideSpec{
							ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
								{
									Group:   "",
									Version: "v1",
									Kind:    "Namespace",
									Name:    "svc-namespace",
								},
							},
						},
					},
				},
			},
			wantRO: []*placementv1beta1.ResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-1",
						Namespace: "svc-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{
									Group:   "",
									Version: "v1",
									Kind:    "Service",
									Name:    "svc-name",
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-2",
						Namespace: "svc-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{
									Group:   "",
									Version: "v1",
									Kind:    "Service",
									Name:    "svc-name",
								},
							},
						},
					},
				},
			},
		},
		{
			name:         "no matched cro and ro which are configured to other placement",
			placementKey: crpName,
			master: &placementv1beta1.ClusterResourceSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf(placementv1beta1.ResourceSnapshotNameFmt, crpName, 0),
					Labels: map[string]string{
						placementv1beta1.ResourceIndexLabel:     "0",
						placementv1beta1.PlacementTrackingLabel: crpName,
					},
					Annotations: map[string]string{
						placementv1beta1.ResourceGroupHashAnnotation:         "abc",
						placementv1beta1.NumberOfResourceSnapshotsAnnotation: "3",
					},
				},
				Spec: placementv1beta1.ResourceSnapshotSpec{
					SelectedResources: []placementv1beta1.ResourceContent{
						*resource.NamespaceResourceContentForTest(t),
						*resource.ServiceResourceContentForTest(t),
					},
				},
			},
			snapshots: []placementv1beta1.ResourceSnapshotObj{
				&placementv1beta1.ClusterResourceSnapshot{
					ObjectMeta: metav1.ObjectMeta{
						Name: fmt.Sprintf(placementv1beta1.ResourceSnapshotNameWithSubindexFmt, crpName, 0, 0),
						Labels: map[string]string{
							placementv1beta1.ResourceIndexLabel:     "0",
							placementv1beta1.PlacementTrackingLabel: crpName,
						},
						Annotations: map[string]string{
							placementv1beta1.SubindexOfResourceSnapshotAnnotation: "0",
						},
					},
					Spec: placementv1beta1.ResourceSnapshotSpec{
						SelectedResources: []placementv1beta1.ResourceContent{
							*resource.DeploymentResourceContentForTest(t),
						},
					},
				},
				&placementv1beta1.ClusterResourceSnapshot{
					ObjectMeta: metav1.ObjectMeta{
						Name: fmt.Sprintf(placementv1beta1.ResourceSnapshotNameWithSubindexFmt, crpName, 0, 1),
						Labels: map[string]string{
							placementv1beta1.ResourceIndexLabel:     "0",
							placementv1beta1.PlacementTrackingLabel: crpName,
						},
						Annotations: map[string]string{
							placementv1beta1.SubindexOfResourceSnapshotAnnotation: "1",
						},
					},
					Spec: placementv1beta1.ResourceSnapshotSpec{
						SelectedResources: []placementv1beta1.ResourceContent{
							*resource.ClusterRoleResourceContentForTest(t),
						},
					},
				},
			},
			croList: []placementv1beta1.ClusterResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cro-1",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ClusterResourceOverrideSpec{
							ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
								{
									Group:   "rbac.authorization.k8s.io",
									Version: "v1",
									Kind:    "ClusterRole",
									Name:    "not-exist",
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cro-2",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ClusterResourceOverrideSpec{
							Placement: &placementv1beta1.PlacementRef{
								Name: "other-placement",
							},
							ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
								{
									Group:   "rbac.authorization.k8s.io",
									Version: "v1",
									Kind:    "ClusterRole",
									Name:    "clusterrole-name",
								},
							},
						},
					},
				},
			},
			roList: []placementv1beta1.ResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-1",
						Namespace: "svc-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							Placement: &placementv1beta1.PlacementRef{
								Name: "other-placement",
							},
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{
									Group:   "apps",
									Version: "v1",
									Kind:    "Deployment",
									Name:    "not-exist",
								},
								{
									Group:   "",
									Version: "v1",
									Kind:    "Service",
									Name:    "svc-name",
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-2",
						Namespace: "deployment-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							Placement: &placementv1beta1.PlacementRef{
								Name: "other-placement",
							},
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{
									Group:   "apps",
									Version: "v1",
									Kind:    "Deployment",
									Name:    "deployment-name",
								},
							},
						},
					},
				},
			},
			wantCRO: []*placementv1beta1.ClusterResourceOverrideSnapshot{},
			wantRO:  []*placementv1beta1.ResourceOverrideSnapshot{},
		},
		{
			name:         "ro match should take placement scope into account",
			placementKey: crpName,
			master: &placementv1beta1.ClusterResourceSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf(placementv1beta1.ResourceSnapshotNameFmt, crpName, 0),
					Labels: map[string]string{
						placementv1beta1.ResourceIndexLabel:     "0",
						placementv1beta1.PlacementTrackingLabel: crpName,
					},
					Annotations: map[string]string{
						placementv1beta1.ResourceGroupHashAnnotation:         "abc",
						placementv1beta1.NumberOfResourceSnapshotsAnnotation: "1",
					},
				},
				Spec: placementv1beta1.ResourceSnapshotSpec{
					SelectedResources: []placementv1beta1.ResourceContent{
						*resource.NamespaceResourceContentForTest(t),
						*resource.ServiceResourceContentForTest(t),
						*resource.DeploymentResourceContentForTest(t),
					},
				},
			},
			roList: []placementv1beta1.ResourceOverrideSnapshot{
				{
					// No OverrideSpec.Placement.Scope specified, should match.
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-1",
						Namespace: "svc-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							Placement: &placementv1beta1.PlacementRef{
								Name: crpName,
							},
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{
									Group:   "",
									Version: "v1",
									Kind:    "Service",
									Name:    "svc-name",
								},
							},
						},
					},
				},
				{
					// OverrideSpec.Placement.Scope specified as Cluster, should match.
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-2",
						Namespace: "deployment-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							Placement: &placementv1beta1.PlacementRef{
								Name:  crpName,
								Scope: placementv1beta1.ClusterScoped,
							},
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{
									Group:   "apps",
									Version: "v1",
									Kind:    "Deployment",
									Name:    "deployment-name",
								},
							},
						},
					},
				},
				{
					// OverrideSpec.Placement.Scope specified as Namespaced, should NOT match.
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-3",
						Namespace: "deployment-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							Placement: &placementv1beta1.PlacementRef{
								Name:  crpName,
								Scope: placementv1beta1.NamespaceScoped,
							},
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{
									Group:   "apps",
									Version: "v1",
									Kind:    "Deployment",
									Name:    "deployment-name",
								},
							},
						},
					},
				},
			},
			wantCRO: []*placementv1beta1.ClusterResourceOverrideSnapshot{},
			wantRO: []*placementv1beta1.ResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-1",
						Namespace: "svc-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							Placement: &placementv1beta1.PlacementRef{
								Name: crpName,
							},
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{
									Group:   "",
									Version: "v1",
									Kind:    "Service",
									Name:    "svc-name",
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-2",
						Namespace: "deployment-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							Placement: &placementv1beta1.PlacementRef{
								Name:  crpName,
								Scope: placementv1beta1.ClusterScoped,
							},
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{
									Group:   "apps",
									Version: "v1",
									Kind:    "Deployment",
									Name:    "deployment-name",
								},
							},
						},
					},
				},
			},
		},
		{
			name:         "ro match with resourceSnapshot",
			placementKey: "deployment-namespace/" + rpName,
			master: &placementv1beta1.ResourceSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf(placementv1beta1.ResourceSnapshotNameFmt, rpName, 0),
					Namespace: "deployment-namespace",
					Labels: map[string]string{
						placementv1beta1.ResourceIndexLabel:     "0",
						placementv1beta1.PlacementTrackingLabel: rpName,
					},
					Annotations: map[string]string{
						placementv1beta1.ResourceGroupHashAnnotation:         "abc",
						placementv1beta1.NumberOfResourceSnapshotsAnnotation: "1",
					},
				},
				Spec: placementv1beta1.ResourceSnapshotSpec{
					SelectedResources: []placementv1beta1.ResourceContent{
						*resource.NamespaceResourceContentForTest(t),
						*resource.ServiceResourceContentForTest(t),
						*resource.DeploymentResourceContentForTest(t),
					},
				},
			},
			roList: []placementv1beta1.ResourceOverrideSnapshot{
				{
					// No OverrideSpec.Placement.Scope specified, should NOT match.
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-1",
						Namespace: "deployment-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							Placement: &placementv1beta1.PlacementRef{
								Name: rpName,
							},
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{
									Group:   "",
									Version: "v1",
									Kind:    "Service",
									Name:    "svc-name",
								},
							},
						},
					},
				},
				{
					// OverrideSpec.Placement.Scope specified as Cluster, should NOT match.
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-2",
						Namespace: "deployment-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							Placement: &placementv1beta1.PlacementRef{
								Name:  rpName,
								Scope: placementv1beta1.ClusterScoped,
							},
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{
									Group:   "apps",
									Version: "v1",
									Kind:    "Deployment",
									Name:    "deployment-name",
								},
							},
						},
					},
				},
				{
					// OverrideSpec.Placement.Scope specified as Namespaced, should match.
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-3",
						Namespace: "deployment-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							Placement: &placementv1beta1.PlacementRef{
								Name:  rpName,
								Scope: placementv1beta1.NamespaceScoped,
							},
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{
									Group:   "apps",
									Version: "v1",
									Kind:    "Deployment",
									Name:    "deployment-name",
								},
							},
						},
					},
				},
				{
					// OverrideSpec.Placement.Name does not exist, should NOT match.
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-4",
						Namespace: "deployment-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							Placement: &placementv1beta1.PlacementRef{
								Name:  "does-not-exist",
								Scope: placementv1beta1.NamespaceScoped,
							},
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{
									Group:   "apps",
									Version: "v1",
									Kind:    "Deployment",
									Name:    "deployment-name",
								},
							},
						},
					},
				},
			},
			wantCRO: []*placementv1beta1.ClusterResourceOverrideSnapshot{},
			wantRO: []*placementv1beta1.ResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-3",
						Namespace: "deployment-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							Placement: &placementv1beta1.PlacementRef{
								Name:  rpName,
								Scope: placementv1beta1.NamespaceScoped,
							},
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{
									Group:   "apps",
									Version: "v1",
									Kind:    "Deployment",
									Name:    "deployment-name",
								},
							},
						},
					},
				},
			},
		},
		{
			// Exercises read-time dedup. Both CRO snapshots for parent "cro-dup" carry
			// IsLatestSnapshotLabel=true (the transient state allowed by the create-first ordering
			// in the override controllers). Both ROs for parent "ro-dup" do too. Dedup must keep
			// only the highest-OverrideIndexLabel snapshot per parent. The lower-index snapshot
			// has a stale spec that selects a different resource, so it would NOT match the master
			// resource snapshot — if dedup leaks it through, wantCRO/wantRO would have two items.
			name:         "duplicate latest snapshots same parent — highest index wins via dedup",
			placementKey: crpName,
			master: &placementv1beta1.ClusterResourceSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf(placementv1beta1.ResourceSnapshotNameFmt, crpName, 0),
					Labels: map[string]string{
						placementv1beta1.ResourceIndexLabel:     "0",
						placementv1beta1.PlacementTrackingLabel: crpName,
					},
					Annotations: map[string]string{
						placementv1beta1.ResourceGroupHashAnnotation:         "abc",
						placementv1beta1.NumberOfResourceSnapshotsAnnotation: "1",
					},
				},
				Spec: placementv1beta1.ResourceSnapshotSpec{
					SelectedResources: []placementv1beta1.ResourceContent{
						*resource.ServiceResourceContentForTest(t),
					},
				},
			},
			croList: []placementv1beta1.ClusterResourceOverrideSnapshot{
				{
					// Stale (lower-index) latest=true: selects a different namespace.
					ObjectMeta: metav1.ObjectMeta{
						Name: "cro-dup-0",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
							placementv1beta1.OverrideTrackingLabel: "cro-dup",
							placementv1beta1.OverrideIndexLabel:    "0",
						},
					},
					Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ClusterResourceOverrideSpec{
							ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
								{Group: "", Version: "v1", Kind: "Namespace", Name: "stale-namespace"},
							},
						},
					},
				},
				{
					// Authoritative (highest-index) latest=true: selects the master's namespace.
					ObjectMeta: metav1.ObjectMeta{
						Name: "cro-dup-1",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
							placementv1beta1.OverrideTrackingLabel: "cro-dup",
							placementv1beta1.OverrideIndexLabel:    "1",
						},
					},
					Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ClusterResourceOverrideSpec{
							ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
								{Group: "", Version: "v1", Kind: "Namespace", Name: "svc-namespace"},
							},
						},
					},
				},
			},
			roList: []placementv1beta1.ResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-dup-0",
						Namespace: "svc-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
							placementv1beta1.OverrideTrackingLabel: "ro-dup",
							placementv1beta1.OverrideIndexLabel:    "0",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{Group: "", Version: "v1", Kind: "ConfigMap", Name: "stale"},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-dup-1",
						Namespace: "svc-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
							placementv1beta1.OverrideTrackingLabel: "ro-dup",
							placementv1beta1.OverrideIndexLabel:    "1",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{Group: "", Version: "v1", Kind: "Service", Name: "svc-name"},
							},
						},
					},
				},
			},
			wantCRO: []*placementv1beta1.ClusterResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cro-dup-1",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
							placementv1beta1.OverrideTrackingLabel: "cro-dup",
							placementv1beta1.OverrideIndexLabel:    "1",
						},
					},
					Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ClusterResourceOverrideSpec{
							ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
								{Group: "", Version: "v1", Kind: "Namespace", Name: "svc-namespace"},
							},
						},
					},
				},
			},
			wantRO: []*placementv1beta1.ResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-dup-1",
						Namespace: "svc-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
							placementv1beta1.OverrideTrackingLabel: "ro-dup",
							placementv1beta1.OverrideIndexLabel:    "1",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							ResourceSelectors: []placementv1beta1.ResourceSelector{
								{Group: "", Version: "v1", Kind: "Service", Name: "svc-name"},
							},
						},
					},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scheme := serviceScheme(t)
			objects := []client.Object{tc.master}
			for i := range tc.snapshots {
				objects = append(objects, tc.snapshots[i])
			}
			// Production-realistic labels: every override snapshot is created with
			// OverrideTrackingLabel and OverrideIndexLabel by the override controllers. The
			// fixtures only declare the latest-snapshot label they care about, so we inject the
			// rest here. Without these the read-time dedup path logs an error and skips the
			// malformed snapshot, which would change the test's expected outputs.
			for i := range tc.croList {
				ensureSnapshotTrackingLabels(&tc.croList[i].ObjectMeta, tc.croList[i].Name, 0)
				objects = append(objects, &tc.croList[i])
			}
			for i := range tc.roList {
				ensureSnapshotTrackingLabels(&tc.roList[i].ObjectMeta, tc.roList[i].Name, 0)
				objects = append(objects, &tc.roList[i])
			}
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()
			gotCRO, gotRO, err := FetchAllMatchingOverridesForResourceSnapshot(context.Background(), fakeClient, &fakeInformer, tc.placementKey, tc.master)
			if err != nil {
				t.Fatalf("fetchAllMatchingOverridesForResourceSnapshot() failed, got err %v, want no err", err)
			}
			options := []cmp.Option{
				cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion"),
				// Ignore the tracking/index labels we inject in setup for production realism;
				// the test fixtures intentionally only declare the latest-snapshot label.
				cmpopts.IgnoreMapEntries(func(k, _ string) bool {
					return k == placementv1beta1.OverrideTrackingLabel || k == placementv1beta1.OverrideIndexLabel
				}),
				cmpopts.SortSlices(func(o1, o2 *placementv1beta1.ClusterResourceOverrideSnapshot) bool {
					return o1.Name < o2.Name
				}),
				cmpopts.SortSlices(func(o1, o2 *placementv1beta1.ResourceOverrideSnapshot) bool {
					if o1.Namespace == o2.Namespace {
						return o1.Name < o2.Name
					}
					return o1.Namespace < o2.Namespace
				}),
				cmpopts.EquateEmpty(),
			}
			if diff := cmp.Diff(tc.wantCRO, gotCRO, options...); diff != "" {
				t.Errorf("fetchAllMatchingOverridesForResourceSnapshot() returned clusterResourceOverrides mismatch (-want, +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantRO, gotRO, options...); diff != "" {
				t.Errorf("fetchAllMatchingOverridesForResourceSnapshot() returned resourceOverrides mismatch (-want, +got):\n%s", diff)
			}
		})
	}
}

func TestPickFromResourceMatchedOverridesForTargetCluster(t *testing.T) {
	clusterName := "cluster-1"
	tests := []struct {
		name    string
		cluster *clusterv1beta1.MemberCluster
		croList []*placementv1beta1.ClusterResourceOverrideSnapshot
		roList  []*placementv1beta1.ResourceOverrideSnapshot
		wantCRO []string
		wantRO  []placementv1beta1.NamespacedName
		wantErr error
	}{
		{
			name: "empty overrides",
			cluster: &clusterv1beta1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: clusterName,
				},
			},
			wantCRO: nil,
			wantRO:  nil,
		},
		{
			name: "non-latest override snapshots",
			cluster: &clusterv1beta1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: clusterName,
				},
			},
			croList: []*placementv1beta1.ClusterResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cro-1",
					},
					Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ClusterResourceOverrideSpec{
							Policy: &placementv1beta1.OverridePolicy{
								OverrideRules: []placementv1beta1.OverrideRule{
									{
										// empty cluster label selector selects all clusters
										ClusterSelector: &placementv1beta1.ClusterSelector{},
									},
									{
										ClusterSelector: &placementv1beta1.ClusterSelector{
											ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
												{
													LabelSelector: &metav1.LabelSelector{
														MatchLabels: map[string]string{
															"key1": "value1",
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cro-2",
					},
					Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ClusterResourceOverrideSpec{
							Policy: &placementv1beta1.OverridePolicy{
								OverrideRules: []placementv1beta1.OverrideRule{
									{
										// empty cluster label selector selects all clusters
										ClusterSelector: &placementv1beta1.ClusterSelector{},
									},
								},
							},
						},
					},
				},
			},
			roList: []*placementv1beta1.ResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-1",
						Namespace: "svc-namespace",
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							Policy: &placementv1beta1.OverridePolicy{
								OverrideRules: []placementv1beta1.OverrideRule{
									{
										// empty cluster label selector selects all clusters
										ClusterSelector: &placementv1beta1.ClusterSelector{},
									},
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-2",
						Namespace: "deployment-namespace",
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							Policy: &placementv1beta1.OverridePolicy{
								OverrideRules: []placementv1beta1.OverrideRule{
									{
										// empty cluster label selector selects all clusters
										ClusterSelector: &placementv1beta1.ClusterSelector{},
									},
								},
							},
						},
					},
				},
			},
			wantCRO: []string{"cro-1", "cro-2"},
			wantRO: []placementv1beta1.NamespacedName{
				{
					Namespace: "deployment-namespace",
					Name:      "ro-2",
				},
				{
					Namespace: "svc-namespace",
					Name:      "ro-1",
				},
			},
		},
		{
			name: "cluster not found",
			croList: []*placementv1beta1.ClusterResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cro-2",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ClusterResourceOverrideSpec{
							Policy: &placementv1beta1.OverridePolicy{
								OverrideRules: []placementv1beta1.OverrideRule{
									{
										// empty cluster label selector selects all clusters
										ClusterSelector: &placementv1beta1.ClusterSelector{},
									},
								},
							},
						},
					},
				},
			},
			cluster: &clusterv1beta1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster-not-exist",
				},
			},
			wantErr: controller.ErrExpectedBehavior,
		},
		{
			name: "matched overrides with empty cluster label",
			cluster: &clusterv1beta1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: clusterName,
				},
			},
			croList: []*placementv1beta1.ClusterResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cro-1",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ClusterResourceOverrideSpec{
							Policy: &placementv1beta1.OverridePolicy{
								OverrideRules: []placementv1beta1.OverrideRule{
									{
										// empty cluster label selector selects all clusters
										ClusterSelector: &placementv1beta1.ClusterSelector{},
									},
									{
										ClusterSelector: &placementv1beta1.ClusterSelector{
											ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
												{
													LabelSelector: &metav1.LabelSelector{
														MatchLabels: map[string]string{
															"key1": "value1",
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cro-2",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ClusterResourceOverrideSpec{
							Policy: &placementv1beta1.OverridePolicy{
								OverrideRules: []placementv1beta1.OverrideRule{
									{
										// empty cluster label selector selects all clusters
										ClusterSelector: &placementv1beta1.ClusterSelector{},
									},
								},
							},
						},
					},
				},
			},
			roList: []*placementv1beta1.ResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-1",
						Namespace: "svc-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							Policy: &placementv1beta1.OverridePolicy{
								OverrideRules: []placementv1beta1.OverrideRule{
									{
										// empty cluster label selector selects all clusters
										ClusterSelector: &placementv1beta1.ClusterSelector{},
									},
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-2",
						Namespace: "deployment-namespace",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							Policy: &placementv1beta1.OverridePolicy{
								OverrideRules: []placementv1beta1.OverrideRule{
									{
										// empty cluster label selector selects all clusters
										ClusterSelector: &placementv1beta1.ClusterSelector{},
									},
								},
							},
						},
					},
				},
			},
			wantCRO: []string{"cro-1", "cro-2"},
			wantRO: []placementv1beta1.NamespacedName{
				{
					Namespace: "deployment-namespace",
					Name:      "ro-2",
				},
				{
					Namespace: "svc-namespace",
					Name:      "ro-1",
				},
			},
		},
		{
			name: "matched overrides with non-empty cluster label",
			cluster: &clusterv1beta1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: clusterName,
					Labels: map[string]string{
						"key1": "value1",
						"key2": "value2",
					},
				},
			},
			croList: []*placementv1beta1.ClusterResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cro-1",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ClusterResourceOverrideSpec{
							Policy: &placementv1beta1.OverridePolicy{
								OverrideRules: []placementv1beta1.OverrideRule{
									{
										ClusterSelector: &placementv1beta1.ClusterSelector{
											ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
												{
													LabelSelector: &metav1.LabelSelector{
														MatchLabels: map[string]string{
															"key1": "value1",
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cro-2",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ClusterResourceOverrideSpec{
							Policy: &placementv1beta1.OverridePolicy{
								OverrideRules: []placementv1beta1.OverrideRule{
									{
										ClusterSelector: &placementv1beta1.ClusterSelector{
											ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
												{
													LabelSelector: &metav1.LabelSelector{
														MatchLabels: map[string]string{
															"key1": "value2",
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			roList: []*placementv1beta1.ResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-1",
						Namespace: "test",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							Policy: &placementv1beta1.OverridePolicy{
								OverrideRules: []placementv1beta1.OverrideRule{
									{
										ClusterSelector: &placementv1beta1.ClusterSelector{
											ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
												{
													LabelSelector: &metav1.LabelSelector{
														MatchLabels: map[string]string{
															"key1": "value1",
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-2",
						Namespace: "test",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							Policy: &placementv1beta1.OverridePolicy{
								OverrideRules: []placementv1beta1.OverrideRule{
									{
										ClusterSelector: &placementv1beta1.ClusterSelector{
											ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
												{
													LabelSelector: &metav1.LabelSelector{
														MatchLabels: map[string]string{
															"key2": "value2",
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			wantCRO: []string{"cro-1"},
			wantRO: []placementv1beta1.NamespacedName{
				{
					Namespace: "test",
					Name:      "ro-1",
				},
				{
					Namespace: "test",
					Name:      "ro-2",
				},
			},
		},
		{
			name: "no matched overrides with non-empty cluster label",
			cluster: &clusterv1beta1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: clusterName,
					Labels: map[string]string{
						"key1": "value1",
						"key2": "value2",
					},
				},
			},
			croList: []*placementv1beta1.ClusterResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cro-1",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ClusterResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ClusterResourceOverrideSpec{
							Policy: &placementv1beta1.OverridePolicy{
								OverrideRules: []placementv1beta1.OverrideRule{
									{
										ClusterSelector: &placementv1beta1.ClusterSelector{
											ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
												{
													LabelSelector: &metav1.LabelSelector{
														MatchLabels: map[string]string{
															"key1": "value2",
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			roList: []*placementv1beta1.ResourceOverrideSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ro-1",
						Namespace: "test",
						Labels: map[string]string{
							placementv1beta1.IsLatestSnapshotLabel: "true",
						},
					},
					Spec: placementv1beta1.ResourceOverrideSnapshotSpec{
						OverrideSpec: placementv1beta1.ResourceOverrideSpec{
							Policy: &placementv1beta1.OverridePolicy{
								OverrideRules: []placementv1beta1.OverrideRule{
									{
										ClusterSelector: &placementv1beta1.ClusterSelector{
											ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
												{
													LabelSelector: &metav1.LabelSelector{
														MatchLabels: map[string]string{
															"key4": "value1",
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			wantCRO: []string{},
			wantRO:  []placementv1beta1.NamespacedName{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scheme := serviceScheme(t)
			objects := []client.Object{tc.cluster}
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()
			gotCRO, gotRO, err := PickFromResourceMatchedOverridesForTargetCluster(context.Background(), fakeClient, clusterName, tc.croList, tc.roList)
			if gotErr, wantErr := err != nil, tc.wantErr != nil; gotErr != wantErr || !errors.Is(err, tc.wantErr) {
				t.Fatalf("pickFromResourceMatchedOverridesForTargetCluster() got error %v, want error %v", err, tc.wantErr)
			}
			if diff := cmp.Diff(tc.wantCRO, gotCRO); diff != "" {
				t.Errorf("pickFromResourceMatchedOverridesForTargetCluster() returned clusterResourceOverrides mismatch (-want, +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantRO, gotRO); diff != "" {
				t.Errorf("pickFromResourceMatchedOverridesForTargetCluster() returned resourceOverrides mismatch (-want, +got):\n%s", diff)
			}
		})
	}
}

func TestIsClusterMatched(t *testing.T) {
	tests := []struct {
		name    string
		cluster clusterv1beta1.MemberCluster
		rule    placementv1beta1.OverrideRule
		want    bool
	}{
		{
			name: "matched overrides with nil cluster selector",
			cluster: clusterv1beta1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster-1",
				},
			},
			rule: placementv1beta1.OverrideRule{}, // nil cluster selector selects no clusters
			want: false,
		},
		{
			name: "rule with empty cluster selector",
			cluster: clusterv1beta1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster-1",
				},
			},
			rule: placementv1beta1.OverrideRule{
				ClusterSelector: &placementv1beta1.ClusterSelector{}, // empty cluster label selects all clusters
			},
			want: true,
		},
		{
			name: "rule with empty cluster selector terms",
			cluster: clusterv1beta1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster-1",
				},
			},
			rule: placementv1beta1.OverrideRule{
				ClusterSelector: &placementv1beta1.ClusterSelector{
					ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{}, // empty cluster label terms selects all clusters
				},
			},
			want: true,
		},
		{
			name: "rule with nil cluster label selector",
			cluster: clusterv1beta1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster-1",
				},
			},
			rule: placementv1beta1.OverrideRule{
				ClusterSelector: &placementv1beta1.ClusterSelector{
					ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
						{
							LabelSelector: nil,
						},
					},
				},
			},
			want: false,
		},
		{
			name: "rule with empty cluster label selector",
			cluster: clusterv1beta1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster-1",
				},
			},
			rule: placementv1beta1.OverrideRule{
				ClusterSelector: &placementv1beta1.ClusterSelector{
					ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
						{
							LabelSelector: &metav1.LabelSelector{}, // empty label selector selects all clusters
						},
					},
				},
			},
			want: true,
		},
		{
			name: "matched overrides with non-empty cluster label",
			cluster: clusterv1beta1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster-1",
					Labels: map[string]string{
						"key1": "value1",
						"key2": "value2",
					},
				},
			},
			rule: placementv1beta1.OverrideRule{
				ClusterSelector: &placementv1beta1.ClusterSelector{
					ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"key1": "value1",
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "matched overrides with multiple cluster terms",
			cluster: clusterv1beta1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster-1",
					Labels: map[string]string{
						"key1": "value1",
						"key2": "value2",
					},
				},
			},
			rule: placementv1beta1.OverrideRule{
				ClusterSelector: &placementv1beta1.ClusterSelector{
					ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"key1": "value2",
								},
							},
						},
						{
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"key1": "value1",
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "no matched overrides with non-empty cluster label",
			cluster: clusterv1beta1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster-1",
					Labels: map[string]string{
						"key1": "value1",
						"key2": "value2",
					},
				},
			},
			rule: placementv1beta1.OverrideRule{
				ClusterSelector: &placementv1beta1.ClusterSelector{
					ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"key1": "value2",
								},
							},
						},
					},
				},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := IsClusterMatched(&tc.cluster, tc.rule)
			if err != nil {
				t.Fatalf("IsClusterMatched() got error %v, want nil", err)
			}

			if got != tc.want {
				t.Errorf("IsClusterMatched() = %v, want %v", got, tc.want)
			}
		})
	}
}

// dedupTestCase is the shared shape for all dedup test cases. T is the snapshot value type.
// inputs are transformed into a slice; want is the deduped key set (order-independent).
type dedupTestCase[T any] struct {
	name  string
	input []T
	want  []string
}

// runDedupTests drives a generic dedup function over the shared cases. keyOf must match the
// production keyer; outputKey produces the comparable string for assertions (e.g. snapshot name
// for CRO, namespace/name for RO). Malformed snapshots are skipped with a loud log rather than
// failing the fetch, so cases assert on the survivors via tc.want.
func runDedupTests[T any, PT interface {
	*T
	client.Object
}](t *testing.T, cases []dedupTestCase[T], keyOf func(PT) string, outputKey func(T) string) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dedupLatestSnapshots(tc.input, keyOf)
			gotKeys := make([]string, 0, len(got))
			for i := range got {
				gotKeys = append(gotKeys, outputKey(got[i]))
			}
			if diff := cmp.Diff(tc.want, gotKeys, cmpopts.SortSlices(func(a, b string) bool { return a < b }), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("dedupLatestSnapshots() returned unexpected keys (-want, +got):\n%s", diff)
			}
		})
	}
}

func croSnapshotForDedup(name, parent, indexLabel string) placementv1beta1.ClusterResourceOverrideSnapshot {
	labelsForSnapshot := map[string]string{
		placementv1beta1.IsLatestSnapshotLabel: "true",
	}
	if parent != "" {
		labelsForSnapshot[placementv1beta1.OverrideTrackingLabel] = parent
	}
	if indexLabel != "" {
		labelsForSnapshot[placementv1beta1.OverrideIndexLabel] = indexLabel
	}
	return placementv1beta1.ClusterResourceOverrideSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labelsForSnapshot,
		},
	}
}

func roSnapshotForDedup(name, namespace, parent, indexLabel string) placementv1beta1.ResourceOverrideSnapshot {
	labelsForSnapshot := map[string]string{
		placementv1beta1.IsLatestSnapshotLabel: "true",
	}
	if parent != "" {
		labelsForSnapshot[placementv1beta1.OverrideTrackingLabel] = parent
	}
	if indexLabel != "" {
		labelsForSnapshot[placementv1beta1.OverrideIndexLabel] = indexLabel
	}
	return placementv1beta1.ResourceOverrideSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labelsForSnapshot,
		},
	}
}

func TestDedupLatestSnapshots_CRO(t *testing.T) {
	cases := []dedupTestCase[placementv1beta1.ClusterResourceOverrideSnapshot]{
		{
			name:  "empty input passes through",
			input: nil,
			want:  nil,
		},
		{
			name: "single snapshot passes through",
			input: []placementv1beta1.ClusterResourceOverrideSnapshot{
				croSnapshotForDedup("cro-a-0", "cro-a", "0"),
			},
			want: []string{"cro-a-0"},
		},
		{
			name: "two snapshots same parent picks highest index",
			input: []placementv1beta1.ClusterResourceOverrideSnapshot{
				croSnapshotForDedup("cro-a-0", "cro-a", "0"),
				croSnapshotForDedup("cro-a-1", "cro-a", "1"),
			},
			want: []string{"cro-a-1"},
		},
		{
			name: "multiple parents each kept with highest index",
			input: []placementv1beta1.ClusterResourceOverrideSnapshot{
				croSnapshotForDedup("cro-a-0", "cro-a", "0"),
				croSnapshotForDedup("cro-a-2", "cro-a", "2"),
				croSnapshotForDedup("cro-a-1", "cro-a", "1"),
				croSnapshotForDedup("cro-b-0", "cro-b", "0"),
			},
			want: []string{"cro-a-2", "cro-b-0"},
		},
		{
			// Malformed snapshots are logged and skipped so a single corrupt entry can't block
			// the rollout hot path; valid snapshots in the same list still survive.
			name: "missing tracking label is skipped, valid sibling kept",
			input: []placementv1beta1.ClusterResourceOverrideSnapshot{
				croSnapshotForDedup("cro-a-0", "cro-a", "0"),
				croSnapshotForDedup("cro-orphan", "", "0"),
			},
			want: []string{"cro-a-0"},
		},
		{
			name: "missing index label is skipped, valid sibling kept",
			input: []placementv1beta1.ClusterResourceOverrideSnapshot{
				croSnapshotForDedup("cro-a-0", "cro-a", "0"),
				croSnapshotForDedup("cro-a-x", "cro-a", ""),
			},
			want: []string{"cro-a-0"},
		},
		{
			name: "non-numeric index is skipped, valid sibling for same parent wins",
			input: []placementv1beta1.ClusterResourceOverrideSnapshot{
				croSnapshotForDedup("cro-a-zero", "cro-a", "zero"),
				croSnapshotForDedup("cro-a-1", "cro-a", "1"),
			},
			want: []string{"cro-a-1"},
		},
	}
	runDedupTests(t, cases, croDedupKey, func(s placementv1beta1.ClusterResourceOverrideSnapshot) string { return s.Name })
}

func TestDedupLatestSnapshots_RO(t *testing.T) {
	cases := []dedupTestCase[placementv1beta1.ResourceOverrideSnapshot]{
		{
			name:  "empty input passes through",
			input: nil,
			want:  nil,
		},
		{
			name: "two snapshots same parent same namespace picks highest index",
			input: []placementv1beta1.ResourceOverrideSnapshot{
				roSnapshotForDedup("ro-a-0", "ns-1", "ro-a", "0"),
				roSnapshotForDedup("ro-a-1", "ns-1", "ro-a", "1"),
			},
			want: []string{"ns-1/ro-a-1"},
		},
		{
			name: "same parent name in different namespaces are not collapsed",
			input: []placementv1beta1.ResourceOverrideSnapshot{
				roSnapshotForDedup("ro-a-0", "ns-1", "ro-a", "0"),
				roSnapshotForDedup("ro-a-0", "ns-2", "ro-a", "0"),
			},
			want: []string{"ns-1/ro-a-0", "ns-2/ro-a-0"},
		},
		{
			name: "missing tracking label is skipped, valid sibling kept",
			input: []placementv1beta1.ResourceOverrideSnapshot{
				roSnapshotForDedup("ro-a-0", "ns-1", "ro-a", "0"),
				roSnapshotForDedup("ro-orphan", "ns-1", "", "0"),
			},
			want: []string{"ns-1/ro-a-0"},
		},
		{
			name: "non-numeric index is skipped, valid sibling for same parent wins",
			input: []placementv1beta1.ResourceOverrideSnapshot{
				roSnapshotForDedup("ro-a-zero", "ns-1", "ro-a", "first"),
				roSnapshotForDedup("ro-a-1", "ns-1", "ro-a", "1"),
			},
			want: []string{"ns-1/ro-a-1"},
		},
	}
	runDedupTests(t, cases, roDedupKey, func(s placementv1beta1.ResourceOverrideSnapshot) string {
		return fmt.Sprintf("%s/%s", s.Namespace, s.Name)
	})
}
