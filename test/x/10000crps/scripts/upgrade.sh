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

make -C "../../../.." docker-build-hub-agent
make -C "../../../.." docker-build-member-agent
make -C "../../../.." docker-build-refresh-token

kubectl config use-context $HUB_CLUSTER
helm upgrade hub-agent ../../../../charts/hub-agent/ \
    --set image.repository=$REGISTRY/$HUB_AGENT_IMAGE \
    --set image.tag=$TAG

kubectl config use-context bravelion-admin
helm upgrade member-agent ../../../../charts/member-agent/ \
    --set image.repository=$REGISTRY/$MEMBER_AGENT_IMAGE \
    --set image.tag=$TAG


kubectl config use-context smartfish-admin
helm upgrade member-agent ../../../../charts/member-agent/ \
    --set image.repository=$REGISTRY/$MEMBER_AGENT_IMAGE \
    --set image.tag=$TAG