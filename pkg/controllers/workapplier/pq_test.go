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
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/priorityqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/google/go-cmp/cmp"
	fleetv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

const (
	workObjAgeForPrioritizedProcessingTestOnly = time.Minute * 5

	pqName                         = "test-pq"
	workNameForPriorityTestingTmpl = "prioritized-work-%s"
)

type pqWrapper struct {
	pq priorityqueue.PriorityQueue[reconcile.Request]
}

// PriorityQueue implements the CustomPriorityQueueManager interface.
func (p *pqWrapper) PriorityQueue() priorityqueue.PriorityQueue[reconcile.Request] {
	return p.pq
}

// Verify that pqWrapper implements the CustomPriorityQueueManager interface.
var _ CustomPriorityQueueManager = &pqWrapper{}

// ExpectedDequeuedKeyAndPriority is used in tests to represent an expected dequeued key along with its priority.
type ExpectedDequeuedKeyAndPriority struct {
	Key      reconcile.Request
	Priority int
}

// TestCreateEventHandler tests the Create event handler of the priority-based Work object event handler.
func TestCreateEventHandler(t *testing.T) {
	ctx := context.Background()
	pq := priorityqueue.New[reconcile.Request](pqName)
	pqEventHandler := &priorityBasedWorkObjEventHandler{
		qm:                                 &pqWrapper{pq: pq},
		workObjAgeForPrioritizedProcessing: workObjAgeForPrioritizedProcessingTestOnly,
	}

	// Add two keys with medium and default priority levels respectively.
	opts := priorityqueue.AddOpts{
		Priority: ptr.To(mediumPriorityLevel),
	}
	workWithMediumPriName := fmt.Sprintf(workNameForPriorityTestingTmpl, "medium")
	key := types.NamespacedName{Namespace: memberReservedNSName1, Name: workWithMediumPriName}
	pq.AddWithOpts(opts, reconcile.Request{NamespacedName: key})

	opts = priorityqueue.AddOpts{
		Priority: ptr.To(defaultPriorityLevel),
	}
	workWithDefaultPriName := fmt.Sprintf(workNameForPriorityTestingTmpl, "default")
	key = types.NamespacedName{Namespace: memberReservedNSName1, Name: workWithDefaultPriName}
	pq.AddWithOpts(opts, reconcile.Request{NamespacedName: key})

	// Handle a CreateEvent, which should add a new key with high priority.
	workJustCreatedName := fmt.Sprintf(workNameForPriorityTestingTmpl, "just-created")
	workObj := fleetv1beta1.Work{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: memberReservedNSName1,
			Name:      workJustCreatedName,
		},
	}
	pqEventHandler.Create(ctx, event.TypedCreateEvent[client.Object]{Object: &workObj}, nil)

	// Check the queue length.
	if !cmp.Equal(pq.Len(), 3) {
		t.Fatalf("priority queue length, expected 3, got %d", pq.Len())
	}

	// Check if the first item dequeued is the one added by the CreateEvent handler (high priority).
	item, pri, _ := pq.GetWithPriority()
	wantItem := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: memberReservedNSName1,
			Name:      workJustCreatedName,
		},
	}
	if diff := cmp.Diff(item, wantItem); diff != "" {
		t.Errorf("dequeued item mismatch (-got, +want):\n%s", diff)
	}
	if !cmp.Equal(pri, highPriorityLevel) {
		t.Errorf("priority of dequeued item, expected %d, got %d", highPriorityLevel, pri)
	}
}

