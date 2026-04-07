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
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/condition"
)

func TestIsBindingSyncedWithClusterStatus(t *testing.T) {
	tests := []struct {
		name                 string
		resourceSnapshotName string
		updateRun            *placementv1beta1.ClusterStagedUpdateRun
		binding              *placementv1beta1.ClusterResourceBinding
		cluster              *placementv1beta1.ClusterUpdatingStatus
		wantEqual            bool
	}{
		{
			name:                 "isBindingSyncedWithClusterStatus should return false if binding and updateRun have different resourceSnapshot",
			resourceSnapshotName: "test-1-snapshot",
			binding: &placementv1beta1.ClusterResourceBinding{
				Spec: placementv1beta1.ResourceBindingSpec{
					ResourceSnapshotName: "test-2-snapshot",
				},
			},
			wantEqual: false,
		},
		{
			name:                 "isBindingSyncedWithClusterStatus should return false if binding and cluster status have different resourceOverrideSnapshot list",
			resourceSnapshotName: "test-1-snapshot",
			binding: &placementv1beta1.ClusterResourceBinding{
				Spec: placementv1beta1.ResourceBindingSpec{
					ResourceSnapshotName: "test-1-snapshot",
					ResourceOverrideSnapshots: []placementv1beta1.NamespacedName{
						{
							Name:      "ro2",
							Namespace: "ns2",
						},
						{
							Name:      "ro1",
							Namespace: "ns1",
						},
					},
				},
			},
			cluster: &placementv1beta1.ClusterUpdatingStatus{
				ResourceOverrideSnapshots: []placementv1beta1.NamespacedName{
					{
						Name:      "ro1",
						Namespace: "ns1",
					},
					{
						Name:      "ro2",
						Namespace: "ns2",
					},
				},
			},
			wantEqual: false,
		},
		{
			name:                 "isBindingSyncedWithClusterStatus should return false if binding and cluster status have different clusterResourceOverrideSnapshot list",
			resourceSnapshotName: "test-1-snapshot",
			binding: &placementv1beta1.ClusterResourceBinding{
				Spec: placementv1beta1.ResourceBindingSpec{
					ResourceSnapshotName: "test-1-snapshot",
					ResourceOverrideSnapshots: []placementv1beta1.NamespacedName{
						{Name: "ro1", Namespace: "ns1"},
						{Name: "ro2", Namespace: "ns2"},
					},
					ClusterResourceOverrideSnapshots: []string{"cr1", "cr2"},
				},
			},
			cluster: &placementv1beta1.ClusterUpdatingStatus{
				ResourceOverrideSnapshots: []placementv1beta1.NamespacedName{
					{Name: "ro1", Namespace: "ns1"},
					{Name: "ro2", Namespace: "ns2"},
				},
				ClusterResourceOverrideSnapshots: []string{"cr1"},
			},
			wantEqual: false,
		},
		{
			name:                 "isBindingSyncedWithClusterStatus should return false if binding and updateRun have different applyStrategy",
			resourceSnapshotName: "test-1-snapshot",
			updateRun: &placementv1beta1.ClusterStagedUpdateRun{
				Status: placementv1beta1.UpdateRunStatus{
					ApplyStrategy: &placementv1beta1.ApplyStrategy{
						Type: placementv1beta1.ApplyStrategyTypeClientSideApply,
					},
				},
			},
			binding: &placementv1beta1.ClusterResourceBinding{
				Spec: placementv1beta1.ResourceBindingSpec{
					ResourceSnapshotName: "test-1-snapshot",
					ResourceOverrideSnapshots: []placementv1beta1.NamespacedName{
						{Name: "ro1", Namespace: "ns1"},
						{Name: "ro2", Namespace: "ns2"},
					},
					ClusterResourceOverrideSnapshots: []string{"cr1", "cr2"},
					ApplyStrategy: &placementv1beta1.ApplyStrategy{
						Type: placementv1beta1.ApplyStrategyTypeReportDiff,
					},
				},
			},
			cluster: &placementv1beta1.ClusterUpdatingStatus{
				ResourceOverrideSnapshots: []placementv1beta1.NamespacedName{
					{Name: "ro1", Namespace: "ns1"},
					{Name: "ro2", Namespace: "ns2"},
				},
				ClusterResourceOverrideSnapshots: []string{"cr1", "cr2"},
			},
			wantEqual: false,
		},
		{
			name:                 "isBindingSyncedWithClusterStatus should return true if resourceSnapshot, applyStrategy, and override lists are all deep equal",
			resourceSnapshotName: "test-1-snapshot",
			updateRun: &placementv1beta1.ClusterStagedUpdateRun{
				Status: placementv1beta1.UpdateRunStatus{
					ApplyStrategy: &placementv1beta1.ApplyStrategy{
						Type: placementv1beta1.ApplyStrategyTypeReportDiff,
					},
				},
			},
			binding: &placementv1beta1.ClusterResourceBinding{
				Spec: placementv1beta1.ResourceBindingSpec{
					ResourceSnapshotName: "test-1-snapshot",
					ResourceOverrideSnapshots: []placementv1beta1.NamespacedName{
						{Name: "ro1", Namespace: "ns1"},
						{Name: "ro2", Namespace: "ns2"},
					},
					ClusterResourceOverrideSnapshots: []string{"cr1", "cr2"},
					ApplyStrategy: &placementv1beta1.ApplyStrategy{
						Type: placementv1beta1.ApplyStrategyTypeReportDiff,
					},
				},
			},
			cluster: &placementv1beta1.ClusterUpdatingStatus{
				ResourceOverrideSnapshots: []placementv1beta1.NamespacedName{
					{Name: "ro1", Namespace: "ns1"},
					{Name: "ro2", Namespace: "ns2"},
				},
				ClusterResourceOverrideSnapshots: []string{"cr1", "cr2"},
			},
			wantEqual: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := isBindingSyncedWithClusterStatus(test.resourceSnapshotName, test.updateRun, test.binding, test.cluster)
			if got != test.wantEqual {
				t.Fatalf("isBindingSyncedWithClusterStatus() got %v; want %v", got, test.wantEqual)
			}
		})
	}
}

