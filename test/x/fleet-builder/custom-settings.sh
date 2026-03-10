cd /Users/michaelawyu/Playground/fleet-builder

export STORAGE_ACCOUNT_NAME=fleetbuilder
export QUEUE_NAME=clusterstoadd
export RESOURCE_GROUP_NAME=dp-perf-test
export LOCATION=westus2
export REGISTRY_NAME=fleetperftest.azurecr.io
export REGISTRY_NAME_WO_SUFFIX=fleetperftest
export KUBECONFIG_DIR="/Users/michaelawyu/Downloads/KUBECONFIGS"
export HUB_CLUSTER_NAME="heartoftheocean"
export HUB_CLUSTER_API_SERVER_ADDR="https://heartoftheocean-dns-zebowa5q.hcp.westus2.azmk8s.io:443"
export KUBEFLEET_SRC_REPO="/Users/michaelawyu/Workplace/kubefleet"

source .venv/bin/activate
