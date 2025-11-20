# Prometheus Test Tool

A simple command-line tool to test querying Prometheus API for metrics.

## Build

```bash
go build -o prometheus-test main.go
```

## Usage

```bash
./prometheus-test <prometheus-url> <metric-name> [namespace]
```

### Examples

Query all instances of `workload_health` metric:
```bash
./prometheus-test http://localhost:9090 workload_health
```

Query `workload_health` metric filtered by namespace:
```bash
./prometheus-test http://localhost:9090 workload_health test-ns
```

Query with port-forward to a Prometheus running in Kubernetes:
```bash
# In one terminal, forward Prometheus port
kubectl port-forward -n monitoring svc/prometheus 9090:9090

# In another terminal, query
./prometheus-test http://localhost:9090 workload_health default
```

## Output

The tool will display:
- Query status
- Number of results
- For each result:
  - All metric labels (pod, namespace, etc.)
  - Timestamp
  - Metric value

## Testing with the Fleet Sample Metric App

If you have the sample-metric-app deployed:

```bash
# Port-forward Prometheus
kubectl port-forward -n monitoring svc/prometheus 9090:9090

# Query the workload_health metric
./prometheus-test http://localhost:9090 'workload_health{app="sample-metric-app"}'
```

## Implementation Notes

This tool uses the same query approach that will be used in the MetricCollector controller:
1. Builds a Prometheus query URL with `/api/v1/query` endpoint
2. Adds query parameters
3. Makes HTTP GET request with context timeout
4. Parses JSON response
5. Extracts metric labels and values

The code in this tool can be directly adapted for use in the MetricCollector controller.