func TestCheckClusterUpdateResult(t *testing.T) {
	updatingStage := &placementv1beta1.StageUpdatingStatus{
		StageName: "test-stage",
	}
	updateRun := &placementv1beta1.ClusterStagedUpdateRun{
		ObjectMeta: metav1.ObjectMeta{
			Generation: 1,
		},
	}
	tests := []struct {
		name          string
		binding       *placementv1beta1.ClusterResourceBinding
		clusterStatus *placementv1beta1.ClusterUpdatingStatus
		wantSucceeded bool
		wantErr       bool
	}{
		{
			name: "checkClusterUpdateResult should return true if the binding has available condition",
			binding: &placementv1beta1.ClusterResourceBinding{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Status: placementv1beta1.ResourceBindingStatus{
					Conditions: []metav1.Condition{
						{
							Type:               string(placementv1beta1.ResourceBindingAvailable),
							Status:             metav1.ConditionTrue,
							ObservedGeneration: 1,
							Reason:             condition.AvailableReason,
						},
					},
				},
			},
			clusterStatus: &placementv1beta1.ClusterUpdatingStatus{ClusterName: "test-cluster"},
			wantSucceeded: true,
			wantErr:       false,
		},
		{
			name: "checkClusterUpdateResult should return false and error if the binding has false overridden condition",
			binding: &placementv1beta1.ClusterResourceBinding{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Status: placementv1beta1.ResourceBindingStatus{
					Conditions: []metav1.Condition{
						{
							Type:               string(placementv1beta1.ResourceBindingOverridden),
							Status:             metav1.ConditionFalse,
							ObservedGeneration: 1,
							Reason:             condition.OverriddenFailedReason,
						},
					},
				},
			},
			clusterStatus: &placementv1beta1.ClusterUpdatingStatus{ClusterName: "test-cluster"},
			wantSucceeded: false,
			wantErr:       true,
		},
		{
			name: "checkClusterUpdateResult should return false and error if the binding has false workSynchronized condition",
			binding: &placementv1beta1.ClusterResourceBinding{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Status: placementv1beta1.ResourceBindingStatus{
					Conditions: []metav1.Condition{
						{
							Type:               string(placementv1beta1.ResourceBindingWorkSynchronized),
							Status:             metav1.ConditionFalse,
							ObservedGeneration: 1,
							Reason:             condition.WorkNotSynchronizedYetReason,
						},
					},
				},
			},
			clusterStatus: &placementv1beta1.ClusterUpdatingStatus{ClusterName: "test-cluster"},
			wantSucceeded: false,
			wantErr:       true,
		},
		{
			name: "checkClusterUpdateResult should return false and error if the binding has false applied condition",
			binding: &placementv1beta1.ClusterResourceBinding{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Status: placementv1beta1.ResourceBindingStatus{
					Conditions: []metav1.Condition{
						{
							Type:               string(placementv1beta1.ResourceBindingApplied),
							Status:             metav1.ConditionFalse,
							ObservedGeneration: 1,
							Reason:             condition.ApplyFailedReason,
						},
					},
				},
			},
			clusterStatus: &placementv1beta1.ClusterUpdatingStatus{ClusterName: "test-cluster"},
			wantSucceeded: false,
			wantErr:       true,
		},
		{
			name: "checkClusterUpdateResult should return false but no error if the binding is not available yet",
			binding: &placementv1beta1.ClusterResourceBinding{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Status: placementv1beta1.ResourceBindingStatus{
					Conditions: []metav1.Condition{
						{
							Type:               string(placementv1beta1.ResourceBindingOverridden),
							Status:             metav1.ConditionTrue,
							ObservedGeneration: 1,
							Reason:             condition.OverriddenSucceededReason,
						},
						{
							Type:               string(placementv1beta1.ResourceBindingWorkSynchronized),
							Status:             metav1.ConditionTrue,
							ObservedGeneration: 1,
							Reason:             condition.WorkSynchronizedReason,
						},
						{
							Type:               string(placementv1beta1.ResourceBindingApplied),
							Status:             metav1.ConditionTrue,
							ObservedGeneration: 1,
							Reason:             condition.ApplySucceededReason,
						},
					},
				},
			},
			clusterStatus: &placementv1beta1.ClusterUpdatingStatus{ClusterName: "test-cluster"},
			wantSucceeded: false,
			wantErr:       false,
		},
		{
			name: "checkClusterUpdateResult should return false but no error if the binding does not have any conditions",
			binding: &placementv1beta1.ClusterResourceBinding{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Status: placementv1beta1.ResourceBindingStatus{
					Conditions: []metav1.Condition{},
				},
			},
			clusterStatus: &placementv1beta1.ClusterUpdatingStatus{ClusterName: "test-cluster"},
			wantSucceeded: false,
			wantErr:       false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotSucceeded, gotErr := checkClusterUpdateResult(test.binding, test.clusterStatus, updatingStage, updateRun)
			if gotSucceeded != test.wantSucceeded {
				t.Fatalf("checkClusterUpdateResult() got %v; want %v", gotSucceeded, test.wantSucceeded)
			}
			if (gotErr != nil) != test.wantErr {
				t.Fatalf("checkClusterUpdateResult() got error %v; want error %v", gotErr, test.wantErr)
			}
			if test.wantSucceeded {
				if !condition.IsConditionStatusTrue(meta.FindStatusCondition(test.clusterStatus.Conditions, string(placementv1beta1.ClusterUpdatingConditionSucceeded)), updateRun.Generation) {
					t.Fatalf("checkClusterUpdateResult() failed to set ClusterUpdatingConditionSucceeded condition")
				}
			}
		})
	}
}

