# Prometheus Setup for MetricCollector Testing

This directory contains manifests to deploy Prometheus for testing the MetricCollector controller with the sample-metric-app.

## Prerequisites

- Kind cluster running (e.g., cluster-1, cluster-2, or cluster-3)
- `test-ns` namespace exists
- `ghcr.io/metric-app:6d6cd69` image loaded into the cluster

## Quick Start

```bash
# Switch to target cluster
kubectl config use-context kind-cluster-1

# Create namespace if needed
kubectl create namespace test-ns --dry-run=client -o yaml | kubectl apply -f -

# Deploy Prometheus
kubectl apply -f rbac.yaml
kubectl apply -f configmap.yaml
kubectl apply -f deployment.yaml
kubectl apply -f service.yaml

# Deploy sample-metric-app (from parent examples directory)
kubectl apply -f ../sample-metric-app.yaml

# Wait for pods to be ready
kubectl wait --for=condition=ready pod -l app=prometheus -n test-ns --timeout=60s
kubectl wait --for=condition=ready pod -l app=sample-metric-app -n test-ns --timeout=60s
```

## Verify Setup

### Check Prometheus is scraping metrics

```bash
# Port-forward Prometheus
kubectl port-forward -n test-ns svc/prometheus 9090:9090
```

Open http://localhost:9090 in your browser and:
1. Go to Status > Targets - you should see `sample-metric-app` pod listed
2. Go to Graph and query: `workload_health` - you should see metrics

### Test with prometheus-test tool

```bash
# In another terminal (while port-forward is running)
cd tools/prometheus-test
go build -o prometheus-test main.go
./prometheus-test http://localhost:9090 workload_health

# Or with namespace filter
./prometheus-test http://localhost:9090 workload_health test-ns
```

Expected output:
```
Querying Prometheus at: http://localhost:9090
Query: workload_health{namespace="test-ns"}

Status: success
Result Type: vector
Number of results: 1

Result 1:
  Labels:
    app: sample-metric-app
    namespace: test-ns
    pod: sample-metric-app-xxxxx
  Timestamp: 1732032000.0
  Value: 1
```

## Configuration Details

### Prometheus ConfigMap
The Prometheus configuration discovers pods with these annotations:
- `prometheus.io/scrape: "true"` - Enable scraping
- `prometheus.io/port: "8080"` - Port to scrape
- `prometheus.io/path: "/metrics"` - Metrics endpoint path

The sample-metric-app already has these annotations configured.

### RBAC
Prometheus needs permissions to discover and scrape pods. The `rbac.yaml` creates:
- ServiceAccount for Prometheus
- ClusterRole with pod discovery permissions
- ClusterRoleBinding to grant permissions

## Testing MetricCollector

Once Prometheus is running, create a MetricCollector CR:

```bash
kubectl apply -f - <<EOF
apiVersion: placement.kubernetes-fleet.io/v1beta1
kind: MetricCollector
metadata:
  name: sample-collector
  namespace: test-ns
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
      url: http://prometheus.test-ns.svc.cluster.local:9090
  collectionInterval: "30s"
  metricsToCollect:
    - workload_health
