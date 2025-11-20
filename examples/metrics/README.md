# Metric Collection API Examples

This directory contains examples for the Fleet metric collection system.

## Architecture

```
Member Cluster                                    Hub Cluster
┌─────────────────────────────────────┐          ┌──────────────────────────────────┐
│                                     │          │                                  │
│  ┌────────────────────────────┐    │          │  ┌────────────────────────────┐  │
│  │ Workload Pods              │    │          │  │ MetricCollectorReport      │  │
│  │ (sample-metric-app)        │    │          │  │ (cluster-1-...)            │  │
│  │ - Expose /metrics endpoint │    │          │  │                            │  │
│  └────────────┬───────────────┘    │          │  │ Status:                    │  │
│               │                     │          │  │ - collectedMetrics[]       │  │
│               │ scrape              │          │  │ - workloadsMonitored       │  │
│               ▼                     │          │  │ - lastCollectionTime       │  │
│  ┌────────────────────────────┐    │          │  └────────────────────────────┘  │
│  │ Prometheus                 │    │          │               ▲                  │
│  │ (test-ns namespace)        │    │          │               │                  │
│  └────────────┬───────────────┘    │          │               │ copy status      │
│               │                     │          │               │                  │
│               │ query               │          │  ┌────────────────────────────┐  │
│               ▼                     │          │  │ Member Agent               │  │
│  ┌────────────────────────────┐    │          │  │ (MC Status Reporter)       │  │
│  │ MetricCollector            │    │          │  │ - Watches MetricCollector  │  │
│  │ (test-ns namespace)        │────┼──────────┼──┤ - Creates/Updates MCR      │  │
│  │                            │    │          │  └────────────────────────────┘  │
│  │ Spec:                      │    │          │                                  │
│  │ - prometheusUrl            │    │          └──────────────────────────────────┘
│  │                            │    │
│  │ Status:                    │    │
│  │ - collectedMetrics[]       │    │
│  │   - namespace              │    │
│  │   - clusterName            │    │
│  │   - workloadName           │    │
│  │   - health (bool)          │    │
│  │ - workloadsMonitored       │    │
│  │ - lastCollectionTime       │    │
│  └────────────────────────────┘    │
│         ▲                           │
│         │ reconcile (every 30s)    │
│         │                           │
│  ┌────────────────────────────┐    │
│  │ Member Agent               │    │
│  │ (MC Controller)            │    │
│  └────────────────────────────┘    │
│                                     │
└─────────────────────────────────────┘
```

## Components

### MetricCollector (Member Cluster)

Runs on each member cluster as part of the member-agent. It:
- Watches for workloads matching the selector
- Scrapes Prometheus-compatible metrics endpoints
- Collects specified metrics (e.g., `workload_health`)
- Stores metrics in its status
- Reports metrics to hub via InternalMemberCluster

**Example:**
```yaml
apiVersion: placement.kubernetes-fleet.io/v1beta1
kind: MetricCollector
metadata:
  name: sample-app-collector
  namespace: fleet-system
spec:
  workloadSelector:
    labelSelector:
      matchLabels:
        app: sample-metric-app
    namespaces:
      - test-ns
  metricsEndpoint:
    port: 8080
    path: /metrics
  collectionInterval: 30s
  metricsToCollect:
    - workload_health
```

### MetricsAggregator (Hub Cluster)

Runs on the hub cluster as part of the hub-agent. It:
- Reads metrics from InternalMemberCluster statuses
- Aggregates metrics across all member clusters
- Determines workload health based on thresholds
- Provides fleet-wide health view

**Example:**
```yaml
apiVersion: placement.kubernetes-fleet.io/v1beta1
kind: MetricsAggregator
metadata:
  name: sample-app-aggregator
spec:
  metricCollectorRef:
    name: sample-app-collector
    namespace: fleet-system
  healthThreshold:
    metricName: workload_health
    minValue: "1"
```

## Setup

### 1. Deploy the Sample Metric App

First, deploy the sample application that exposes metrics:

```bash
kubectl apply -f ../sample-metric-app.yaml
```

This creates a Pod that exposes a `/metrics` endpoint with `workload_health` metric.

### 2. (Optional) Setup Prometheus

If using the Prometheus approach (recommended), ensure Prometheus is deployed:

```bash
# Add Prometheus helm repo
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update

# Install Prometheus
helm install prometheus prometheus-community/prometheus \
  --namespace monitoring \
  --create-namespace
```