func TestBuildApprovalRequestObject(t *testing.T) {
	tests := []struct {
		name           string
		namespacedName types.NamespacedName
		stageName      string
		updateRunName  string
		stageTaskType  string
		want           placementv1beta1.ApprovalRequestObj
	}{
		{
			name: "should create ClusterApprovalRequest when namespace is empty",
			namespacedName: types.NamespacedName{
				Name:      fmt.Sprintf(placementv1beta1.BeforeStageApprovalTaskNameFmt, "test-update-run", "test-stage"),
				Namespace: "",
			},
			stageName:     "test-stage",
			updateRunName: "test-update-run",
			stageTaskType: placementv1beta1.BeforeStageTaskLabelValue,
			want: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf(placementv1beta1.BeforeStageApprovalTaskNameFmt, "test-update-run", "test-stage"),
					Labels: map[string]string{
						placementv1beta1.TargetUpdatingStageNameLabel:   "test-stage",
						placementv1beta1.TargetUpdateRunLabel:           "test-update-run",
						placementv1beta1.TaskTypeLabel:                  placementv1beta1.BeforeStageTaskLabelValue,
						placementv1beta1.IsLatestUpdateRunApprovalLabel: "true",
					},
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test-update-run",
					TargetStage:     "test-stage",
				},
			},
		},
		{
			name: "should create namespaced ApprovalRequest when namespace is provided",
			namespacedName: types.NamespacedName{
				Name:      fmt.Sprintf(placementv1beta1.AfterStageApprovalTaskNameFmt, "test-update-run", "test-stage"),
				Namespace: testNamespaceName,
			},
			stageName:     "test-stage",
			updateRunName: "test-update-run",
			stageTaskType: placementv1beta1.AfterStageTaskLabelValue,
			want: &placementv1beta1.ApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf(placementv1beta1.AfterStageApprovalTaskNameFmt, "test-update-run", "test-stage"),
					Namespace: testNamespaceName,
					Labels: map[string]string{
						placementv1beta1.TargetUpdatingStageNameLabel:   "test-stage",
						placementv1beta1.TargetUpdateRunLabel:           "test-update-run",
						placementv1beta1.TaskTypeLabel:                  placementv1beta1.AfterStageTaskLabelValue,
						placementv1beta1.IsLatestUpdateRunApprovalLabel: "true",
					},
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test-update-run",
					TargetStage:     "test-stage",
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := buildApprovalRequestObject(test.namespacedName, test.stageName, test.updateRunName, test.stageTaskType)

			// Compare the whole objects using cmp.Diff with ignore options
			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("buildApprovalRequestObject() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TODO(arvindth): Add more test cases to cover aggregate error scenarios both positive and negative cases.
func TestExecuteUpdatingStage_Error(t *testing.T) {
	tests := []struct {
		name            string
		updateRun       *placementv1beta1.ClusterStagedUpdateRun
		bindings        []placementv1beta1.BindingObj
		interceptorFunc *interceptor.Funcs
		wantErr         error
		wantAbortErr    bool
		wantWaitTime    time.Duration
	}{
		{
			name: "cluster update failed",
			updateRun: &placementv1beta1.ClusterStagedUpdateRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-update-run",
					Generation: 1,
				},
				Spec: placementv1beta1.UpdateRunSpec{
					PlacementName:         "test-placement",
					ResourceSnapshotIndex: "1",
				},
				Status: placementv1beta1.UpdateRunStatus{
					StagesStatus: []placementv1beta1.StageUpdatingStatus{
						{
							StageName: "test-stage",
							Clusters: []placementv1beta1.ClusterUpdatingStatus{
								{
									ClusterName: "cluster-1",
									Conditions: []metav1.Condition{
										{
											Type:               string(placementv1beta1.ClusterUpdatingConditionSucceeded),
											Status:             metav1.ConditionFalse,
											ObservedGeneration: 1,
											Reason:             condition.ClusterUpdatingFailedReason,
											Message:            "cluster update failed",
										},
									},
								},
							},
						},
					},
					UpdateStrategySnapshot: &placementv1beta1.UpdateStrategySpec{
						Stages: []placementv1beta1.StageConfig{
							{
								Name:           "test-stage",
								MaxConcurrency: &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
							},
						},
					},
				},
			},
			bindings:        nil,
			interceptorFunc: nil,
			wantErr:         errors.New("the cluster `cluster-1` in the stage test-stage has failed"),
			wantAbortErr:    true,
			wantWaitTime:    0,
		},
		{
			name: "binding update failure",
			updateRun: &placementv1beta1.ClusterStagedUpdateRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-update-run",
					Generation: 1,
				},
				Spec: placementv1beta1.UpdateRunSpec{
					PlacementName:         "test-placement",
					ResourceSnapshotIndex: "1",
				},
				Status: placementv1beta1.UpdateRunStatus{
					StagesStatus: []placementv1beta1.StageUpdatingStatus{
						{
							StageName: "test-stage",
							Clusters: []placementv1beta1.ClusterUpdatingStatus{
								{
									ClusterName: "cluster-1",
								},
							},
						},
					},
					UpdateStrategySnapshot: &placementv1beta1.UpdateStrategySpec{
						Stages: []placementv1beta1.StageConfig{
							{
								Name:           "test-stage",
								MaxConcurrency: &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
							},
						},
					},
				},
			},
			bindings: []placementv1beta1.BindingObj{
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "binding-1",
						Generation: 1,
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster: "cluster-1",
						State:         placementv1beta1.BindingStateScheduled,
					},
				},
			},
			interceptorFunc: &interceptor.Funcs{
				Update: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
					return errors.New("simulated update error")
				},
			},
			wantErr:      errors.New("simulated update error"),
			wantWaitTime: 0,
		},
		{
			name: "missing binding in map lookup - nil pointer guard",
			updateRun: &placementv1beta1.ClusterStagedUpdateRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-update-run",
					Generation: 1,
				},
				Spec: placementv1beta1.UpdateRunSpec{
					PlacementName:         "test-placement",
					ResourceSnapshotIndex: "1",
				},
				Status: placementv1beta1.UpdateRunStatus{
					StagesStatus: []placementv1beta1.StageUpdatingStatus{
						{
							StageName: "test-stage",
							Clusters: []placementv1beta1.ClusterUpdatingStatus{
								{
									ClusterName: "cluster-1",
								},
							},
						},
					},
					UpdateStrategySnapshot: &placementv1beta1.UpdateStrategySpec{
						Stages: []placementv1beta1.StageConfig{
							{
								Name:           "test-stage",
								MaxConcurrency: &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
							},
						},
					},
				},
			},
			bindings:     nil, // No bindings provided, so cluster-1 will not be found in the map.
			wantErr:      errors.New("the binding for cluster `cluster-1` in stage `test-stage` is not found in the toBeUpdatedBindings map"),
			wantAbortErr: true,
			wantWaitTime: 0,
		},
		{
			name: "binding preemption",
			updateRun: &placementv1beta1.ClusterStagedUpdateRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-update-run",
					Generation: 1,
				},
				Spec: placementv1beta1.UpdateRunSpec{
					PlacementName:         "test-placement",
					ResourceSnapshotIndex: "1",
				},
				Status: placementv1beta1.UpdateRunStatus{
					StagesStatus: []placementv1beta1.StageUpdatingStatus{
						{
							StageName: "test-stage",
							Clusters: []placementv1beta1.ClusterUpdatingStatus{
								{
									ClusterName: "cluster-1",
									Conditions: []metav1.Condition{
										{
											Type:               string(placementv1beta1.ClusterUpdatingConditionStarted),
											Status:             metav1.ConditionTrue,
											ObservedGeneration: 1,
											Reason:             condition.ClusterUpdatingStartedReason,
										},
									},
								},
							},
						},
					},
					UpdateStrategySnapshot: &placementv1beta1.UpdateStrategySpec{
						Stages: []placementv1beta1.StageConfig{
							{
								Name:           "test-stage",
								MaxConcurrency: &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
							},
						},
					},
				},
			},
			bindings: []placementv1beta1.BindingObj{
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "binding-1",
						Generation: 1,
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster:        "cluster-1",
						ResourceSnapshotName: "wrong-snapshot",
						State:                placementv1beta1.BindingStateBound,
					},
					Status: placementv1beta1.ResourceBindingStatus{
						Conditions: []metav1.Condition{
							{
								Type:               string(placementv1beta1.ResourceBindingRolloutStarted),
								Status:             metav1.ConditionTrue,
								ObservedGeneration: 1,
							},
						},
					},
				},
			},
			interceptorFunc: nil,
			wantErr:         errors.New("the binding of the updating cluster `cluster-1` in the stage `test-stage` is not up-to-date with the desired status"),
			wantAbortErr:    true,
			wantWaitTime:    0,
		},
		{
			name: "binding synced but state not bound - update binding state fails",
			updateRun: &placementv1beta1.ClusterStagedUpdateRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-update-run",
					Generation: 1,
				},
				Spec: placementv1beta1.UpdateRunSpec{
					PlacementName:         "test-placement",
					ResourceSnapshotIndex: "1",
				},
				Status: placementv1beta1.UpdateRunStatus{
					ResourceSnapshotIndexUsed: "1",
					StagesStatus: []placementv1beta1.StageUpdatingStatus{
						{
							StageName: "test-stage",
							Clusters: []placementv1beta1.ClusterUpdatingStatus{
								{
									ClusterName: "cluster-1",
									// No conditions - cluster has not started updating yet.
								},
							},
						},
					},
					UpdateStrategySnapshot: &placementv1beta1.UpdateStrategySpec{
						Stages: []placementv1beta1.StageConfig{
							{
								Name:           "test-stage",
								MaxConcurrency: &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
							},
						},
					},
				},
			},
			bindings: []placementv1beta1.BindingObj{
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "binding-1",
						Generation: 1,
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster:        "cluster-1",
						ResourceSnapshotName: "test-placement-1-snapshot",            // Already synced.
						State:                placementv1beta1.BindingStateScheduled, // But not Bound yet.
					},
				},
			},
			interceptorFunc: &interceptor.Funcs{
				Update: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
					return errors.New("failed to update binding state")
				},
			},
			wantErr:      errors.New("failed to update binding state"),
			wantWaitTime: 0,
		},
		{
			name: "binding synced and bound but generation updated - update rolloutStarted fails",
			updateRun: &placementv1beta1.ClusterStagedUpdateRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-update-run",
					Generation: 1,
				},
				Spec: placementv1beta1.UpdateRunSpec{
					PlacementName:         "test-placement",
					ResourceSnapshotIndex: "1",
				},
				Status: placementv1beta1.UpdateRunStatus{
					ResourceSnapshotIndexUsed: "1",
					StagesStatus: []placementv1beta1.StageUpdatingStatus{
						{
							StageName: "test-stage",
							Clusters: []placementv1beta1.ClusterUpdatingStatus{
								{
									ClusterName: "cluster-1",
									// No conditions - cluster has not started updating yet.
								},
							},
						},
					},
					UpdateStrategySnapshot: &placementv1beta1.UpdateStrategySpec{
						Stages: []placementv1beta1.StageConfig{
							{
								Name:           "test-stage",
								MaxConcurrency: &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
							},
						},
					},
				},
			},
			bindings: []placementv1beta1.BindingObj{
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "binding-1",
						Generation: 2, // Generation updated by scheduler.
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster:        "cluster-1",
						ResourceSnapshotName: "test-placement-1-snapshot",        // Already synced.
						State:                placementv1beta1.BindingStateBound, // Already Bound.
					},
					Status: placementv1beta1.ResourceBindingStatus{
						Conditions: []metav1.Condition{
							{
								Type:               string(placementv1beta1.ResourceBindingRolloutStarted),
								Status:             metav1.ConditionTrue,
								ObservedGeneration: 1, // Old generation - needs update.
								Reason:             condition.RolloutStartedReason,
							},
						},
					},
				},
			},
			interceptorFunc: &interceptor.Funcs{
				SubResourceUpdate: func(ctx context.Context, client client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
					// Fail the status update for rolloutStarted.
					return errors.New("failed to update binding rolloutStarted status")
				},
			},
			wantErr:      errors.New("failed to update binding rolloutStarted status"),
			wantWaitTime: 0,
		},
		{
			name: "binding synced, bound, rolloutStarted true, but binding has failed condition",
			updateRun: &placementv1beta1.ClusterStagedUpdateRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-update-run",
					Generation: 1,
				},
				Spec: placementv1beta1.UpdateRunSpec{
					PlacementName:         "test-placement",
					ResourceSnapshotIndex: "1",
				},
				Status: placementv1beta1.UpdateRunStatus{
					ResourceSnapshotIndexUsed: "1",
					StagesStatus: []placementv1beta1.StageUpdatingStatus{
						{
							StageName: "test-stage",
							Clusters: []placementv1beta1.ClusterUpdatingStatus{
								{
									ClusterName: "cluster-1",
									// No conditions - cluster has not started updating yet.
								},
							},
						},
					},
					UpdateStrategySnapshot: &placementv1beta1.UpdateStrategySpec{
						Stages: []placementv1beta1.StageConfig{
							{
								Name:           "test-stage",
								MaxConcurrency: &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
							},
						},
					},
				},
			},
			bindings: []placementv1beta1.BindingObj{
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "binding-1",
						Generation: 1,
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster:        "cluster-1",
						ResourceSnapshotName: "test-placement-1-snapshot",        // Already synced.
						State:                placementv1beta1.BindingStateBound, // Already Bound.
					},
					Status: placementv1beta1.ResourceBindingStatus{
						Conditions: []metav1.Condition{
							{
								Type:               string(placementv1beta1.ResourceBindingRolloutStarted),
								Status:             metav1.ConditionTrue,
								ObservedGeneration: 1,
								Reason:             condition.RolloutStartedReason,
							},
							{
								Type:               string(placementv1beta1.ResourceBindingApplied),
								Status:             metav1.ConditionFalse,
								ObservedGeneration: 1,
								Reason:             condition.ApplyFailedReason,
							},
						},
					},
				},
			},
			interceptorFunc: nil,
			wantErr:         errors.New("cluster updating encountered an error at stage"),
			wantWaitTime:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			_ = placementv1beta1.AddToScheme(scheme)

			var fakeClient client.Client
			objs := make([]client.Object, len(tt.bindings))
			for i := range tt.bindings {
				objs[i] = tt.bindings[i]
			}
			if tt.interceptorFunc != nil {
				fakeClient = interceptor.NewClient(
					fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build(),
					*tt.interceptorFunc,
				)
			} else {
				fakeClient = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
			}

			r := &Reconciler{
				Client: fakeClient,
			}

			// Execute the stage.
			waitTime, gotErr := r.executeUpdatingStage(ctx, tt.updateRun, 0, tt.bindings, 1)

			// Verify error expectation.
			if (tt.wantErr != nil) != (gotErr != nil) {
				t.Fatalf("executeUpdatingStage() want error: %v, got error: %v", tt.wantErr, gotErr)
			}

			// Verify error message contains expected substring.
			if tt.wantErr != nil && gotErr != nil {
				if errors.Is(gotErr, errStagedUpdatedAborted) != tt.wantAbortErr {
					t.Fatalf("executeUpdatingStage() want abort error: %v, got error: %v", tt.wantAbortErr, gotErr)
				}
				if !strings.Contains(gotErr.Error(), tt.wantErr.Error()) {
					t.Fatalf("executeUpdatingStage() want error: %v, got error: %v", tt.wantErr, gotErr)
				}
			}

			// Verify wait time.
			if waitTime != tt.wantWaitTime {
				t.Fatalf("executeUpdatingStage() want waitTime: %v, got waitTime: %v", tt.wantWaitTime, waitTime)
			}
		})
	}
}

