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

package gvkwhitelist

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	sortByPath = func(a, b []string) bool {
		for i := 0; i < 3; i++ {
			switch {
			case a[i] < b[i]:
				return true
			case a[i] > b[i]:
				return false
			default:
				// continue to the next path part.
				i++
			}
		}
		return false // This should never happen.
	}
)

func walkWhitelistedGVKTrieTree(node *tNode, pathSoFar []string, res *[][]string) {
	if len(node.children) == 0 {
		*res = append(*res, pathSoFar)
		return
	}

	for k, v := range node.children {
		updatedPathSoFar := make([]string, 0, len(pathSoFar)+1)
		updatedPathSoFar = append(updatedPathSoFar, pathSoFar...)
		updatedPathSoFar = append(updatedPathSoFar, k)
		walkWhitelistedGVKTrieTree(v, updatedPathSoFar, res)
	}
}

// TestRegister tests the Register method of the WhitelistedGVKs.
func TestRegister(t *testing.T) {
	// Register multiple GVKs, with and without wildcards to a whitelist.
	whitelist := NewWhitelistedGVKs()

	whitelist.Register("apps", "v1", "Deployment")
	whitelist.Register("batch", "*", "Job")
	whitelist.Register("", "v1", "ConfigMap")
	whitelist.Register("example.com", "*", "*")
	whitelist.Register("*", "*", "*")

	// Verify that the tree has been constructed as expected.
	//
	// For simplicity reasons, verify only the paths to each leaf node.
	paths := make([][]string, 0, 5)
	walkWhitelistedGVKTrieTree(whitelist.root, []string{}, &paths)

	expectedPaths := [][]string{
		{"apps", "v1", "Deployment"},
		{"batch", "*", "Job"},
		{"", "v1", "ConfigMap"},
		{"example.com", "*", "*"},
		{"*", "*", "*"},
	}
	// Golang has fair lock implementation; however, there is no guarantee on when a specific goroutine
	// will wake up first to acquire the lock. As a result, we must sort the path collection before doing
	// the comparison.
	if diff := cmp.Diff(paths, expectedPaths, cmpopts.SortSlices(sortByPath)); diff != "" {
		t.Errorf("unexpected trie tree structure (-got, +want):\n%s", diff)
	}
}

// TestIsWhitelisted tests the IsWhitelisted method of the WhitelistedGVKs.
func TestIsWhitelisted(t *testing.T) {
	// Build the whitelist.
	whitelist := NewWhitelistedGVKs()

	whitelist.Register("apps", "v1", "Deployment")
	whitelist.Register("*", "v1", "CronJob")
	whitelist.Register("batch", "*", "Job")
	whitelist.Register("networking.k8s.io", "v1", "*")
	whitelist.Register("", "v1", "ConfigMap")
	whitelist.Register("example.com", "*", "*")
	whitelist.Register("*", "v2alpha1", "*")
	whitelist.Register("*", "*", "FooBar")

	testCases := []struct {
		name string
		gvk  schema.GroupVersionKind
		want bool
	}{
		{
			name: "apps/v1/Deployment (whitelisted)",
			gvk: schema.GroupVersionKind{
				Group:   "apps",
				Version: "v1",
				Kind:    "Deployment",
			},
			want: true,
		},
		{
			name: "batch/v1/CronJob (whitelisted via wildcard group)",
			gvk: schema.GroupVersionKind{
				Group:   "batch",
				Version: "v1",
				Kind:    "CronJob",
			},
			want: true,
		},
		{
			name: "batch/v1/Job (whitelisted via wildcard version)",
			gvk: schema.GroupVersionKind{
				Group:   "batch",
				Version: "v1",
				Kind:    "Job",
			},
			want: true,
		},
		{
			name: "networking.k8s.io/v1/Ingress (whitelisted via wildcard kind)",
			gvk: schema.GroupVersionKind{
				Group:   "networking.k8s.io",
				Version: "v1",
				Kind:    "Ingress",
			},
			want: true,
		},
		{
			name: "v1/ConfigMap (whitelisted)",
			gvk: schema.GroupVersionKind{
				Group:   "",
				Version: "v1",
				Kind:    "ConfigMap",
			},
			want: true,
		},
		{
			name: "example.com/vAny/AnyKind (whitelisted via wildcard version and kind)",
			gvk: schema.GroupVersionKind{
				Group:   "example.com",
				Version: "vAny",
				Kind:    "AnyKind",
			},
			want: true,
		},
		{
			name: "anygroup/v2alpha1/AnyKind (whitelisted via wildcard group and version)",
			gvk: schema.GroupVersionKind{
				Group:   "anygroup",
				Version: "v2alpha1",
				Kind:    "AnyKind",
			},
			want: true,
		},
		{
			name: "anygroup/vAny/FooBar (whitelisted via wildcard group and any version)",
			gvk: schema.GroupVersionKind{
				Group:   "anygroup",
				Version: "vAny",
				Kind:    "FooBar",
			},
			want: true,
		},
		{
			name: "v1/Pod (not whitelisted)",
			gvk: schema.GroupVersionKind{
				Group:   "",
				Version: "v1",
				Kind:    "Pod",
			},
		},
		{
			name: "storage.k8s.io/v1/StorageClass (not whitelisted)",
			gvk: schema.GroupVersionKind{
				Group:   "storage.k8s.io",
				Version: "v1",
				Kind:    "StorageClass",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := whitelist.IsWhitelisted(tc.gvk)
			if got != tc.want {
				t.Errorf("IsWhitelisted(%v) = %v; want %v", tc.gvk, got, tc.want)
			}
		})
	}
}

