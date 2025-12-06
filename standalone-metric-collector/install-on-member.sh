#!/bin/bash
set -e

# Configuration
HUB_CONTEXT="kind-hub"
MEMBER_CLUSTER_COUNT="${1:-1}"  # Default to 1 if not specified
MEMBER_NAMESPACE="default"
PROMETHEUS_URL="http://prometheus.test-ns:9090"
IMAGE_NAME="metric-collector"
IMAGE_TAG="latest"

# Get hub cluster API server URL dynamically using docker inspect (following kubefleet pattern)
HUB_API_SERVER="https://$(docker inspect hub-control-plane --format='{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}'):6443"

echo "=== Installing MetricCollector on ${MEMBER_CLUSTER_COUNT} member cluster(s) ==="
echo "Hub cluster: ${HUB_CONTEXT}"
echo "Hub API server: ${HUB_API_SERVER}"
echo ""

# Step 0: Build and load Docker image (once for all clusters)
echo "Step 0: Building Docker image..."
docker buildx build \
  --file docker/Dockerfile \
  --output=type=docker \
  --platform=linux/$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/') \
  --tag ${IMAGE_NAME}:${IMAGE_TAG} \
  --build-arg GOARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/') \
  --build-arg GOOS=linux \
  .
echo "✓ Docker image built"
echo ""

# Install on each member cluster
for i in $(seq 1 ${MEMBER_CLUSTER_COUNT}); do
  MEMBER_CONTEXT="kind-cluster-${i}"
  MEMBER_CLUSTER_NAME="cluster-${i}"
  HUB_NAMESPACE="fleet-member-${MEMBER_CLUSTER_NAME}"

  echo "========================================"
  echo "Installing on Member Cluster ${i}/${MEMBER_CLUSTER_COUNT}"
  echo "  Context: ${MEMBER_CONTEXT}"
  echo "  Cluster Name: ${MEMBER_CLUSTER_NAME}"
  echo "========================================"
  echo ""

  # Load image into this member cluster
  echo "Loading Docker image into ${MEMBER_CONTEXT}..."
  kind load docker-image ${IMAGE_NAME}:${IMAGE_TAG} --name cluster-${i}
  echo "✓ Image loaded into kind cluster"
  echo ""

  # Step 1: Setup RBAC on hub cluster
  echo "Step 1: Setting up RBAC on hub cluster..."
  kubectl --context=${HUB_CONTEXT} create namespace ${HUB_NAMESPACE} --dry-run=client -o yaml | kubectl --context=${HUB_CONTEXT} apply -f -
  kubectl --context=${HUB_CONTEXT} create serviceaccount metric-collector-sa -n ${HUB_NAMESPACE} --dry-run=client -o yaml | kubectl --context=${HUB_CONTEXT} apply -f -

  cat <<EOF | kubectl --context=${HUB_CONTEXT} apply -f -
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

  echo "✓ RBAC configured on hub cluster"
  echo ""

  # Step 2: Create token secret on hub cluster
  echo "Step 2: Creating token secret on hub cluster..."
  cat <<EOF | kubectl --context=${HUB_CONTEXT} apply -f -
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
  echo "Waiting for token to be created..."
  sleep 3

  # Get token
  TOKEN=$(kubectl --context=${HUB_CONTEXT} get secret metric-collector-token -n ${HUB_NAMESPACE} -o jsonpath='{.data.token}' | base64 -d)
  if [ -z "$TOKEN" ]; then
      echo "Error: Failed to get token from hub cluster for ${MEMBER_CLUSTER_NAME}"
      exit 1
  fi

  echo "✓ Token created on hub cluster"
  echo ""

  # Step 3: Create namespace and secrets on member cluster
  echo "Step 3: Creating secrets on member cluster..."

  kubectl --context=${MEMBER_CONTEXT} create secret generic hub-token \
    --from-literal=token="${TOKEN}" \
    -n ${MEMBER_NAMESPACE} \
    --dry-run=client -o yaml | kubectl --context=${MEMBER_CONTEXT} apply -f -

  echo "✓ Secrets created on member cluster"
  echo ""

  # Step 4: Install helm chart on member cluster (includes CRD)
  echo "Step 4: Installing helm chart on member cluster..."
  helm upgrade --install metric-collector ./charts/metric-collector \
    --kube-context=${MEMBER_CONTEXT} \
    --namespace ${MEMBER_NAMESPACE} \
    --set memberCluster.name=${MEMBER_CLUSTER_NAME} \
    --set hubCluster.url=${HUB_API_SERVER} \
    --set hubCluster.tls.insecure=true \
    --set prometheus.url=${PROMETHEUS_URL} \
    --set image.repository=${IMAGE_NAME} \
    --set image.tag=${IMAGE_TAG} \
    --set image.pullPolicy=IfNotPresent

  echo "✓ Helm chart installed on member cluster"
  echo ""

  # Step 5: Verify installation
  echo "Step 5: Verifying installation..."
  echo "Checking pods on member cluster..."
  kubectl --context=${MEMBER_CONTEXT} get pods -n ${MEMBER_NAMESPACE} -l app.kubernetes.io/name=metric-collector

  echo ""
  echo "✓ Installation complete for ${MEMBER_CLUSTER_NAME}"
  echo ""
done

echo "========================================"
echo "=== All Installations Complete ==="
echo "========================================"
echo ""
echo "To check logs from a specific member cluster:"
echo "  kubectl --context=kind-cluster-1 logs -n ${MEMBER_NAMESPACE} -l app.kubernetes.io/name=metric-collector -f"
echo ""
echo "To check MetricCollectorReports on hub:"
echo "  kubectl --context=${HUB_CONTEXT} get metriccollectorreports -A"
echo ""
