#!/bin/sh
set -e

# Check the required environment variables.
RESOURCE_GROUP_NAME=${RESOURCE_GROUP_NAME:?Environment variable RESOURCE_GROUP_NAME is not set}
LOCATION=${LOCATION:?Environment variable LOCATION is not set}
REGISTRY_NAME=${REGISTRY_NAME:?Environment variable REGISTRY_NAME is not set}
HUB_CLUSTER_NAME=${HUB_CLUSTER_NAME:-hub}
HUB_AGENT_IMAGE_NAME=${HUB_AGENT_IMAGE_NAME:-hub-agent}
COMMON_IMAGE_TAG=${COMMON_IMAGE_TAG:-experimental}
RESOURCE_SNAPSHOT_CREATION_MINIMUM_INTERVAL="${RESOURCE_SNAPSHOT_CREATION_MINIMUM_INTERVAL:-0m}"
RESOURCE_CHANGES_COLLECTION_DURATION="${RESOURCE_CHANGES_COLLECTION_DURATION:-0m}"

KUBEFLEET_SRC_REPO=${KUBEFLEET_SRC_REPO:-"/Users/michaelawyu/Workplace/kubefleet"}

# Retrieve the hub cluster credential.
echo "Retrieving the credential for hub cluster $HUB_CLUSTER_NAME..."
az aks get-credentials --resource-group $RESOURCE_GROUP_NAME --name $HUB_CLUSTER_NAME

# Install the hub agent.
kubectl config use-context $HUB_CLUSTER_NAME

pushd $KUBEFLEET_SRC_REPO
helm upgrade hub-agent charts/hub-agent/ \
    --install \
    --set image.pullPolicy=Always \
    --set image.repository=$REGISTRY_NAME/$HUB_AGENT_IMAGE_NAME \
    --set image.tag=$COMMON_IMAGE_TAG \
    --set resources.requests.cpu=1 \
    --set resources.requests.memory=1Gi \
    --set resources.limits.cpu=6 \
    --set resources.limits.memory=24Gi \
    --set namespace=fleet-system \
    --set logVerbosity=5 \
    --set enableWebhook=false \
    --set enableWorkload=false \
    --set webhookClientConnectionType=service \
    --set forceDeleteWaitTime="5m0s" \
    --set clusterUnhealthyThreshold="3m0s" \
    --set hubAPIQPS=250 \
    --set hubAPIBurst=1000 \
    --set MaxFleetSizeSupported=100 \
    --set logFileMaxSize=100000 \
    --set MaxConcurrentClusterPlacement=100 \
    --set ConcurrentResourceChangeSyncs=20 \
    --set resourceSnapshotCreationMinimumInterval=$RESOURCE_SNAPSHOT_CREATION_MINIMUM_INTERVAL \
    --set resourceChangesCollectionDuration=$RESOURCE_CHANGES_COLLECTION_DURATION

popd

helm install kube-prometheus-stack oci://ghcr.io/prometheus-community/charts/kube-prometheus-stack -n monitoring --create-namespace

helm upgrade kube-prometheus-stack -n monitoring oci://ghcr.io/prometheus-community/charts/kube-prometheus-stack \
    --set prometheus.prometheusSpec.scrapeConfigSelectorNilUsesHelmValues=true \
    --set-json 'prometheus.prometheusSpec.scrapeConfigSelector={"matchLabels":{"prom": "monitoring"}}'

cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Service
metadata:
  name: fleet-metrics
  namespace: fleet-system
spec:
  selector:
    app.kubernetes.io/name: hub-agent
  ports:
    - protocol: TCP
      port: 8080
      targetPort: 8080
  type: ClusterIP
EOF

cat <<EOF | kubectl apply -f -
apiVersion: monitoring.coreos.com/v1alpha1
kind: ScrapeConfig
metadata:
  name: fleet-metrics-scrape-config
  labels:
    prom: monitoring
spec:
  staticConfigs:
    - targets:
      - fleet-metrics.fleet-system.svc.cluster.local:8080
EOF