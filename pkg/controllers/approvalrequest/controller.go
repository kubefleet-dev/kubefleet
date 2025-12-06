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

// Package approvalrequest features a controller to reconcile ApprovalRequest objects
// and create MetricCollector resources on member clusters for approved stages.
package approvalrequest

import (
	"context"
	"fmt"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils"
)

const (
	// metricCollectorFinalizer is the finalizer added to ApprovalRequest objects
	metricCollectorFinalizer = "kubernetes-fleet.io/metric-collector-cleanup"

	// prometheusURL is the default Prometheus URL to use
	prometheusURL = "http://prometheus.test-ns.svc.cluster.local:9090"
)

// Reconciler reconciles an ApprovalRequest object and creates MetricCollector resources
// on member clusters when the approval is granted.
type Reconciler struct {
	client.Client
	recorder record.EventRecorder
}

// Reconcile reconciles an ApprovalRequest or ClusterApprovalRequest object.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	startTime := time.Now()
	klog.V(2).InfoS("ApprovalRequest reconciliation starts", "request", req.NamespacedName)
	defer func() {
		latency := time.Since(startTime).Milliseconds()
		klog.V(2).InfoS("ApprovalRequest reconciliation ends", "request", req.NamespacedName, "latency", latency)
	}()

	var approvalReqObj placementv1beta1.ApprovalRequestObj
	var isClusterScoped bool

	// Check if request has a namespace to determine resource type
	if req.Namespace != "" {
		// Fetch namespaced ApprovalRequest
		approvalReq := &placementv1beta1.ApprovalRequest{}
		if err := r.Client.Get(ctx, req.NamespacedName, approvalReq); err != nil {
			if errors.IsNotFound(err) {
				klog.V(2).InfoS("ApprovalRequest not found, ignoring", "request", req.NamespacedName)
				return ctrl.Result{}, nil
			}
			klog.ErrorS(err, "Failed to get ApprovalRequest", "request", req.NamespacedName)
			return ctrl.Result{}, err
		}
		approvalReqObj = approvalReq
		isClusterScoped = false
	} else {
		// Fetch cluster-scoped ClusterApprovalRequest
		clusterApprovalReq := &placementv1beta1.ClusterApprovalRequest{}
		if err := r.Client.Get(ctx, types.NamespacedName{Name: req.Name}, clusterApprovalReq); err != nil {
			if errors.IsNotFound(err) {
				klog.V(2).InfoS("ClusterApprovalRequest not found, ignoring", "request", req.Name)
				return ctrl.Result{}, nil
			}
			klog.ErrorS(err, "Failed to get ClusterApprovalRequest", "request", req.Name)
			return ctrl.Result{}, err
		}
		approvalReqObj = clusterApprovalReq
		isClusterScoped = true
	}

	return r.reconcileApprovalRequestObj(ctx, approvalReqObj, isClusterScoped)
}

