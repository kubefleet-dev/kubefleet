HUB_CLUSTER="hub-admin"
declare -a MEMBER_CLUSTERS=(
    "bravelion-admin"
    "smartfish-admin"
)
MEMBER_CLUSTER_COUNT=${#MEMBER_CLUSTERS[@]}

export REGISTRY="${REGISTRY:-chenyu1scaletest.azurecr.io}"
export TAG="${TAG:-baseline}"

export HUB_AGENT_IMAGE="${HUB_AGENT_IMAGE:-hub-agent}"
export MEMBER_AGENT_IMAGE="${MEMBER_AGENT_IMAGE:-member-agent}"
export REFRESH_TOKEN_IMAGE="${REFRESH_TOKEN_IMAGE:-refresh-token}"
export PROPERTY_PROVIDER="${PROPERTY_PROVIDER:-azure}"
export USE_PREDEFINED_REGIONS="${USE_PREDEFINED_REGIONS:-false}"
export RESOURCE_SNAPSHOT_CREATION_MINIMUM_INTERVAL="${RESOURCE_SNAPSHOT_CREATION_MINIMUM_INTERVAL:-0m}"
export RESOURCE_CHANGES_COLLECTION_DURATION="${RESOURCE_CHANGES_COLLECTION_DURATION:-0m}"

export HUB_SERVER_URL="https://eyeofthestorm-dns-jsahra43.hcp.eastus2.azmk8s.io"

# Build the Fleet agent images.
echo "Building and the Fleet agent images..."

make -C "../../../.." docker-build-hub-agent
make -C "../../../.." docker-build-member-agent
make -C "../../../.." docker-build-refresh-token

kubectl config use-context $HUB_CLUSTER
helm install hub-agent ../../../../charts/hub-agent/ \
    --set image.pullPolicy=IfNotPresent \
    --set image.repository=$REGISTRY/$HUB_AGENT_IMAGE \
    --set image.tag=$TAG \
    --set namespace=fleet-system \
    --set logVerbosity=5 \
    --set enableWebhook=false \
    --set webhookClientConnectionType=service \
    --set forceDeleteWaitTime="1m0s" \
    --set clusterUnhealthyThreshold="3m0s" \
    --set logFileMaxSize=1000000 \
    --set resourceSnapshotCreationMinimumInterval=$RESOURCE_SNAPSHOT_CREATION_MINIMUM_INTERVAL \
    --set resourceChangesCollectionDuration=$RESOURCE_CHANGES_COLLECTION_DURATION

for i in "${MEMBER_CLUSTERS[@]}"
do
    kubectl config use-context $HUB_CLUSTER
    kubectl create serviceaccount fleet-member-agent-$i -n fleet-system
    cat <<EOF | kubectl apply -f -
    apiVersion: v1
    kind: Secret
    metadata:
        name: fleet-member-agent-$i-sa
        namespace: fleet-system
        annotations:
            kubernetes.io/service-account.name: fleet-member-agent-$i
    type: kubernetes.io/service-account-token
EOF
done

cat <<EOF | kubectl apply -f -
apiVersion: cluster.kubernetes-fleet.io/v1beta1
kind: MemberCluster
metadata:
    name: bravelion
spec:
    identity:
        name: fleet-member-agent-bravelion-admin
        kind: ServiceAccount
        namespace: fleet-system
        apiGroup: ""
    heartbeatPeriodSeconds: 15
EOF

cat <<EOF | kubectl apply -f -
apiVersion: cluster.kubernetes-fleet.io/v1beta1
kind: MemberCluster
metadata:
    name: smartfish
spec:
    identity:
        name: fleet-member-agent-smartfish-admin
        kind: ServiceAccount
        namespace: fleet-system
        apiGroup: ""
    heartbeatPeriodSeconds: 15
EOF

for i in "${MEMBER_CLUSTERS[@]}"
do
    kubectl config use-context $HUB_CLUSTER
    TOKEN=$(kubectl get secret fleet-member-agent-$i-sa -n fleet-system -o jsonpath='{.data.token}' | base64 -d)
    kubectl config use-context "$i"
    kubectl delete secret hub-kubeconfig-secret --ignore-not-found
    kubectl create secret generic hub-kubeconfig-secret --from-literal=token=$TOKEN
done

kubectl config use-context bravelion-admin
helm install member-agent ../../../../charts/member-agent/ \
    --set config.hubURL=$HUB_SERVER_URL \
    --set image.repository=$REGISTRY/$MEMBER_AGENT_IMAGE \
    --set image.tag=$TAG \
    --set refreshtoken.repository=$REGISTRY/$REFRESH_TOKEN_IMAGE \
    --set refreshtoken.tag=$TAG \
    --set image.pullPolicy=IfNotPresent \
    --set refreshtoken.pullPolicy=IfNotPresent \
    --set config.memberClusterName="bravelion" \
    --set logVerbosity=5 \
    --set namespace=fleet-system \
    --set enableV1Alpha1APIs=false \
    --set enableV1Beta1APIs=true \
    --set propertyProvider=$PROPERTY_PROVIDER

kubectl config use-context smartfish-admin
helm install member-agent ../../../../charts/member-agent/ \
    --set config.hubURL=$HUB_SERVER_URL \
    --set image.repository=$REGISTRY/$MEMBER_AGENT_IMAGE \
    --set image.tag=$TAG \
    --set refreshtoken.repository=$REGISTRY/$REFRESH_TOKEN_IMAGE \
    --set refreshtoken.tag=$TAG \
    --set image.pullPolicy=IfNotPresent \
    --set refreshtoken.pullPolicy=IfNotPresent \
    --set config.memberClusterName="smartfish" \
    --set logVerbosity=5 \
    --set namespace=fleet-system \
    --set enableV1Alpha1APIs=false \
    --set enableV1Beta1APIs=true \
    --set propertyProvider=$PROPERTY_PROVIDER

kubectl config use-context hub-admin

helm upgrade monitoring oci://ghcr.io/prometheus-community/charts/kube-prometheus-stack \
    --set kubeStateMetrics.enabled=false \
    --set grafana.enabled=false \
    --set prometheus.service.type=LoadBalancer \
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
