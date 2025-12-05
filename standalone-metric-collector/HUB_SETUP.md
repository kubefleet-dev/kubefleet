# Hub Cluster RBAC Setup

This guide explains how to set up RBAC permissions on the hub cluster for the MetricCollector controller running on member clusters.

## Overview

The MetricCollector controller needs permissions on the **hub cluster** to:
- Create/update `MetricCollectorReport` resources in `fleet-{cluster}` namespaces
- List namespaces

## Option 1: Using the Helm Chart Template (Recommended)

Generate and apply the RBAC resources from the helm chart:

```bash
# Generate hub RBAC manifest
helm template metric-collector ./charts/metric-collector \
  --set hubCluster.createRBAC=true \
  --set memberCluster.name=cluster-1 \
  --show-only templates/hub-rbac.yaml > hub-rbac.yaml

# Apply on the hub cluster
kubectl apply -f hub-rbac.yaml --context=hub-cluster
```

## Option 2: Manual RBAC Setup

Apply this manifest directly on the hub cluster:

```yaml
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: metric-collector-hub-access
  labels:
    app: metric-collector
rules:
  # MetricCollectorReport access
  - apiGroups: ["placement.kubernetes-fleet.io"]
    resources: ["metriccollectorreports"]
    verbs: ["get", "list", "create", "update", "patch", "delete"]
  # Namespace access
  - apiGroups: [""]
    resources: ["namespaces"]
    verbs: ["get", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: metric-collector-cluster-1
  labels:
    app: metric-collector
    fleet.kubernetes.io/member-cluster: cluster-1
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: metric-collector-hub-access
subjects:
  # Option A: Use ServiceAccount from hub cluster
  - kind: ServiceAccount
    name: metric-collector-sa
    namespace: fleet-member-cluster-1
  
  # Option B: Use token directly (for testing)
  # Create token secret on member cluster and reference here
```

## Creating ServiceAccount Token for Member Cluster

On the **hub cluster**:

```bash
# 1. Create namespace for the member cluster
kubectl create namespace fleet-member-cluster-1

# 2. Create ServiceAccount
kubectl create serviceaccount metric-collector-sa -n fleet-member-cluster-1

# 3. Bind to ClusterRole
kubectl create clusterrolebinding metric-collector-cluster-1 \
  --clusterrole=metric-collector-hub-access \
  --serviceaccount=fleet-member-cluster-1:metric-collector-sa

# 4. Create token secret
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

# 5. Get the token
kubectl get secret metric-collector-token -n fleet-member-cluster-1 \
  -o jsonpath='{.data.token}' | base64 -d > hub-token.txt

# 6. Get CA certificate
kubectl get secret metric-collector-token -n fleet-member-cluster-1 \
  -o jsonpath='{.data.ca\.crt}' | base64 -d > hub-ca.crt
```

On the **member cluster**:

```bash
# 1. Create namespace
kubectl create namespace fleet-system

# 2. Create token secret
kubectl create secret generic hub-token \
  --from-file=token=hub-token.txt \
  -n fleet-system

# 3. (Optional) Create CA secret
kubectl create secret generic hub-ca \
  --from-file=ca.crt=hub-ca.crt \
  -n fleet-system

# 4. Install the chart
helm install metric-collector ./charts/metric-collector \
  --namespace fleet-system \
  --set memberCluster.name=cluster-1 \
  --set hubCluster.url=https://hub-cluster:6443 \
  --set hubCluster.tls.caSecretName=hub-ca \
  --set prometheus.url=http://prometheus:9090
```

## Verification

On the **hub cluster**:

```bash
# Check if RBAC is created
kubectl get clusterrole metric-collector-hub-access
kubectl get clusterrolebinding metric-collector-cluster-1

# Check if namespace exists for reports
kubectl get namespace fleet-cluster-1

# Watch for reports
kubectl get metriccollectorreports -n fleet-cluster-1 --watch
```

On the **member cluster**:

```bash
# Check controller logs
kubectl logs -n fleet-system deployment/metric-collector -f

# Check for errors
kubectl logs -n fleet-system deployment/metric-collector | grep -i error
```

## Troubleshooting

### Permission Denied Errors

If you see errors like:
```
Failed to sync report to hub: ... forbidden: User "system:serviceaccount:fleet-member-cluster-1:metric-collector-sa" cannot create resource "metriccollectorreports"
```

**Solution**: Verify RBAC is correctly configured on hub cluster:
```bash
kubectl auth can-i create metriccollectorreports \
  --as=system:serviceaccount:fleet-member-cluster-1:metric-collector-sa \
  -n fleet-cluster-1 \
  --context=hub-cluster
```

### Token Expired

If authentication fails:
```
Failed to connect to hub: Unauthorized
```

**Solution**: Regenerate the token secret on the hub cluster and update the secret on the member cluster.

### Namespace Not Found

If reports fail to be created:
```
Failed to sync report: namespace "fleet-cluster-1" not found
```

**Solution**: Ensure the `fleet-{cluster}` namespace exists on the hub cluster. The hub agent typically creates these.

## Multi-Cluster Setup

For multiple member clusters, repeat the RBAC setup for each cluster:

```bash
# For cluster-1
helm template metric-collector ./charts/metric-collector \
  --set hubCluster.createRBAC=true \
  --set memberCluster.name=cluster-1 \
  --show-only templates/hub-rbac.yaml | \
  kubectl apply -f - --context=hub-cluster

# For cluster-2
helm template metric-collector ./charts/metric-collector \
  --set hubCluster.createRBAC=true \
  --set memberCluster.name=cluster-2 \
  --show-only templates/hub-rbac.yaml | \
  kubectl apply -f - --context=hub-cluster
```

Each member cluster will have its own ClusterRoleBinding with a unique ServiceAccount.