// TestUpdateEventHandler_NormalOps tests the Update event handler of the priority-based Work object event handler
// under normal operations.
func TestUpdateEventHandler_NormalOps(t *testing.T) {
	ctx := context.Background()
	pq := priorityqueue.New[reconcile.Request](pqName)
	pqEventHandler := &priorityBasedWorkObjEventHandler{
		qm:                                 &pqWrapper{pq: pq},
		workObjAgeForPrioritizedProcessing: workObjAgeForPrioritizedProcessingTestOnly,
	}

	// Add a key with default priority levels respectively.
	opts := priorityqueue.AddOpts{
		Priority: ptr.To(defaultPriorityLevel),
	}
	workWithDefaultPriName := fmt.Sprintf(workNameForPriorityTestingTmpl, "default")
	key := types.NamespacedName{Namespace: memberReservedNSName1, Name: workWithDefaultPriName}
	pq.AddWithOpts(opts, reconcile.Request{NamespacedName: key})

	// Handle an UpdateEvent that concerns a Work object with ReportDiff strategy and has been
	// processed successfully long before (>5 minutes ago).
	workInReportDiffModeAndProcessedLongBfrName := fmt.Sprintf(workNameForPriorityTestingTmpl, "report-diff-processed-long-bfr")
	longAgo := time.Now().Add(-time.Minute * 10)
	oldWorkObj := &fleetv1beta1.Work{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         memberReservedNSName1,
			Name:              workInReportDiffModeAndProcessedLongBfrName,
			CreationTimestamp: metav1.Time{Time: longAgo},
		},
		Spec: fleetv1beta1.WorkSpec{
			ApplyStrategy: &fleetv1beta1.ApplyStrategy{
				Type: fleetv1beta1.ApplyStrategyTypeReportDiff,
			},
		},
		Status: fleetv1beta1.WorkStatus{
			Conditions: []metav1.Condition{
				{
					Type:   fleetv1beta1.WorkConditionTypeDiffReported,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}
	newWorkObj := oldWorkObj.DeepCopy()
	// Simulate an update.
	newWorkObj.Generation += 1
	pqEventHandler.Update(ctx, event.TypedUpdateEvent[client.Object]{ObjectOld: oldWorkObj, ObjectNew: newWorkObj}, nil)

	// Handle an UpdateEvent that concerns a normal Work object that was created very recently (<5 minutes ago).
	workInCSAModeAndJustProcessedName := fmt.Sprintf(workNameForPriorityTestingTmpl, "csa-just-processed")
	shortWhileAgo := time.Now().Add(-time.Minute * 2)
	oldWorkObj = &fleetv1beta1.Work{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         memberReservedNSName1,
			Name:              workInCSAModeAndJustProcessedName,
			CreationTimestamp: metav1.Time{Time: shortWhileAgo},
		},
		Spec: fleetv1beta1.WorkSpec{},
		Status: fleetv1beta1.WorkStatus{
			Conditions: []metav1.Condition{
				{
					Type:   fleetv1beta1.WorkConditionTypeApplied,
					Status: metav1.ConditionTrue,
				},
				{
					Type:   fleetv1beta1.WorkConditionTypeAvailable,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}
	newWorkObj = oldWorkObj.DeepCopy()
	// Simulate an update.
	newWorkObj.Generation += 1
	pqEventHandler.Update(ctx, event.TypedUpdateEvent[client.Object]{ObjectOld: oldWorkObj, ObjectNew: newWorkObj}, nil)

	// Check the queue length.
	if !cmp.Equal(pq.Len(), 3) {
		t.Fatalf("priority queue length, expected 3, got %d", pq.Len())
	}

	// Dequeue all items and check if the keys and their assigned priorities are as expected.
	wantDequeuedItemsWithPriorities := []ExpectedDequeuedKeyAndPriority{
		{
			Key: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: memberReservedNSName1,
					Name:      workInCSAModeAndJustProcessedName,
				},
			},
			Priority: highPriorityLevel,
		},
		{
			Key: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: memberReservedNSName1,
					Name:      workInReportDiffModeAndProcessedLongBfrName,
				},
			},
			Priority: mediumPriorityLevel,
		},
		{
			Key: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: memberReservedNSName1,
					Name:      workWithDefaultPriName,
				},
			},
			Priority: defaultPriorityLevel,
		},
	}

	for i := 0; i < 3; i++ {
		item, pri, _ := pq.GetWithPriority()
		wantItemWithPri := wantDequeuedItemsWithPriorities[i]
		if diff := cmp.Diff(item, wantItemWithPri.Key); diff != "" {
			t.Errorf("dequeued item #%d mismatch (-got, +want):\n%s", i, diff)
		}
		if !cmp.Equal(pri, wantItemWithPri.Priority) {
			t.Errorf("priority of dequeued item #%d, expected %d, got %d", i, wantItemWithPri.Priority, pri)
		}
	}
}

