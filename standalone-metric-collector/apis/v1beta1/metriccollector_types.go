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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubefleet-dev/standalone-metric-collector/apis"
)

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope="Cluster",shortName=mc,categories={fleet,fleet-metrics}
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:JSONPath=`.metadata.generation`,name="Gen",type=string
// +kubebuilder:printcolumn:JSONPath=`.status.conditions[?(@.type=="MetricCollectorReady")].status`,name="Ready",type=string
// +kubebuilder:printcolumn:JSONPath=`.status.workloadsMonitored`,name="Workloads",type=integer
// +kubebuilder:printcolumn:JSONPath=`.status.lastCollectionTime`,name="Last-Collection",type=date
// +kubebuilder:printcolumn:JSONPath=`.metadata.creationTimestamp`,name="Age",type=date

// MetricCollector is used by member-agent to scrape and collect metrics from workloads
// running on the member cluster. It runs on each member cluster and collects metrics
// from Prometheus-compatible endpoints.
type MetricCollector struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// The desired state of MetricCollector.
	// +required
	Spec MetricCollectorSpec `json:"spec"`

	// The observed status of MetricCollector.
	// +optional
	Status MetricCollectorStatus `json:"status,omitempty"`
}

// MetricCollectorSpec defines the desired state of MetricCollector.
type MetricCollectorSpec struct {
	// PrometheusURL is the URL of the Prometheus server.
	// Example: http://prometheus.test-ns.svc.cluster.local:9090
	// +required
	// +kubebuilder:validation:Pattern=`^https?://.*$`
	PrometheusURL string `json:"prometheusUrl"`

	// ReportNamespace is the namespace in the hub cluster where the MetricCollectorReport will be created.
	// This should be the fleet-member-{clusterName} namespace.
	// Example: fleet-member-cluster-1
	// +required
	ReportNamespace string `json:"reportNamespace"`
}

// MetricsEndpointSpec defines how to access the metrics endpoint.ctor.
type MetricCollectorStatus struct {
	// Conditions is an array of current observed conditions.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration is the generation most recently observed.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// WorkloadsMonitored is the count of workloads being monitored.
	// +optional
	WorkloadsMonitored int32 `json:"workloadsMonitored,omitempty"`

	// LastCollectionTime is when metrics were last collected.
	// +optional
	LastCollectionTime *metav1.Time `json:"lastCollectionTime,omitempty"`

	// CollectedMetrics contains the most recent metrics from each workload.
	// +optional
	CollectedMetrics []WorkloadMetrics `json:"collectedMetrics,omitempty"`
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

const (
	// MetricCollectorConditionTypeReady indicates the collector is ready.
	MetricCollectorConditionTypeReady string = "MetricCollectorReady"

	// MetricCollectorConditionTypeCollecting indicates metrics are being collected.
	MetricCollectorConditionTypeCollecting string = "MetricsCollecting"

	// MetricCollectorConditionTypeReported indicates metrics were successfully reported to hub.
	MetricCollectorConditionTypeReported string = "MetricsReported"
)

// +kubebuilder:object:root=true

// MetricCollectorList contains a list of MetricCollector.
type MetricCollectorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MetricCollector `json:"items"`
}

// GetConditions returns the conditions of the MetricCollector.
func (m *MetricCollector) GetConditions() []metav1.Condition {
	return m.Status.Conditions
}

// SetConditions sets the conditions of the MetricCollector.
func (m *MetricCollector) SetConditions(conditions ...metav1.Condition) {
	m.Status.Conditions = conditions
}

// GetCondition returns the condition of the given MetricCollector.
func (m *MetricCollector) GetCondition(conditionType string) *metav1.Condition {
	return meta.FindStatusCondition(m.Status.Conditions, conditionType)
}

// Ensure MetricCollector implements the ConditionedObj interface.
var _ apis.ConditionedObj = &MetricCollector{}

func init() {
	SchemeBuilder.Register(&MetricCollector{}, &MetricCollectorList{})
}
