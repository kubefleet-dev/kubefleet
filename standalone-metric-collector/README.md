# Standalone MetricCollector

This is a standalone implementation of the MetricCollector controller for Kubernetes Fleet. It collects workload health metrics from Prometheus on member clusters and reports them to the hub cluster.

## Overview

The MetricCollector controller:
- Runs on member clusters
- Watches `MetricCollector` CRDs on the member cluster
- Queries Prometheus for `workload_health` metrics
- Creates/updates `MetricCollectorReport` resources on the hub cluster in `fleet-{cluster}` namespaces
- Supports both token and certificate-based authentication to the hub cluster

## Prerequisites

- Kubernetes cluster (member cluster)
- Access to a hub cluster
- Prometheus running on the member cluster
- Hub cluster credentials (token or certificates)

## Installation

### 1. Create Hub Token Secret

On the **member cluster**, create a secret with the hub cluster token:

```bash
kubectl create namespace fleet-system

kubectl create secret generic hub-token \
  --from-literal=token=<your-hub-token> \
  -n fleet-system
```

### 2. Install the Helm Chart

```bash
helm install metric-collector ./charts/metric-collector \
  --namespace fleet-system \
  --set memberCluster.name=cluster-1 \
  --set hubCluster.url=https://hub-cluster:6443 \
  --set prometheus.url=http://prometheus.test-ns:9090
```

### 3. Verify Installation

```bash
kubectl get pods -n fleet-system
kubectl logs -n fleet-system deployment/metric-collector
```

## Configuration

Key configuration options in `values.yaml`:

```yaml
memberCluster:
  name: "cluster-1"  # Your cluster name

hubCluster:
  url: "https://hub-cluster:6443"  # Hub API server URL
  auth:
    tokenSecretName: "hub-token"  # Secret with hub token

prometheus:
  url: "http://prometheus.test-ns:9090"  # Prometheus URL
```

See [values.yaml](charts/metric-collector/values.yaml) for all options.

## Hub Cluster Setup

On the **hub cluster**, you need to create RBAC permissions for the MetricCollector:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: metric-collector-hub-access
rules:
  - apiGroups: ["placement.kubernetes-fleet.io"]
    resources: ["metriccollectorreports"]
    verbs: ["get", "list", "create", "update", "patch", "delete"]
  - apiGroups: [""]
    resources: ["namespaces"]
    verbs: ["get", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: metric-collector-cluster-1
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: metric-collector-hub-access
subjects:
  - kind: ServiceAccount
    name: metric-collector-sa  # Match your token's SA
    namespace: fleet-system
```

## Development

### Build Binary

```bash
make build
```

### Build Docker Image

```bash
make docker-build IMG=your-registry/metric-collector:tag
```

### Run Locally

```bash
export HUB_SERVER_URL=https://hub-cluster:6443
export PROMETHEUS_URL=http://localhost:9090
export CONFIG_PATH=/path/to/hub-token

make run
```

## Architecture

```
Member Cluster:
  ┌─────────────────────────────────────┐
  │  MetricCollector Controller         │
  │  ┌───────────────────────────────┐  │
  │  │  Member Client (in-cluster)   │  │ ─── Watches MetricCollector CRDs
  │  └───────────────────────────────┘  │
  │  ┌───────────────────────────────┐  │
  │  │  Hub Client (remote)          │  │ ─── Creates MetricCollectorReport
  │  └───────────────────────────────┘  │
  │  ┌───────────────────────────────┐  │
  │  │  Prometheus Client            │  │ ─── Queries metrics
  │  └───────────────────────────────┘  │
  └─────────────────────────────────────┘
           │              │
           │              └────────────────┐
           ▼                               ▼
    Prometheus (9090)              Hub Cluster (6443)
    workload_health metrics        fleet-{cluster} namespace
```

## Troubleshooting

### Controller not starting

Check logs:
```bash
kubectl logs -n fleet-system deployment/metric-collector
```

Common issues:
- Hub cluster URL incorrect
- Token expired or invalid
- Network connectivity to hub cluster

### No metrics collected

1. Verify Prometheus is accessible:
```bash
kubectl run -it --rm debug --image=curlimages/curl --restart=Never -- \
  curl http://prometheus.test-ns:9090/api/v1/query?query=workload_health
```

2. Check MetricCollector status:
```bash
kubectl get metriccollectors -A
kubectl describe metriccollector <name>
```

### Reports not appearing on hub

1. Verify hub cluster connectivity
2. Check RBAC permissions on hub cluster
3. Verify `fleet-{cluster}` namespace exists on hub

## License

Apache License 2.0