// reconcileApprovalRequestObj reconciles an ApprovalRequestObj (either ApprovalRequest or ClusterApprovalRequest).
func (r *Reconciler) reconcileApprovalRequestObj(ctx context.Context, approvalReqObj placementv1beta1.ApprovalRequestObj, isClusterScoped bool) (ctrl.Result, error) {
	obj := approvalReqObj.(client.Object)
	approvalReqRef := klog.KObj(obj)

	// Handle deletion
	if !obj.GetDeletionTimestamp().IsZero() {
		return r.handleDelete(ctx, approvalReqObj, isClusterScoped)
	}

	// Check if the approval request is already approved or rejected - stop reconciliation if so
	approvedCond := meta.FindStatusCondition(approvalReqObj.GetApprovalRequestStatus().Conditions, string(placementv1beta1.ApprovalRequestConditionApproved))
	if approvedCond != nil && approvedCond.Status == metav1.ConditionTrue {
		klog.V(2).InfoS("ApprovalRequest has been approved, stopping reconciliation", "approvalRequest", approvalReqRef)
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(obj, metricCollectorFinalizer) {
		controllerutil.AddFinalizer(obj, metricCollectorFinalizer)
		if err := r.Client.Update(ctx, obj); err != nil {
			klog.ErrorS(err, "Failed to add finalizer", "approvalRequest", approvalReqRef)
			return ctrl.Result{}, err
		}
		klog.V(2).InfoS("Added finalizer to ApprovalRequest", "approvalRequest", approvalReqRef)
	}

	// Get the UpdateRun (ClusterStagedUpdateRun or StagedUpdateRun)
	spec := approvalReqObj.GetApprovalRequestSpec()
	updateRunName := spec.TargetUpdateRun
	stageName := spec.TargetStage

	var stageStatus *placementv1beta1.StageUpdatingStatus
	if isClusterScoped {
		updateRun := &placementv1beta1.ClusterStagedUpdateRun{}
		if err := r.Client.Get(ctx, types.NamespacedName{Name: updateRunName}, updateRun); err != nil {
			klog.ErrorS(err, "Failed to get ClusterStagedUpdateRun", "approvalRequest", approvalReqRef, "updateRun", updateRunName)
			return ctrl.Result{}, err
		}

		// Find the stage
		for i := range updateRun.Status.StagesStatus {
			if updateRun.Status.StagesStatus[i].StageName == stageName {
				stageStatus = &updateRun.Status.StagesStatus[i]
				break
			}
		}
	} else {
		updateRun := &placementv1beta1.StagedUpdateRun{}
		if err := r.Client.Get(ctx, types.NamespacedName{Name: updateRunName, Namespace: obj.GetNamespace()}, updateRun); err != nil {
			klog.ErrorS(err, "Failed to get StagedUpdateRun", "approvalRequest", approvalReqRef, "updateRun", updateRunName)
			return ctrl.Result{}, err
		}

		// Find the stage
		for i := range updateRun.Status.StagesStatus {
			if updateRun.Status.StagesStatus[i].StageName == stageName {
				stageStatus = &updateRun.Status.StagesStatus[i]
				break
			}
		}
	}

	if stageStatus == nil {
		err := fmt.Errorf("stage %s not found in UpdateRun %s", stageName, updateRunName)
		klog.ErrorS(err, "Failed to find stage", "approvalRequest", approvalReqRef)
		return ctrl.Result{}, err
	}

	// Get all cluster names from the stage
	clusterNames := make([]string, 0, len(stageStatus.Clusters))
	for _, cluster := range stageStatus.Clusters {
		clusterNames = append(clusterNames, cluster.ClusterName)
	}

	if len(clusterNames) == 0 {
		klog.V(2).InfoS("No clusters in stage, skipping", "approvalRequest", approvalReqRef, "stage", stageName)
		return ctrl.Result{}, nil
	}

	klog.V(2).InfoS("Found clusters in stage", "approvalRequest", approvalReqRef, "stage", stageName, "clusters", clusterNames)

	// Create or update the MetricCollector resource, CRP, and ResourceOverrides
	if err := r.ensureMetricCollectorResources(ctx, obj, clusterNames, updateRunName, stageName); err != nil {
		klog.ErrorS(err, "Failed to ensure MetricCollector resources", "approvalRequest", approvalReqRef)
		return ctrl.Result{}, err
	}

	klog.V(2).InfoS("Successfully ensured MetricCollector resources", "approvalRequest", approvalReqRef, "clusters", clusterNames)

	// Check workload health and approve if all workloads are healthy
	if err := r.checkWorkloadHealthAndApprove(ctx, approvalReqObj, clusterNames, updateRunName, stageName); err != nil {
		klog.ErrorS(err, "Failed to check workload health", "approvalRequest", approvalReqRef)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, err
	}

	// Requeue after 15 seconds to check again (will stop if approved in next reconciliation)
	return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
}

// ensureMetricCollectorResources creates the Namespace, MetricCollector, CRP, and ResourceOverrides
func (r *Reconciler) ensureMetricCollectorResources(
	ctx context.Context,
	approvalReq client.Object,
	clusterNames []string,
	updateRunName, stageName string,
) error {
	// Generate names
	metricCollectorName := fmt.Sprintf("mc-%s-%s", updateRunName, stageName)
	crpName := fmt.Sprintf("crp-mc-%s-%s", updateRunName, stageName)
	roName := fmt.Sprintf("ro-mc-%s-%s", updateRunName, stageName)

	// Create MetricCollector resource (cluster-scoped) on hub
	metricCollector := &placementv1beta1.MetricCollector{
		ObjectMeta: metav1.ObjectMeta{
			Name: metricCollectorName,
			Labels: map[string]string{
				"app":              "metric-collector",
				"approval-request": approvalReq.GetName(),
				"update-run":       updateRunName,
				"stage":            stageName,
			},
		},
		Spec: placementv1beta1.MetricCollectorSpec{
			PrometheusURL: prometheusURL,
			// ReportNamespace will be overridden per cluster
			ReportNamespace: "placeholder",
		},
	}

	// Create or update MetricCollector
	existingMC := &placementv1beta1.MetricCollector{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: metricCollectorName}, existingMC)
	if err != nil {
		if errors.IsNotFound(err) {
			if err := r.Client.Create(ctx, metricCollector); err != nil {
				return fmt.Errorf("failed to create MetricCollector: %w", err)
			}
			klog.V(2).InfoS("Created MetricCollector", "metricCollector", klog.KObj(metricCollector))
		} else {
			return fmt.Errorf("failed to get MetricCollector: %w", err)
		}
	}

	// Create ResourceOverride with rules for each cluster
	overrideRules := make([]placementv1beta1.OverrideRule, 0, len(clusterNames))
	for _, clusterName := range clusterNames {
		reportNamespace := fmt.Sprintf(utils.NamespaceNameFormat, clusterName)

		overrideRules = append(overrideRules, placementv1beta1.OverrideRule{
			ClusterSelector: &placementv1beta1.ClusterSelector{
				ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"kubernetes-fleet.io/cluster-name": clusterName,
							},
						},
					},
				},
			},
			JSONPatchOverrides: []placementv1beta1.JSONPatchOverride{
				{
					Operator: placementv1beta1.JSONPatchOverrideOpReplace,
					Path:     "/spec/reportNamespace",
					Value:    apiextensionsv1.JSON{Raw: []byte(fmt.Sprintf(`"%s"`, reportNamespace))},
				},
			},
		})
	}

	// Create ClusterResourceOverride with rules for each cluster
	clusterResourceOverride := &placementv1beta1.ClusterResourceOverride{
		ObjectMeta: metav1.ObjectMeta{
			Name: roName,
			Labels: map[string]string{
				"approval-request": approvalReq.GetName(),
				"update-run":       updateRunName,
				"stage":            stageName,
			},
		},
		Spec: placementv1beta1.ClusterResourceOverrideSpec{
			ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
				{
					Group:   "placement.kubernetes-fleet.io",
					Version: "v1beta1",
					Kind:    "MetricCollector",
					Name:    metricCollectorName,
				},
			},
			Policy: &placementv1beta1.OverridePolicy{
				OverrideRules: overrideRules,
			},
		},
	}

	// Create or update ClusterResourceOverride
	existingCRO := &placementv1beta1.ClusterResourceOverride{}
	err = r.Client.Get(ctx, types.NamespacedName{Name: roName}, existingCRO)
	if err != nil {
		if errors.IsNotFound(err) {
			if err := r.Client.Create(ctx, clusterResourceOverride); err != nil {
				return fmt.Errorf("failed to create ClusterResourceOverride: %w", err)
			}
			klog.V(2).InfoS("Created ClusterResourceOverride", "clusterResourceOverride", roName)
		} else {
			return fmt.Errorf("failed to get ClusterResourceOverride: %w", err)
		}
	}

	// Create ClusterResourcePlacement with PickFixed policy
	// CRP resource selector selects the MetricCollector directly
	crp := &placementv1beta1.ClusterResourcePlacement{
		ObjectMeta: metav1.ObjectMeta{
			Name: crpName,
			Labels: map[string]string{
				"approval-request": approvalReq.GetName(),
				"update-run":       updateRunName,
				"stage":            stageName,
			},
		},
		Spec: placementv1beta1.PlacementSpec{
			ResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
				{
					Group:   "placement.kubernetes-fleet.io",
					Version: "v1beta1",
					Kind:    "MetricCollector",
					Name:    metricCollectorName,
				},
			},
			Policy: &placementv1beta1.PlacementPolicy{
				PlacementType: placementv1beta1.PickFixedPlacementType,
				ClusterNames:  clusterNames,
			},
		},
	}

	// Create or update CRP
	existingCRP := &placementv1beta1.ClusterResourcePlacement{}
	err = r.Client.Get(ctx, types.NamespacedName{Name: crpName}, existingCRP)
	if err != nil {
		if errors.IsNotFound(err) {
			if err := r.Client.Create(ctx, crp); err != nil {
				return fmt.Errorf("failed to create ClusterResourcePlacement: %w", err)
			}
			klog.V(2).InfoS("Created ClusterResourcePlacement", "crp", crpName)
		} else {
			return fmt.Errorf("failed to get ClusterResourcePlacement: %w", err)
		}
	}

	return nil
}

