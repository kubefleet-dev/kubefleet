package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

// PrometheusQueryResult represents the Prometheus API response
type PrometheusQueryResult struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []interface{}     `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: prometheus-test <prometheus-url> <metric-name>")
		fmt.Println("Example: prometheus-test http://localhost:9090 workload_health")
		os.Exit(1)
	}

	prometheusURL := os.Args[1]
	metricName := os.Args[2]

	// Build the query - get all instances of the specified metric
	query := metricName

	// Optional: filter by namespace if provided
	if len(os.Args) > 3 {
		namespace := os.Args[3]
		query = fmt.Sprintf(`%s{namespace="%s"}`, metricName, namespace)
	}

	fmt.Printf("Querying Prometheus at: %s\n", prometheusURL)
	fmt.Printf("Query: %s\n", query)
	fmt.Println()

	// Query Prometheus
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := queryPrometheus(ctx, prometheusURL, query)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error querying Prometheus: %v\n", err)
		os.Exit(1)
	}

	// Print results
	fmt.Printf("Status: %s\n", result.Status)
	fmt.Printf("Result Type: %s\n", result.Data.ResultType)
	fmt.Printf("Number of results: %d\n", len(result.Data.Result))
	fmt.Println()

	for i, res := range result.Data.Result {
		fmt.Printf("Result %d:\n", i+1)
		fmt.Printf("  Labels:\n")
		for k, v := range res.Metric {
			fmt.Printf("    %s: %s\n", k, v)
		}
		
		if len(res.Value) >= 2 {
			timestamp := res.Value[0]
			value := res.Value[1]
			fmt.Printf("  Timestamp: %v\n", timestamp)
			fmt.Printf("  Value: %v\n", value)
		}
		fmt.Println()
	}
}

// queryPrometheus queries the Prometheus HTTP API
func queryPrometheus(ctx context.Context, prometheusURL, query string) (*PrometheusQueryResult, error) {
	// Build the query URL
	apiURL := fmt.Sprintf("%s/api/v1/query", prometheusURL)
	
	// Add query parameters
	params := url.Values{}
	params.Add("query", query)
	
	fullURL := fmt.Sprintf("%s?%s", apiURL, params.Encode())
	
	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "GET", fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	
	// Execute request
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()
	
	// Check status code
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}
	
	// Parse response
	var result PrometheusQueryResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	
	if result.Status != "success" {
		return nil, fmt.Errorf("prometheus query failed with status: %s", result.Status)
	}
	
	return &result, nil
}