// TestUpdateEventHandler_Erred tests the Update event handler of the priority-based Work object event handler
// when it encounters errors.
func TestUpdateEventHandler_Erred(t *testing.T) {
	ctx := context.Background()

	workObj := &fleetv1beta1.Work{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: memberReservedNSName1,
			Name:      fmt.Sprintf(workNameForPriorityTestingTmpl, "erred"),
		},
	}
	statusOnlyUpdateEvent := event.TypedUpdateEvent[client.Object]{
		ObjectOld: workObj,
		ObjectNew: workObj.DeepCopy(),
	}

	nsObj := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  memberReservedNSName1,
			Name:       nsName,
			Generation: 1,
		},
	}
	invalidUpdateEvent1 := event.TypedUpdateEvent[client.Object]{
		ObjectOld: nsObj,
		ObjectNew: workObj,
	}
	invalidUpdateEvent2 := event.TypedUpdateEvent[client.Object]{
		ObjectOld: workObj,
		ObjectNew: nsObj,
	}

	testCases := []struct {
		name        string
		updateEvent event.TypedUpdateEvent[client.Object]
	}{
		{
			name:        "status only update",
			updateEvent: statusOnlyUpdateEvent,
		},
		{
			// Normally this should never occur.
			name:        "invalid update event with the old object not being a Work",
			updateEvent: invalidUpdateEvent1,
		},
		{
			// Normally this should never occur.
			name:        "invalid update event with the new object not being a Work",
			updateEvent: invalidUpdateEvent2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pq := priorityqueue.New[reconcile.Request](pqName)
			pqEventHandler := &priorityBasedWorkObjEventHandler{
				qm:                                 &pqWrapper{pq: pq},
				workObjAgeForPrioritizedProcessing: workObjAgeForPrioritizedProcessingTestOnly,
			}
			pqEventHandler.Update(ctx, tc.updateEvent, nil)

			// Check the queue length.
			if !cmp.Equal(pq.Len(), 0) {
				t.Fatalf("priority queue length, expected 0, got %d", pq.Len())
			}
		})
	}
}

// TestDeleteEventHandler tests the Delete event handler of the priority-based Work object event handler.
func TestDeleteEventHandler(t *testing.T) {
	ctx := context.Background()
	pq := priorityqueue.New[reconcile.Request](pqName)
	pqEventHandler := &priorityBasedWorkObjEventHandler{
		qm:                                 &pqWrapper{pq: pq},
		workObjAgeForPrioritizedProcessing: workObjAgeForPrioritizedProcessingTestOnly,
	}

	// Add two keys with medium and default priority levels respectively.
	opts := priorityqueue.AddOpts{
		Priority: ptr.To(mediumPriorityLevel),
	}
	workWithMediumPriName := fmt.Sprintf(workNameForPriorityTestingTmpl, "medium")
	key := types.NamespacedName{Namespace: memberReservedNSName1, Name: workWithMediumPriName}
	pq.AddWithOpts(opts, reconcile.Request{NamespacedName: key})

	opts = priorityqueue.AddOpts{
		Priority: ptr.To(defaultPriorityLevel),
	}
	workWithDefaultPriName := fmt.Sprintf(workNameForPriorityTestingTmpl, "default")
	key = types.NamespacedName{Namespace: memberReservedNSName1, Name: workWithDefaultPriName}
	pq.AddWithOpts(opts, reconcile.Request{NamespacedName: key})

	// Handle a DeleteEvent, which should add a new key with high priority.
	workJustDeletedName := fmt.Sprintf(workNameForPriorityTestingTmpl, "just-deleted")
	workObj := fleetv1beta1.Work{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: memberReservedNSName1,
			Name:      workJustDeletedName,
		},
	}
	pqEventHandler.Delete(ctx, event.TypedDeleteEvent[client.Object]{Object: &workObj}, nil)

	// Check the queue length.
	if !cmp.Equal(pq.Len(), 3) {
		t.Fatalf("priority queue length, expected 3, got %d", pq.Len())
	}

	// Check if the first item dequeued is the one added by the DeleteEvent handler (high priority).
	item, pri, _ := pq.GetWithPriority()
	wantItem := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: memberReservedNSName1,
			Name:      workJustDeletedName,
		},
	}
	if diff := cmp.Diff(item, wantItem); diff != "" {
		t.Errorf("dequeued item mismatch (-got, +want):\n%s", diff)
	}
	if !cmp.Equal(pri, highPriorityLevel) {
		t.Errorf("priority of dequeued item, expected %d, got %d", highPriorityLevel, pri)
	}
}