func TestExecuteUpdatingStage_ParallelWithinStage(t *testing.T) {
	tests := []struct {
		name                         string
		updateRun                    *placementv1beta1.ClusterStagedUpdateRun
		bindings                     []placementv1beta1.BindingObj
		maxConcurrency               int
		wantNewlyUpdatedBindings     []string // binding names that should be newly updated (from Scheduled to Bound)
		wantNewlyStartedClusterNames []string // cluster names that should be newly started in this round
		wantNotStartedClusterNames   []string // cluster names that should NOT be started due to maxConcurrency limit
		wantWaitTime                 time.Duration
		wantErr                      bool
	}{
		{
			name: "should update multiple clusters in parallel when maxConcurrency allows",
			updateRun: &placementv1beta1.ClusterStagedUpdateRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-update-run",
					Generation: 1,
				},
				Spec: placementv1beta1.UpdateRunSpec{
					PlacementName:         "test-placement",
					ResourceSnapshotIndex: "1",
				},
				Status: placementv1beta1.UpdateRunStatus{
					ResourceSnapshotIndexUsed: "1",
					StagesStatus: []placementv1beta1.StageUpdatingStatus{
						{
							StageName: "test-stage",
							Clusters: []placementv1beta1.ClusterUpdatingStatus{
								{ClusterName: "cluster-1"},
								{ClusterName: "cluster-2"},
								{ClusterName: "cluster-3"},
							},
						},
					},
					UpdateStrategySnapshot: &placementv1beta1.UpdateStrategySpec{
						Stages: []placementv1beta1.StageConfig{
							{
								Name:           "test-stage",
								MaxConcurrency: &intstr.IntOrString{Type: intstr.Int, IntVal: 3},
							},
						},
					},
				},
			},
			bindings: []placementv1beta1.BindingObj{
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "binding-1",
						Generation: 1,
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster:        "cluster-1",
						State:                placementv1beta1.BindingStateScheduled,
						ResourceSnapshotName: "test-placement-1-snapshot",
					},
				},
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "binding-2",
						Generation: 1,
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster:        "cluster-2",
						State:                placementv1beta1.BindingStateScheduled,
						ResourceSnapshotName: "test-placement-1-snapshot",
					},
				},
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "binding-3",
						Generation: 1,
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster:        "cluster-3",
						State:                placementv1beta1.BindingStateScheduled,
						ResourceSnapshotName: "test-placement-1-snapshot",
					},
				},
			},
			maxConcurrency:               3,
			wantNewlyUpdatedBindings:     []string{"binding-1", "binding-2", "binding-3"},
			wantNewlyStartedClusterNames: []string{"cluster-1", "cluster-2", "cluster-3"},
			wantNotStartedClusterNames:   []string{},
			wantWaitTime:                 clusterUpdatingWaitTime,
			wantErr:                      false,
		},
		{
			name: "should respect maxConcurrency limit when fewer than total clusters",
			updateRun: &placementv1beta1.ClusterStagedUpdateRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-update-run",
					Generation: 1,
				},
				Spec: placementv1beta1.UpdateRunSpec{
					PlacementName:         "test-placement",
					ResourceSnapshotIndex: "1",
				},
				Status: placementv1beta1.UpdateRunStatus{
					ResourceSnapshotIndexUsed: "1",
					StagesStatus: []placementv1beta1.StageUpdatingStatus{
						{
							StageName: "test-stage",
							Clusters: []placementv1beta1.ClusterUpdatingStatus{
								{ClusterName: "cluster-1"},
								{ClusterName: "cluster-2"},
								{ClusterName: "cluster-3"},
								{ClusterName: "cluster-4"},
								{ClusterName: "cluster-5"},
							},
						},
					},
					UpdateStrategySnapshot: &placementv1beta1.UpdateStrategySpec{
						Stages: []placementv1beta1.StageConfig{
							{
								Name:           "test-stage",
								MaxConcurrency: &intstr.IntOrString{Type: intstr.Int, IntVal: 2},
							},
						},
					},
				},
			},
			bindings: []placementv1beta1.BindingObj{
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "binding-1",
						Generation: 1,
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster:        "cluster-1",
						State:                placementv1beta1.BindingStateScheduled,
						ResourceSnapshotName: "test-placement-1-snapshot",
					},
				},
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "binding-2",
						Generation: 1,
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster:        "cluster-2",
						State:                placementv1beta1.BindingStateScheduled,
						ResourceSnapshotName: "test-placement-1-snapshot",
					},
				},
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "binding-3",
						Generation: 1,
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster:        "cluster-3",
						State:                placementv1beta1.BindingStateScheduled,
						ResourceSnapshotName: "test-placement-1-snapshot",
					},
				},
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "binding-4",
						Generation: 1,
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster:        "cluster-4",
						State:                placementv1beta1.BindingStateScheduled,
						ResourceSnapshotName: "test-placement-1-snapshot",
					},
				},
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "binding-5",
						Generation: 1,
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster:        "cluster-5",
						State:                placementv1beta1.BindingStateScheduled,
						ResourceSnapshotName: "test-placement-1-snapshot",
					},
				},
			},
			maxConcurrency:               2,
			wantNewlyUpdatedBindings:     []string{"binding-1", "binding-2"},
			wantNewlyStartedClusterNames: []string{"cluster-1", "cluster-2"},
			wantNotStartedClusterNames:   []string{"cluster-3", "cluster-4", "cluster-5"},
			wantWaitTime:                 clusterUpdatingWaitTime,
			wantErr:                      false,
		},
		{
			name: "should process next batch after previous clusters succeed",
			updateRun: &placementv1beta1.ClusterStagedUpdateRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-update-run",
					Generation: 1,
				},
				Spec: placementv1beta1.UpdateRunSpec{
					PlacementName:         "test-placement",
					ResourceSnapshotIndex: "1",
				},
				Status: placementv1beta1.UpdateRunStatus{
					ResourceSnapshotIndexUsed: "1",
					StagesStatus: []placementv1beta1.StageUpdatingStatus{
						{
							StageName: "test-stage",
							Clusters: []placementv1beta1.ClusterUpdatingStatus{
								{
									ClusterName: "cluster-1",
									Conditions: []metav1.Condition{
										{
											Type:               string(placementv1beta1.ClusterUpdatingConditionStarted),
											Status:             metav1.ConditionTrue,
											ObservedGeneration: 1,
											Reason:             condition.ClusterUpdatingStartedReason,
										},
										{
											Type:               string(placementv1beta1.ClusterUpdatingConditionSucceeded),
											Status:             metav1.ConditionTrue,
											ObservedGeneration: 1,
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
											Reason:             condition.ClusterUpdatingStartedReason,
										},
										{
											Type:               string(placementv1beta1.ClusterUpdatingConditionSucceeded),
											Status:             metav1.ConditionTrue,
											ObservedGeneration: 1,
											Reason:             condition.ClusterUpdatingSucceededReason,
										},
									},
								},
								{ClusterName: "cluster-3"},
								{ClusterName: "cluster-4"},
							},
						},
					},
					UpdateStrategySnapshot: &placementv1beta1.UpdateStrategySpec{
						Stages: []placementv1beta1.StageConfig{
							{
								Name:           "test-stage",
								MaxConcurrency: &intstr.IntOrString{Type: intstr.Int, IntVal: 2},
							},
						},
					},
				},
			},
			bindings: []placementv1beta1.BindingObj{
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "binding-1",
						Generation: 1,
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster:        "cluster-1",
						ResourceSnapshotName: "test-placement-1-snapshot",
						State:                placementv1beta1.BindingStateBound,
					},
					Status: placementv1beta1.ResourceBindingStatus{
						Conditions: []metav1.Condition{
							// Simulate an available binding (ignoring the rest of the conditions).
							{
								Type:               string(placementv1beta1.ResourceBindingAvailable),
								Status:             metav1.ConditionTrue,
								ObservedGeneration: 1,
							},
						},
					},
				},
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "binding-2",
						Generation: 1,
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster:        "cluster-2",
						ResourceSnapshotName: "test-placement-1-snapshot",
						State:                placementv1beta1.BindingStateBound,
					},
					Status: placementv1beta1.ResourceBindingStatus{
						Conditions: []metav1.Condition{
							// Simulate an available binding (ignoring the rest of the conditions).
							{
								Type:               string(placementv1beta1.ResourceBindingAvailable),
								Status:             metav1.ConditionTrue,
								ObservedGeneration: 1,
							},
						},
					},
				},
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "binding-3",
						Generation: 1,
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster:        "cluster-3",
						State:                placementv1beta1.BindingStateScheduled,
						ResourceSnapshotName: "test-placement-1-snapshot",
					},
				},
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "binding-4",
						Generation: 1,
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster:        "cluster-4",
						State:                placementv1beta1.BindingStateScheduled,
						ResourceSnapshotName: "test-placement-1-snapshot",
					},
				},
			},
			maxConcurrency:               2,
			wantNewlyUpdatedBindings:     []string{"binding-3", "binding-4"}, // cluster-1 and cluster-2 already succeeded, now cluster-3 and cluster-4 can start
			wantNewlyStartedClusterNames: []string{"cluster-3", "cluster-4"}, // only cluster-3 and cluster-4 are newly started (cluster-1 and cluster-2 were already started)
			wantNotStartedClusterNames:   []string{},
			wantWaitTime:                 clusterUpdatingWaitTime,
			wantErr:                      false,
		},
		{
			name: "should count in-progress clusters towards maxConcurrency limit",
			updateRun: &placementv1beta1.ClusterStagedUpdateRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-update-run",
					Generation: 1,
				},
				Spec: placementv1beta1.UpdateRunSpec{
					PlacementName:         "test-placement",
					ResourceSnapshotIndex: "1",
				},
				Status: placementv1beta1.UpdateRunStatus{
					ResourceSnapshotIndexUsed: "1",
					StagesStatus: []placementv1beta1.StageUpdatingStatus{
						{
							StageName: "test-stage",
							Clusters: []placementv1beta1.ClusterUpdatingStatus{
								{
									ClusterName: "cluster-1",
									Conditions: []metav1.Condition{
										{
											Type:               string(placementv1beta1.ClusterUpdatingConditionStarted),
											Status:             metav1.ConditionTrue,
											ObservedGeneration: 1,
											Reason:             condition.ClusterUpdatingStartedReason,
											LastTransitionTime: metav1.Now(),
										},
									},
								},
								{ClusterName: "cluster-2"},
								{ClusterName: "cluster-3"},
							},
						},
					},
					UpdateStrategySnapshot: &placementv1beta1.UpdateStrategySpec{
						Stages: []placementv1beta1.StageConfig{
							{
								Name:           "test-stage",
								MaxConcurrency: &intstr.IntOrString{Type: intstr.Int, IntVal: 2},
							},
						},
					},
				},
			},
			bindings: []placementv1beta1.BindingObj{
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "binding-1",
						Generation: 1,
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster:        "cluster-1",
						ResourceSnapshotName: "test-placement-1-snapshot",
						State:                placementv1beta1.BindingStateBound,
					},
					Status: placementv1beta1.ResourceBindingStatus{
						Conditions: []metav1.Condition{
							// Simulate an in-progress binding.
							{
								Type:               string(placementv1beta1.ResourceBindingRolloutStarted),
								Status:             metav1.ConditionTrue,
								ObservedGeneration: 1,
								Reason:             condition.RolloutStartedReason,
							},
						},
					},
				},
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "binding-2",
						Generation: 1,
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster:        "cluster-2",
						State:                placementv1beta1.BindingStateScheduled,
						ResourceSnapshotName: "test-placement-1-snapshot",
					},
				},
				&placementv1beta1.ClusterResourceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "binding-3",
						Generation: 1,
					},
					Spec: placementv1beta1.ResourceBindingSpec{
						TargetCluster:        "cluster-3",
						State:                placementv1beta1.BindingStateScheduled,
						ResourceSnapshotName: "test-placement-1-snapshot",
					},
				},
			},
			maxConcurrency:               2,
			wantNewlyUpdatedBindings:     []string{"binding-2"}, // Only cluster-2 should be updated (cluster-1 is in-progress, cluster-3 exceeds limit)
			wantNewlyStartedClusterNames: []string{"cluster-2"}, // Only cluster-2 is newly started (cluster-1 was already started)
			wantNotStartedClusterNames:   []string{"cluster-3"}, // cluster-3 exceeds maxConcurrency limit
			wantWaitTime:                 clusterUpdatingWaitTime,
			wantErr:                      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			_ = placementv1beta1.AddToScheme(scheme)

			// Track original states before execution.
			originalBindingStates := make(map[string]placementv1beta1.BindingState)
			for i := range tt.bindings {
				bindingSpec := tt.bindings[i].GetBindingSpec()
				originalBindingStates[tt.bindings[i].GetName()] = bindingSpec.State
			}
			originalClusterStarted := make(map[string]bool)
			for _, cluster := range tt.updateRun.Status.StagesStatus[0].Clusters {
				startedCond := meta.FindStatusCondition(cluster.Conditions, string(placementv1beta1.ClusterUpdatingConditionStarted))
				originalClusterStarted[cluster.ClusterName] = condition.IsConditionStatusTrue(startedCond, tt.updateRun.Generation)
			}

			// Set up fake client.
			objs := make([]client.Object, len(tt.bindings))
			for i := range tt.bindings {
				objs[i] = tt.bindings[i]
			}
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objs...).
				WithStatusSubresource(objs...).
				Build()

			r := &Reconciler{
				Client: fakeClient,
			}

			// Execute the stage.
			waitTime, gotErr := r.executeUpdatingStage(ctx, tt.updateRun, 0, tt.bindings, tt.maxConcurrency)

			// Verify error.
			if (gotErr != nil) != tt.wantErr {
				t.Fatalf("executeUpdatingStage() error = %v, wantErr %v", gotErr, tt.wantErr)
			}

			// Verify wait time.
			if waitTime != tt.wantWaitTime {
				t.Fatalf("executeUpdatingStage() waitTime = %v, want %v", waitTime, tt.wantWaitTime)
			}

			// Verify bindings: check which ones transitioned from Scheduled to Bound.
			verifyNewlyUpdatedBindings(t, ctx, fakeClient, tt.bindings, originalBindingStates, tt.wantNewlyUpdatedBindings)

			// Verify clusters: check which ones got Started condition added.
			verifyNewlyStartedClusters(t, tt.updateRun, originalClusterStarted, tt.wantNewlyStartedClusterNames)

			// Verify clusters that should NOT have Started condition.
			verifyNotStartedClusters(t, tt.updateRun, tt.wantNotStartedClusterNames)
		})
	}
}

