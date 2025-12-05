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
	"k8s.io/klog/v2"

	placementv1beta1 "github.com/kubefleet-dev/standalone-metric-collector/apis/v1beta1"
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

// collectFromPrometheus collects metrics from a Prometheus endpoint
func (r *Reconciler) collectFromPrometheus(ctx context.Context, mc *placementv1beta1.MetricCollector) ([]placementv1beta1.WorkloadMetrics, error) {
	// Create Prometheus client without auth (simplified)
	promClient := NewPrometheusClient(mc.Spec.PrometheusURL, "", nil)

	// Build PromQL query for workload_health metric with label selector
	query := buildPromQLQuery(mc)
	klog.V(4).InfoS("Executing PromQL query", "query", query)

	result, err := promClient.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query Prometheus: %w", err)
	}

	// Parse Prometheus response
	data, ok := result.(PrometheusData)
	if !ok {
		return nil, fmt.Errorf("invalid Prometheus response type")
	}

	// Extract metrics for each workload
	workloadMetrics := make([]placementv1beta1.WorkloadMetrics, 0, len(data.Result))
	for _, res := range data.Result {
		namespace := res.Metric["namespace"]
		workloadName := res.Metric["app"]

		if namespace == "" || workloadName == "" {
			continue
		}

		// Extract health value
		var health float64
		if len(res.Value) >= 2 {
			if valueStr, ok := res.Value[1].(string); ok {
				fmt.Sscanf(valueStr, "%f", &health)
			}
		}

		wm := placementv1beta1.WorkloadMetrics{
			Namespace:    namespace,
			WorkloadName: workloadName,
			Health:       health == 1.0, // Convert to boolean: 1.0 = true, 0.0 = false
		}
		workloadMetrics = append(workloadMetrics, wm)
	}

	klog.V(2).InfoS("Collected metrics from Prometheus", "workloads", len(workloadMetrics))
	return workloadMetrics, nil
}

// buildPromQLQuery builds a PromQL query for workload_health metric
func buildPromQLQuery(mc *placementv1beta1.MetricCollector) string {
	// Query all workload_health metrics (MetricCollector is cluster-scoped)
	return `workload_health`
}
