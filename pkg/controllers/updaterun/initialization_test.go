package updaterun

import (
	"context"
	"testing"
	"time"

	"github.com/crossplane/crossplane-runtime/pkg/test"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

func TestRemoveWaitTimeFromAfterStageTasks(t *testing.T) {
	waitTime := &metav1.Duration{Duration: 5 * time.Minute}
	tests := map[string]struct {
		strategy  *placementv1beta1.ClusterStagedUpdateStrategy
		updateErr error
		wantErr   bool
	}{
		"should handle empty stages": {
			strategy: &placementv1beta1.ClusterStagedUpdateStrategy{
				Spec: placementv1beta1.StagedUpdateStrategySpec{
					Stages: []placementv1beta1.StageConfig{},
				},
			},
			wantErr: false,
		},
		"should remove waitTime from Approval tasks": {
			strategy: &placementv1beta1.ClusterStagedUpdateStrategy{
				Spec: placementv1beta1.StagedUpdateStrategySpec{
					Stages: []placementv1beta1.StageConfig{
						{
							AfterStageTasks: []placementv1beta1.AfterStageTask{
								{
									Type:     placementv1beta1.AfterStageTaskTypeApproval,
									WaitTime: waitTime,
								},
								{
									Type:     placementv1beta1.AfterStageTaskTypeTimedWait,
									WaitTime: waitTime,
								},
							},
						},
					},
				},
			},
			wantErr: false,
		},
		"should handle multiple stages": {
			strategy: &placementv1beta1.ClusterStagedUpdateStrategy{
				Spec: placementv1beta1.StagedUpdateStrategySpec{
					Stages: []placementv1beta1.StageConfig{
						{
							AfterStageTasks: []placementv1beta1.AfterStageTask{
								{
									Type:     placementv1beta1.AfterStageTaskTypeApproval,
									WaitTime: waitTime,
								},
							},
						},
						{
							AfterStageTasks: []placementv1beta1.AfterStageTask{
								{
									Type:     placementv1beta1.AfterStageTaskTypeTimedWait,
									WaitTime: waitTime,
								},
								{
									Type:     placementv1beta1.AfterStageTaskTypeApproval,
									WaitTime: waitTime,
								},
							},
						},
					},
				},
			},
			wantErr: false,
		},
		"should handle update error": {
			strategy: &placementv1beta1.ClusterStagedUpdateStrategy{
				Spec: placementv1beta1.StagedUpdateStrategySpec{
					Stages: []placementv1beta1.StageConfig{
						{
							AfterStageTasks: []placementv1beta1.AfterStageTask{
								{
									Type:     placementv1beta1.AfterStageTaskTypeApproval,
									WaitTime: waitTime,
								},
							},
						},
					},
				},
			},
			updateErr: context.DeadlineExceeded,
			wantErr:   true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			r := &Reconciler{
				Client: &test.MockClient{
					MockUpdate: func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
						if tt.updateErr != nil {
							return tt.updateErr
						}
						strategy := obj.(*placementv1beta1.ClusterStagedUpdateStrategy)
						for _, stage := range strategy.Spec.Stages {
							for _, task := range stage.AfterStageTasks {
								if task.Type == placementv1beta1.AfterStageTaskTypeApproval && task.WaitTime != nil {
									t.Errorf("waitTime should be nil for Approval tasks, got %v", task.WaitTime)
								}
								if task.Type == placementv1beta1.AfterStageTaskTypeTimedWait && task.WaitTime == nil {
									t.Error("waitTime should not be nil for TimedWait tasks")
								}
							}
						}
						return nil
					},
				},
			}

			err := r.removeWaitTimeFromAfterStageTasks(context.Background(), tt.strategy)
			if (err != nil) != tt.wantErr {
				t.Errorf("removeWaitTimeFromAfterStageTasks() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