// TestGenericEventHandler tests the Generic event handler of the priority-based Work object event handler.
func TestGenericEventHandler(t *testing.T) {
	ctx := context.Background()
	pq := priorityqueue.New[reconcile.Request](pqName)
	pqEventHandler := &priorityBasedWorkObjEventHandler{
		qm:                                 &pqWrapper{pq: pq},
		workObjAgeForPrioritizedProcessing: workObjAgeForPrioritizedProcessingTestOnly,
	}

	// Add two keys with high and medium priority levels respectively.
	opts := priorityqueue.AddOpts{
		Priority: ptr.To(highPriorityLevel),
	}
	workWithHighPriName := fmt.Sprintf(workNameForPriorityTestingTmpl, "high")
	key := types.NamespacedName{Namespace: memberReservedNSName1, Name: workWithHighPriName}
	pq.AddWithOpts(opts, reconcile.Request{NamespacedName: key})

	opts = priorityqueue.AddOpts{
		Priority: ptr.To(mediumPriorityLevel),
	}
	workWithMediumPriName := fmt.Sprintf(workNameForPriorityTestingTmpl, "medium")
	key = types.NamespacedName{Namespace: memberReservedNSName1, Name: workWithMediumPriName}
	pq.AddWithOpts(opts, reconcile.Request{NamespacedName: key})

	// Handle a GenericEvent, which should add a new key with default priority.
	workGenericEventName := fmt.Sprintf(workNameForPriorityTestingTmpl, "generic")
	workObj := fleetv1beta1.Work{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: memberReservedNSName1,
			Name:      workGenericEventName,
		},
	}
	pqEventHandler.Generic(ctx, event.TypedGenericEvent[client.Object]{Object: &workObj}, nil)

	// Check the queue length.
	if !cmp.Equal(pq.Len(), 3) {
		t.Fatalf("priority queue length, expected 3, got %d", pq.Len())
	}

	// Dequeue all items and check if the keys and their assigned priorities are as expected.
	wantDequeuedItemsWithPriorities := []ExpectedDequeuedKeyAndPriority{
		{
			Key: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: memberReservedNSName1,
					Name:      workWithHighPriName,
				},
			},
			Priority: highPriorityLevel,
		},
		{
			Key: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: memberReservedNSName1,
					Name:      workWithMediumPriName,
				},
			},
			Priority: mediumPriorityLevel,
		},
		{
			Key: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: memberReservedNSName1,
					Name:      workGenericEventName,
				},
			},
			Priority: defaultPriorityLevel,
		},
	}

	for i := 0; i < 3; i++ {
		item, pri, _ := pq.GetWithPriority()
		wantItemWithPri := wantDequeuedItemsWithPriorities[i]
		if diff := cmp.Diff(item, wantItemWithPri.Key); diff != "" {
			t.Errorf("dequeued item #%d mismatch (-got, +want):\n%s", i, diff)
		}
		if !cmp.Equal(pri, wantItemWithPri.Priority) {
			t.Errorf("priority of dequeued item #%d, expected %d, got %d", i, wantItemWithPri.Priority, pri)
		}
	}
}

