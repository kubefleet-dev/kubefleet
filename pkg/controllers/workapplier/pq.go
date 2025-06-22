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
	"time"

	"github.com/kubefleet-dev/kubefleet/pkg/utils/condition"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/controller"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/priorityqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	fleetv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

const (
	// A list of priority levels and their targets for the work applier priority queue.
	//
	// The work applier, when a priority queue is in use, will prioritize requests in the following
	// order:
	// * with highest priority (-2): all Create/Delete events, and all Update events
	//   that concern recently created Work objects or Work objects that are in a failed/undeterminted
	//   state (apply op/availability check failure, or diff reporting failure).
	// * with medium priority (-1): all other Update events.
	// * with default priority (0): all requeues (with or with errors), and all Generic events.
	//
	// Note that requests with the same priority level will be processed in the FIFO order.
	//
	// TO-DO (chenyu1): evaluate if/how we need to/should prioritize requeues properly.
	highPriorityLevel    = 2
	mediumPriorityLevel  = 1
	defaultPriorityLevel = 0
)

type CustomPriorityQueueManager interface {
	PriorityQueue() priorityqueue.PriorityQueue[reconcile.Request]
}

var _ handler.TypedEventHandler[client.Object, reconcile.Request] = &priorityBasedWorkObjEventHandler{}

// priorityBasedWorkObjEventHandler implements the TypedEventHandler interface.
//
// It is used to process work object events in a priority-based manner with a priority queue.
type priorityBasedWorkObjEventHandler struct {
	qm                                 CustomPriorityQueueManager
	workObjAgeForPrioritizedProcessing time.Duration
}

// Create implements the TypedEventHandler interface.
func (h *priorityBasedWorkObjEventHandler) Create(_ context.Context, createEvent event.TypedCreateEvent[client.Object], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	// Do a sanity check.
	//
	// Normally when this method is called, the priority queue has been initialized.
	if h.qm.PriorityQueue() == nil {
		wrappedErr := fmt.Errorf("received a Create event, but the priority queue is not initialized")
		_ = controller.NewUnexpectedBehaviorError(wrappedErr)
		klog.ErrorS(wrappedErr, "Failed to process Create event")
		return
	}

	// Enqueue the request with high priority.
	opts := priorityqueue.AddOpts{
		Priority: highPriorityLevel,
	}
	workObjName := createEvent.Object.GetName()
	workObjNS := createEvent.Object.GetNamespace()
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: workObjNS,
			Name:      workObjName,
		},
	}
	h.qm.PriorityQueue().AddWithOpts(opts, req)
}

// Delete implements the TypedEventHandler interface.
func (h *priorityBasedWorkObjEventHandler) Delete(_ context.Context, deleteEvent event.TypedDeleteEvent[client.Object], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	// Do a sanity check.
	if h.qm.PriorityQueue() == nil {
		wrappedErr := fmt.Errorf("received a Delete event, but the priority queue is not initialized")
		_ = controller.NewUnexpectedBehaviorError(wrappedErr)
		klog.ErrorS(wrappedErr, "Failed to process Delete event")
		return
	}

	// Enqueue the request with high priority.
	opts := priorityqueue.AddOpts{
		Priority: highPriorityLevel,
	}
	workObjName := deleteEvent.Object.GetName()
	workObjNS := deleteEvent.Object.GetNamespace()
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: workObjNS,
			Name:      workObjName,
		},
	}
	h.qm.PriorityQueue().AddWithOpts(opts, req)
}