// checkWorkloadHealthAndApprove checks if all workloads specified in WorkloadTracker are healthy
// across all clusters in the stage, and approves the ApprovalRequest if they are.
func (r *Reconciler) checkWorkloadHealthAndApprove(
	ctx context.Context,
	approvalReqObj placementv1beta1.ApprovalRequestObj,
	clusterNames []string,
	updateRunName, stageName string,
) error {
	obj := approvalReqObj.(client.Object)
	approvalReqRef := klog.KObj(obj)

	klog.V(2).InfoS("Starting workload health check", "approvalRequest", approvalReqRef, "clusters", clusterNames)

	// Get the WorkloadTracker (there should be one cluster-scoped object)
	workloadTrackerList := &placementv1beta1.WorkloadTrackerList{}
	if err := r.Client.List(ctx, workloadTrackerList); err != nil {
		klog.ErrorS(err, "Failed to list WorkloadTracker", "approvalRequest", approvalReqRef)
		return fmt.Errorf("failed to list WorkloadTracker: %w", err)
	}

	if len(workloadTrackerList.Items) == 0 {
		klog.V(2).InfoS("No WorkloadTracker found, skipping health check", "approvalRequest", approvalReqRef)
		return nil
	}

	// Use the first WorkloadTracker (assuming there's only one)
	workloadTracker := &workloadTrackerList.Items[0]
	klog.V(2).InfoS("Found WorkloadTracker", "approvalRequest", approvalReqRef, "workloadTracker", klog.KObj(workloadTracker), "workloadCount", len(workloadTracker.Workloads))

	if len(workloadTracker.Workloads) == 0 {
		klog.V(2).InfoS("WorkloadTracker has no workloads defined, skipping health check", "approvalRequest", approvalReqRef)
		return nil
	}

	// MetricCollectorReport name is same as MetricCollector name
	metricCollectorName := fmt.Sprintf("mc-%s-%s", updateRunName, stageName)

	// Check each cluster for the required workloads
	allHealthy := true
	unhealthyDetails := []string{}

	for _, clusterName := range clusterNames {
		reportNamespace := fmt.Sprintf(utils.NamespaceNameFormat, clusterName)

		klog.V(2).InfoS("Checking MetricCollectorReport",
			"approvalRequest", approvalReqRef,
			"cluster", clusterName,
			"reportName", metricCollectorName,
			"reportNamespace", reportNamespace)

		// Get MetricCollectorReport for this cluster
		report := &placementv1beta1.MetricCollectorReport{}
		err := r.Client.Get(ctx, types.NamespacedName{
			Name:      metricCollectorName,
			Namespace: reportNamespace,
		}, report)

		if err != nil {
			if errors.IsNotFound(err) {
				klog.V(2).InfoS("MetricCollectorReport not found yet",
					"approvalRequest", approvalReqRef,
					"cluster", clusterName,
					"report", metricCollectorName,
					"namespace", reportNamespace)
				allHealthy = false
				unhealthyDetails = append(unhealthyDetails, fmt.Sprintf("cluster %s: report not found", clusterName))
				continue
			}
			klog.ErrorS(err, "Failed to get MetricCollectorReport",
				"approvalRequest", approvalReqRef,
				"cluster", clusterName,
				"report", metricCollectorName,
				"namespace", reportNamespace)
			return fmt.Errorf("failed to get MetricCollectorReport for cluster %s: %w", clusterName, err)
		}

		klog.V(2).InfoS("Found MetricCollectorReport",
			"approvalRequest", approvalReqRef,
			"cluster", clusterName,
			"collectedMetrics", len(report.CollectedMetrics),
			"workloadsMonitored", report.WorkloadsMonitored)

		// Check if all workloads from WorkloadTracker are present and healthy
		for _, trackedWorkload := range workloadTracker.Workloads {
			found := false
			healthy := false

			for _, collectedMetric := range report.CollectedMetrics {
				if collectedMetric.Namespace == trackedWorkload.Namespace &&
					collectedMetric.WorkloadName == trackedWorkload.Name {
					found = true
					healthy = collectedMetric.Health
					klog.V(3).InfoS("Workload metric found",
						"approvalRequest", approvalReqRef,
						"cluster", clusterName,
						"workload", trackedWorkload.Name,
						"namespace", trackedWorkload.Namespace,
						"healthy", healthy)
					break
				}
			}

			if !found {
				klog.V(2).InfoS("Workload not found in MetricCollectorReport",
					"approvalRequest", approvalReqRef,
					"cluster", clusterName,
					"workload", trackedWorkload.Name,
					"namespace", trackedWorkload.Namespace)
				allHealthy = false
				unhealthyDetails = append(unhealthyDetails,
					fmt.Sprintf("cluster %s: workload %s/%s not found", clusterName, trackedWorkload.Namespace, trackedWorkload.Name))
			} else if !healthy {
				klog.V(2).InfoS("Workload is not healthy",
					"approvalRequest", approvalReqRef,
					"cluster", clusterName,
					"workload", trackedWorkload.Name,
					"namespace", trackedWorkload.Namespace)
				allHealthy = false
				unhealthyDetails = append(unhealthyDetails,
					fmt.Sprintf("cluster %s: workload %s/%s unhealthy", clusterName, trackedWorkload.Namespace, trackedWorkload.Name))
			}
		}
	}

	// If all workloads are healthy across all clusters, approve the ApprovalRequest
	if allHealthy {
		klog.InfoS("All workloads are healthy, approving ApprovalRequest",
			"approvalRequest", approvalReqRef,
			"clusters", clusterNames,
			"workloads", len(workloadTracker.Workloads))

		status := approvalReqObj.GetApprovalRequestStatus()
		approvedCond := meta.FindStatusCondition(status.Conditions, string(placementv1beta1.ApprovalRequestConditionApproved))

		// Only update if not already approved
		if approvedCond == nil || approvedCond.Status != metav1.ConditionTrue {
			meta.SetStatusCondition(&status.Conditions, metav1.Condition{
				Type:               string(placementv1beta1.ApprovalRequestConditionApproved),
				Status:             metav1.ConditionTrue,
				ObservedGeneration: obj.GetGeneration(),
				Reason:             "AllWorkloadsHealthy",
				Message:            fmt.Sprintf("All %d workloads are healthy across %d clusters", len(workloadTracker.Workloads), len(clusterNames)),
			})

			approvalReqObj.SetApprovalRequestStatus(*status)
			if err := r.Client.Status().Update(ctx, obj); err != nil {
				klog.ErrorS(err, "Failed to approve ApprovalRequest", "approvalRequest", approvalReqRef)
				return fmt.Errorf("failed to approve ApprovalRequest: %w", err)
			}

			klog.InfoS("Successfully approved ApprovalRequest", "approvalRequest", approvalReqRef)
			r.recorder.Event(obj, "Normal", "Approved", fmt.Sprintf("All %d workloads are healthy across %d clusters in stage %s", len(workloadTracker.Workloads), len(clusterNames), stageName))
		} else {
			klog.V(2).InfoS("ApprovalRequest already approved", "approvalRequest", approvalReqRef)
		}

		// Approval successful or already approved
		return nil
	}

	// Not all workloads are healthy yet, log details and return nil (reconcile will requeue)
	klog.V(2).InfoS("Not all workloads are healthy yet",
		"approvalRequest", approvalReqRef,
		"unhealthyDetails", unhealthyDetails)

	return nil
}

