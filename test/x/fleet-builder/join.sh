#!/bin/sh
set -e

# Check the required environment variables.
RESOURCE_GROUP_NAME=${RESOURCE_GROUP_NAME:?Environment variable RESOURCE_GROUP_NAME is not set}
LOCATION=${LOCATION:?Environment variable LOCATION is not set}
REGISTRY_NAME=${REGISTRY_NAME:?Environment variable REGISTRY_NAME is not set}
REGISTRY_NAME_WO_SUFFIX=${REGISTRY_NAME_WO_SUFFIX:?Environment variable REGISTRY_NAME_WO_SUFFIX is not set}
KUBECONFIG_DIR=${KUBECONFIG_DIR:?Environment variable KUBECONFIG_DIR is not set}
KUBEFLEET_SRC_REPO=${KUBEFLEET_SRC_REPO:-"/Users/michaelawyu/Workplace/kubefleet"}

HUB_CLUSTER_NAME=${HUB_CLUSTER_NAME:-hub}
HUB_CLUSTER_API_SERVER_ADDR=${HUB_CLUSTER_API_SERVER_ADDR:?Environment variable HUB_CLUSTER_API_SERVER_ADDR is not set}

MEMBER_AGENT_IMAGE_NAME="${MEMBER_AGENT_IMAGE_NAME:-member-agent}"
REFRESH_TOKEN_IMAGE_NAME="${REFRESH_TOKEN_IMAGE_NAME:-refresh-token}"
PROPERTY_PROVIDER="${PROPERTY_PROVIDER:-azure}"
COMMON_IMAGE_TAG=${COMMON_IMAGE_TAG:-experimental}

while true; do
    # Retrieve a cluster name from the work queue.
    echo "Retrieving cluster name from the work queue..."
    CLUSTER_NAME=$(python retrieve_from_queue.py)
    if [ -z "$CLUSTER_NAME" ]; then
        echo "No more clusters to create. Exiting."
        break
    fi

    # Attach the ACR to the AKS cluster.
    #echo "Attaching ACR $REGISTRY_NAME to AKS cluster $CLUSTER_NAME..."
    #az aks update -n $CLUSTER_NAME -g $RESOURCE_GROUP_NAME --attach-acr $REGISTRY_NAME_WO_SUFFIX

    # Update the AKS cluster to disable the Azure Policy add-on.
    #echo "Updating AKS cluster $CLUSTER_NAME to disable Azure Policy add-on..."
    #az aks disable-addons --addons azure-policy --name $CLUSTER_NAME --resource-group $RESOURCE_GROUP_NAME

    # Retrieve the member cluster credential.
    az aks get-credentials --resource-group $RESOURCE_GROUP_NAME --name $CLUSTER_NAME --file $KUBECONFIG_DIR/$CLUSTER_NAME.kubeconfig --admin
    KUBECONFIG_PATH=$KUBECONFIG_DIR/$CLUSTER_NAME.kubeconfig

    # Set up a service account for the member cluster in the hub cluster.
    #kubectl config use-context $HUB_CLUSTER_NAME

    echo "Setting up service account for member cluster $CLUSTER_NAME in the hub cluster..."
    kubectl create serviceaccount fleet-member-agent-$CLUSTER_NAME -n fleet-system
    cat <<EOF | kubectl apply -f -
    apiVersion: v1
    kind: Secret
    metadata:
        name: fleet-member-agent-$CLUSTER_NAME-sa
        namespace: fleet-system
        annotations:
            kubernetes.io/service-account.name: fleet-member-agent-$CLUSTER_NAME
    type: kubernetes.io/service-account-token
EOF

    echo "Retrieving the service account token for member cluster $CLUSTER_NAME..."
    TOKEN=$(kubectl get secret fleet-member-agent-$CLUSTER_NAME-sa -n fleet-system -o jsonpath='{.data.token}' | base64 -d)

    echo "Installing the service account token secret in member cluster $CLUSTER_NAME..."
    kubectl delete secret hub-kubeconfig-secret --kubeconfig $KUBECONFIG_PATH --ignore-not-found
    kubectl create secret generic hub-kubeconfig-secret --kubeconfig $KUBECONFIG_PATH --from-literal=token=$TOKEN

    echo "Setting up MemberCluster CR in the hub cluster..."
    cat <<EOF | kubectl apply -f -
    apiVersion: cluster.kubernetes-fleet.io/v1beta1
    kind: MemberCluster
    metadata:
        name: $CLUSTER_NAME
    spec:
        identity:
            name: fleet-member-agent-$CLUSTER_NAME
            kind: ServiceAccount
            namespace: fleet-system
            apiGroup: ""
        heartbeatPeriodSeconds: 15
EOF

    echo "Installing the member agent in member cluster $CLUSTER_NAME..."
    pushd $KUBEFLEET_SRC_REPO
    helm upgrade member-agent charts/member-agent/ \
        --install \
        --kubeconfig $KUBECONFIG_PATH \
        --set config.hubURL=$HUB_CLUSTER_API_SERVER_ADDR \
        --set image.repository=$REGISTRY_NAME/$MEMBER_AGENT_IMAGE_NAME \
        --set image.tag=$COMMON_IMAGE_TAG \
        --set refreshtoken.repository=$REGISTRY_NAME/$REFRESH_TOKEN_IMAGE_NAME \
        --set refreshtoken.tag=$COMMON_IMAGE_TAG \
        --set image.pullPolicy=Always \
        --set refreshtoken.pullPolicy=Always \
        --set resources.requests.cpu=1 \
        --set resources.requests.memory=1Gi \
        --set resources.limits.cpu=4 \
        --set resources.limits.memory=16Gi \
        --set config.memberClusterName="$CLUSTER_NAME" \
        --set logVerbosity=5 \
        --set namespace=fleet-system \
        --set enableV1Beta1APIs=true \
        --set propertyProvider=$PROPERTY_PROVIDER
    popd
done