func TestDetermineUpdateEventPriority(t *testing.T) {
	now := metav1.Now()
	longAgo := metav1.NewTime(now.Add(-time.Minute * 10))

	pq := priorityqueue.New[reconcile.Request](pqName)
	pqEventHandler := &priorityBasedWorkObjEventHandler{
		qm:                                 &pqWrapper{pq: pq},
		workObjAgeForPrioritizedProcessing: workObjAgeForPrioritizedProcessingTestOnly,
	}

	testCases := []struct {
		name         string
		oldWorkObj   *fleetv1beta1.Work
		newWorkObj   *fleetv1beta1.Work
		wantPriority int
	}{
		{
			name: "fresh work object",
			newWorkObj: &fleetv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:         memberReservedNSName1,
					Name:              workName,
					CreationTimestamp: now,
				},
			},
			wantPriority: highPriorityLevel,
		},
		{
			name: "reportDiff mode, diff reported",
			oldWorkObj: &fleetv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:         memberReservedNSName1,
					Name:              workName,
					CreationTimestamp: longAgo,
				},
				Spec: fleetv1beta1.WorkSpec{
					ApplyStrategy: &fleetv1beta1.ApplyStrategy{
						Type: fleetv1beta1.ApplyStrategyTypeReportDiff,
					},
				},
				Status: fleetv1beta1.WorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:   fleetv1beta1.WorkConditionTypeDiffReported,
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
			newWorkObj: &fleetv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:         memberReservedNSName1,
					Name:              workName,
					CreationTimestamp: longAgo,
				},
			},
			wantPriority: mediumPriorityLevel,
		},
		{
			name: "reportDiff mode, diff not reported",
			oldWorkObj: &fleetv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:         memberReservedNSName1,
					Name:              workName,
					CreationTimestamp: longAgo,
				},
				Spec: fleetv1beta1.WorkSpec{
					ApplyStrategy: &fleetv1beta1.ApplyStrategy{
						Type: fleetv1beta1.ApplyStrategyTypeReportDiff,
					},
				},
				Status: fleetv1beta1.WorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:   fleetv1beta1.WorkConditionTypeDiffReported,
							Status: metav1.ConditionFalse,
						},
					},
				},
			},
			newWorkObj: &fleetv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:         memberReservedNSName1,
					Name:              workName,
					CreationTimestamp: longAgo,
				},
			},
			wantPriority: highPriorityLevel,
		},
		{
			name: "CSA/SSA mode, applied and available",
			oldWorkObj: &fleetv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:         memberReservedNSName1,
					Name:              workName,
					CreationTimestamp: longAgo,
				},
				Spec: fleetv1beta1.WorkSpec{},
				Status: fleetv1beta1.WorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:   fleetv1beta1.WorkConditionTypeApplied,
							Status: metav1.ConditionTrue,
						},
						{
							Type:   fleetv1beta1.WorkConditionTypeAvailable,
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
			newWorkObj: &fleetv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:         memberReservedNSName1,
					Name:              workName,
					CreationTimestamp: longAgo,
				},
			},
			wantPriority: mediumPriorityLevel,
		},
		{
			name: "CSA/SSA mode, not applied and available",
			oldWorkObj: &fleetv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:         memberReservedNSName1,
					Name:              workName,
					CreationTimestamp: longAgo,
				},
				Spec: fleetv1beta1.WorkSpec{},
				Status: fleetv1beta1.WorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:   fleetv1beta1.WorkConditionTypeApplied,
							Status: metav1.ConditionFalse,
						},
					},
				},
			},
			newWorkObj: &fleetv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:         memberReservedNSName1,
					Name:              workName,
					CreationTimestamp: longAgo,
				},
			},
			wantPriority: highPriorityLevel,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pri := pqEventHandler.determineUpdateEventPriority(tc.oldWorkObj, tc.newWorkObj)
			if !cmp.Equal(pri, tc.wantPriority) {
				t.Errorf("determined priority, expected %d, got %d", tc.wantPriority, pri)
			}
		})
	}
}