// verifyNewlyUpdatedBindings checks that exactly the expected bindings transitioned from Scheduled to Bound.
func verifyNewlyUpdatedBindings(
	t *testing.T,
	ctx context.Context,
	fakeClient client.Client,
	bindings []placementv1beta1.BindingObj,
	originalStates map[string]placementv1beta1.BindingState,
	wantNewlyUpdated []string,
) {
	t.Helper()

	wantSet := make(map[string]bool)
	for _, name := range wantNewlyUpdated {
		wantSet[name] = true
	}

	for _, binding := range bindings {
		updatedBinding := &placementv1beta1.ClusterResourceBinding{}
		if err := fakeClient.Get(ctx, client.ObjectKeyFromObject(binding), updatedBinding); err != nil {
			t.Fatalf("Failed to get binding %s: %v", binding.GetName(), err)
		}

		name := binding.GetName()
		wasScheduled := originalStates[name] == placementv1beta1.BindingStateScheduled
		isNowBound := updatedBinding.Spec.State == placementv1beta1.BindingStateBound

		if wasScheduled && isNowBound {
			if !wantSet[name] {
				t.Errorf("executeUpdatingStage() unexpectedly updated binding %s to Bound", name)
			}
			delete(wantSet, name)
		}
	}

	for name := range wantSet {
		t.Errorf("executeUpdatingStage() did not update binding %s to Bound", name)
	}
}

