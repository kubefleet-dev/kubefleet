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
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	hubmetrics "github.com/kubefleet-dev/kubefleet/pkg/metrics/hub"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/condition"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/controller"
)

func TestDetermineFailureType(t *testing.T) {
	tests := []struct {
		name       string
		cond       *metav1.Condition
		generation int64
		err        error
		want       hubmetrics.UpdateRunFailureType
	}{
		{
			name: "update run succeeded - no failure",
			cond: &metav1.Condition{
				Type:               string(placementv1beta1.StagedUpdateRunConditionSucceeded),
				Status:             metav1.ConditionTrue,
				ObservedGeneration: 1,
				Reason:             condition.UpdateRunSucceededReason,
			},
			generation: 1,
			err:        nil,
			want:       hubmetrics.UpdateRunFailureTypeNone,
		},
		{
			name: "update run is stopping - no failure",
			cond: &metav1.Condition{
				Type:               string(placementv1beta1.StagedUpdateRunConditionProgressing),
				Status:             metav1.ConditionUnknown,
				ObservedGeneration: 1,
				Reason:             condition.UpdateRunStoppingReason,
			},
			generation: 1,
			err:        nil,
			want:       hubmetrics.UpdateRunFailureTypeNone,
		},
		{
			name: "update run failed with user error",
			cond: &metav1.Condition{
				Type:               string(placementv1beta1.StagedUpdateRunConditionSucceeded),
				Status:             metav1.ConditionFalse,
				ObservedGeneration: 1,
				Reason:             condition.UpdateRunFailedReason,
			},
			generation: 1,
			err:        fmt.Errorf("cannot continue the updateRun: failed to validate the updateRun: %w", controller.ErrUserError),
			want:       hubmetrics.UpdateRunFailureTypeUserError,
		},
		{
			name: "update run failed with internal error",
			cond: &metav1.Condition{
				Type:               string(placementv1beta1.StagedUpdateRunConditionSucceeded),
				Status:             metav1.ConditionFalse,
				ObservedGeneration: 1,
				Reason:             condition.UpdateRunFailedReason,
			},
			generation: 1,
			err:        errors.New("cannot continue the updateRun"),
			want:       hubmetrics.UpdateRunFailureTypeInternalError,
		},
		{
			name: "update run is waiting for stage task - no error",
			cond: &metav1.Condition{
				Type:               string(placementv1beta1.StagedUpdateRunConditionProgressing),
				Status:             metav1.ConditionFalse,
				ObservedGeneration: 1,
				Reason:             condition.UpdateRunWaitingReason,
			},
			generation: 1,
			err:        nil,
			want:       hubmetrics.UpdateRunFailureTypeNone,
		},
		{
			name: "update run is stuck - internal error",
			cond: &metav1.Condition{
				Type:               string(placementv1beta1.StagedUpdateRunConditionProgressing),
				Status:             metav1.ConditionFalse,
				ObservedGeneration: 1,
				Reason:             condition.UpdateRunStuckReason,
			},
			generation: 1,
			err:        errors.New("updateRun is stuck waiting for 1 cluster(s) in stage stage1 to finish updating, please check placement status for potential errors"),
			want:       hubmetrics.UpdateRunFailureTypeInternalError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := determineFailureType(tc.cond, tc.generation, tc.err)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("determineFailureType() = %v, want %v, diff (-want +got):\n%s", got, tc.want, diff)
			}
		})
	}
}
