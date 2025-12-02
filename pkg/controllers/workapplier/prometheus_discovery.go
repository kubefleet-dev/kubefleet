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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// discoverPrometheus discovers the Prometheus service in the given namespace and
// creates a PrometheusClient if found. This method is idempotent and caches the
// discovery result.
func (r *Reconciler) discoverPrometheus(ctx context.Context, namespace string) error {
	// Check if discovery has already been attempted
	if r.prometheusDiscovered.Load() {
		return nil
	}

	// List services in the namespace
	var serviceList corev1.ServiceList
	listOpts := []client.ListOption{
		client.InNamespace(namespace),
	}

	if err := r.spokeClient.List(ctx, &serviceList, listOpts...); err != nil {
		klog.V(2).InfoS("Failed to list services in namespace", "namespace", namespace, "error", err)
		r.prometheusDiscovered.Store(true)
		r.prometheusAvailable.Store(false)
		return fmt.Errorf("failed to list services: %w", err)
	}

	// Search for Prometheus service
	// Look for service with label app=prometheus or named "prometheus"
	var prometheusService *corev1.Service
	for i := range serviceList.Items {
		svc := &serviceList.Items[i]

		// Check if service is named "prometheus"
		if svc.Name == "prometheus" {
			prometheusService = svc
			break
		}

		// Check if service has label app=prometheus
		if labels := svc.Labels; labels != nil {
			if appLabel, ok := labels["app"]; ok && appLabel == "prometheus" {
				prometheusService = svc
				break
			}
		}
	}

	// Mark discovery as complete
	r.prometheusDiscovered.Store(true)

	if prometheusService == nil {
		klog.V(2).InfoS("Prometheus service not found in namespace, health checks disabled", "namespace", namespace)
		r.prometheusAvailable.Store(false)
		return nil
	}

	// Construct Prometheus URL
	// Format: http://<service-name>.<namespace>.svc.cluster.local:<port>
	port := 9090 // Default Prometheus port
	if len(prometheusService.Spec.Ports) > 0 {
		port = int(prometheusService.Spec.Ports[0].Port)
	}

	r.prometheusURL = fmt.Sprintf("http://%s.%s.svc.cluster.local:%d",
		prometheusService.Name,
		prometheusService.Namespace,
		port)

	// Create Prometheus client (no authentication for now)
	r.prometheusClient = NewPrometheusClient(r.prometheusURL, "", nil)
	r.prometheusAvailable.Store(true)

	klog.V(2).InfoS("Discovered Prometheus service",
		"service", klog.KObj(prometheusService),
		"url", r.prometheusURL)

	return nil
}