// TestIsWhitelisted_MatchAll tests the IsWhitelisted method of the WhitelistedGVKs
// when the whitelist has a rule that matches all GVKs.
func TestIsWhitelisted_MatchAll(t *testing.T) {
	// Build the whitelist.
	whitelist := NewWhitelistedGVKs()

	whitelist.Register("*", "*", "*")

	testCases := []struct {
		name string
		gvk  schema.GroupVersionKind
		want bool
	}{
		{
			name: "apps/v1/Deployment (whitelisted)",
			gvk: schema.GroupVersionKind{
				Group:   "apps",
				Version: "v1",
				Kind:    "Deployment",
			},
			want: true,
		},
		{
			name: "batch/v1/CronJob (whitelisted via wildcard group)",
			gvk: schema.GroupVersionKind{
				Group:   "batch",
				Version: "v1",
				Kind:    "CronJob",
			},
			want: true,
		},
		{
			name: "batch/v1/Job (whitelisted via wildcard version)",
			gvk: schema.GroupVersionKind{
				Group:   "batch",
				Version: "v1",
				Kind:    "Job",
			},
			want: true,
		},
		{
			name: "networking.k8s.io/v1/Ingress (whitelisted via wildcard kind)",
			gvk: schema.GroupVersionKind{
				Group:   "networking.k8s.io",
				Version: "v1",
				Kind:    "Ingress",
			},
			want: true,
		},
		{
			name: "v1/ConfigMap (whitelisted)",
			gvk: schema.GroupVersionKind{
				Group:   "",
				Version: "v1",
				Kind:    "ConfigMap",
			},
			want: true,
		},
		{
			name: "example.com/vAny/AnyKind (whitelisted via wildcard version and kind)",
			gvk: schema.GroupVersionKind{
				Group:   "example.com",
				Version: "vAny",
				Kind:    "AnyKind",
			},
			want: true,
		},
		{
			name: "anygroup/v2alpha1/AnyKind (whitelisted via wildcard group and version)",
			gvk: schema.GroupVersionKind{
				Group:   "anygroup",
				Version: "v2alpha1",
				Kind:    "AnyKind",
			},
			want: true,
		},
		{
			name: "anygroup/vAny/FooBar (whitelisted via wildcard group and any version)",
			gvk: schema.GroupVersionKind{
				Group:   "anygroup",
				Version: "vAny",
				Kind:    "FooBar",
			},
			want: true,
		},
		{
			name: "v1/Pod (not whitelisted)",
			gvk: schema.GroupVersionKind{
				Group:   "",
				Version: "v1",
				Kind:    "Pod",
			},
			want: true,
		},
		{
			name: "storage.k8s.io/v1/StorageClass (not whitelisted)",
			gvk: schema.GroupVersionKind{
				Group:   "storage.k8s.io",
				Version: "v1",
				Kind:    "StorageClass",
			},
			want: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := whitelist.IsWhitelisted(tc.gvk)
			if got != tc.want {
				t.Errorf("IsWhitelisted(%v) = %v; want %v", tc.gvk, got, tc.want)
			}
		})
	}
}

