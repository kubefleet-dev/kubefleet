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

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope="Namespaced",shortName=mcr,categories={fleet,fleet-metrics}
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:JSONPath=`.workloadsMonitored`,name="Workloads",type=integer
// +kubebuilder:printcolumn:JSONPath=`.lastCollectionTime`,name="Last-Collection",type=date
// +kubebuilder:printcolumn:JSONPath=`.metadata.creationTimestamp`,name="Age",type=date

// MetricCollectorReport is created by the MetricCollector controller on the hub cluster
// in the fleet-member-{clusterName} namespace to report collected metrics from a member cluster.
// The controller watches MetricCollector objects on the member cluster, collects metrics,
// and syncs the status to the hub as MetricCollectorReport objects.
//
// Controller workflow:
// 1. MetricCollector reconciles and collects metrics on member cluster
// 2. Metrics include clusterName from workload_health labels
// 3. Controller creates/updates MetricCollectorReport in fleet-member-{clusterName} namespace on hub
// 4. Report name matches MetricCollector name for easy lookup
//
// Namespace: fleet-member-{clusterName} (extracted from CollectedMetrics[0].ClusterName)
// Name: Same as MetricCollector name
// All metrics in CollectedMetrics are guaranteed to have the same ClusterName.
type MetricCollectorReport struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Conditions copied from the MetricCollector status.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration is the generation most recently observed from the MetricCollector.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// WorkloadsMonitored is the count of workloads being monitored.
	// +optional
	WorkloadsMonitored int32 `json:"workloadsMonitored,omitempty"`

	// LastCollectionTime is when metrics were last collected on the member cluster.
	// +optional
	LastCollectionTime *metav1.Time `json:"lastCollectionTime,omitempty"`

	// CollectedMetrics contains the most recent metrics from each workload.
	// All metrics are guaranteed to have the same ClusterName since they're collected from one member cluster.
	// +optional
	CollectedMetrics []WorkloadMetrics `json:"collectedMetrics,omitempty"`

	// LastReportTime is when this report was last synced to the hub.
	// +optional
	LastReportTime *metav1.Time `json:"lastReportTime,omitempty"`
}

// WorkloadMetrics represents metrics collected from a single workload pod.
type WorkloadMetrics struct {
	// Namespace is the namespace of the pod.
	// +required
	Namespace string `json:"namespace"`

	// ClusterName from the workload_health metric label.
	// +required
	ClusterName string `json:"clusterName"`

	// WorkloadName from the workload_health metric label (typically the deployment name).
	// +required
	WorkloadName string `json:"workloadName"`

	// Health indicates if the workload is healthy (true=healthy, false=unhealthy).
	// +required
	Health bool `json:"health"`
}

// +kubebuilder:object:root=true

// MetricCollectorReportList contains a list of MetricCollectorReport.
type MetricCollectorReportList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MetricCollectorReport `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MetricCollectorReport{}, &MetricCollectorReportList{})
}
