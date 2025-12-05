# ApprovalRequest Controller

The ApprovalRequest Controller is a standalone controller that runs on the **hub cluster** to automate approval decisions for staged updates based on workload health metrics.

## Overview

This controller:
- Watches `ApprovalRequest` and `ClusterApprovalRequest` resources
- Creates `MetricCollector` resources on member clusters via ClusterResourcePlacement
- Monitors workload health via `MetricCollectorReport` objects
- Automatically approves requests when all tracked workloads are healthy
- Runs every 15 seconds to check health status

## Architecture

The controller is designed to run on the hub cluster and:
1. Deploys MetricCollector instances to member clusters using CRP
2. Collects health metrics from MetricCollectorReports
3. Compares metrics against WorkloadTracker specifications
4. Approves ApprovalRequests when all workloads are healthy

## Installation

### Prerequisites

The following CRDs must be installed on the hub cluster:
- `approvalrequests.placement.kubernetes-fleet.io`
- `clusterapprovalrequests.placement.kubernetes-fleet.io`
- `metriccollectors.placement.kubernetes-fleet.io`
- `metriccollectorreports.placement.kubernetes-fleet.io` (installed by this chart)
- `workloadtrackers.placement.kubernetes-fleet.io`
- `clusterresourceplacements.placement.kubernetes-fleet.io`
- `clusterresourceoverrides.placement.kubernetes-fleet.io`
- `clusterstagedupdateruns.placement.kubernetes-fleet.io`
- `stagedupdateruns.placement.kubernetes-fleet.io`

### Install via Helm

```bash
# Build the image
make docker-build IMAGE_NAME=approval-request-controller IMAGE_TAG=latest

# Load into kind (if using kind)
kind load docker-image approval-request-controller:latest --name hub

# Install the chart
helm install approval-request-controller ./charts/approval-request-controller \
  --namespace fleet-system \
  --create-namespace
```

## Configuration

See `charts/approval-request-controller/values.yaml` for all configuration options.

Key settings:
- `controller.logLevel`: Log verbosity (default: 2)
- `controller.resources`: Resource requests and limits
- `rbac.create`: Create RBAC resources (default: true)
- `crds.install`: Install MetricCollectorReport CRD (default: true)

## Development

### Build

```bash
make docker-build
```

### Test Locally

```bash
go run ./cmd/approvalrequestcontroller/main.go
```