// Update implements the TypedEventHandler interface.
func (h *priorityBasedWorkObjEventHandler) Update(_ context.Context, updateEvent event.TypedUpdateEvent[client.Object], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	// Do a sanity check.
	if h.qm.PriorityQueue() == nil {
		wrappedErr := fmt.Errorf("received an Update event, but the priority queue is not initialized")
		_ = controller.NewUnexpectedBehaviorError(wrappedErr)
		klog.ErrorS(wrappedErr, "Failed to process Update event")
		return
	}

	// Ignore status only updates.
	if updateEvent.ObjectOld.GetGeneration() == updateEvent.ObjectNew.GetGeneration() {
		return
	}

	oldWorkObj, oldOK := updateEvent.ObjectOld.(*fleetv1beta1.Work)
	newWorkObj, newOK := updateEvent.ObjectNew.(*fleetv1beta1.Work)
	if !oldOK || !newOK {
		wrappedErr := fmt.Errorf("received an Update event, but the objects cannot be cast to Work objects")
		_ = controller.NewUnexpectedBehaviorError(wrappedErr)
		klog.ErrorS(wrappedErr, "Failed to process Update event")
		return
	}

	pri := h.determineUpdateEventPriority(oldWorkObj, newWorkObj)
	opts := priorityqueue.AddOpts{
		Priority: pri,
	}
	workObjName := newWorkObj.GetName()
	workObjNS := newWorkObj.GetNamespace()
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: workObjNS,
			Name:      workObjName,
		},
	}
	h.qm.PriorityQueue().AddWithOpts(opts, req)
}

// Generic implements the TypedEventHandler interface.
func (h *priorityBasedWorkObjEventHandler) Generic(_ context.Context, genericEvent event.TypedGenericEvent[client.Object], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	// Do a sanity check.
	if h.qm.PriorityQueue() == nil {
		wrappedErr := fmt.Errorf("received a Generic event, but the priority queue is not initialized")
		_ = controller.NewUnexpectedBehaviorError(wrappedErr)
		klog.ErrorS(wrappedErr, "Failed to process Generic event")
		return
	}

	// Enqueue the request with default priority.
	opts := priorityqueue.AddOpts{
		Priority: defaultPriorityLevel,
	}
	workObjName := genericEvent.Object.GetName()
	workObjNS := genericEvent.Object.GetNamespace()
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: workObjNS,
			Name:      workObjName,
		},
	}
	h.qm.PriorityQueue().AddWithOpts(opts, req)
}

func (h *priorityBasedWorkObjEventHandler) determineUpdateEventPriority(oldWorkObj, newWorkObj *fleetv1beta1.Work) int {
	// If the work object is recently created (its age is within the given threshold),
	// process its Update event with high priority.

	// The age is expected to be the same for both old and new work objects, as the field
	// is immutable and not user configurable.
	workObjAge := time.Since(newWorkObj.CreationTimestamp.Time)
	if workObjAge <= h.workObjAgeForPrioritizedProcessing {
		return highPriorityLevel
	}

	// Check if the work object is in a failed/undetermined state.
	oldApplyStrategy := oldWorkObj.Spec.ApplyStrategy
	isReportDiffModeEnabled := oldApplyStrategy != nil && oldApplyStrategy.Type == fleetv1beta1.ApplyStrategyTypeReportDiff

	appliedCond := meta.FindStatusCondition(oldWorkObj.Status.Conditions, fleetv1beta1.WorkConditionTypeApplied)
	availableCond := meta.FindStatusCondition(oldWorkObj.Status.Conditions, fleetv1beta1.WorkConditionTypeAvailable)
	diffReportedCond := meta.FindStatusCondition(oldWorkObj.Status.Conditions, fleetv1beta1.WorkConditionTypeDiffReported)

	// Note (chenyu1): it might be true that the Update event involves an apply strategy change; however, the prioritization
	// logic stays the same: if the old work object is in a failed/undetermined state, the apply strategy change
	// should receive the highest priority; otherwise, the Update event should be processed with medium priority.
	switch {
	case isReportDiffModeEnabled && condition.IsConditionStatusTrue(diffReportedCond, oldWorkObj.Generation):
		// The ReportDiff mode is enabled and the status suggests that the diff reporting has been completed successfully.
		// Use medium priority for the Update event.
		return mediumPriorityLevel
	case isReportDiffModeEnabled:
		// The ReportDiff mode is enabled, but the diff reporting has not been completed yet or has failed.
		// Use high priority for the Update event.
		return highPriorityLevel
	case condition.IsConditionStatusTrue(appliedCond, oldWorkObj.Generation) && condition.IsConditionStatusTrue(availableCond, oldWorkObj.Generation):
		// The apply strategy is set to the CSA/SSA mode and the work object is applied and available.
		// Use medium priority for the Update event.
		return mediumPriorityLevel
	default:
		// The apply strategy is set to the CSA/SSA mode and the work object is in a failed/undetermined state.
		// Use high priority for the Update event.
		return highPriorityLevel
	}
}