// verifyNewlyStartedClusters checks that exactly the expected clusters got Started condition added.
func verifyNewlyStartedClusters(
	t *testing.T,
	updateRun *placementv1beta1.ClusterStagedUpdateRun,
	originalStarted map[string]bool,
	wantNewlyStarted []string,
) {
	t.Helper()

	wantSet := make(map[string]bool)
	for _, name := range wantNewlyStarted {
		wantSet[name] = true
	}

	for _, cluster := range updateRun.Status.StagesStatus[0].Clusters {
		startedCond := meta.FindStatusCondition(cluster.Conditions, string(placementv1beta1.ClusterUpdatingConditionStarted))
		isStartedNow := condition.IsConditionStatusTrue(startedCond, updateRun.Generation)
		wasStartedBefore := originalStarted[cluster.ClusterName]

		if isStartedNow && !wasStartedBefore {
			// Cluster was newly started.
			if !wantSet[cluster.ClusterName] {
				t.Errorf("executeUpdatingStage() unexpectedly started cluster %s", cluster.ClusterName)
			}
			delete(wantSet, cluster.ClusterName)
		}
	}

	for name := range wantSet {
		t.Errorf("executeUpdatingStage() did not start cluster %s", name)
	}
}

// verifyNotStartedClusters checks that the specified clusters do NOT have Started condition.
func verifyNotStartedClusters(
	t *testing.T,
	updateRun *placementv1beta1.ClusterStagedUpdateRun,
	wantNotStarted []string,
) {
	t.Helper()

	notStartedSet := make(map[string]bool)
	for _, name := range wantNotStarted {
		notStartedSet[name] = true
	}

	for _, cluster := range updateRun.Status.StagesStatus[0].Clusters {
		if !notStartedSet[cluster.ClusterName] {
			continue
		}
		startedCond := meta.FindStatusCondition(cluster.Conditions, string(placementv1beta1.ClusterUpdatingConditionStarted))
		if condition.IsConditionStatusTrue(startedCond, updateRun.Generation) {
			t.Errorf("executeUpdatingStage() started cluster %s, want not started", cluster.ClusterName)
		}
	}
}