// handleDelete handles the deletion of an ApprovalRequest or ClusterApprovalRequest
func (r *Reconciler) handleDelete(ctx context.Context, approvalReqObj placementv1beta1.ApprovalRequestObj, isClusterScoped bool) (ctrl.Result, error) {
	obj := approvalReqObj.(client.Object)
	if !controllerutil.ContainsFinalizer(obj, metricCollectorFinalizer) {
		return ctrl.Result{}, nil
	}

	approvalReqRef := klog.KObj(obj)
	klog.V(2).InfoS("Cleaning up resources for ApprovalRequest", "approvalRequest", approvalReqRef)

	// Delete CRP (it will cascade delete the resources on member clusters)
	spec := approvalReqObj.GetApprovalRequestSpec()
	updateRunName := spec.TargetUpdateRun
	stageName := spec.TargetStage
	crpName := fmt.Sprintf("crp-mc-%s-%s", updateRunName, stageName)
	metricCollectorName := fmt.Sprintf("mc-%s-%s", updateRunName, stageName)
	croName := fmt.Sprintf("ro-mc-%s-%s", updateRunName, stageName)

	crp := &placementv1beta1.ClusterResourcePlacement{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: crpName}, crp); err == nil {
		if err := r.Client.Delete(ctx, crp); err != nil && !errors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to delete CRP: %w", err)
		}
		klog.V(2).InfoS("Deleted ClusterResourcePlacement", "crp", crpName)
	}

	// Delete ClusterResourceOverride
	cro := &placementv1beta1.ClusterResourceOverride{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: croName}, cro); err == nil {
		if err := r.Client.Delete(ctx, cro); err != nil && !errors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to delete ClusterResourceOverride: %w", err)
		}
		klog.V(2).InfoS("Deleted ClusterResourceOverride", "clusterResourceOverride", croName)
	}

	// Delete MetricCollector
	metricCollector := &placementv1beta1.MetricCollector{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: metricCollectorName}, metricCollector); err == nil {
		if err := r.Client.Delete(ctx, metricCollector); err != nil && !errors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to delete MetricCollector: %w", err)
		}
		klog.V(2).InfoS("Deleted MetricCollector", "metricCollector", metricCollectorName)
	}

	// Remove finalizer
	controllerutil.RemoveFinalizer(obj, metricCollectorFinalizer)
	if err := r.Client.Update(ctx, obj); err != nil {
		klog.ErrorS(err, "Failed to remove finalizer", "approvalRequest", approvalReqRef)
		return ctrl.Result{}, err
	}

	klog.V(2).InfoS("Successfully cleaned up resources", "approvalRequest", approvalReqRef)
	return ctrl.Result{}, nil
}

// SetupWithManagerForClusterApprovalRequest sets up the controller with the Manager for ClusterApprovalRequest resources.
func (r *Reconciler) SetupWithManagerForClusterApprovalRequest(mgr ctrl.Manager) error {
	r.recorder = mgr.GetEventRecorderFor("clusterapprovalrequest-controller")
	return ctrl.NewControllerManagedBy(mgr).
		Named("clusterapprovalrequest-controller").
		For(&placementv1beta1.ClusterApprovalRequest{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

// SetupWithManagerForApprovalRequest sets up the controller with the Manager for ApprovalRequest resources.
func (r *Reconciler) SetupWithManagerForApprovalRequest(mgr ctrl.Manager) error {
	r.recorder = mgr.GetEventRecorderFor("approvalrequest-controller")
	return ctrl.NewControllerManagedBy(mgr).
		Named("approvalrequest-controller").
		For(&placementv1beta1.ApprovalRequest{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}
