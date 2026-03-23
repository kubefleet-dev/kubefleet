# Note: you must have at least one hub cluster and one member cluster.

# Example usage: ./join-member-clusters.sh demo-hub-01 0.2.2 member-cluster-1 [<member-cluster-name-2> ...]

usage() {
    cat <<'EOF'
Usage:
    ./join-member-clusters.sh <hub-cluster-name> <kubefleet-version> <member-cluster-name-1> [<member-cluster-name-2> ...]

Example:
    ./join-member-clusters.sh demo-hub-01 0.2.2 member-cluster-1 member-cluster-2

Requirements:
    - kubectl and helm must be installed
    - hub and member cluster names must exist in your kubeconfig
EOF
}

fail_with_help() {
    echo "Error: $1" >&2
    echo >&2
    usage >&2
    exit 1
}

if [ "$1" = "-h" ] || [ "$1" = "--help" ]; then
    usage
    exit 0
fi

if [ "$#" -lt 3 ]; then
    fail_with_help "expected at least 3 arguments, got $#"
fi

if ! command -v kubectl >/dev/null 2>&1; then
    fail_with_help "kubectl is not installed or not available in PATH"
fi

if ! command -v helm >/dev/null 2>&1; then
    fail_with_help "helm is not installed or not available in PATH"
fi

export HUB_CLUSTER_NAME="$1"
export KUBEFLEET_VERSION="$2"

if [ -z "$HUB_CLUSTER_NAME" ]; then
    fail_with_help "hub cluster name cannot be empty"
fi

if [ -z "$KUBEFLEET_VERSION" ]; then
    fail_with_help "kubefleet version cannot be empty"
fi

if ! kubectl config get-clusters 2>/dev/null | grep -Fxq "$HUB_CLUSTER_NAME"; then
    fail_with_help "hub cluster '$HUB_CLUSTER_NAME' was not found in kubeconfig"
fi

for MC in "${@:3}"; do
    if [ -z "$MC" ]; then
        fail_with_help "member cluster name cannot be empty"
    fi

    if [ "$MC" = "$HUB_CLUSTER_NAME" ]; then
        fail_with_help "member cluster '$MC' cannot be the same as hub cluster"
    fi

    if ! kubectl config get-clusters 2>/dev/null | grep -Fxq "$MC"; then
        fail_with_help "member cluster '$MC' was not found in kubeconfig"
    fi
done

# Get the API server address of the hub cluster, which will be used by member clusters to connect to the hub cluster.
export HUB_CLUSTER_ADDRESS=$(kubectl config view -o jsonpath="{.clusters[?(@.name==\"$HUB_CLUSTER_NAME\")].cluster.server}")

if [ -z "$HUB_CLUSTER_ADDRESS" ]; then
    fail_with_help "unable to resolve API server address for hub cluster '$HUB_CLUSTER_NAME'"
fi

for MC in "${@:3}"; do

# Note that Fleet will recognize your cluster with this name once it joins.
export MEMBER_CLUSTER_NAME=$MC
export SERVICE_ACCOUNT="$MEMBER_CLUSTER_NAME-hub-cluster-access"

#echo "Switching into hub cluster context..."
kubectl config use $HUB_CLUSTER_NAME
# The service account can, in theory, be created in any namespace; for simplicity reasons,
# here you will use the namespace reserved by Fleet installation, `fleet-system`.
#
# Note that if you choose a different value, commands in some steps below need to be
# modified accordingly.
echo "Creating member service account..."
kubectl create serviceaccount $SERVICE_ACCOUNT -n fleet-system

echo "Creating member service account secret..."
export SERVICE_ACCOUNT_SECRET="$MEMBER_CLUSTER_NAME-hub-cluster-access-token"
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Secret
metadata:
    name: $SERVICE_ACCOUNT_SECRET
    namespace: fleet-system
    annotations:
        kubernetes.io/service-account.name: $SERVICE_ACCOUNT
type: kubernetes.io/service-account-token
EOF

echo "Creating member cluster custom resource on hub cluster..."
export TOKEN="$(kubectl get secret $SERVICE_ACCOUNT_SECRET -n fleet-system -o jsonpath='{.data.token}' | base64 --decode)"
cat <<EOF | kubectl apply -f -
apiVersion: cluster.kubernetes-fleet.io/v1
kind: MemberCluster
metadata:
    name: $MEMBER_CLUSTER_NAME
spec:
    identity:
        name: $MEMBER_CLUSTER_NAME-hub-cluster-access
        kind: ServiceAccount
        namespace: fleet-system
        apiGroup: ""
    heartbeatPeriodSeconds: 15
EOF

# Install the member agent helm chart on the member cluster.

echo "Switching to member cluster context.."
kubectl config use $MEMBER_CLUSTER_NAME

# Create the secret with the token extracted previously for member agent to use.
echo "Creating secret..."
kubectl delete secret hub-kubeconfig-secret
kubectl create secret generic hub-kubeconfig-secret --from-literal=token=$TOKEN

echo "Uninstalling any existing member-agent instances..."
helm uninstall member-agent -n fleet-system --wait

echo "Installing member-agent..."
helm install member-agent oci://ghcr.io/kubefleet-dev/kubefleet/charts/member-agent \
  --version $KUBEFLEET_VERSION \
  --set config.hubURL=$HUB_CLUSTER_ADDRESS  \
  --set config.memberClusterName=$MEMBER_CLUSTER_NAME \
  --set logFileMaxSize=100000 \
  --namespace fleet-system \
  --create-namespace 

kubectl get pods -A
kubectl config use $HUB_CLUSTER_NAME
kubectl get membercluster $MEMBER_CLUSTER_NAME
done
