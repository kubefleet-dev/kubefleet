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

package metriccollector

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

const (
	// defaultCollectionInterval is the interval for collecting metrics (30 seconds)
	defaultCollectionInterval = 30 * time.Second
)

// Reconciler reconciles a MetricCollector object
type Reconciler struct {
	// client is the client to access the member cluster
	client.Client

	// recorder is the event recorder
	recorder record.EventRecorder

	// prometheusClient is the client to query Prometheus
	prometheusClient PrometheusClient
}

// Reconcile reconciles a MetricCollector object
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	startTime := time.Now()
	klog.V(2).InfoS("MetricCollector reconciliation starts", "metricCollector", req.NamespacedName)
	defer func() {
		latency := time.Since(startTime).Milliseconds()
		klog.V(2).InfoS("MetricCollector reconciliation ends", "metricCollector", req.NamespacedName, "latency", latency)
	}()

	// Fetch the MetricCollector instance
	mc := &placementv1beta1.MetricCollector{}
	if err := r.Get(ctx, req.NamespacedName, mc); err != nil {
		if errors.IsNotFound(err) {
			klog.V(2).InfoS("MetricCollector not found, ignoring", "metricCollector", req.NamespacedName)
			return ctrl.Result{}, nil
		}
		klog.ErrorS(err, "Failed to get MetricCollector", "metricCollector", req.NamespacedName)
		return ctrl.Result{}, err
	}

	// Collect metrics from Prometheus
	collectedMetrics, collectErr := r.collectFromPrometheus(ctx, mc)

	// Update status with collected metrics
	now := metav1.Now()
	mc.Status.LastCollectionTime = &now
	mc.Status.CollectedMetrics = collectedMetrics
	mc.Status.WorkloadsMonitored = int32(len(collectedMetrics))
	mc.Status.ObservedGeneration = mc.Generation

	if collectErr != nil {
		klog.ErrorS(collectErr, "Failed to collect metrics", "metricCollector", req.NamespacedName)
		meta.SetStatusCondition(&mc.Status.Conditions, metav1.Condition{
			Type:               placementv1beta1.MetricCollectorConditionTypeReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: mc.Generation,
			Reason:             "CollectorConfigured",
			Message:            "Collector is configured",
		})
		meta.SetStatusCondition(&mc.Status.Conditions, metav1.Condition{
			Type:               placementv1beta1.MetricCollectorConditionTypeCollecting,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: mc.Generation,
			Reason:             "CollectionFailed",
			Message:            fmt.Sprintf("Failed to collect metrics: %v", collectErr),
		})
	} else {
		klog.V(2).InfoS("Successfully collected metrics", "metricCollector", req.NamespacedName, "workloads", len(collectedMetrics))
		meta.SetStatusCondition(&mc.Status.Conditions, metav1.Condition{
			Type:               placementv1beta1.MetricCollectorConditionTypeReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: mc.Generation,
			Reason:             "CollectorConfigured",
			Message:            "Collector is configured and collecting metrics",
		})
		meta.SetStatusCondition(&mc.Status.Conditions, metav1.Condition{
			Type:               placementv1beta1.MetricCollectorConditionTypeCollecting,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: mc.Generation,
			Reason:             "MetricsCollected",
			Message:            fmt.Sprintf("Successfully collected metrics from %d workloads", len(collectedMetrics)),
		})
	}

	if err := r.Status().Update(ctx, mc); err != nil {
		klog.ErrorS(err, "Failed to update MetricCollector status", "metricCollector", req.NamespacedName)
		return ctrl.Result{}, err
	}

	// Requeue after 30 seconds
	return ctrl.Result{RequeueAfter: defaultCollectionInterval}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.recorder = mgr.GetEventRecorderFor("metriccollector-controller")
	return ctrl.NewControllerManagedBy(mgr).
		Named("metriccollector-controller").
		For(&placementv1beta1.MetricCollector{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}
