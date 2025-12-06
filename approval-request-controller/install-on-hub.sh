#!/bin/bash
set -e

# Configuration
HUB_CONTEXT="kind-hub"
IMAGE_NAME="approval-request-controller"
IMAGE_TAG="latest"
NAMESPACE="fleet-system"
CHART_NAME="approval-request-controller"

echo "=== Installing ApprovalRequest Controller on hub cluster ==="
echo "Hub cluster: ${HUB_CONTEXT}"
echo "Namespace: ${NAMESPACE}"
echo ""

# Step 0: Build and load Docker image
echo "Step 0: Building and loading Docker image..."
cd ..
docker buildx build \
  --file approval-request-controller/docker/Dockerfile \
  --output=type=docker \
  --platform=linux/$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/') \
  --tag ${IMAGE_NAME}:${IMAGE_TAG} \
  --build-arg GOARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/') \
  .
cd approval-request-controller
kind load docker-image ${IMAGE_NAME}:${IMAGE_TAG} --name hub
echo "✓ Docker image built and loaded into kind cluster"
echo ""

# Step 1: Verify kubefleet CRDs are installed
echo "Step 1: Verifying required kubefleet CRDs..."
REQUIRED_CRDS=(
  "approvalrequests.placement.kubernetes-fleet.io"
  "clusterapprovalrequests.placement.kubernetes-fleet.io"
  "clusterresourceplacements.placement.kubernetes-fleet.io"
  "clusterresourceoverrides.placement.kubernetes-fleet.io"
  "clusterstagedupdateruns.placement.kubernetes-fleet.io"
  "stagedupdateruns.placement.kubernetes-fleet.io"
)

MISSING_CRDS=()
for crd in "${REQUIRED_CRDS[@]}"; do
  if ! kubectl --context=${HUB_CONTEXT} get crd ${crd} &>/dev/null; then
    MISSING_CRDS+=("${crd}")
  fi
done

if [ ${#MISSING_CRDS[@]} -ne 0 ]; then
  echo "Error: Missing required CRDs from kubefleet hub-agent:"
  for crd in "${MISSING_CRDS[@]}"; do
    echo "  - ${crd}"
  done
  echo ""
  echo "Please ensure kubefleet hub-agent is installed first."
  exit 1
fi

echo "✓ All required kubefleet CRDs are installed"
echo ""

# Step 2: Install helm chart on hub cluster (includes MetricCollector, MetricCollectorReport, WorkloadTracker CRDs)
echo "Step 2: Installing helm chart on hub cluster..."
helm upgrade --install ${CHART_NAME} ./charts/${CHART_NAME} \
  --kube-context=${HUB_CONTEXT} \
  --namespace ${NAMESPACE} \
  --create-namespace \
  --set image.repository=${IMAGE_NAME} \
  --set image.tag=${IMAGE_TAG} \
  --set image.pullPolicy=IfNotPresent \
  --set controller.logLevel=2

echo "✓ Helm chart installed on hub cluster"
echo ""

# Step 3: Verify installation
echo "Step 3: Verifying installation..."
echo "Checking CRDs installed by this chart..."
kubectl --context=${HUB_CONTEXT} get crd | grep -E "metriccollectors|metriccollectorreports|workloadtrackers" || echo "  (CRDs may take a moment to appear)"

echo ""
echo "Checking pods in ${NAMESPACE}..."
kubectl --context=${HUB_CONTEXT} get pods -n ${NAMESPACE} -l app.kubernetes.io/name=${CHART_NAME}

echo ""
echo "=== Installation Complete ==="
echo ""
echo "To check controller logs:"
echo "  kubectl --context=${HUB_CONTEXT} logs -n ${NAMESPACE} -l app.kubernetes.io/name=${CHART_NAME} -f"
echo ""
echo "To verify CRDs:"
echo "  kubectl --context=${HUB_CONTEXT} get crd | grep placement.kubernetes-fleet.io"
echo ""
echo "Next steps:"
echo "  1. Create a WorkloadTracker to define which workloads to monitor"
echo "  2. ApprovalRequests will be automatically processed when created by staged updates"
echo ""
