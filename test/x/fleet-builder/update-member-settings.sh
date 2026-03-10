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

    # Retrieve the member cluster credential.
    az aks get-credentials --resource-group $RESOURCE_GROUP_NAME --name $CLUSTER_NAME --file $KUBECONFIG_DIR/$CLUSTER_NAME.kubeconfig --admin
    KUBECONFIG_PATH=$KUBECONFIG_DIR/$CLUSTER_NAME.kubeconfig

    echo "Upgrading the member agent in member cluster $CLUSTER_NAME..."
    pushd $KUBEFLEET_SRC_REPO
    helm upgrade member-agent charts/member-agent/ \
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
        --set propertyProvider=$PROPERTY_PROVIDER \
        --set enableWorkApplierPriorityQueue=true
    popd
done