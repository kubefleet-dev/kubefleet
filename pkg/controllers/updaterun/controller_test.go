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

package updaterun

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllertest"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

func TestHandleClusterApprovalRequestUpdate(t *testing.T) {
	tests := map[string]struct {
		oldObj        client.Object
		newObj        client.Object
		shouldEnqueue bool
		queuedName    string
	}{
		"it should not enqueue anything if the obj is not a ClusterApprovalRequest": {
			oldObj:        &placementv1beta1.ClusterStagedUpdateRun{},
			shouldEnqueue: false,
		},
		"it should not enqueue anything if targetUpdateRun in spec is empty": {
			oldObj: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 1,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "",
				},
			},
			newObj: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 1,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "",
				},
				Status: placementv1beta1.ApprovalRequestStatus{
					Conditions: []metav1.Condition{
						{
							Status:             metav1.ConditionTrue,
							Type:               string(placementv1beta1.ApprovalRequestConditionApproved),
							ObservedGeneration: 1,
						},
					},
				},
			},
			shouldEnqueue: false,
		},
		"it should enqueue the targetUpdateRun if oldObj is not approved while newobj is approved": {
			oldObj: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 1,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test",
				},
			},
			newObj: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 1,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test",
				},
				Status: placementv1beta1.ApprovalRequestStatus{
					Conditions: []metav1.Condition{
						{
							Status:             metav1.ConditionTrue,
							Type:               string(placementv1beta1.ApprovalRequestConditionApproved),
							ObservedGeneration: 1,
						},
					},
				},
			},
			shouldEnqueue: true,
			queuedName:    "test",
		},
		"it should enqueue the targetUpdateRun if oldObj is not declined while newobj is approved": {
			oldObj: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 1,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test",
				},
				Status: placementv1beta1.ApprovalRequestStatus{
					Conditions: []metav1.Condition{
						{
							Status:             metav1.ConditionFalse,
							Type:               string(placementv1beta1.ApprovalRequestConditionApproved),
							ObservedGeneration: 1,
						},
					},
				},
			},
			newObj: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 1,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test",
				},
				Status: placementv1beta1.ApprovalRequestStatus{
					Conditions: []metav1.Condition{
						{
							Status:             metav1.ConditionTrue,
							Type:               string(placementv1beta1.ApprovalRequestConditionApproved),
							ObservedGeneration: 1,
						},
					},
				},
			},
			shouldEnqueue: true,
			queuedName:    "test",
		},
		"it should enqueue the targetUpdateRun if oldObj is approved while newobj is not approved": {
			oldObj: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 1,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test",
				},
				Status: placementv1beta1.ApprovalRequestStatus{
					Conditions: []metav1.Condition{
						{
							Status:             metav1.ConditionTrue,
							Type:               string(placementv1beta1.ApprovalRequestConditionApproved),
							ObservedGeneration: 1,
						},
					},
				},
			},
			newObj: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 1,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test",
				},
			},
			shouldEnqueue: true,
			queuedName:    "test",
		},
		"it should enqueue the targetUpdateRun if oldObj is approved while newobj is declined": {
			oldObj: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 1,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test",
				},
				Status: placementv1beta1.ApprovalRequestStatus{
					Conditions: []metav1.Condition{
						{
							Status:             metav1.ConditionTrue,
							Type:               string(placementv1beta1.ApprovalRequestConditionApproved),
							ObservedGeneration: 1,
						},
					},
				},
			},
			newObj: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 1,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test",
				},
				Status: placementv1beta1.ApprovalRequestStatus{
					Conditions: []metav1.Condition{
						{
							Status:             metav1.ConditionFalse,
							Type:               string(placementv1beta1.ApprovalRequestConditionApproved),
							ObservedGeneration: 1,
						},
					},
				},
			},
			shouldEnqueue: true,
			queuedName:    "test",
		},
		"it should not enqueue the targetUpdateRun if neither oldObj nor newobj is approved": {
			oldObj: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 1,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test",
				},
			},
			newObj: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 1,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test",
				},
			},
			shouldEnqueue: false,
		},
		"it should not enqueue the targetUpdateRun if both oldObj and newobj are approved": {
			oldObj: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 1,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test",
				},
				Status: placementv1beta1.ApprovalRequestStatus{
					Conditions: []metav1.Condition{
						{
							Status:             metav1.ConditionTrue,
							Type:               string(placementv1beta1.ApprovalRequestConditionApproved),
							ObservedGeneration: 1,
						},
					},
				},
			},
			newObj: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 1,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test",
				},
				Status: placementv1beta1.ApprovalRequestStatus{
					Conditions: []metav1.Condition{
						{
							Status:             metav1.ConditionTrue,
							Type:               string(placementv1beta1.ApprovalRequestConditionApproved),
							ObservedGeneration: 1,
						},
					},
				},
			},
			shouldEnqueue: false,
		},
		"it should not enqueue the targetUpdateRun if both oldObj and newobj are declined": {
			oldObj: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 1,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test",
				},
				Status: placementv1beta1.ApprovalRequestStatus{
					Conditions: []metav1.Condition{
						{
							Status:             metav1.ConditionFalse,
							Type:               string(placementv1beta1.ApprovalRequestConditionApproved),
							ObservedGeneration: 1,
						},
					},
				},
			},
			newObj: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 1,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test",
				},
				Status: placementv1beta1.ApprovalRequestStatus{
					Conditions: []metav1.Condition{
						{
							Status:             metav1.ConditionFalse,
							Type:               string(placementv1beta1.ApprovalRequestConditionApproved),
							ObservedGeneration: 1,
						},
					},
				},
			},
			shouldEnqueue: false,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			queue := &controllertest.Queue{TypedInterface: workqueue.NewTypedRateLimitingQueue[reconcile.Request](
				workqueue.DefaultTypedItemBasedRateLimiter[reconcile.Request]())}
			handleClusterApprovalRequestUpdate(tt.oldObj, tt.newObj, queue)
			if got := queue.Len() != 0; got != tt.shouldEnqueue {
				t.Fatalf("handleClusterApprovalRequest() shouldEnqueue got %t, want %t", got, tt.shouldEnqueue)
			}
			if tt.shouldEnqueue {
				req, _ := queue.TypedInterface.Get()
				if req.Name != tt.queuedName {
					t.Fatalf("handleClusterApprovalRequest() queuedName got %s, want %s", req.Name, tt.queuedName)
				}
			}
		})
	}
}

