#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

# Configuration
KUBECONFIG="${KUBECONFIG:-$HOME/.kube/config}"
MEMBER_CLUSTER_COUNT="${1:-3}"
NAMESPACE="fleet-system"
LOG_DIR="${LOG_DIR:-logs}"
TIMESTAMP=$(date '+%Y%m%d-%H%M%S')
LOG_DIR="${LOG_DIR}/${TIMESTAMP}"

# Cluster names
HUB_CLUSTER="hub"
declare -a MEMBER_CLUSTERS=()

for (( i=1;i<=MEMBER_CLUSTER_COUNT;i++ ))
do
  MEMBER_CLUSTERS+=("cluster-$i")
done

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Create log directory
mkdir -p "${LOG_DIR}"

echo -e "${GREEN}Starting log collection at ${TIMESTAMP}${NC}"
echo "Logs will be saved to: ${LOG_DIR}"
echo ""

# Function to get pod UID for log file lookup
get_pod_uid() {
    local pod_name=$1
    kubectl get pod "${pod_name}" -n "${NAMESPACE}" -o jsonpath='{.metadata.uid}' 2>/dev/null || echo ""
}

# Function to get Kind node name for cluster
get_kind_node_name() {
    local cluster_name=$1
    echo "${cluster_name}-control-plane"
}

# Function to collect logs directly from Kind node filesystem (much faster and complete)
collect_pod_logs_direct() {
    local pod_name=$1
    local cluster_name=$2
    local log_file_prefix=$3

    echo -e "${YELLOW}Collecting logs from pod ${pod_name} in cluster ${cluster_name} (direct access)${NC}"

    # Get pod UID for log directory lookup
    local pod_uid
    pod_uid=$(get_pod_uid "${pod_name}")
    if [ -z "$pod_uid" ]; then
        echo -e "${RED}Could not get UID for pod ${pod_name}, falling back to kubectl logs${NC}"
        collect_pod_logs_kubectl "$@"
        return
    fi

    # Get all containers in the pod
    local containers
    containers=$(kubectl get pod "${pod_name}" -n "${NAMESPACE}" -o jsonpath='{.spec.containers[*].name}' 2>/dev/null || echo "")
    if [ -z "$containers" ]; then
        echo -e "${RED}No containers found in pod ${pod_name}${NC}"
        return
    fi

    # Get Kind node name
    local node_name
    node_name=$(get_kind_node_name "${cluster_name}")

    # Construct log directory path inside the Kind node
    local log_dir="/var/log/pods/${NAMESPACE}_${pod_name}_${pod_uid}"

    # Collect logs for each container
    for container in $containers; do
        echo "  - Container ${container}:"

        # Get all log files for this container from the Kind node
        local container_log_dir="${log_dir}/${container}"
        local log_files
        log_files=$(docker exec "${node_name}" find "${container_log_dir}" -name "*.log" 2>/dev/null | sort -V || echo "")

        if [ -z "$log_files" ]; then
            echo -e "    ${RED}No direct log files found, falling back to kubectl logs${NC}"
            # Fallback to kubectl approach for this container
            local log_file="${log_file_prefix}-${container}.log"
            if kubectl logs "${pod_name}" -n "${NAMESPACE}" -c "${container}" > "${log_file}" 2>&1; then
                echo "    -> ${log_file} (via kubectl)"
            else
                echo "    -> Failed to get logs via kubectl" > "${log_file}"
            fi
            continue
        fi

        # Copy individual log files for this container
        local file_count=0
        for log_file_path in $log_files; do
            file_count=$((file_count + 1))
            local base_name
            base_name=$(basename "${log_file_path}")
            local individual_log_file="${log_file_prefix}-${container}-${base_name}"

            {
                echo "# Log file metadata"
                echo "# Timestamp: $(date -u '+%Y-%m-%d %H:%M:%S UTC')"
                echo "# Source: ${log_file_path}"
                echo "# Pod: ${pod_name}"
                echo "# Container: ${container}"
                echo "# Cluster: ${cluster_name}"
                echo "# Namespace: ${NAMESPACE}"
                echo "# Method: Direct file access from Kind node"
                echo "# Node: ${node_name}"
                echo "# Part: ${file_count} of $(echo "$log_files" | wc -l)"
                echo "# =================================="
                echo ""
                docker exec "${node_name}" cat "${log_file_path}" 2>/dev/null || echo "Failed to read ${log_file_path}"
            } > "${individual_log_file}"

            echo "    -> ${individual_log_file}"
        done
    done
}

# Function to collect logs using kubectl (fallback method)
collect_pod_logs_kubectl() {
    local pod_name=$1
    local cluster_name=$2
    local log_file_prefix=$3

    echo -e "${YELLOW}Collecting logs from pod ${pod_name} in cluster ${cluster_name} (kubectl fallback)${NC}"

    # Get all containers in the pod
    containers=$(kubectl get pod "${pod_name}" -n "${NAMESPACE}" -o jsonpath='{.spec.containers[*].name}' 2>/dev/null || echo "")

    if [ -z "$containers" ]; then
        echo -e "${RED}No containers found in pod ${pod_name}${NC}"
        return
    fi

    # Collect logs for each container
    for container in $containers; do
        log_file="${log_file_prefix}-${container}.log"
        echo "  - Container ${container} -> ${log_file}"

        # Get current logs
        kubectl logs "${pod_name}" -n "${NAMESPACE}" -c "${container}" > "${log_file}" 2>&1 || \
            echo "Failed to get logs for container ${container}" > "${log_file}"

        # Try to get previous logs if pod was restarted
        previous_log_file="${log_file_prefix}-${container}-previous.log"
        if kubectl logs "${pod_name}" -n "${NAMESPACE}" -c "${container}" --previous > "${previous_log_file}" 2>&1; then
            echo "  - Previous logs for ${container} -> ${previous_log_file}"
        else
            rm -f "${previous_log_file}"
        fi
    done
}

