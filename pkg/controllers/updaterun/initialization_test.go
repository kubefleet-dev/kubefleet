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
	"context"
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

func TestValidateBeforeStageTask(t *testing.T) {
	tests := []struct {
		name    string
		task    []placementv1beta1.StageTask
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid BeforeTasks",
			task: []placementv1beta1.StageTask{
				{
					Type: placementv1beta1.StageTaskTypeApproval,
				},
			},
			wantErr: false,
		},
		{
			name: "invalid AfterTasks, greater than 1 task",
			task: []placementv1beta1.StageTask{
				{
					Type: placementv1beta1.StageTaskTypeApproval,
				},
				{
					Type: placementv1beta1.StageTaskTypeApproval,
				},
			},
			wantErr: true,
			errMsg:  "beforeStageTasks can have at most one task",
		},
		{
			name: "invalid BeforeTasks, with invalid task type",
			task: []placementv1beta1.StageTask{
				{
					Type:     placementv1beta1.StageTaskTypeTimedWait,
					WaitTime: ptr.To(metav1.Duration{Duration: 5 * time.Minute}),
				},
			},
			wantErr: true,
			errMsg:  fmt.Sprintf("task %d of type TimedWait is not allowed in beforeStageTasks", 0),
		},
		{
			name: "invalid BeforeTasks, with duration for Approval",
			task: []placementv1beta1.StageTask{
				{
					Type:     placementv1beta1.StageTaskTypeApproval,
					WaitTime: ptr.To(metav1.Duration{Duration: 1 * time.Minute}),
				},
			},
			wantErr: true,
			errMsg:  fmt.Sprintf("task %d of type Approval cannot have wait duration set", 0),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBeforeStageTask(tt.task)
			if tt.wantErr {
				if err == nil {
					t.Errorf("validateBeforeStageTask() error = nil, wantErr %v", tt.wantErr)
					return
				}
				if err.Error() != tt.errMsg {
					t.Errorf("validateBeforeStageTask() error = %v, wantErr %v", err, tt.errMsg)
				}
			} else if err != nil {
				t.Errorf("validateBeforeStageTask() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateAfterStageTask(t *testing.T) {
	tests := []struct {
		name    string
		task    []placementv1beta1.StageTask
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid AfterTasks",
			task: []placementv1beta1.StageTask{
				{
					Type: placementv1beta1.StageTaskTypeApproval,
				},
				{
					Type:     placementv1beta1.StageTaskTypeTimedWait,
					WaitTime: ptr.To(metav1.Duration{Duration: 5 * time.Minute}),
				},
			},
			wantErr: false,
		},
		{
			name: "invalid AfterTasks, same type of tasks",
			task: []placementv1beta1.StageTask{
				{
					Type:     placementv1beta1.StageTaskTypeTimedWait,
					WaitTime: ptr.To(metav1.Duration{Duration: 1 * time.Minute}),
				},
				{
					Type:     placementv1beta1.StageTaskTypeTimedWait,
					WaitTime: ptr.To(metav1.Duration{Duration: 5 * time.Minute}),
				},
			},
			wantErr: true,
			errMsg:  "afterStageTasks cannot have two tasks of the same type: TimedWait",
		},
		{
			name: "invalid AfterTasks, with nil duration for TimedWait",
			task: []placementv1beta1.StageTask{
				{
					Type: placementv1beta1.StageTaskTypeTimedWait,
				},
			},
			wantErr: true,
			errMsg:  "task 0 of type TimedWait has wait duration set to nil",
		},
		{
			name: "invalid AfterTasks, with zero duration for TimedWait",
			task: []placementv1beta1.StageTask{
				{
					Type:     placementv1beta1.StageTaskTypeTimedWait,
					WaitTime: ptr.To(metav1.Duration{Duration: 0 * time.Minute}),
				},
			},
			wantErr: true,
			errMsg:  "task 0 of type TimedWait has wait duration <= 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAfterStageTask(tt.task)
			if tt.wantErr {
				if err == nil {
					t.Errorf("validateAfterStageTask() error = nil, wantErr %v", tt.wantErr)
					return
				}
				if err.Error() != tt.errMsg {
					t.Errorf("validateAfterStageTask() error = %v, wantErr %v", err, tt.errMsg)
				}
			} else if err != nil {
				t.Errorf("validateAfterStageTask() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestComputeRunStageStatus_NegativeBeforeStageTasks(t *testing.T) {
	stageName := "stage-0"
	testUpdateRunName = "test-update-run"
	tests := []struct {
		name      string
		updateRun *placementv1beta1.ClusterStagedUpdateRun
		wantError bool
	}{
		{
			name: "two BeforeStageTasks in one stage should return error",
			updateRun: &placementv1beta1.ClusterStagedUpdateRun{
				ObjectMeta: metav1.ObjectMeta{
					Name: testUpdateRunName,
				},
				Status: placementv1beta1.UpdateRunStatus{
					UpdateStrategySnapshot: &placementv1beta1.UpdateStrategySpec{
						Stages: []placementv1beta1.StageConfig{
							{
								Name: stageName,
								BeforeStageTasks: []placementv1beta1.StageTask{
									{
										Type: placementv1beta1.StageTaskTypeApproval,
									},
									{
										Type: placementv1beta1.StageTaskTypeApproval,
									},
								},
							},
						},
					},
				},
			},
			wantError: true,
		},
		{
			name: "invalid BeforeStageTasks with TimeWait task type should return error",
			updateRun: &placementv1beta1.ClusterStagedUpdateRun{
				ObjectMeta: metav1.ObjectMeta{
					Name: testUpdateRunName,
				},
				Status: placementv1beta1.UpdateRunStatus{
					UpdateStrategySnapshot: &placementv1beta1.UpdateStrategySpec{
						Stages: []placementv1beta1.StageConfig{
							{
								Name: stageName,
								BeforeStageTasks: []placementv1beta1.StageTask{
									{
										Type: placementv1beta1.StageTaskTypeTimedWait,
									},
								},
							},
						},
					},
				},
			},
			wantError: true,
		},
		{
			name: "invalid BeforeStageTasks with duration for approval task type should return error",
			updateRun: &placementv1beta1.ClusterStagedUpdateRun{
				ObjectMeta: metav1.ObjectMeta{
					Name: testUpdateRunName,
				},
				Status: placementv1beta1.UpdateRunStatus{
					UpdateStrategySnapshot: &placementv1beta1.UpdateStrategySpec{
						Stages: []placementv1beta1.StageConfig{
							{
								Name: stageName,
								BeforeStageTasks: []placementv1beta1.StageTask{
									{
										Type:     placementv1beta1.StageTaskTypeApproval,
										WaitTime: ptr.To(metav1.Duration{Duration: 1 * time.Minute}),
									},
								},
							},
						},
					},
				},
			},
			wantError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []client.Object{tt.updateRun}
			scheme := runtime.NewScheme()
			_ = placementv1beta1.AddToScheme(scheme)
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				WithStatusSubresource(objects...).
				Build()
			r := Reconciler{
				Client: fakeClient,
			}
			ctx := context.Background()
			err := r.computeRunStageStatus(ctx, []placementv1beta1.BindingObj{}, tt.updateRun)
			if (err != nil) != tt.wantError {
				t.Fatal("computeRunStageStatus() error =", err, ", wantError", tt.wantError)
			}
		})
	}
}