func TestHandleClusterApprovalRequestDelete(t *testing.T) {
	tests := map[string]struct {
		obj           client.Object
		shouldEnqueue bool
		queuedName    string
	}{
		"it should not enqueue anything if the obj is not a ClusterApprovalRequest": {
			obj:           &placementv1beta1.ClusterStagedUpdateRun{},
			shouldEnqueue: false,
		},
		"it should not enqueue anything if targetUpdateRun in spec is empty": {
			obj: &placementv1beta1.ClusterApprovalRequest{
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "",
				},
			},
			shouldEnqueue: false,
		},
		"it should enqueue the targetUpdateRun, if ClusterApprovalRequest has neither Approved/ApprovalAccepted status set": {
			obj: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 1,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test-update-run",
				},
			},
			shouldEnqueue: true,
			queuedName:    "test-update-run",
		},
		"it should enqueue the targetUpdateRun, if ClusterApprovalRequest has only Approved status set to true": {
			obj: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 1,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test-update-run",
				},
				Status: placementv1beta1.ApprovalRequestStatus{
					Conditions: []metav1.Condition{
						{
							Status:             metav1.ConditionTrue,
							Type:               string(placementv1beta1.ApprovalRequestConditionApproved),
							ObservedGeneration: 1,
						},
					},
				},
			},
			shouldEnqueue: true,
			queuedName:    "test-update-run",
		},
		"it should enqueue the targetUpdateRun, if ClusterApprovalRequest has only Approved status set to false": {
			obj: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 1,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test-update-run",
				},
				Status: placementv1beta1.ApprovalRequestStatus{
					Conditions: []metav1.Condition{
						{
							Status:             metav1.ConditionTrue,
							Type:               string(placementv1beta1.ApprovalRequestConditionApproved),
							ObservedGeneration: 1,
						},
					},
				},
			},
			shouldEnqueue: true,
			queuedName:    "test-update-run",
		},
		"it should not enqueue updateRun, if ClusterApprovalRequest has Approved set to false, ApprovalAccepted status set to true": {
			obj: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 1,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test-update-run",
				},
				Status: placementv1beta1.ApprovalRequestStatus{
					Conditions: []metav1.Condition{
						{
							Status:             metav1.ConditionFalse,
							Type:               string(placementv1beta1.ApprovalRequestConditionApproved),
							ObservedGeneration: 1,
						},
						{
							Status:             metav1.ConditionTrue,
							Type:               string(placementv1beta1.ApprovalRequestConditionApprovalAccepted),
							ObservedGeneration: 1,
						},
					},
				},
			},
			shouldEnqueue: false,
		},
		"it should not enqueue updateRun, if ClusterApprovalRequest has Approved, ApprovalAccepted status set to true": {
			obj: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 1,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test-update-run",
				},
				Status: placementv1beta1.ApprovalRequestStatus{
					Conditions: []metav1.Condition{
						{
							Status:             metav1.ConditionTrue,
							Type:               string(placementv1beta1.ApprovalRequestConditionApproved),
							ObservedGeneration: 1,
						},
						{
							Status:             metav1.ConditionTrue,
							Type:               string(placementv1beta1.ApprovalRequestConditionApprovalAccepted),
							ObservedGeneration: 1,
						},
					},
				},
			},
			shouldEnqueue: false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			queue := &controllertest.Queue{TypedInterface: workqueue.NewTypedRateLimitingQueue[reconcile.Request](
				workqueue.DefaultTypedItemBasedRateLimiter[reconcile.Request]())}
			handleClusterApprovalRequestDelete(tt.obj, queue)
			if got := queue.Len() != 0; got != tt.shouldEnqueue {
				t.Fatalf("handleClusterApprovalRequestDelete() shouldEnqueue got %t, want %t", got, tt.shouldEnqueue)
			}
			if tt.shouldEnqueue {
				req, _ := queue.TypedInterface.Get()
				if req.Name != tt.queuedName {
					t.Fatalf("handleClusterApprovalRequestDelete() queuedName got %s, want %s", req.Name, tt.queuedName)
				}
			}
		})
	}
}

