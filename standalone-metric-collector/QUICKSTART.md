# Quick Start Guide

This guide will help you quickly install and configure the MetricCollector on a member cluster.

## Prerequisites

- Kubernetes member cluster (v1.24+)
- Access to a hub cluster
- Prometheus running on the member cluster
- Helm 3.x

## Installation Steps

### Step 1: Setup Hub Cluster RBAC

See [HUB_SETUP.md](HUB_SETUP.md) for detailed instructions.

Quick version:

```bash
# On hub cluster
kubectl create namespace fleet-member-cluster-1
kubectl create serviceaccount metric-collector-sa -n fleet-member-cluster-1

# Apply RBAC
helm template metric-collector ./charts/metric-collector \
  --set hubCluster.createRBAC=true \
  --set memberCluster.name=cluster-1 \
  --show-only templates/hub-rbac.yaml | \
  kubectl apply -f - --context=hub-cluster

# Create token
kubectl apply -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: metric-collector-token
  namespace: fleet-member-cluster-1
  annotations:
    kubernetes.io/service-account.name: metric-collector-sa
type: kubernetes.io/service-account-token
EOF

# Get token
kubectl get secret metric-collector-token -n fleet-member-cluster-1 \
  -o jsonpath='{.data.token}' | base64 -d > hub-token.txt
```

### Step 2: Install MetricCollector CRDs

```bash
# On member cluster
kubectl apply -f config/crd/bases/placement.kubernetes-fleet.io_metriccollectors.yaml
kubectl apply -f config/crd/bases/placement.kubernetes-fleet.io_metriccollectorreports.yaml
```

### Step 3: Create Hub Token Secret

```bash
# On member cluster
kubectl create namespace fleet-system

kubectl create secret generic hub-token \
  --from-file=token=hub-token.txt \
  -n fleet-system
```

### Step 4: Install Helm Chart

```bash
# On member cluster
helm install metric-collector ./charts/metric-collector \
  --namespace fleet-system \
  --set memberCluster.name=cluster-1 \
  --set hubCluster.url=https://hub-api-server:6443 \
  --set prometheus.url=http://prometheus.test-ns:9090
```

### Step 5: Verify Installation

```bash
# Check pod is running
kubectl get pods -n fleet-system

# Check logs
kubectl logs -n fleet-system deployment/metric-collector

# On hub cluster, check for reports
kubectl get metriccollectorreports -n fleet-cluster-1
```

## Example: Complete Setup

Here's a complete example with real values:

```bash
# === On Hub Cluster ===
export MEMBER_CLUSTER_NAME="prod-us-east-1"
export HUB_NAMESPACE="fleet-member-${MEMBER_CLUSTER_NAME}"

# Create namespace and SA
kubectl create namespace ${HUB_NAMESPACE}
kubectl create serviceaccount metric-collector-sa -n ${HUB_NAMESPACE}

# Apply RBAC
cat <<EOF | kubectl apply -f -
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
  name: metric-collector-${MEMBER_CLUSTER_NAME}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: metric-collector-hub-access
subjects:
  - kind: ServiceAccount
    name: metric-collector-sa
    namespace: ${HUB_NAMESPACE}
EOF

# Create token
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: metric-collector-token
  namespace: ${HUB_NAMESPACE}
  annotations:
    kubernetes.io/service-account.name: metric-collector-sa
type: kubernetes.io/service-account-token
EOF

# Wait for token to be created
sleep 2

# Get token
kubectl get secret metric-collector-token -n ${HUB_NAMESPACE} \
  -o jsonpath='{.data.token}' | base64 -d > /tmp/hub-token.txt

echo "Hub token saved to /tmp/hub-token.txt"

# Get hub API server URL
export HUB_URL=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')
echo "Hub URL: ${HUB_URL}"

# === On Member Cluster ===

# Switch to member cluster context
kubectl config use-context prod-us-east-1

# Create namespace
kubectl create namespace fleet-system

# Create token secret
kubectl create secret generic hub-token \
  --from-file=token=/tmp/hub-token.txt \
  -n fleet-system

# Install CRDs
kubectl apply -f config/crd/bases/placement.kubernetes-fleet.io_metriccollectors.yaml
kubectl apply -f config/crd/bases/placement.kubernetes-fleet.io_metriccollectorreports.yaml

# Install chart
helm install metric-collector ./charts/metric-collector \
  --namespace fleet-system \
  --set memberCluster.name=${MEMBER_CLUSTER_NAME} \
  --set hubCluster.url=${HUB_URL} \
  --set prometheus.url=http://prometheus.monitoring:9090 \
  --set image.tag=v0.1.0 \
  --set resources.limits.memory=256Mi \
  --set resources.requests.memory=128Mi

# Verify
kubectl get pods -n fleet-system
kubectl logs -n fleet-system -l app.kubernetes.io/name=metric-collector --tail=50

# === Verification on Hub ===
kubectl config use-context hub-cluster
kubectl get metriccollectorreports -n fleet-${MEMBER_CLUSTER_NAME}
```

## Next Steps

1. **Create a MetricCollector**: See [examples/metriccollector-sample.yaml](../examples/metrics/metriccollector-sample.yaml)
2. **Setup Prometheus**: Ensure Prometheus has `workload_health` metrics
3. **Monitor Reports**: Watch for `MetricCollectorReport` resources on the hub cluster
4. **Integration**: Use with ApprovalRequest controller for automated health checks

## Common Configuration

### Custom Prometheus URL

```bash
helm upgrade metric-collector ./charts/metric-collector \
  --namespace fleet-system \
  --reuse-values \
  --set prometheus.url=http://prometheus.custom-ns:9090
```

### Use Certificate Auth Instead of Token

```bash
# Create cert secret
kubectl create secret tls hub-cert \
  --cert=hub-client.crt \
  --key=hub-client.key \
  -n fleet-system

helm upgrade metric-collector ./charts/metric-collector \
  --namespace fleet-system \
  --reuse-values \
  --set hubCluster.auth.useTokenAuth=false \
  --set hubCluster.auth.useCertificateAuth=true \
  --set hubCluster.auth.certSecretName=hub-cert
```

### Adjust Collection Interval

Edit the MetricCollector resource:

```yaml
apiVersion: placement.kubernetes-fleet.io/v1beta1
kind: MetricCollector
metadata:
  name: workload-health-collector
spec:
  prometheusURL: http://prometheus:9090
  promQLQuery: workload_health
  pollingIntervalSeconds: 60  # Collect every 60 seconds
  reportNamespace: fleet-cluster-1
```

## Uninstall

```bash
# On member cluster
helm uninstall metric-collector -n fleet-system

# On hub cluster
kubectl delete clusterrolebinding metric-collector-cluster-1
kubectl delete clusterrole metric-collector-hub-access
kubectl delete namespace fleet-member-cluster-1
```

## Support

For issues, see [Troubleshooting](README.md#troubleshooting) in the main README.