// TestParse tests the Parse function.
func TestParse(t *testing.T) {
	testCases := []struct {
		name               string
		gvkWhitelistRawStr string
		wantPaths          [][]string
		wantErred          bool
		wantErrPrefix      string
	}{
		{
			name:               "single entry, core API group",
			gvkWhitelistRawStr: "v1/ConfigMap",
			wantPaths: [][]string{
				{"", "v1", "ConfigMap"},
			},
		},
		{
			name:               "single entry, non-core API group",
			gvkWhitelistRawStr: "apps/v1/Deployment",
			wantPaths: [][]string{
				{"apps", "v1", "Deployment"},
			},
		},
		{
			name:               "wildcards in API group",
			gvkWhitelistRawStr: "*/v1/CronJob",
			wantPaths: [][]string{
				{"*", "v1", "CronJob"},
			},
		},
		{
			name:               "wildcards in API version",
			gvkWhitelistRawStr: "batch/*/Job",
			wantPaths: [][]string{
				{"batch", "*", "Job"},
			},
		},
		{
			name:               "wildcards in Kind",
			gvkWhitelistRawStr: "networking.k8s.io/v1/*",
			wantPaths: [][]string{
				{"networking.k8s.io", "v1", "*"},
			},
		},
		{
			name:               "wildcards in multiple parts",
			gvkWhitelistRawStr: "*/*/*",
			wantPaths: [][]string{
				{"*", "*", "*"},
			},
		},
		{
			name:               "multiple entries with and without wildcards",
			gvkWhitelistRawStr: "apps/v1/Deployment,v1/ConfigMap,*/v1/CronJob,batch/*/Job,networking.k8s.io/v1/*,example.com/*/*",
			wantPaths: [][]string{
				{"apps", "v1", "Deployment"},
				{"", "v1", "ConfigMap"},
				{"*", "v1", "CronJob"},
				{"batch", "*", "Job"},
				{"networking.k8s.io", "v1", "*"},
				{"example.com", "*", "*"},
			},
		},
		{
			name:               "invalid API group",
			gvkWhitelistRawStr: "InvalidGroup!/v1/Deployment",
			wantErred:          true,
			wantErrPrefix:      "failed to parse GVK whitelist raw string: segment InvalidGroup!/v1/Deployment has an invalid API group part",
		},
		{
			name:               "invalid API version",
			gvkWhitelistRawStr: "apps/InvalidVersion!/Deployment",
			wantErred:          true,
			wantErrPrefix:      "failed to parse GVK whitelist raw string: segment apps/InvalidVersion!/Deployment has an invalid API version part",
		},
		{
			name:               "invalid Kind",
			gvkWhitelistRawStr: "apps/v1/InvalidKind!",
			wantErred:          true,
			wantErrPrefix:      "failed to parse GVK whitelist raw string: segment apps/v1/InvalidKind! has an invalid Kind part",
		},
		{
			name:               "too few parts",
			gvkWhitelistRawStr: "v1",
			wantErred:          true,
			wantErrPrefix:      "failed to parse GVK whitelist raw string: segment v1 is not valid (too few or too many parts)",
		},
		{
			name:               "too many parts",
			gvkWhitelistRawStr: "apps/v1/Deployment/ExtraPart",
			wantErred:          true,
			wantErrPrefix:      "failed to parse GVK whitelist raw string: segment apps/v1/Deployment/ExtraPart is not valid (too few or too many parts)",
		},
		{
			name:               "empty string",
			gvkWhitelistRawStr: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			whitelist, err := Parse(tc.gvkWhitelistRawStr)
			if tc.wantErred {
				if err == nil {
					t.Fatalf("Parse() = nil, want erred")
				}

				if !strings.HasPrefix(err.Error(), tc.wantErrPrefix) {
					t.Fatalf("Parse() error = %v, want to have error msg prefix %q", err, tc.wantErrPrefix)
				}
				return
			}
			if len(tc.gvkWhitelistRawStr) == 0 {
				if whitelist != nil {
					t.Fatalf("Parse() = %v, want nil whitelist", whitelist)
				}
				return
			}

			if err != nil {
				t.Fatalf("Parse() = %v, want no error", err)
			}

			// Verify the trie tree structure.
			paths := make([][]string, 0, 100)
			walkWhitelistedGVKTrieTree(whitelist.root, []string{}, &paths)

			// Golang has fair lock implementation; however, there is no guarantee on when a specific goroutine
			// will wake up first to acquire the lock. As a result, we must sort the path collection before doing
			// the comparison.
			if diff := cmp.Diff(paths, tc.wantPaths, cmpopts.SortSlices(sortByPath)); diff != "" {
				t.Errorf("unexpected trie tree structure (-got, +want):\n%s", diff)
			}
		})
	}
}
