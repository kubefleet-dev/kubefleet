/*
Copyright 2026 The KubeFleet Authors.

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

package placementpolicy

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	hubmetrics "github.com/kubefleet-dev/kubefleet/pkg/metrics/hub"
)

// TestReportPolicyMetrics pins the per-state gauge: every state is published, to zero when empty,
// and the sum over states is the outstanding count.
func TestReportPolicyMetrics(t *testing.T) {
	policy := &kfplacementv1alpha1.PlacementPolicy{ObjectMeta: metav1.ObjectMeta{Name: "metrics-pp", Namespace: testLifecycleNamespace}}
	report := &claimReport{outstanding: 3, states: map[string]int{claimStatePending: 1, claimStateTerminal: 2}}
	reportPolicyMetrics(policy, report, metav1.Condition{Type: kfplacementv1alpha1.PlacementPolicyCondTypeScheduled})
	sum := 0.0
	for _, state := range claimStates {
		got := testutil.ToFloat64(hubmetrics.FleetPlacementPolicyActiveClusterClaims.WithLabelValues(testLifecycleNamespace, "metrics-pp", state))
		if want := float64(report.states[state]); got != want {
			t.Errorf("active claims gauge state=%s = %v, want %v", state, got, want)
		}
		sum += got
	}
	if sum != float64(report.outstanding) {
		t.Errorf("active claims gauge sums to %v over states, want the outstanding count %d", sum, report.outstanding)
	}
	forgetPolicyMetrics(testLifecycleNamespace, "metrics-pp")
}
