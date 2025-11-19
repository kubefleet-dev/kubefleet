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

package metriccollector

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

// PrometheusClient is the interface for querying Prometheus
type PrometheusClient interface {
	Query(ctx context.Context, query string) (interface{}, error)
}

// prometheusClient implements PrometheusClient for querying Prometheus API
type prometheusClient struct {
	baseURL    string
	authType   string
	authSecret *corev1.Secret
	httpClient *http.Client
}

// NewPrometheusClient creates a new Prometheus client
func NewPrometheusClient(baseURL, authType string, authSecret *corev1.Secret) PrometheusClient {
	return &prometheusClient{
		baseURL:    baseURL,
		authType:   authType,
		authSecret: authSecret,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Query executes a PromQL query against Prometheus API
func (c *prometheusClient) Query(ctx context.Context, query string) (interface{}, error) {
	// Build query URL
	queryURL := fmt.Sprintf("%s/api/v1/query", strings.TrimSuffix(c.baseURL, "/"))
	params := url.Values{}
	params.Add("query", query)
	fullURL := fmt.Sprintf("%s?%s", queryURL, params.Encode())

	// Create request
	req, err := http.NewRequestWithContext(ctx, "GET", fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add authentication
	if err := c.addAuth(req); err != nil {
		return nil, fmt.Errorf("failed to add authentication: %w", err)
	}

	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to query Prometheus: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Prometheus query failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var result PrometheusResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if result.Status != "success" {
		return nil, fmt.Errorf("Prometheus query failed: %s", result.Error)
	}

	return result.Data, nil
}

// addAuth adds authentication to the request
func (c *prometheusClient) addAuth(req *http.Request) error {
	if c.authType == "" || c.authSecret == nil {
		return nil
	}

	switch c.authType {
	case "bearer":
		token, ok := c.authSecret.Data["token"]
		if !ok {
			return fmt.Errorf("token not found in secret")
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", string(token)))
	case "basic":
		username, ok := c.authSecret.Data["username"]
		if !ok {
			return fmt.Errorf("username not found in secret")
		}
		password, ok := c.authSecret.Data["password"]
		if !ok {
			return fmt.Errorf("password not found in secret")
		}
		auth := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", username, password)))
		req.Header.Set("Authorization", fmt.Sprintf("Basic %s", auth))
	}

	return nil
}

// PrometheusResponse represents the Prometheus API response
type PrometheusResponse struct {
	Status string         `json:"status"`
	Data   PrometheusData `json:"data"`
	Error  string         `json:"error,omitempty"`
}

// PrometheusData represents the data section of Prometheus response
type PrometheusData struct {
	ResultType string             `json:"resultType"`
	Result     []PrometheusResult `json:"result"`
}

// PrometheusResult represents a single result from Prometheus
type PrometheusResult struct {
	Metric map[string]string `json:"metric"`
	Value  []interface{}     `json:"value"` // [timestamp, value]
}

// MetricsCollector is the interface for collecting metrics from pods
type MetricsCollector interface {
	CollectFromPods(ctx context.Context, mc *placementv1beta1.MetricCollector) ([]placementv1beta1.WorkloadMetrics, error)
}

// collectFromPrometheus collects metrics from a Prometheus endpoint
func (r *Reconciler) collectFromPrometheus(ctx context.Context, mc *placementv1beta1.MetricCollector) ([]placementv1beta1.WorkloadMetrics, error) {
	promEndpoint := mc.Spec.MetricsEndpoint.PrometheusEndpoint
	if promEndpoint == nil {
		return nil, fmt.Errorf("prometheusEndpoint is nil")
	}

	// Fetch authentication secret if configured
	var authSecret *corev1.Secret
	if promEndpoint.Auth != nil && promEndpoint.Auth.SecretRef != nil {
		secret := &corev1.Secret{}
		secretName := types.NamespacedName{
			Name:      promEndpoint.Auth.SecretRef.Name,
			Namespace: mc.Namespace,
		}
		if err := r.Get(ctx, secretName, secret); err != nil {
			return nil, fmt.Errorf("failed to get auth secret: %w", err)
		}
		authSecret = secret
	}

	// Create Prometheus client
	authType := ""
	if promEndpoint.Auth != nil {
		authType = promEndpoint.Auth.Type
	}
	promClient := NewPrometheusClient(promEndpoint.URL, authType, authSecret)

	// Build PromQL query from workloadSelector and metricsToCollect
	queries := buildPromQLQueries(mc)
	if len(queries) == 0 {
		return nil, fmt.Errorf("no metrics to collect")
	}

	// Collect metrics for each query
	workloadMetricsMap := make(map[string]*placementv1beta1.WorkloadMetrics)

	for metricName, query := range queries {
		klog.V(4).InfoS("Executing PromQL query", "metric", metricName, "query", query)
		result, err := promClient.Query(ctx, query)
		if err != nil {
			klog.ErrorS(err, "Failed to query Prometheus", "metric", metricName, "query", query)
			continue
		}

		// Parse Prometheus response
		data, ok := result.(PrometheusData)
		if !ok {
			klog.ErrorS(nil, "Invalid Prometheus response type", "metric", metricName)
			continue
		}

		// Extract metrics for each workload
		for _, res := range data.Result {
			workloadKey := buildWorkloadKey(res.Metric)
			if workloadKey == "" {
				continue
			}

			// Get or create workload metrics entry
			if _, exists := workloadMetricsMap[workloadKey]; !exists {
				workloadMetricsMap[workloadKey] = &placementv1beta1.WorkloadMetrics{
					Name:      res.Metric["pod"],
					Namespace: res.Metric["namespace"],
					Kind:      getWorkloadKind(res.Metric),
					Metrics:   make(map[string]string),
				}
			}

			// Extract metric value
			if len(res.Value) >= 2 {
				if value, ok := res.Value[1].(string); ok {
					workloadMetricsMap[workloadKey].Metrics[metricName] = value
				}
			}
		}
	}

	// Convert map to slice
	workloadMetrics := make([]placementv1beta1.WorkloadMetrics, 0, len(workloadMetricsMap))
	for _, wm := range workloadMetricsMap {
		// Set health status based on workload_health metric if present
		if healthValue, ok := wm.Metrics["workload_health"]; ok {
			healthy := false
			if healthValue == "1" || healthValue == "1.0" {
				healthy = true
			}
			wm.Healthy = &healthy
		}
		workloadMetrics = append(workloadMetrics, *wm)
	}

	klog.V(2).InfoS("Collected metrics from Prometheus", "workloads", len(workloadMetrics))
	return workloadMetrics, nil
}

// buildPromQLQueries builds PromQL queries based on workloadSelector and metricsToCollect
func buildPromQLQueries(mc *placementv1beta1.MetricCollector) map[string]string {
	queries := make(map[string]string)
	selector := mc.Spec.WorkloadSelector

	// Build label selector string
	labelSelectors := []string{}
	if selector.LabelSelector != nil {
		for key, value := range selector.LabelSelector.MatchLabels {
			labelSelectors = append(labelSelectors, fmt.Sprintf(`%s="%s"`, key, value))
		}
	}

	// Add namespace filter if specified
	if len(selector.Namespaces) > 0 {
		namespaceFilter := strings.Join(selector.Namespaces, "|")
		labelSelectors = append(labelSelectors, fmt.Sprintf(`namespace=~"%s"`, namespaceFilter))
	}

	labelSelectorStr := strings.Join(labelSelectors, ",")

	// Build query for each metric
	for _, metricName := range mc.Spec.MetricsToCollect {
		if labelSelectorStr != "" {
			queries[metricName] = fmt.Sprintf("%s{%s}", metricName, labelSelectorStr)
		} else {
			queries[metricName] = metricName
		}
	}

	return queries
}

// buildWorkloadKey creates a unique key for a workload from metric labels
func buildWorkloadKey(labels map[string]string) string {
	namespace := labels["namespace"]
	pod := labels["pod"]
	if namespace == "" || pod == "" {
		return ""
	}
	return fmt.Sprintf("%s/%s", namespace, pod)
}

// getWorkloadKind attempts to determine the workload kind from metric labels
func getWorkloadKind(labels map[string]string) string {
	// Try common Kubernetes labels
	if kind := labels["kind"]; kind != "" {
		return kind
	}
	if labels["deployment"] != "" {
		return "Deployment"
	}
	if labels["statefulset"] != "" {
		return "StatefulSet"
	}
	if labels["daemonset"] != "" {
		return "DaemonSet"
	}
	return "Pod" // Default to Pod if unable to determine
}

// collectDirect collects metrics by directly scraping pod endpoints
func (r *Reconciler) collectDirect(ctx context.Context, mc *placementv1beta1.MetricCollector) ([]placementv1beta1.WorkloadMetrics, error) {
	// TODO: Implement direct pod scraping logic
	// 1. List pods using workloadSelector
	// 2. For each pod, scrape metrics endpoint
	// 3. Parse Prometheus text format
	// 4. Return WorkloadMetrics array
	return nil, nil
}
