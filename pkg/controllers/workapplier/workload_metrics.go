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

package workapplier

// WorkloadMetrics represents metrics collected from a single workload pod.
type WorkloadMetrics struct {
	// Namespace is the namespace of the pod.
	Namespace string `json:"namespace"`

	// ClusterName from the workload_health metric label.
	ClusterName string `json:"clusterName"`

	// WorkloadName from the workload_health metric label (typically the deployment name).
	WorkloadName string `json:"workloadName"`

	// Health indicates if the workload is healthy (true=healthy, false=unhealthy).
	Health bool `json:"health"`
}
