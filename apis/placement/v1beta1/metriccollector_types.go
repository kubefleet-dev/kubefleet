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

	"github.com/kubefleet-dev/kubefleet/apis"
)

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope="Namespaced",shortName=mc,categories={fleet,fleet-metrics}
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
	// WorkloadSelector defines which workloads to monitor.
	// +required
	WorkloadSelector WorkloadSelectorSpec `json:"workloadSelector"`

	// MetricsEndpoint defines how to access the metrics endpoint.
	// +required
	MetricsEndpoint MetricsEndpointSpec `json:"metricsEndpoint"`

	// CollectionInterval specifies how often to scrape metrics.
	// Default is 30s. Minimum is 10s.
	// +kubebuilder:default="30s"
	// +kubebuilder:validation:Pattern="^([0-9]+(\\.[0-9]+)?(s|m|h))+$"
	// +optional
	CollectionInterval string `json:"collectionInterval,omitempty"`

	// MetricsToCollect specifies which metrics to collect from the endpoint.
	// If empty, specific metrics like workload_health will be collected.
	// +optional
	// +kubebuilder:validation:MaxItems=50
	MetricsToCollect []string `json:"metricsToCollect,omitempty"`
}

// WorkloadSelectorSpec defines how to select workloads for monitoring.
type WorkloadSelectorSpec struct {
	// LabelSelector to match workloads.
	// +required
	LabelSelector *metav1.LabelSelector `json:"labelSelector"`

	// Namespaces to monitor. If empty, all namespaces are monitored.
	// +optional
	// +kubebuilder:validation:MaxItems=100
	Namespaces []string `json:"namespaces,omitempty"`

	// WorkloadTypes specifies which types of workloads to monitor.
	// Supported: Deployment, StatefulSet, DaemonSet, Pod.
	// If empty, only Pods are monitored.
	// +optional
	// +kubebuilder:validation:MaxItems=10
	WorkloadTypes []string `json:"workloadTypes,omitempty"`
}

// MetricsEndpointSpec defines how to access the metrics endpoint.
type MetricsEndpointSpec struct {
	// SourceType defines the type of metrics source.
	// "prometheus" - Query a centralized Prometheus server (recommended for production)
	// "direct" - Directly scrape each pod's metrics endpoint
	// Default is "prometheus".
	// +kubebuilder:validation:Enum=prometheus;direct
	// +kubebuilder:default="prometheus"
	// +optional
	SourceType string `json:"sourceType,omitempty"`

	// PrometheusEndpoint specifies the Prometheus server to query.
	// Required when SourceType is "prometheus".
	// +optional
	PrometheusEndpoint *PrometheusEndpointSpec `json:"prometheusEndpoint,omitempty"`

	// DirectEndpoint specifies how to scrape pods directly.
	// Required when SourceType is "direct".
	// +optional
	DirectEndpoint *DirectEndpointSpec `json:"directEndpoint,omitempty"`
}

// PrometheusEndpointSpec defines how to connect to a Prometheus server.
type PrometheusEndpointSpec struct {
	// URL of the Prometheus server.
	// Example: http://prometheus-server.monitoring:9090
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^https?://.*$`
	URL string `json:"url"`

	// Auth specifies authentication configuration for Prometheus.
	// +optional
	Auth *PrometheusAuthConfig `json:"auth,omitempty"`
}

// PrometheusAuthConfig specifies authentication for Prometheus.
type PrometheusAuthConfig struct {
	// Type of authentication (bearer or basic).
	// +kubebuilder:validation:Enum=bearer;basic
	// +optional
	Type string `json:"type,omitempty"`

	// SecretRef references a secret containing authentication credentials.
	// For bearer: key "token"
	// For basic: keys "username" and "password"
	// +optional
	SecretRef *SecretReference `json:"secretRef,omitempty"`
}

// SecretReference identifies a secret.
type SecretReference struct {
	// Name of the secret.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// Namespace of the secret.
	// If not specified, the namespace of the MetricCollector will be used.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// DirectEndpointSpec defines how to scrape pods directly.
type DirectEndpointSpec struct {
	// Port where metrics are exposed on pods.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +required
	Port int32 `json:"port"`

	// Path to the metrics endpoint.
	// Default is "/metrics".
	// +kubebuilder:default="/metrics"
	// +optional
	Path string `json:"path,omitempty"`

	// Scheme for the metrics endpoint (http or https).
	// Default is "http".
	// +kubebuilder:default="http"
	// +kubebuilder:validation:Enum=http;https
	// +optional
	Scheme string `json:"scheme,omitempty"`
}

// MetricCollectorStatus defines the observed state of MetricCollector.
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

// WorkloadMetrics represents metrics collected from a single workload.
type WorkloadMetrics struct {
	// WorkloadName is the name of the workload.
	// +required
	Name string `json:"name"`

	// WorkloadNamespace is the namespace of the workload.
	// +required
	Namespace string `json:"namespace"`

	// WorkloadKind is the kind of workload (Pod, Deployment, etc.).
	// +required
	Kind string `json:"kind"`

	// Metrics contains the collected metric values.
	// Key is metric name, value is the metric value.
	// +optional
	Metrics map[string]string `json:"metrics,omitempty"`

	// Labels from the metric (like cluster_name, workload_name from the app).
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// LastScrapedTime is when metrics were last scraped from this workload.
	// +optional
	LastScrapedTime *metav1.Time `json:"lastScrapedTime,omitempty"`

	// Healthy indicates if the workload is healthy based on metrics.
	// +optional
	Healthy *bool `json:"healthy,omitempty"`

	// ErrorMessage if scraping failed.
	// +optional
	ErrorMessage string `json:"errorMessage,omitempty"`
}

const (
	// MetricCollectorConditionTypeReady indicates the collector is ready.
	MetricCollectorConditionTypeReady string = "MetricCollectorReady"

	// MetricCollectorConditionTypeCollecting indicates metrics are being collected.
	MetricCollectorConditionTypeCollecting string = "MetricsCollecting"
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