func TestCalculateMaxConcurrencyValue(t *testing.T) {
	tests := []struct {
		name           string
		maxConcurrency *intstr.IntOrString
		clusterCount   int
		wantValue      int
		wantErr        bool
	}{
		{
			name:           "integer value - less than cluster count",
			maxConcurrency: &intstr.IntOrString{Type: intstr.Int, IntVal: 3},
			clusterCount:   10,
			wantValue:      3,
			wantErr:        false,
		},
		{
			name:           "integer value - equal to cluster count",
			maxConcurrency: &intstr.IntOrString{Type: intstr.Int, IntVal: 10},
			clusterCount:   10,
			wantValue:      10,
			wantErr:        false,
		},
		{
			name:           "integer value - greater than cluster count",
			maxConcurrency: &intstr.IntOrString{Type: intstr.Int, IntVal: 15},
			clusterCount:   10,
			wantValue:      15,
			wantErr:        false,
		},
		{
			name:           "percentage value - 50% with cluster count > 1",
			maxConcurrency: &intstr.IntOrString{Type: intstr.String, StrVal: "50%"},
			clusterCount:   10,
			wantValue:      5,
			wantErr:        false,
		},
		{
			name:           "percentage value - non zero percentage with cluster count equal to 1",
			maxConcurrency: &intstr.IntOrString{Type: intstr.String, StrVal: "10%"},
			clusterCount:   1,
			wantValue:      1,
			wantErr:        false,
		},
		{
			name:           "percentage value - 33% rounds down",
			maxConcurrency: &intstr.IntOrString{Type: intstr.String, StrVal: "33%"},
			clusterCount:   10,
			wantValue:      3,
			wantErr:        false,
		},
		{
			name:           "percentage value - 100%",
			maxConcurrency: &intstr.IntOrString{Type: intstr.String, StrVal: "100%"},
			clusterCount:   10,
			wantValue:      10,
			wantErr:        false,
		},
		{
			name:           "percentage value - 25% with 7 clusters",
			maxConcurrency: &intstr.IntOrString{Type: intstr.String, StrVal: "25%"},
			clusterCount:   7,
			wantValue:      1,
			wantErr:        false,
		},
		{
			name:           "zero clusters",
			maxConcurrency: &intstr.IntOrString{Type: intstr.Int, IntVal: 3},
			clusterCount:   0,
			wantValue:      3,
			wantErr:        false,
		},
		{
			name:           "non-zero percentage with zero clusters",
			maxConcurrency: &intstr.IntOrString{Type: intstr.String, StrVal: "50%"},
			clusterCount:   0,
			wantValue:      1,
			wantErr:        false,
		},
		{
			name:           "non-zero value as string without percentage with zero clusters",
			maxConcurrency: &intstr.IntOrString{Type: intstr.String, StrVal: "50"},
			clusterCount:   0,
			wantValue:      0,
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := &placementv1beta1.UpdateRunStatus{
				StagesStatus: []placementv1beta1.StageUpdatingStatus{
					{
						StageName: "test-stage",
						Clusters:  make([]placementv1beta1.ClusterUpdatingStatus, tt.clusterCount),
					},
				},
				UpdateStrategySnapshot: &placementv1beta1.UpdateStrategySpec{
					Stages: []placementv1beta1.StageConfig{
						{
							Name:           "test-stage",
							MaxConcurrency: tt.maxConcurrency,
						},
					},
				},
			}

			gotValue, gotErr := calculateMaxConcurrencyValue(status, 0)

			if (gotErr != nil) != tt.wantErr {
				t.Fatalf("calculateMaxConcurrencyValue() error = %v, wantErr %v", gotErr, tt.wantErr)
			}

			if gotValue != tt.wantValue {
				t.Fatalf("calculateMaxConcurrencyValue() = %v, want %v", gotValue, tt.wantValue)
			}
		})
	}
}