Configure Prometheus to scrape your metric-app pods by adding annotations:
```yaml
annotations:
  prometheus.io/scrape: "true"
  prometheus.io/port: "8080"
  prometheus.io/path: "/metrics"
```

### 3. Create MetricCollector on Member Clusters

On each member cluster, create a MetricCollector:

**Option A: Using Prometheus (Recommended)**
```bash
kubectl apply -f - <<EOF
apiVersion: placement.kubernetes-fleet.io/v1beta1
kind: MetricCollector
metadata:
  name: sample-app-collector
  namespace: fleet-system
spec:
  workloadSelector:
    labelSelector:
      matchLabels:
        app: sample-metric-app
    namespaces:
      - test-ns
  metricsEndpoint:
    sourceType: prometheus
    prometheusEndpoint:
      url: http://prometheus-server.monitoring:9090
  collectionInterval: 30s
  metricsToCollect:
    - workload_health
EOF
```

The member-agent controller will:
- Query Prometheus: `workload_health{app="sample-metric-app",namespace="test-ns"}`
- Get all matching workload metrics in one API call
- Store collected metrics in the MetricCollector status

**Option B: Direct Pod Scraping (No Prometheus)**
```bash
kubectl apply -f - <<EOF
apiVersion: placement.kubernetes-fleet.io/v1beta1
kind: MetricCollector
metadata:
  name: sample-app-collector
  namespace: fleet-system
spec:
  workloadSelector:
    labelSelector:
      matchLabels:
        app: sample-metric-app
    namespaces:
      - test-ns
    workloadTypes:
      - Pod
  metricsEndpoint:
    sourceType: direct
    directEndpoint:
      port: 8080
      path: /metrics
  collectionInterval: 30s
  metricsToCollect:
    - workload_health
EOF
```

The member-agent controller will:
- Discover pods matching `app=sample-metric-app`
- Scrape `http://pod-ip:8080/metrics` for each pod
- Store collected metrics in the MetricCollector status

### 3. Create MetricsAggregator on Hub Cluster

On the hub cluster, create a MetricsAggregator:

```bash
kubectl apply -f metricsaggregator-sample.yaml
```

The hub-agent controller will:
- Read MetricCollector statuses from member clusters (via IMC)
- Aggregate metrics across all clusters
- Update the MetricsAggregator status with fleet-wide health

## Checking Status

### View MetricCollector Status (on member cluster)

```bash
kubectl get metriccollector sample-app-collector -n fleet-system -o yaml
```

Example status:
```yaml
status:
  conditions:
    - type: MetricCollectorReady
      status: "True"
      reason: CollectorConfigured
    - type: MetricsCollecting
      status: "True"
      reason: MetricsCollectedSuccessfully
  observedGeneration: 1
  workloadsMonitored: 1
  lastCollectionTime: "2025-11-19T10:30:00Z"
  collectedMetrics:
    - name: sample-metric-app-xyz
      namespace: test-ns
      kind: Pod
      metrics:
        workload_health: "1"
      labels:
        cluster_name: cluster-1
        workload_name: sample-metric-app
      lastScrapedTime: "2025-11-19T10:30:00Z"
      healthy: true
```

### View MetricsAggregator Status (on hub cluster)

```bash
kubectl get metricsaggregator sample-app-aggregator -o yaml
```

Example status:
```yaml
status:
  conditions:
    - type: MetricsAggregatorReady
      status: "True"
      reason: AggregatorConfigured
    - type: MetricsAggregating
      status: "True"
      reason: MetricsAggregatedSuccessfully
    - type: AllClustersHealthy
      status: "True"
      reason: AllWorkloadsHealthy
  observedGeneration: 1
  totalClusters: 3
  healthyClusters: 3
  totalWorkloads: 3
  healthyWorkloads: 3
  lastUpdateTime: "2025-11-19T10:30:00Z"
  clusterMetrics:
    - clusterName: cluster-1
      workloadsMonitored: 1
      healthyWorkloads: 1
      unhealthyWorkloads: 0
      lastReportedTime: "2025-11-19T10:30:00Z"
      clusterHealthy: true
      workloadHealth:
        - name: sample-metric-app-xyz
          namespace: test-ns
          kind: Pod
          healthy: true
          healthMetricValue: "1"
          lastScrapedTime: "2025-11-19T10:30:00Z"
    - clusterName: cluster-2
      workloadsMonitored: 1
      healthyWorkloads: 1
      unhealthyWorkloads: 0
      lastReportedTime: "2025-11-19T10:30:00Z"
      clusterHealthy: true
      workloadHealth:
        - name: sample-metric-app-abc
          namespace: test-ns
          kind: Pod
          healthy: true
          healthMetricValue: "1"
          lastScrapedTime: "2025-11-19T10:30:00Z"
```