func TestRemoveWaitTimeFromUpdateRunStatus(t *testing.T) {
	waitTime := metav1.Duration{Duration: 5 * time.Minute}
	tests := map[string]struct {
		inputUpdateRun *placementv1beta1.ClusterStagedUpdateRun
		wantUpdateRun  *placementv1beta1.ClusterStagedUpdateRun
	}{
		"should handle empty stages": {
			inputUpdateRun: &placementv1beta1.ClusterStagedUpdateRun{
				Status: placementv1beta1.StagedUpdateRunStatus{
					StagedUpdateStrategySnapshot: &placementv1beta1.StagedUpdateStrategySpec{
						Stages: []placementv1beta1.StageConfig{},
					},
				},
			},
			wantUpdateRun: &placementv1beta1.ClusterStagedUpdateRun{
				Status: placementv1beta1.StagedUpdateRunStatus{
					StagedUpdateStrategySnapshot: &placementv1beta1.StagedUpdateStrategySpec{
						Stages: []placementv1beta1.StageConfig{},
					},
				},
			},
		},
		"should handle nil StagedUpdateStrategySnapshot": {
			inputUpdateRun: &placementv1beta1.ClusterStagedUpdateRun{
				Status: placementv1beta1.StagedUpdateRunStatus{
					StagedUpdateStrategySnapshot: nil,
				},
			},
			wantUpdateRun: &placementv1beta1.ClusterStagedUpdateRun{
				Status: placementv1beta1.StagedUpdateRunStatus{
					StagedUpdateStrategySnapshot: nil,
				},
			},
		},
		"should remove waitTime from Approval tasks only": {
			inputUpdateRun: &placementv1beta1.ClusterStagedUpdateRun{
				Status: placementv1beta1.StagedUpdateRunStatus{
					StagedUpdateStrategySnapshot: &placementv1beta1.StagedUpdateStrategySpec{
						Stages: []placementv1beta1.StageConfig{
							{
								AfterStageTasks: []placementv1beta1.AfterStageTask{
									{
										Type:     placementv1beta1.AfterStageTaskTypeApproval,
										WaitTime: &waitTime,
									},
									{
										Type:     placementv1beta1.AfterStageTaskTypeTimedWait,
										WaitTime: &waitTime,
									},
								},
							},
						},
					},
				},
			},
			wantUpdateRun: &placementv1beta1.ClusterStagedUpdateRun{
				Status: placementv1beta1.StagedUpdateRunStatus{
					StagedUpdateStrategySnapshot: &placementv1beta1.StagedUpdateStrategySpec{
						Stages: []placementv1beta1.StageConfig{
							{
								AfterStageTasks: []placementv1beta1.AfterStageTask{
									{
										Type: placementv1beta1.AfterStageTaskTypeApproval,
									},
									{
										Type:     placementv1beta1.AfterStageTaskTypeTimedWait,
										WaitTime: &waitTime,
									},
								},
							},
						},
					},
				},
			},
		},
		"should handle multiple stages": {
			inputUpdateRun: &placementv1beta1.ClusterStagedUpdateRun{
				Status: placementv1beta1.StagedUpdateRunStatus{
					StagedUpdateStrategySnapshot: &placementv1beta1.StagedUpdateStrategySpec{
						Stages: []placementv1beta1.StageConfig{
							{
								AfterStageTasks: []placementv1beta1.AfterStageTask{
									{
										Type:     placementv1beta1.AfterStageTaskTypeApproval,
										WaitTime: &waitTime,
									},
								},
							},
							{
								AfterStageTasks: []placementv1beta1.AfterStageTask{
									{
										Type:     placementv1beta1.AfterStageTaskTypeTimedWait,
										WaitTime: &waitTime,
									},
									{
										Type:     placementv1beta1.AfterStageTaskTypeApproval,
										WaitTime: &waitTime,
									},
								},
							},
						},
					},
				},
			},
			wantUpdateRun: &placementv1beta1.ClusterStagedUpdateRun{
				Status: placementv1beta1.StagedUpdateRunStatus{
					StagedUpdateStrategySnapshot: &placementv1beta1.StagedUpdateStrategySpec{
						Stages: []placementv1beta1.StageConfig{
							{
								AfterStageTasks: []placementv1beta1.AfterStageTask{
									{
										Type: placementv1beta1.AfterStageTaskTypeApproval,
									},
								},
							},
							{
								AfterStageTasks: []placementv1beta1.AfterStageTask{
									{
										Type:     placementv1beta1.AfterStageTaskTypeTimedWait,
										WaitTime: &waitTime,
									},
									{
										Type: placementv1beta1.AfterStageTaskTypeApproval,
									},
								},
							},
						},
					},
				},
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			removeWaitTimeFromUpdateRunStatus(tt.inputUpdateRun)
			if diff := cmp.Diff(tt.wantUpdateRun, tt.inputUpdateRun); diff != "" {
				t.Errorf("removeWaitTimeFromUpdateRunStatus() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