func TestCheckBeforeStageTasksStatus_NegativeCases(t *testing.T) {
	stageName := "stage-0"
	testUpdateRunName = "test-update-run"
	approvalRequestName := fmt.Sprintf(placementv1beta1.BeforeStageApprovalTaskNameFmt, testUpdateRunName, stageName)
	tests := []struct {
		name            string
		stageIndex      int
		updateRun       *placementv1beta1.ClusterStagedUpdateRun
		approvalRequest *placementv1beta1.ClusterApprovalRequest
		wantErrMsg      string
		wantErrAborted  bool
	}{
		// Negative test cases only
		{
			name:       "should return err if before stage task is TimedWait",
			stageIndex: 0,
			updateRun: &placementv1beta1.ClusterStagedUpdateRun{
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
					StagesStatus: []placementv1beta1.StageUpdatingStatus{
						{
							StageName: stageName,
							BeforeStageTaskStatus: []placementv1beta1.StageTaskStatus{
								{
									Type: placementv1beta1.StageTaskTypeTimedWait,
								},
							},
						},
					},
				},
			},
			wantErrMsg:     fmt.Sprintf("found unsupported task type in before stage tasks: %s", placementv1beta1.StageTaskTypeTimedWait),
			wantErrAborted: true,
		},
		{
			name:       "should return err if Approval request has wrong target stage in spec",
			stageIndex: 0,
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
								},
							},
						},
					},
					StagesStatus: []placementv1beta1.StageUpdatingStatus{
						{
							StageName: stageName,
							BeforeStageTaskStatus: []placementv1beta1.StageTaskStatus{
								{
									Type:                placementv1beta1.StageTaskTypeApproval,
									ApprovalRequestName: fmt.Sprintf(placementv1beta1.BeforeStageApprovalTaskNameFmt, testUpdateRunName, stageName),
									Conditions: []metav1.Condition{
										{
											Type:   string(placementv1beta1.StageTaskConditionApprovalRequestCreated),
											Status: metav1.ConditionTrue,
										},
									},
								},
							},
						},
					},
				},
			},
			approvalRequest: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Name: approvalRequestName,
					Labels: map[string]string{
						placementv1beta1.TargetUpdatingStageNameLabel:   stageName,
						placementv1beta1.TargetUpdateRunLabel:           testUpdateRunName,
						placementv1beta1.TaskTypeLabel:                  placementv1beta1.BeforeStageTaskLabelValue,
						placementv1beta1.IsLatestUpdateRunApprovalLabel: "true",
					},
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: testUpdateRunName,
					TargetStage:     "stage-1",
				},
			},
			wantErrMsg:     fmt.Sprintf("the approval request task `/%s` is targeting update run `/%s` and stage `stage-1`", approvalRequestName, testUpdateRunName),
			wantErrAborted: true,
		},
		{
			name:       "should return err if Approval request has wrong target update run in spec",
			stageIndex: 0,
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
								},
							},
						},
					},
					StagesStatus: []placementv1beta1.StageUpdatingStatus{
						{
							StageName: stageName,
							BeforeStageTaskStatus: []placementv1beta1.StageTaskStatus{
								{
									Type:                placementv1beta1.StageTaskTypeApproval,
									ApprovalRequestName: fmt.Sprintf(placementv1beta1.BeforeStageApprovalTaskNameFmt, testUpdateRunName, stageName),
									Conditions: []metav1.Condition{
										{
											Type:   string(placementv1beta1.StageTaskConditionApprovalRequestCreated),
											Status: metav1.ConditionTrue,
										},
									},
								},
							},
						},
					},
				},
			},
			approvalRequest: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf(placementv1beta1.BeforeStageApprovalTaskNameFmt, testUpdateRunName, stageName),
					Labels: map[string]string{
						placementv1beta1.TargetUpdatingStageNameLabel:   stageName,
						placementv1beta1.TargetUpdateRunLabel:           testUpdateRunName,
						placementv1beta1.TaskTypeLabel:                  placementv1beta1.BeforeStageTaskLabelValue,
						placementv1beta1.IsLatestUpdateRunApprovalLabel: "true",
					},
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "wrong-update-run",
					TargetStage:     stageName,
				},
			},
			wantErrMsg:     fmt.Sprintf("the approval request task `/%s` is targeting update run `/wrong-update-run` and stage `%s`", approvalRequestName, stageName),
			wantErrAborted: true,
		},
		{
			name:       "should return err if cannot update Approval request that is approved as accepted",
			stageIndex: 0,
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
								},
							},
						},
					},
					StagesStatus: []placementv1beta1.StageUpdatingStatus{
						{
							StageName: stageName,
							BeforeStageTaskStatus: []placementv1beta1.StageTaskStatus{
								{
									Type:                placementv1beta1.StageTaskTypeApproval,
									ApprovalRequestName: fmt.Sprintf(placementv1beta1.BeforeStageApprovalTaskNameFmt, testUpdateRunName, stageName),
									Conditions: []metav1.Condition{
										{
											Type:   string(placementv1beta1.StageTaskConditionApprovalRequestCreated),
											Status: metav1.ConditionTrue,
										},
									},
								},
							},
						},
					},
				},
			},
			approvalRequest: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf(placementv1beta1.BeforeStageApprovalTaskNameFmt, testUpdateRunName, stageName),
					Labels: map[string]string{
						placementv1beta1.TargetUpdatingStageNameLabel:   stageName,
						placementv1beta1.TargetUpdateRunLabel:           testUpdateRunName,
						placementv1beta1.TaskTypeLabel:                  placementv1beta1.BeforeStageTaskLabelValue,
						placementv1beta1.IsLatestUpdateRunApprovalLabel: "true",
					},
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: testUpdateRunName,
					TargetStage:     stageName,
				},
				Status: placementv1beta1.ApprovalRequestStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(placementv1beta1.ApprovalRequestConditionApproved),
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
			wantErrMsg: fmt.Sprintf("error returned by the API server: clusterapprovalrequests.placement.kubernetes-fleet.io \"%s\" not found", approvalRequestName),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []client.Object{tt.updateRun}
			if tt.approvalRequest != nil {
				objects = append(objects, tt.approvalRequest)
			}
			objectsWithStatus := []client.Object{tt.updateRun}
			scheme := runtime.NewScheme()
			_ = placementv1beta1.AddToScheme(scheme)
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				WithStatusSubresource(objectsWithStatus...).
				Build()
			r := Reconciler{
				Client: fakeClient,
			}
			ctx := context.Background()
			_, gotErr := r.checkBeforeStageTasksStatus(ctx, tt.stageIndex, tt.updateRun)
			if gotErr == nil {
				t.Fatalf("checkBeforeStageTasksStatus() want error but got nil")
			}
			if !strings.Contains(gotErr.Error(), tt.wantErrMsg) {
				t.Fatalf("checkBeforeStageTasksStatus() error = %v, wantErr %v", gotErr, tt.wantErrMsg)
			}
			if tt.wantErrAborted && !errors.Is(gotErr, errStagedUpdatedAborted) {
				t.Fatalf("checkBeforeStageTasksStatus() want aborted error but got different error: %v", gotErr)
			}
		})
	}
}

func TestGenerateStuckClustersString(t *testing.T) {
	tests := []struct {
		name              string
		stuckClusterNames []string
		wantClusterString string
	}{
		{
			name:              "empty cluster list",
			stuckClusterNames: []string{},
			wantClusterString: "",
		},
		{
			name:              "single cluster",
			stuckClusterNames: []string{"cluster1"},
			wantClusterString: "cluster1",
		},
		{
			name:              "two clusters",
			stuckClusterNames: []string{"cluster1", "cluster2"},
			wantClusterString: "cluster1, cluster2",
		},
		{
			name:              "three clusters",
			stuckClusterNames: []string{"cluster1", "cluster2", "cluster3"},
			wantClusterString: "cluster1, cluster2, cluster3",
		},
		{
			name:              "five clusters - should only show first three",
			stuckClusterNames: []string{"cluster1", "cluster2", "cluster3", "cluster4", "cluster5"},
			wantClusterString: "cluster1, cluster2, cluster3...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateStuckClustersString(tt.stuckClusterNames)

			if got != tt.wantClusterString {
				t.Fatalf("generateStuckClustersString() = %v, want %v", got, tt.wantClusterString)
			}
		})
	}
}
