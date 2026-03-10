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
#az aks get-credentials --resource-group $RESOURCE_GROUP_NAME --name $HUB_CLUSTER_NAME

# Install the hub agent.
kubectl config use-context $HUB_CLUSTER_NAME

for (( i=0; i<500; i++ ));
do
    #kubectl delete membercluster cluster-$i --ignore-not-found=true
    #kubectl patch membercluster cluster-$i -n fleet-system --type='json' -p='[{"op":"replace","path":"/spec/heartbeatPeriodSeconds","value":15}]'
    kubectl label membercluster cluster-$i placement-group="$(($i % 20))" 
done