## How It Works

### 1. MetricCollector Controller (Member Agent)

The controller on the member cluster:

**Prometheus Mode (Recommended):**
1. Watches MetricCollector CRs
2. Builds Prometheus query based on `workloadSelector`
   ```
   Query: workload_health{app="sample-metric-app",namespace="test-ns"}
   ```
3. Calls Prometheus API:
   ```
   GET http://prometheus-server.monitoring:9090/api/v1/query?query=workload_health{...}
   ```
4. Parses JSON response containing all matching pods' metrics
5. Updates MetricCollector status with collected metrics
6. Reports metrics via InternalMemberCluster status

**Direct Mode:**
1. Watches MetricCollector CRs
2. Uses `workloadSelector` to list matching Pods
3. For each Pod:
   - Builds URL: `http://<pod-ip>:<port><path>`
   - Scrapes metrics using HTTP client
   - Parses Prometheus text format
4. Updates MetricCollector status with collected metrics
5. Reports metrics via InternalMemberCluster status

**Key Difference:**
- Prometheus: **1 API call** → get all workload metrics
- Direct: **N API calls** → one per pod

### 2. MetricsAggregator Controller (Hub Agent)

The controller on the hub cluster:

1. Watches MetricsAggregator CRs
2. Lists InternalMemberClusters (filtered by `clusterSelector` if specified)
3. For each cluster:
   - Reads MetricCollector status from IMC
   - Extracts workload metrics
   - Applies health threshold (e.g., `workload_health >= 1`)
   - Determines per-workload and per-cluster health
4. Aggregates across all clusters:
   - Counts total/healthy clusters and workloads
   - Sets conditions (Ready, Aggregating, AllClustersHealthy)
5. Updates MetricsAggregator status

## Integration with Sample Metric App

The metric-app (`cmd/metric-app/main.go`) exposes:

```
workload_health{cluster_name="cluster-1", workload_name="sample-metric-app"} 1
```

- **Metric Name**: `workload_health`
- **Labels**: `cluster_name`, `workload_name` (from environment variables)
- **Value**: `1` (healthy) or `0` (unhealthy)

The MetricCollector scrapes this and stores:
```yaml
metrics:
  workload_health: "1"
labels:
  cluster_name: "cluster-1"
  workload_name: "sample-metric-app"
healthy: true  # Because workload_health == 1
```

## Advanced Examples

### Monitor Multiple Workload Types

```yaml
apiVersion: placement.kubernetes-fleet.io/v1beta1
kind: MetricCollector
metadata:
  name: multi-workload-collector
  namespace: fleet-system
spec:
  workloadSelector:
    labelSelector:
      matchLabels:
        tier: backend
    workloadTypes:
      - Deployment
      - StatefulSet
  metricsEndpoint:
    port: 9090
    path: /metrics
  metricsToCollect:
    - http_requests_total
    - http_request_duration_seconds
    - workload_health
```

### Cluster-Specific Aggregation

```yaml
apiVersion: placement.kubernetes-fleet.io/v1beta1
kind: MetricsAggregator
metadata:
  name: prod-clusters-aggregator
spec:
  metricCollectorRef:
    name: multi-workload-collector
    namespace: fleet-system
  clusterSelector:
    matchLabels:
      environment: production
  healthThreshold:
    metricName: workload_health
    minValue: "1"
```

## Troubleshooting

### Metrics Not Being Collected

Check MetricCollector status:
```bash
kubectl get mc sample-app-collector -n fleet-system -o jsonpath='{.status.conditions}'
```

Common issues:
- Pod selector not matching any pods
- Wrong port or path
- Network policy blocking access to pod metrics endpoint
- Workload not exposing metrics

### Aggregator Not Showing Metrics

Check:
1. MetricCollector is running and collecting on member clusters
2. InternalMemberCluster status contains metrics
3. MetricsAggregator `metricCollectorRef` matches the MetricCollector name/namespace

```bash
# On hub cluster
kubectl get imc -A -o yaml | grep -A 20 "collectedMetrics"
```

## Future Enhancements

- Support for multiple health metrics
- Alerting based on aggregated metrics
- Historical metric trends
- Integration with external monitoring systems
- Automatic scaling based on metrics
