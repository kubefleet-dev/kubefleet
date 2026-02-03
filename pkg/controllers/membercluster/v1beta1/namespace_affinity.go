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

package v1beta1

import (
	"strings"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

const (
	// NamespaceAffinityLabelKeyPrefix is the prefix for namespace affinity labels on MemberCluster
	// Format: kubernetes-fleet.io/namespace-<namespace-name>
	NamespaceAffinityLabelKeyPrefix = placementv1beta1.FleetPrefix + "namespace-"

	// MaxNamespaceLabelsPerCluster is the maximum number of namespace affinity labels allowed per MemberCluster
	// to prevent excessive resource usage and API server overhead
	MaxNamespaceLabelsPerCluster = 200
)

// BuildNamespaceAffinityLabelKey builds the label key for namespace affinity
// Returns: kubernetes-fleet.io/namespace-<namespace-name>
func BuildNamespaceAffinityLabelKey(namespaceName string) string {
	return NamespaceAffinityLabelKeyPrefix + namespaceName
}

// ParseNamespaceFromAffinityLabel extracts namespace name from affinity label key
// Returns namespace name if the key matches the pattern, empty string otherwise
func ParseNamespaceFromAffinityLabel(labelKey string) string {
	if len(labelKey) <= len(NamespaceAffinityLabelKeyPrefix) {
		return ""
	}

	if !startsWithPrefix(labelKey, NamespaceAffinityLabelKeyPrefix) {
		return ""
	}

	return labelKey[len(NamespaceAffinityLabelKeyPrefix):]
}

// IsNamespaceAffinityLabel checks if a label key is a namespace affinity label
func IsNamespaceAffinityLabel(labelKey string) bool {
	if !strings.HasPrefix(labelKey, NamespaceAffinityLabelKeyPrefix) {
		return false
	}

	// Extract namespace name and validate it's not empty or invalid
	namespace := strings.TrimPrefix(labelKey, NamespaceAffinityLabelKeyPrefix)
	if namespace == "" || strings.HasPrefix(namespace, "-") || strings.HasSuffix(namespace, "-") || strings.Contains(namespace, "--") {
		return false
	}

	return true
}

// startsWithPrefix checks if string starts with prefix (avoiding external dependencies)
func startsWithPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[0:len(prefix)] == prefix
}
