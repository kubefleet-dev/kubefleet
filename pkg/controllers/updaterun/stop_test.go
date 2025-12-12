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
	"errors"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/condition"
)

func TestStopDeleteStage(t *testing.T) {
	now := metav1.Now()
	deletionTime := metav1.NewTime(now.Add(-1 * time.Minute))

	tests := []struct {
		name                string
		updateRun           *placementv1beta1.ClusterStagedUpdateRun
		toBeDeletedBindings []placementv1beta1.BindingObj
		wantFinished        bool
		wantError           bool
		wantAbortError      bool
		wantStageStatus     metav1.ConditionStatus
		wantReason          string
	}{
		{
			name: "no bindings to delete - should finish and mark stage as stopped",
			updateRun: &placementv1beta1.ClusterStagedUpdateRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-updaterun",
					Generation: 1,
				},
				Status: placementv1beta1.UpdateRunStatus{
					DeletionStageStatus: &placementv1beta1.StageUpdatingStatus{
						StageName: "deletion",
						Clusters:  []placementv1beta1.ClusterUpdatingStatus{},
					},
				},
			},
			toBeDeletedBindings: []placementv1beta1.BindingObj{},
			wantFinished:        true,
			wantError:           false,
			wantStageStatus:     metav1.ConditionFalse,
			wantReason:          condition.StageUpdatingStoppedReason,
		},
		{
			name: "cluster being deleted with proper binding deletion timestamp - should not finish",
			updateRun: &placementv1beta1.ClusterStagedUpdateRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-updaterun",
					Generation: 1,
				},
				Status: placementv1beta1.UpdateRunStatus{
					DeletionStageStatus: &placementv1beta1.StageUpdatingStatus{
						StageName: "deletion",
						Clusters: []placementv1beta1.ClusterUpdatingStatus{
							{
								ClusterName: "cluster-1",
								Conditions: []metav1.Condition{
									{
										Type:               string(placementv1beta1.ClusterUpdatingConditionStarted),
										Status:             metav1.ConditionTrue,
										ObservedGeneration: 1,
										LastTransitionTime: now,
										Reason:             condition.ClusterUpdatingStartedReason,
									},
								},
							},
						},
					},
				},
			},
			toBeDeletedBindings: []placementv1beta1.BindingObj{
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						DeletionTimestamp: &deletionTime,
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster: "cluster-1",
					},
				},
			},
			wantFinished:    false,
			wantError:       false,
			wantStageStatus: metav1.ConditionUnknown,
			wantReason:      condition.StageUpdatingStoppingReason,
		},
		{
			name: "cluster marked as deleting but binding not deleting - should abort",
			updateRun: &placementv1beta1.ClusterStagedUpdateRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-updaterun",
					Generation: 1,
				},
				Status: placementv1beta1.UpdateRunStatus{
					DeletionStageStatus: &placementv1beta1.StageUpdatingStatus{
						StageName: "deletion",
						Clusters: []placementv1beta1.ClusterUpdatingStatus{
							{
								ClusterName: "cluster-1",
								Conditions: []metav1.Condition{
									{
										Type:               string(placementv1beta1.ClusterUpdatingConditionStarted),
										Status:             metav1.ConditionTrue,
										ObservedGeneration: 1,
										LastTransitionTime: now,
										Reason:             condition.ClusterUpdatingStartedReason,
									},
								},
							},
						},
					},
				},
			},
			toBeDeletedBindings: []placementv1beta1.BindingObj{
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						// No DeletionTimestamp set
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster: "cluster-1",
					},
				},
			},
			wantFinished:    false,
			wantError:       true,
			wantAbortError:  true,
			wantStageStatus: metav1.ConditionUnknown,
			wantReason:      condition.StageUpdatingStoppingReason,
		},
		{
			name: "multiple clusters with mixed states",
			updateRun: &placementv1beta1.ClusterStagedUpdateRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-updaterun",
					Generation: 1,
				},
				Status: placementv1beta1.UpdateRunStatus{
					DeletionStageStatus: &placementv1beta1.StageUpdatingStatus{
						StageName: "deletion",
						Clusters: []placementv1beta1.ClusterUpdatingStatus{
							{
								ClusterName: "cluster-1",
								Conditions: []metav1.Condition{
									{
										Type:               string(placementv1beta1.ClusterUpdatingConditionStarted),
										Status:             metav1.ConditionTrue,
										ObservedGeneration: 1,
										LastTransitionTime: now,
										Reason:             condition.ClusterUpdatingStartedReason,
									},
									{
										Type:               string(placementv1beta1.ClusterUpdatingConditionSucceeded),
										Status:             metav1.ConditionTrue,
										ObservedGeneration: 1,
										LastTransitionTime: now,
										Reason:             condition.ClusterUpdatingSucceededReason,
									},
								},
							},
							{
								ClusterName: "cluster-2",
								Conditions: []metav1.Condition{
									{
										Type:               string(placementv1beta1.ClusterUpdatingConditionStarted),
										Status:             metav1.ConditionTrue,
										ObservedGeneration: 1,
										LastTransitionTime: now,
										Reason:             condition.ClusterUpdatingStartedReason,
									},
								},
							},
						},
					},
				},
			},
			toBeDeletedBindings: []placementv1beta1.BindingObj{
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						DeletionTimestamp: &deletionTime,
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster: "cluster-1",
					},
				},
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						DeletionTimestamp: &deletionTime,
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster: "cluster-2",
					},
				},
			},
			wantFinished:    false,
			wantError:       false,
			wantStageStatus: metav1.ConditionUnknown,
			wantReason:      condition.StageUpdatingStoppingReason,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Reconciler{}

			gotFinished, gotErr := r.stopDeleteStage(tt.updateRun, tt.toBeDeletedBindings)

			// Check finished result.
			if gotFinished != tt.wantFinished {
				t.Errorf("stopDeleteStage() finished = %v, want %v", gotFinished, tt.wantFinished)
			}

			// Check error expectations.
			if tt.wantError {
				if gotErr == nil {
					t.Errorf("stopDeleteStage() error = nil, want error")
				} else if tt.wantAbortError && !errors.Is(gotErr, errStagedUpdatedAborted) {
					t.Errorf("stopDeleteStage() error = %v, want errStagedUpdatedAborted", gotErr)
				}
			} else if gotErr != nil {
				t.Errorf("stopDeleteStage() error = %v, want nil", gotErr)
			}

			// Check stage status condition.
			progressingCond := meta.FindStatusCondition(
				tt.updateRun.Status.DeletionStageStatus.Conditions,
				string(placementv1beta1.StageUpdatingConditionProgressing),
			)
			if progressingCond == nil {
				t.Errorf("stopDeleteStage() missing progressing condition")
			} else {
				if progressingCond.Status != tt.wantStageStatus {
					t.Errorf("stopDeleteStage() progressing condition status = %v, want %v",
						progressingCond.Status, tt.wantStageStatus)
				}

				if progressingCond.Reason != tt.wantReason {
					t.Errorf("stopDeleteStage() progressing condition reason = %v, want %v",
						progressingCond.Reason, tt.wantReason)
				}
			}
		})
	}
}
