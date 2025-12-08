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



# Function to collect fleet agent logs from node filesystem using kubectl debug
collect_node_agent_logs() {
    local cluster_name=$1
    local node_log_dir=$2
    local agent_type=$3  # "hub-agent" or "member-agent"

    echo -e "${YELLOW}Collecting ${agent_type} logs from cluster ${cluster_name} nodes${NC}"

    # Get all nodes in the cluster
    local nodes
    nodes=$(kubectl get nodes -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || echo "")

    if [ -z "$nodes" ]; then
        echo -e "${RED}No nodes found in cluster ${cluster_name}${NC}"
        return
    fi

    # Create node logs directory
    mkdir -p "${node_log_dir}"

    for node in $nodes; do
        echo "  - Collecting ${agent_type} logs from node ${node}"
        local node_specific_dir="${node_log_dir}/${node}"
        mkdir -p "${node_specific_dir}"

        # Collect specific agent logs from node filesystem
        collect_agent_logs_from_node "${node}" "${cluster_name}" "${node_specific_dir}" "${agent_type}"
    done
}

# Function to collect specific agent logs from node filesystem
collect_agent_logs_from_node() {
    local node=$1
    local cluster_name=$2
    local node_log_dir=$3
    local agent_type=$4  # "hub-agent" or "member-agent"

    echo "    -> Collecting ${agent_type} logs from node filesystem"

    # Find and collect specific agent logs from /var/log/pods
    local agent_logs_file="${node_log_dir}/${agent_type}-logs.log"

    # First check if any agent logs exist on this node
    local log_files
    log_files=$(docker exec "${node}" find /var/log/pods -path "*/fleet-system_*${agent_type}*/*.log" -type f 2>/dev/null || echo "")

    if [ -n "$log_files" ]; then
        # Agent logs found, create the log file
        {
            echo "# ${agent_type} logs from node filesystem"
            echo "# Timestamp: $(date -u '+%Y-%m-%d %H:%M:%S UTC')"
            echo "# Node: ${node}"
            echo "# Cluster: ${cluster_name}"
            echo "# Method: Direct access to /var/log/pods via docker exec"
            echo "# =================================="
            echo ""

            # Use docker exec to collect agent logs directly from Kind node container
            echo "$log_files" | while read logfile; do
                echo "--- Log file: $logfile ---"
                docker exec "${node}" cat "$logfile" 2>/dev/null || echo "Failed to read $logfile"
                echo ""
            done
        } > "${agent_logs_file}"
        echo "    -> ${agent_type}-logs.log"
    else
        # No agent logs found, don't create the file and remove directory if empty
        echo "    -> No ${agent_type} logs found on node ${node}"
        rmdir "${node_log_dir}" 2>/dev/null || true
    fi
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

# Collect hub-agent logs from hub cluster nodes
collect_node_agent_logs "${HUB_CLUSTER}" "${HUB_LOG_DIR}/nodes" "hub-agent"

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

    # Collect member-agent logs from member cluster nodes
    collect_node_agent_logs "${cluster}" "${MEMBER_LOG_DIR}/nodes" "member-agent"

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
