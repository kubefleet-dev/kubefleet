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

for (( i=10; i<200; i++ ));
do
    CLUSTER_NAME="cluster-$i"
    KUBECONFIG_PATH=$KUBECONFIG_DIR/$CLUSTER_NAME.kubeconfig
    echo "Downscaling the member agent deployment in member cluster $CLUSTER_NAME..."
    kubectl scale deploy/member-agent -n fleet-system --kubeconfig $KUBECONFIG_PATH --replicas=0
done