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

package utils

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestResourceConfigParse(t *testing.T) {
	tests := []struct {
		name                  string
		input                 string
		wantGroups            map[string]struct{}
		wantGroupVersions     map[schema.GroupVersion]struct{}
		wantGroupVersionKinds map[schema.GroupVersionKind]struct{}
		wantErr               bool
	}{
		{
			name:  "empty input",
			input: "",
		},
		{
			name:       "single group",
			input:      "apps",
			wantGroups: map[string]struct{}{"apps": {}},
		},
		{
			name:  "group version",
			input: "apps/v1",
			wantGroupVersions: map[schema.GroupVersion]struct{}{
				{Group: "apps", Version: "v1"}: {},
			},
		},
		{
			name:  "core group single kind",
			input: "v1/Pod",
			wantGroupVersionKinds: map[schema.GroupVersionKind]struct{}{
				{Version: "v1", Kind: "Pod"}: {},
			},
		},
		{
			name:  "core group multiple kinds",
			input: "v1/Pod,Node,Secret",
			wantGroupVersionKinds: map[schema.GroupVersionKind]struct{}{
				{Version: "v1", Kind: "Pod"}:    {},
				{Version: "v1", Kind: "Node"}:   {},
				{Version: "v1", Kind: "Secret"}: {},
			},
		},
		{
			name:  "group version single kind",
			input: "apps/v1/Deployment",
			wantGroupVersionKinds: map[schema.GroupVersionKind]struct{}{
				{Group: "apps", Version: "v1", Kind: "Deployment"}: {},
			},
		},
		{
			name:  "group version multiple kinds",
			input: "apps/v1/Deployment,StatefulSet",
			wantGroupVersionKinds: map[schema.GroupVersionKind]struct{}{
				{Group: "apps", Version: "v1", Kind: "Deployment"}:  {},
				{Group: "apps", Version: "v1", Kind: "StatefulSet"}: {},
			},
		},
		{
			name:       "multiple tokens of different forms",
			input:      "rbac.authorization.k8s.io;batch/v1;v1/ConfigMap;apps/v1/DaemonSet",
			wantGroups: map[string]struct{}{"rbac.authorization.k8s.io": {}},
			wantGroupVersions: map[schema.GroupVersion]struct{}{
				{Group: "batch", Version: "v1"}: {},
			},
			wantGroupVersionKinds: map[schema.GroupVersionKind]struct{}{
				{Version: "v1", Kind: "ConfigMap"}:                {},
				{Group: "apps", Version: "v1", Kind: "DaemonSet"}: {},
			},
		},
		{
			name:    "too many slashes",
			input:   "apps/v1/Deployment/extra",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := NewResourceConfig(true)
			err := r.Parse(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Parse(%q) error = %v, wantErr %v", tc.input, err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if diff := cmp.Diff(orEmpty(tc.wantGroups), r.groups); diff != "" {
				t.Errorf("Parse(%q) groups mismatch (-want +got):\n%s", tc.input, diff)
			}
			if diff := cmp.Diff(orEmpty(tc.wantGroupVersions), r.groupVersions); diff != "" {
				t.Errorf("Parse(%q) groupVersions mismatch (-want +got):\n%s", tc.input, diff)
			}
			if diff := cmp.Diff(orEmpty(tc.wantGroupVersionKinds), r.groupVersionKinds); diff != "" {
				t.Errorf("Parse(%q) groupVersionKinds mismatch (-want +got):\n%s", tc.input, diff)
			}
		})
	}
}

func orEmpty[K comparable](m map[K]struct{}) map[K]struct{} {
	if m == nil {
		return map[K]struct{}{}
	}
	return m
}
