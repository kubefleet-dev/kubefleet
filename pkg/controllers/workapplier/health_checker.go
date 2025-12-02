/*
Copyright 2025 The KubeFleet Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package workapplier

import (
	"context"
	"fmt"

	appv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
)

// WorkloadHealthChecker defines the interface for checking the health of workloads
// running on member clusters. Implementations can provide custom health check logic
// based on workload type and business requirements.
type WorkloadHealthChecker interface {
	// IsWorkloadHealthy checks if a workload is healthy based on its current state
	// in the member cluster.
	//
	// Parameters:
	//   - ctx: The context for the health check operation
	//   - gvr: The GroupVersionResource of the workload
	//   - obj: The unstructured object representing the workload's current state
	//   - err: an error if the health check itself failed (not the same as unhealthy)
	IsWorkloadHealthy(ctx context.Context, gvr schema.GroupVersionResource, obj *unstructured.Unstructured) (healthy bool, reason string, err error)
}

// DeploymentHealthChecker implements WorkloadHealthChecker for Kubernetes Deployments
// by querying Prometheus for workload health metrics.
type DeploymentHealthChecker struct {
	prometheusClient PrometheusClient
}

// NewDeploymentHealthChecker creates a new DeploymentHealthChecker with the given PrometheusClient.
func NewDeploymentHealthChecker(prometheusClient PrometheusClient) *DeploymentHealthChecker {
	return &DeploymentHealthChecker{
		prometheusClient: prometheusClient,
	}
}

// IsWorkloadHealthy checks if a deployment is healthy by querying Prometheus for workload_health metrics.
func (d *DeploymentHealthChecker) IsWorkloadHealthy(ctx context.Context, gvr schema.GroupVersionResource, obj *unstructured.Unstructured) (bool, string, error) {
	// 1. Convert to Deployment
	var deploy appv1.Deployment
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &deploy); err != nil {
		return false, "failed to convert to deployment", fmt.Errorf("failed to convert unstructured to deployment: %w", err)
	}

	// 2. Collect workload health metrics
	workloadMetrics, err := d.prometheusClient.CollectWorkloadHealthMetrics(ctx, deploy.Namespace)
	if err != nil {
		klog.V(2).InfoS("Failed to collect health metrics, assuming healthy",
			"deployment", klog.KObj(&deploy), "error", err)
		return true, "failed to query prometheus, assuming healthy", nil
	}

	if len(workloadMetrics) > 0 {
		klog.V(2).InfoS("Collected workload health metrics", "deployment",
			types.NamespacedName{Namespace: deploy.Namespace, Name: deploy.Name}, "metricsCount",
			len(workloadMetrics), "workload", types.NamespacedName{Namespace: workloadMetrics[0].Namespace, Name: workloadMetrics[0].WorkloadName},
			"health", workloadMetrics[0].Health)
	}

	// 3. Find this deployment's health
	for _, wm := range workloadMetrics {
		if wm.WorkloadName == deploy.Name && wm.Namespace == deploy.Namespace {
			if wm.Health {
				return true, "deployment is healthy", nil
			}
			return false, "deployment is unhealthy", nil
		}
	}

	// 4. No health metric found - assume healthy
	klog.V(2).InfoS("No health metric found for deployment, assuming healthy",
		"deployment", klog.KObj(&deploy))
	return true, "no health metric found, assuming healthy", nil
}
