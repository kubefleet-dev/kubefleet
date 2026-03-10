#!/bin/sh
set -e

# Check the required environment variables.
RESOURCE_GROUP_NAME=${RESOURCE_GROUP_NAME:?Environment variable RESOURCE_GROUP_NAME is not set}
LOCATION=${LOCATION:?Environment variable LOCATION is not set}
REGISTRY_NAME=${REGISTRY_NAME:?Environment variable REGISTRY_NAME is not set}
NODE_COUNT=${NODE_COUNT:-1}
VM_SIZE=${VM_SIZE:-Standard_D4s_v3}

# Log in with a system-assigned managed identity.
#az login

while true; do
    # Retrieve a cluster name from the work queue.
    echo "Retrieving cluster name from the work queue..."
    CLUSTER_NAME=$(python retrieve_from_queue.py)
    if [ -z "$CLUSTER_NAME" ]; then
        echo "No more clusters to create. Exiting."
        break
    fi

    # Create the AKS cluster.
    echo "Creating AKS cluster $CLUSTER_NAME..."
    az aks create \
        -g $RESOURCE_GROUP_NAME \
        -n $CLUSTER_NAME \
        --location $LOCATION \
        --node-count $NODE_COUNT \
        --node-vm-size $VM_SIZE \
        --enable-aad \
        --enable-azure-rbac \
        --tier standard \
        --network-plugin azure \
        --attach-acr $REGISTRY_NAME_WO_SUFFIX \
        --tags exempted_by_qi=34648409
    
    echo "Updating AKS cluster $CLUSTER_NAME to disable Azure Policy add-on..."
    az aks disable-addons --addons azure-policy --name $CLUSTER_NAME --resource-group $RESOURCE_GROUP_NAME
    
    # Sleep for a short while.
    echo "Cluster $CLUSTER_NAME created. Sleeping for 15 seconds before processing the next cluster..."
    sleep 15
done