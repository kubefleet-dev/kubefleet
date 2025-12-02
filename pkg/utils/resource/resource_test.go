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

package resource

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestHashOf(t *testing.T) {
	testCases := []struct {
		name  string
		input any
	}{
		{
			name: "resource snapshot spec",
			input: &placementv1beta1.ResourceSnapshotSpec{
				SelectedResources: []placementv1beta1.ResourceContent{},
			},
		},
		{
			name:  "nil resource",
			input: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := HashOf(tc.input)
			if err != nil {
				t.Fatalf("HashOf() got error %v, want nil", err)
			}
			if len(got) == 0 {
				t.Errorf("HashOf() got empty, want not empty")
			}
		})
	}
}

// TestIsObjOversized tests the IsObjOversized function.
func TestIsObjOversized(t *testing.T) {
	cm := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "app",
			Namespace: "default",
		},
		Data: map[string]string{
			"key": "value",
		},
	}

	testCases := []struct {
		name               string
		sizeLimitBytes     int
		wantErred          bool
		wantSizeDeltaBytes int
	}{
		{
			name:               "positize size delta",
			sizeLimitBytes:     10000,
			wantSizeDeltaBytes: -9866,
		},
		{
			name:               "negative size delta",
			sizeLimitBytes:     1,
			wantSizeDeltaBytes: 133,
		},
		{
			name: "negative size limit",
			// Invalid size limit.
			sizeLimitBytes: -1,
			wantErred:      true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sizeDeltaBytes, err := IsObjOversized(cm, tc.sizeLimitBytes)

			if tc.wantErred {
				if err == nil {
					t.Fatalf("IsObjOversized() error = nil, want erred")
				}
				return
			}
			if !cmp.Equal(sizeDeltaBytes, tc.wantSizeDeltaBytes) {
				t.Errorf("IsObjOversized() = %d, want %d", sizeDeltaBytes, tc.wantSizeDeltaBytes)
			}
		})
	}
}
