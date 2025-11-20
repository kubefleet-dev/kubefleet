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
	"github.com/kubefleet-dev/kubefleet/pkg/utils"
)

const (
	// defaultCollectionInterval is the interval for collecting metrics (30 seconds)
	defaultCollectionInterval = 30 * time.Second
)

// Reconciler reconciles a MetricCollector object
type Reconciler struct {
	// MemberClient is the client to access the member cluster
	MemberClient client.Client

	// HubClient is the client to access the hub cluster
	HubClient client.Client

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
	if err := r.MemberClient.Get(ctx, req.NamespacedName, mc); err != nil {
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

	if err := r.MemberClient.Status().Update(ctx, mc); err != nil {
		klog.ErrorS(err, "Failed to update MetricCollector status", "metricCollector", req.NamespacedName)
		return ctrl.Result{}, err
	}

	// Sync MetricCollectorReport to hub cluster
	if err := r.syncReportToHub(ctx, mc); err != nil {
		klog.ErrorS(err, "Failed to sync MetricCollectorReport to hub", "metricCollector", req.NamespacedName)
		// Don't fail the reconcile loop, just log the error
	}

	// Requeue after 30 seconds
	return ctrl.Result{RequeueAfter: defaultCollectionInterval}, nil
}

// syncReportToHub syncs the MetricCollectorReport to the hub cluster
func (r *Reconciler) syncReportToHub(ctx context.Context, mc *placementv1beta1.MetricCollector) error {
	// Extract cluster name from collected metrics
	// All metrics are guaranteed to have the same ClusterName
	if len(mc.Status.CollectedMetrics) == 0 {
		klog.V(4).InfoS("No metrics collected, skipping report sync", "metricCollector", klog.KObj(mc))
		return nil
	}

	clusterName := mc.Status.CollectedMetrics[0].ClusterName
	if clusterName == "" {
		return fmt.Errorf("clusterName is empty in collected metrics")
	}

	// Calculate the namespace using the same pattern as Work objects
	reportNamespace := fmt.Sprintf(utils.NamespaceNameFormat, clusterName)

	// Create or update MetricCollectorReport on hub
	report := &placementv1beta1.MetricCollectorReport{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mc.Name,
			Namespace: reportNamespace,
			Labels: map[string]string{
				"metriccollector-name":      mc.Name,
				"metriccollector-namespace": mc.Namespace,
			},
		},
	}

	// Check if report already exists
	existingReport := &placementv1beta1.MetricCollectorReport{}
	err := r.HubClient.Get(ctx, client.ObjectKey{Name: mc.Name, Namespace: reportNamespace}, existingReport)

	now := metav1.Now()
	if err != nil {
		if errors.IsNotFound(err) {
			// Create new report
			report.Conditions = mc.Status.Conditions
			report.ObservedGeneration = mc.Status.ObservedGeneration
			report.WorkloadsMonitored = mc.Status.WorkloadsMonitored
			report.LastCollectionTime = mc.Status.LastCollectionTime
			report.CollectedMetrics = mc.Status.CollectedMetrics
			report.LastReportTime = &now

			if err := r.HubClient.Create(ctx, report); err != nil {
				klog.ErrorS(err, "Failed to create MetricCollectorReport", "report", klog.KObj(report))
				return err
			}
			klog.V(2).InfoS("Created MetricCollectorReport on hub", "report", klog.KObj(report), "clusterName", clusterName)
			return nil
		}
		return err
	}

	// Update existing report
	existingReport.Labels = report.Labels
	existingReport.Conditions = mc.Status.Conditions
	existingReport.ObservedGeneration = mc.Status.ObservedGeneration
	existingReport.WorkloadsMonitored = mc.Status.WorkloadsMonitored
	existingReport.LastCollectionTime = mc.Status.LastCollectionTime
	existingReport.CollectedMetrics = mc.Status.CollectedMetrics
	existingReport.LastReportTime = &now

	if err := r.HubClient.Update(ctx, existingReport); err != nil {
		klog.ErrorS(err, "Failed to update MetricCollectorReport", "report", klog.KObj(existingReport))
		return err
	}
	klog.V(2).InfoS("Updated MetricCollectorReport on hub", "report", klog.KObj(existingReport), "clusterName", clusterName)
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.recorder = mgr.GetEventRecorderFor("metriccollector-controller")
	return ctrl.NewControllerManagedBy(mgr).
		Named("metriccollector-controller").
		For(&placementv1beta1.MetricCollector{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}