# Function to collect logs from a pod (tries direct access first, falls back to kubectl)
collect_pod_logs() {
    local pod_name=$1
    local cluster_name=$2
    local log_file_prefix=$3

    # Get pod info for debugging
    local pod_info_file="${log_file_prefix}-pod-info.txt"
    echo "  - Pod info -> ${pod_info_file}"
    {
        echo "=== Pod Description ==="
        kubectl describe pod "${pod_name}" -n "${NAMESPACE}"
        echo ""
        echo "=== Pod YAML ==="
        kubectl get pod "${pod_name}" -n "${NAMESPACE}" -o yaml
    } > "${pod_info_file}" 2>&1

    # Try direct access first (much better), fallback to kubectl if needed
    collect_pod_logs_direct "$@"
}

# Collect hub cluster logs
echo -e "${GREEN}=== Collecting Hub Cluster Logs ===${NC}"
kind export kubeconfig --name "${HUB_CLUSTER}" 2>/dev/null || {
    echo -e "${RED}Failed to export kubeconfig for hub cluster${NC}"
    exit 1
}

# Create hub logs directory
HUB_LOG_DIR="${LOG_DIR}/hub"
mkdir -p "${HUB_LOG_DIR}"

# Get all hub-agent pods
hub_pods=$(kubectl get pods -n "${NAMESPACE}" -l app.kubernetes.io/name=hub-agent -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || echo "")

if [ -z "$hub_pods" ]; then
    echo -e "${RED}No hub-agent pods found${NC}"
else
    for pod in $hub_pods; do
        collect_pod_logs "${pod}" "${HUB_CLUSTER}" "${HUB_LOG_DIR}/${pod}"
    done
fi

# Collect member cluster logs
for cluster in "${MEMBER_CLUSTERS[@]}"; do
    echo -e "${GREEN}=== Collecting Member Cluster Logs: ${cluster} ===${NC}"
    
    # Export kubeconfig for the member cluster
    if ! kind export kubeconfig --name "${cluster}" 2>/dev/null; then
        echo -e "${RED}Failed to export kubeconfig for cluster ${cluster}, skipping...${NC}"
        continue
    fi
    
    # Create member logs directory
    MEMBER_LOG_DIR="${LOG_DIR}/${cluster}"
    mkdir -p "${MEMBER_LOG_DIR}"
    
    # Get all member-agent pods
    member_pods=$(kubectl get pods -n "${NAMESPACE}" -l app.kubernetes.io/name=member-agent -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || echo "")
    
    if [ -z "$member_pods" ]; then
        echo -e "${RED}No member-agent pods found in cluster ${cluster}${NC}"
    else
        for pod in $member_pods; do
            collect_pod_logs "${pod}" "${cluster}" "${MEMBER_LOG_DIR}/${pod}"
        done
    fi
    
    echo ""
done

# Collect additional debugging information
echo -e "${GREEN}=== Collecting Additional Debug Information ===${NC}"

# Hub cluster debug info
kind export kubeconfig --name "${HUB_CLUSTER}" 2>/dev/null
{
    echo "=== Hub Cluster Pod Status ==="
    kubectl get pods -n "${NAMESPACE}" -o wide
    echo ""
    echo "=== Hub Cluster Events ==="
    kubectl get events -n "${NAMESPACE}" --sort-by='.lastTimestamp'
} > "${LOG_DIR}/hub-debug-info.txt" 2>&1

# Member clusters debug info
for cluster in "${MEMBER_CLUSTERS[@]}"; do
    if kind export kubeconfig --name "${cluster}" 2>/dev/null; then
        {
            echo "=== ${cluster} Pod Status ==="
            kubectl get pods -n "${NAMESPACE}" -o wide
            echo ""
            echo "=== ${cluster} Events ==="
            kubectl get events -n "${NAMESPACE}" --sort-by='.lastTimestamp'
        } > "${LOG_DIR}/${cluster}-debug-info.txt" 2>&1
    fi
done

# Create a summary file
echo -e "${GREEN}=== Creating Summary ===${NC}"
{
    echo "Log Collection Summary"
    echo "====================="
    echo "Timestamp: ${TIMESTAMP}"
    echo "Hub Cluster: ${HUB_CLUSTER}"
    echo "Member Clusters: ${MEMBER_CLUSTERS[*]}"
    echo ""
    echo "Directory Structure:"
    find "${LOG_DIR}" -type f -name "*.log" -o -name "*.txt" | sort
} > "${LOG_DIR}/summary.txt"

echo ""
echo -e "${GREEN}Log collection completed!${NC}"
echo "All logs saved to: ${LOG_DIR}"
echo ""
echo "To view the summary:"
echo "  cat ${LOG_DIR}/summary.txt"
echo ""
echo "To search across all logs:"
echo "  grep -r 'ERROR' ${LOG_DIR}"
