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

package labels

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	fleetv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

const (
	snapshotName = "test-snapshot"

	nonIntegerIndex = "abc"
)

func TestExtractResourceIndexFromClusterResourceSnapshot(t *testing.T) {
	testCases := []struct {
		name      string
		snapshot  *fleetv1beta1.ClusterResourceSnapshot
		wantIndex int
		wantError bool
	}{
		{
			name: "valid annotation",
			snapshot: &fleetv1beta1.ClusterResourceSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name: snapshotName,
					Labels: map[string]string{
						fleetv1beta1.ResourceIndexLabel: "1",
					},
				},
			},
			wantIndex: 1,
		},
		{
			name: "no label",
			snapshot: &fleetv1beta1.ClusterResourceSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name: snapshotName,
				},
			},
			wantIndex: -1,
		},
		{
			name: "invalid label: not an integer",
			snapshot: &fleetv1beta1.ClusterResourceSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name: snapshotName,
					Labels: map[string]string{
						fleetv1beta1.ResourceIndexLabel: "abc",
					},
				},
			},
			wantError: true,
		},
		{
			name: "invalid label: negative integer",
			snapshot: &fleetv1beta1.ClusterResourceSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name: snapshotName,
					Labels: map[string]string{
						fleetv1beta1.ResourceIndexLabel: "-1",
					},
				},
			},
			wantError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotIndex, err := ExtractResourceIndexFromResourceSnapshot(tc.snapshot)
			if tc.wantError {
				if err == nil {
					t.Fatalf("ExtractResourceIndexFromClusterResourceSnapshot() =  %v, want error", gotIndex)
				}
				return
			}

			if gotIndex != tc.wantIndex {
				t.Fatalf("ExtractResourceIndexFromClusterResourceSnapshot() = %v, want %v", gotIndex, tc.wantIndex)
			}
		})
	}
}

func TestExtractResourceSnapshotIndexFromWork(t *testing.T) {
	testCases := []struct {
		name      string
		snapshot  *fleetv1beta1.Work
		wantIndex int
		wantError bool
	}{
		{
			name: "valid annotation",
			snapshot: &fleetv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Name: snapshotName,
					Labels: map[string]string{
						fleetv1beta1.ParentResourceSnapshotIndexLabel: "1",
					},
				},
			},
			wantIndex: 1,
		},
		{
			name: "no label",
			snapshot: &fleetv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Name: snapshotName,
				},
			},
			wantIndex: -1,
		},
		{
			name: "invalid label: not an integer",
			snapshot: &fleetv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Name: snapshotName,
					Labels: map[string]string{
						fleetv1beta1.ParentResourceSnapshotIndexLabel: "abc",
					},
				},
			},
			wantError: true,
		},
		{
			name: "invalid label: negative integer",
			snapshot: &fleetv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Name: snapshotName,
					Labels: map[string]string{
						fleetv1beta1.ParentResourceSnapshotIndexLabel: "-1",
					},
				},
			},
			wantError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotIndex, err := ExtractResourceSnapshotIndexFromWork(tc.snapshot)
			if tc.wantError {
				if err == nil {
					t.Fatalf("ExtractResourceSnapshotIndexFromWork() =  %v, want error", gotIndex)
				}
				return
			}

			if gotIndex != tc.wantIndex {
				t.Fatalf("ExtractResourceSnapshotIndexFromWork() = %v, want %v", gotIndex, tc.wantIndex)
			}
		})
	}
}

func TestExtractIndex_ErrorMessage(t *testing.T) {
	testCases := []struct {
		name       string
		labelValue string
		wantErrMsg string
	}{
		{
			name:       "not an integer",
			labelValue: nonIntegerIndex,
			wantErrMsg: `invalid resource index "abc", error: strconv.Atoi: parsing "abc": invalid syntax`,
		},
		{
			name:       "negative integer",
			labelValue: "-1",
			wantErrMsg: `invalid resource index "-1": must be a non-negative integer`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := &fleetv1beta1.ClusterResourceSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name:   snapshotName,
					Labels: map[string]string{fleetv1beta1.ResourceIndexLabel: tc.labelValue},
				},
			}
			_, err := ExtractIndex(snapshot, fleetv1beta1.ResourceIndexLabel)
			if err == nil {
				t.Fatalf("ExtractIndex() error = nil, want %q", tc.wantErrMsg)
			}
			if err.Error() != tc.wantErrMsg {
				t.Errorf("ExtractIndex() error = %q, want %q", err.Error(), tc.wantErrMsg)
			}
		})
	}
}

func TestParsePolicyIndexFromLabel(t *testing.T) {
	testCases := []struct {
		name           string
		policySnapshot client.Object
		wantIndex      int
		wantErrMsg     string
	}{
		{
			name: "valid index on a cluster-scoped snapshot",
			policySnapshot: &fleetv1beta1.ClusterSchedulingPolicySnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name:   snapshotName,
					Labels: map[string]string{fleetv1beta1.PolicyIndexLabel: "3"},
				},
			},
			wantIndex: 3,
		},
		{
			name: "valid index on a namespaced snapshot",
			policySnapshot: &fleetv1beta1.SchedulingPolicySnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name:      snapshotName,
					Namespace: "test-namespace",
					Labels:    map[string]string{fleetv1beta1.PolicyIndexLabel: "0"},
				},
			},
			wantIndex: 0,
		},
		{
			name: "no labels",
			policySnapshot: &fleetv1beta1.ClusterSchedulingPolicySnapshot{
				ObjectMeta: metav1.ObjectMeta{Name: snapshotName},
			},
			wantIndex:  -1,
			wantErrMsg: "no labels found on policy snapshot",
		},
		{
			name: "index label missing",
			policySnapshot: &fleetv1beta1.ClusterSchedulingPolicySnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name:   snapshotName,
					Labels: map[string]string{"other": "label"},
				},
			},
			wantIndex:  -1,
			wantErrMsg: `invalid policy index "", error: strconv.Atoi: parsing "": invalid syntax`,
		},
		{
			name: "not an integer",
			policySnapshot: &fleetv1beta1.ClusterSchedulingPolicySnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name:   snapshotName,
					Labels: map[string]string{fleetv1beta1.PolicyIndexLabel: nonIntegerIndex},
				},
			},
			wantIndex:  -1,
			wantErrMsg: `invalid policy index "abc", error: strconv.Atoi: parsing "abc": invalid syntax`,
		},
		{
			name: "negative integer",
			policySnapshot: &fleetv1beta1.ClusterSchedulingPolicySnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name:   snapshotName,
					Labels: map[string]string{fleetv1beta1.PolicyIndexLabel: "-1"},
				},
			},
			wantIndex:  -1,
			wantErrMsg: `invalid policy index "-1": must be a non-negative integer`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotIndex, err := ParsePolicyIndexFromLabel(tc.policySnapshot)
			if gotIndex != tc.wantIndex {
				t.Errorf("ParsePolicyIndexFromLabel() index = %d, want %d", gotIndex, tc.wantIndex)
			}
			if tc.wantErrMsg == "" {
				if err != nil {
					t.Fatalf("ParsePolicyIndexFromLabel() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ParsePolicyIndexFromLabel() error = nil, want %q", tc.wantErrMsg)
			}
			if err.Error() != tc.wantErrMsg {
				t.Errorf("ParsePolicyIndexFromLabel() error = %q, want %q", err.Error(), tc.wantErrMsg)
			}
		})
	}
}
