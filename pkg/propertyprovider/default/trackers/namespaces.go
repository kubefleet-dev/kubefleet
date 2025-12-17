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

// Package trackers feature implementations that help track specific stats about
// Kubernetes resources, e.g., namespaces in the default property provider.
package trackers

import (
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

var (
	// namespaceTrackerLimit is the maximum number of namespaces that the namespace tracker can track.
	// When the limit is reached, no new namespaces will be tracked.
	namespaceTrackerLimit = 200
)

// NamespaceTracker helps track specific stats about namespaces in a Kubernetes cluster, e.g., its ownerReference.
type NamespaceTracker struct {
	// namespacesByName tracks namespaces by their name
	namespacesByName map[string]*corev1.Namespace
	// namespaceCreationTimes tracks when each namespace was first tracked.
	namespaceCreationTimes map[string]time.Time

	// reachLimit indicates whether the tracker has reached its tracking limit.
	reachLimit bool

	// client is used to fetch Work objects when extracting CRP names from AppliedWork owner references
	client client.Client

	// mu is a RWMutex that protects the tracker against concurrent access.
	mu sync.RWMutex
}

// NewNamespaceTracker returns a namespace tracker.
func NewNamespaceTracker(client client.Client) *NamespaceTracker {
	return &NamespaceTracker{
		namespacesByName:       make(map[string]*corev1.Namespace),
		namespaceCreationTimes: make(map[string]time.Time),
		client:                 client,
	}
}

// AddOrUpdate starts tracking a namespace or updates the information about a namespace that has been
// tracked.
func (nt *NamespaceTracker) AddOrUpdate(namespace *corev1.Namespace) {
	nt.mu.Lock()
	defer nt.mu.Unlock()

	nsKObj := klog.KObj(namespace)
	if _, exists := nt.namespacesByName[namespace.Name]; !exists {
		if len(nt.namespacesByName) == namespaceTrackerLimit {
			if !nt.reachLimit {
				nt.reachLimit = true
				klog.Warning("Namespace tracker has reached its tracking limit; new namespaces will not be tracked anymore", "limit", namespaceTrackerLimit)
			}
			klog.V(2).InfoS("Ignoring namespace as namespace tracker has reached its tracking limit", "namespace", nsKObj)
			return
		}
		// New namespace being tracked
		nt.namespaceCreationTimes[namespace.Name] = time.Now()
		klog.V(2).InfoS("Started tracking namespace", "namespace", nsKObj)
	} else {
		klog.V(4).InfoS("Updated namespace information", "namespace", nsKObj)
	}

	// Store a copy of the namespace to avoid potential modifications
	nt.namespacesByName[namespace.Name] = namespace.DeepCopy()
}

// Remove stops tracking a namespace.
func (nt *NamespaceTracker) Remove(namespaceName string) {
	nt.mu.Lock()
	defer nt.mu.Unlock()

	if _, exists := nt.namespacesByName[namespaceName]; exists {
		delete(nt.namespacesByName, namespaceName)
		delete(nt.namespaceCreationTimes, namespaceName)

		// Reset reachLimit flag if we were at the limit and now have space for new namespaces
		if nt.reachLimit && len(nt.namespacesByName) < namespaceTrackerLimit {
			nt.reachLimit = false
			klog.V(2).InfoS("Namespace tracker limit reset - can accept new namespaces", "currentCount", len(nt.namespacesByName), "limit", namespaceTrackerLimit)
		}

		klog.V(2).InfoS("Stopped tracking namespace", "namespace", namespaceName)
	}
}

// ListNamespaces returns a map of namespace names to their associated work name and
// whether it reaches the track limit.
// It checks namespace owner references for AppliedWork and extracts work name.
func (nt *NamespaceTracker) ListNamespaces() (map[string]string, bool) {
	nt.mu.RLock()
	defer nt.mu.RUnlock()

	result := make(map[string]string, len(nt.namespacesByName))
	for name, namespace := range nt.namespacesByName {
		workName := nt.extractWorkNameFromNamespace(namespace)
		result[name] = workName
	}
	return result, nt.reachLimit
}

// extractWorkNameFromNamespace extracts the work name from a namespace by checking its owner references.
func (nt *NamespaceTracker) extractWorkNameFromNamespace(namespace *corev1.Namespace) string {
	// Check if namespace has AppliedWork owner references
	for _, ownerRef := range namespace.GetOwnerReferences() {
		if ownerRef.APIVersion == placementv1beta1.GroupVersion.String() &&
			ownerRef.Kind == placementv1beta1.AppliedWorkKind {
			return ownerRef.Name
		}
	}
	return ""
}
