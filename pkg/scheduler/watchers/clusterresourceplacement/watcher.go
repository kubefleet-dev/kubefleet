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

// Package clusterresourceplacement features a controller that enqueues placement objects for the
// scheduler to process where the placement object is marked for deletion.
package clusterresourceplacement

import (
	"context"
	"fmt"
	"time"

	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	fleetv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/scheduler/queue"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/controller"
)

// Reconciler reconciles the deletion of a placement object.
type Reconciler struct {
	// Client is the client the controller uses to access the hub cluster.
	client.Client
	// SchedulerWorkQueue is the workqueue in use by the scheduler.
	SchedulerWorkQueue queue.PlacementSchedulingQueueWriter
}

// Reconcile reconciles the placement object.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	placementRef := klog.KRef(req.Namespace, req.Name)
	startTime := time.Now()
	klog.V(2).InfoS("Scheduler source reconciliation starts", "placement", placementRef)
	defer func() {
		latency := time.Since(startTime).Milliseconds()
		klog.V(2).InfoS("Scheduler source reconciliation ends", "placement", placementRef, "latency", latency)
	}()

	// Retrieve the placement object (either ClusterResourcePlacement or ResourcePlacement).
	placementKey := controller.GetObjectKeyFromRequest(req)
	placement, err := controller.FetchPlacementFromKey(ctx, r.Client, placementKey)
	if err != nil {
		klog.ErrorS(err, "Failed to get placement", "placement", placementRef)
		return ctrl.Result{}, controller.NewAPIServerError(true, client.IgnoreNotFound(err))
	}

	// Check if the placement has been deleted and has the scheduler finalizer.
	if placement.GetDeletionTimestamp() != nil && controllerutil.ContainsFinalizer(placement, fleetv1beta1.SchedulerCleanupFinalizer) {
		// The placement has been deleted and still has the scheduler finalizer;
		// enqueue it for the scheduler to process.
		r.SchedulerWorkQueue.AddRateLimited(placementKey)
	}

	// No action is needed for the scheduler to take in other cases.
	return ctrl.Result{}, nil
}

func buildCustomPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			// Ignore creation events.
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			// Ignore deletion events (events emitted when the object is actually removed
			// from storage).
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			// Check if the update event is valid.
			if e.ObjectOld == nil || e.ObjectNew == nil {
				err := controller.NewUnexpectedBehaviorError(fmt.Errorf("update event is invalid"))
				klog.ErrorS(err, "Failed to process update event")
				return false
			}

			// Check if the deletion timestamp has been set.
			oldDeletionTimestamp := e.ObjectOld.GetDeletionTimestamp()
			newDeletionTimestamp := e.ObjectNew.GetDeletionTimestamp()
			if oldDeletionTimestamp == nil && newDeletionTimestamp != nil {
				return true
			}

			return false
		},
	}
}

// SetupWithManagerForClusterResourcePlacement sets up the controller with the manager for ClusterResourcePlacement.
func (r *Reconciler) SetupWithManagerForClusterResourcePlacement(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).Named("clusterresourceplacement-scheduler-watcher").
		For(&fleetv1beta1.ClusterResourcePlacement{}).
		WithEventFilter(buildCustomPredicate()).
		Complete(r)
}

// SetupWithManagerForResourcePlacement sets up the controller with the manager for ResourcePlacement.
func (r *Reconciler) SetupWithManagerForResourcePlacement(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).Named("resourceplacement-scheduler-watcher").
		For(&fleetv1beta1.ResourcePlacement{}).
		WithEventFilter(buildCustomPredicate()).
		Complete(r)
}
