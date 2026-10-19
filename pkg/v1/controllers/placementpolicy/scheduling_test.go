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
	"time"

	"github.com/google/go-cmp/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/propertyprovider"
)

// allEligible is a test eligibility gate that admits every cluster.
type allEligible struct{}

func (allEligible) IsEligible(*clusterv1beta1.MemberCluster) (bool, string) {
	return true, ""
}

func (allEligible) HeartbeatTimeout() time.Duration {
	return 5 * time.Minute
}

func (allEligible) HealthCheckTimeout() time.Duration {
	return 5 * time.Minute
}

func TestEvaluateSelectorsSkipsClustersWithUnevaluableData(t *testing.T) {
	spec := &kfplacementv1alpha1.PlacementPolicySpec{
		ClusterSelectors: []kfplacementv1alpha1.ClusterSelector{{
			Terms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{{
				MatchClusterPropertyExpressions: []kfplacementv1alpha1.LabelClusterPropertyExpression{
					{Key: propertyprovider.NodeCountProperty, Operator: kfplacementv1alpha1.LabelClusterPropertyExpressionOperatorGe, Values: []string{"2"}},
				},
			}},
			Count: ptr.To(intstr.FromInt32(1)),
		}},
	}
	clusters := []clusterv1beta1.MemberCluster{
		*testCluster("good-cluster", nil, map[string]string{propertyprovider.NodeCountProperty: "4"}, nil),
		*testCluster("bad-data-cluster", nil, map[string]string{propertyprovider.NodeCountProperty: "not-a-number"}, nil),
	}

	outcomes, err := evaluateSelectors(spec, clusters, allEligible{})
	if err != nil {
		t.Fatalf("evaluateSelectors(%v) returned unexpected error: %v", spec, err)
	}
	if len(outcomes) != 1 {
		t.Fatalf("evaluateSelectors(%v) returned %d outcomes, want 1", spec, len(outcomes))
	}
	wantMatched := []string{"good-cluster"}
	if diff := cmp.Diff(outcomes[0].matched, wantMatched); diff != "" {
		t.Errorf("evaluateSelectors(%v) matched mismatch (-got, +want):\n%s", spec, diff)
	}
}

func TestAggregateCounts(t *testing.T) {
	testCases := []struct {
		name          string
		outcomes      []selectorOutcome
		wantDesired   int32
		wantScheduled int32
	}{
		{
			name: "integer counts sum per selector",
			outcomes: []selectorOutcome{
				{counts: resolvedCounts{desired: 2, minimum: 2}, matched: []string{"a", "b", "c"}},
				{counts: resolvedCounts{desired: 3, minimum: 3}, matched: []string{"a"}},
			},
			wantDesired:   5,
			wantScheduled: 3,
		},
		{
			name: "selectAll desired floors at the minimum when unfulfilled",
			outcomes: []selectorOutcome{
				{counts: resolvedCounts{selectAll: true, minimum: 2}, matched: []string{"a"}},
			},
			wantDesired:   2,
			wantScheduled: 1,
		},
		{
			name: "selectAll desired follows the match count when fulfilled",
			outcomes: []selectorOutcome{
				{counts: resolvedCounts{selectAll: true, minimum: 1}, matched: []string{"a", "b", "c"}},
			},
			wantDesired:   3,
			wantScheduled: 3,
		},
		{
			name: "empty fleet with a bare selectAll selector shows the gap",
			outcomes: []selectorOutcome{
				{counts: resolvedCounts{selectAll: true, minimum: 1}},
			},
			wantDesired:   1,
			wantScheduled: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			desired, scheduled := aggregateCounts(tc.outcomes)
			if desired != tc.wantDesired || scheduled != tc.wantScheduled {
				t.Errorf("aggregateCounts(%v) = %d, %d, want %d, %d", tc.outcomes, desired, scheduled, tc.wantDesired, tc.wantScheduled)
			}
		})
	}
}

func TestScheduledCondition(t *testing.T) {
	testCases := []struct {
		name       string
		outcomes   []selectorOutcome
		wantStatus metav1.ConditionStatus
		wantReason string
	}{
		{
			name: "all selectors fulfilled in full",
			outcomes: []selectorOutcome{
				{counts: resolvedCounts{desired: 1, minimum: 1}, matched: []string{"a"}},
			},
			wantStatus: metav1.ConditionTrue,
			wantReason: kfplacementv1alpha1.PlacementPolicyScheduledCondReasonFoundAllClusters,
		},
		{
			name: "floor reached but short of desired stays False per the binary contract",
			outcomes: []selectorOutcome{
				{counts: resolvedCounts{desired: 3, minimum: 1}, matched: []string{"a"}},
			},
			wantStatus: metav1.ConditionFalse,
			wantReason: kfplacementv1alpha1.PlacementPolicyScheduledCondReasonFailedToFindSomeClusters,
		},
		{
			name: "below the floor",
			outcomes: []selectorOutcome{
				{counts: resolvedCounts{desired: 3, minimum: 2}, matched: []string{"a"}},
			},
			wantStatus: metav1.ConditionFalse,
			wantReason: kfplacementv1alpha1.PlacementPolicyScheduledCondReasonFailedToFindSomeClusters,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := scheduledCondition(7, tc.outcomes)
			if got.Status != tc.wantStatus || got.Reason != tc.wantReason {
				t.Errorf("scheduledCondition(7, %v) = %s/%s, want %s/%s", tc.outcomes, got.Status, got.Reason, tc.wantStatus, tc.wantReason)
			}
			if got.ObservedGeneration != 7 {
				t.Errorf("scheduledCondition(7, %v) observedGeneration = %d, want 7", tc.outcomes, got.ObservedGeneration)
			}
		})
	}
}

func TestTolerationTolerates(t *testing.T) {
	noScheduleTaint := clusterv1beta1.Taint{Key: "workload", Value: "restricted", Effect: "NoSchedule"}

	testCases := []struct {
		name       string
		toleration kfplacementv1alpha1.Toleration
		want       bool
	}{
		{
			name:       "equal operator with matching value",
			toleration: kfplacementv1alpha1.Toleration{Key: "workload", Operator: "Equal", Value: "restricted", Effect: "NoSchedule"},
			want:       true,
		},
		{
			name:       "equal operator with mismatched value",
			toleration: kfplacementv1alpha1.Toleration{Key: "workload", Operator: "Equal", Value: "other", Effect: "NoSchedule"},
			want:       false,
		},
		{
			name:       "exists operator ignores the value",
			toleration: kfplacementv1alpha1.Toleration{Key: "workload", Operator: "Exists", Effect: "NoSchedule"},
			want:       true,
		},
		{
			name:       "empty key with exists matches every taint",
			toleration: kfplacementv1alpha1.Toleration{Operator: "Exists"},
			want:       true,
		},
		{
			name:       "empty effect matches every effect",
			toleration: kfplacementv1alpha1.Toleration{Key: "workload", Operator: "Equal", Value: "restricted"},
			want:       true,
		},
		{
			name:       "unrecognized operator tolerates nothing",
			toleration: kfplacementv1alpha1.Toleration{Key: "workload", Operator: "Fuzzy", Value: "restricted", Effect: "NoSchedule"},
			want:       false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := tolerationTolerates(&noScheduleTaint, &tc.toleration)
			if got != tc.want {
				t.Errorf("tolerationTolerates(%v, %v) = %t, want %t", noScheduleTaint, tc.toleration, got, tc.want)
			}
		})
	}
}

func clusterWithAgentStatus(name string, heartbeat metav1.Time, healthyStatus metav1.ConditionStatus, healthyTransition metav1.Time) clusterv1beta1.MemberCluster {
	if healthyStatus == "" {
		healthyStatus = metav1.ConditionTrue
	}
	conditions := []metav1.Condition{
		{
			Type:               string(clusterv1beta1.AgentJoined),
			Status:             metav1.ConditionTrue,
			LastTransitionTime: heartbeat,
		},
		{
			Type:               string(clusterv1beta1.AgentHealthy),
			Status:             healthyStatus,
			LastTransitionTime: healthyTransition,
		},
	}
	return clusterv1beta1.MemberCluster{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: clusterv1beta1.MemberClusterStatus{
			AgentStatus: []clusterv1beta1.AgentStatus{{
				Type:                  clusterv1beta1.MemberAgent,
				LastReceivedHeartbeat: heartbeat,
				Conditions:            conditions,
			}},
		},
	}
}

func clusterWithMemberAgentHeartbeat(name string, heartbeat metav1.Time) clusterv1beta1.MemberCluster {
	return clusterWithAgentStatus(name, heartbeat, "", metav1.Time{})
}

func TestCalculateHeartbeatRequeue(t *testing.T) {
	baseTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	defaultTimeout := 5 * time.Minute

	testCases := []struct {
		name             string
		outcomes         []selectorOutcome
		clusters         []clusterv1beta1.MemberCluster
		timeout          time.Duration
		healthTimeout    time.Duration
		now              time.Time
		wantRequeueAfter time.Duration
		wantNeeded       bool
	}{
		{
			name:             "empty fleet: no counted clusters",
			outcomes:         []selectorOutcome{{counts: resolvedCounts{desired: 1}, matched: []string{"c1"}}},
			clusters:         nil,
			timeout:          defaultTimeout,
			now:              baseTime,
			wantRequeueAfter: 0,
			wantNeeded:       false,
		},
		{
			name:             "empty outcomes: no counted clusters",
			outcomes:         nil,
			clusters:         []clusterv1beta1.MemberCluster{clusterWithMemberAgentHeartbeat("c1", metav1.NewTime(baseTime.Add(-time.Minute)))},
			timeout:          defaultTimeout,
			now:              baseTime,
			wantRequeueAfter: 0,
			wantNeeded:       false,
		},
		{
			name:             "zero matches: outcomes select no clusters",
			outcomes:         []selectorOutcome{{counts: resolvedCounts{desired: 1}, matched: nil}},
			clusters:         []clusterv1beta1.MemberCluster{clusterWithMemberAgentHeartbeat("c1", metav1.NewTime(baseTime.Add(-time.Minute)))},
			timeout:          defaultTimeout,
			now:              baseTime,
			wantRequeueAfter: 0,
			wantNeeded:       false,
		},
		{
			name:             "single counted cluster: deadline plus safety buffer",
			outcomes:         []selectorOutcome{{counts: resolvedCounts{desired: 1}, matched: []string{"c1"}}},
			clusters:         []clusterv1beta1.MemberCluster{clusterWithMemberAgentHeartbeat("c1", metav1.NewTime(baseTime.Add(-time.Minute)))},
			timeout:          defaultTimeout,
			now:              baseTime,
			wantRequeueAfter: 4*time.Minute + time.Second,
			wantNeeded:       true,
		},
		{
			name: "multiple selected clusters: choose earliest deadline",
			outcomes: []selectorOutcome{{
				counts:  resolvedCounts{desired: 2},
				matched: []string{"c1", "c2"},
			}},
			clusters: []clusterv1beta1.MemberCluster{
				clusterWithMemberAgentHeartbeat("c1", metav1.NewTime(baseTime.Add(-4*time.Minute))),
				clusterWithMemberAgentHeartbeat("c2", metav1.NewTime(baseTime.Add(-1*time.Minute))),
			},
			timeout:          defaultTimeout,
			now:              baseTime,
			wantRequeueAfter: 1*time.Minute + time.Second,
			wantNeeded:       true,
		},
		{
			name: "surplus cluster: unselected matching cluster does not influence deadline",
			outcomes: []selectorOutcome{{
				counts:  resolvedCounts{desired: 1},
				matched: []string{"c1", "c2"}, // c1 is selected, c2 is surplus
			}},
			clusters: []clusterv1beta1.MemberCluster{
				clusterWithMemberAgentHeartbeat("c1", metav1.NewTime(baseTime.Add(-1*time.Minute))),
				clusterWithMemberAgentHeartbeat("c2", metav1.NewTime(baseTime.Add(-4*time.Minute))),
			},
			timeout:          defaultTimeout,
			now:              baseTime,
			wantRequeueAfter: 4*time.Minute + time.Second, // derived from c1 (4m+1s), ignoring older surplus c2 (1m+1s)
			wantNeeded:       true,
		},
		{
			name: "multiple disjoint selectors: earliest deadline across selected clusters",
			outcomes: []selectorOutcome{
				{counts: resolvedCounts{desired: 1}, matched: []string{"c1"}},
				{counts: resolvedCounts{desired: 1}, matched: []string{"c2"}},
			},
			clusters: []clusterv1beta1.MemberCluster{
				clusterWithMemberAgentHeartbeat("c1", metav1.NewTime(baseTime.Add(-2*time.Minute))),
				clusterWithMemberAgentHeartbeat("c2", metav1.NewTime(baseTime.Add(-3*time.Minute))),
			},
			timeout:          defaultTimeout,
			now:              baseTime,
			wantRequeueAfter: 2*time.Minute + time.Second, // c2 is earlier: (5m - 3m) + 1s = 2m1s
			wantNeeded:       true,
		},
		{
			name: "overlapping selectors: repeated cluster selection does not alter result",
			outcomes: []selectorOutcome{
				{counts: resolvedCounts{desired: 1}, matched: []string{"c1"}},
				{counts: resolvedCounts{desired: 1}, matched: []string{"c1"}},
			},
			clusters: []clusterv1beta1.MemberCluster{
				clusterWithMemberAgentHeartbeat("c1", metav1.NewTime(baseTime.Add(-2*time.Minute))),
			},
			timeout:          defaultTimeout,
			now:              baseTime,
			wantRequeueAfter: 3*time.Minute + time.Second,
			wantNeeded:       true,
		},
		{
			name:             "deadline already expired: safely ignored (already ineligible, no useless timer)",
			outcomes:         []selectorOutcome{{counts: resolvedCounts{desired: 1}, matched: []string{"c1"}}},
			clusters:         []clusterv1beta1.MemberCluster{clusterWithMemberAgentHeartbeat("c1", metav1.NewTime(baseTime.Add(-6*time.Minute)))},
			timeout:          defaultTimeout,
			now:              baseTime,
			wantRequeueAfter: 0,
			wantNeeded:       false,
		},
		{
			name:             "deadline exactly now: returns positive floor (1 second)",
			outcomes:         []selectorOutcome{{counts: resolvedCounts{desired: 1}, matched: []string{"c1"}}},
			clusters:         []clusterv1beta1.MemberCluster{clusterWithMemberAgentHeartbeat("c1", metav1.NewTime(baseTime.Add(-5*time.Minute)))},
			timeout:          defaultTimeout,
			now:              baseTime,
			wantRequeueAfter: 1 * time.Second,
			wantNeeded:       true,
		},
		{
			name:             "imminent deadline within buffer: clamps to positive floor (1 second)",
			outcomes:         []selectorOutcome{{counts: resolvedCounts{desired: 1}, matched: []string{"c1"}}},
			clusters:         []clusterv1beta1.MemberCluster{clusterWithMemberAgentHeartbeat("c1", metav1.NewTime(baseTime.Add(-5*time.Minute-500*time.Millisecond)))},
			timeout:          defaultTimeout,
			now:              baseTime,
			wantRequeueAfter: 1 * time.Second,
			wantNeeded:       true,
		},
		{
			name:     "deleting cluster: safely ignored (no deadline)",
			outcomes: []selectorOutcome{{counts: resolvedCounts{desired: 1}, matched: []string{"c1"}}},
			clusters: []clusterv1beta1.MemberCluster{
				func() clusterv1beta1.MemberCluster {
					mc := clusterWithMemberAgentHeartbeat("c1", metav1.NewTime(baseTime.Add(-time.Minute)))
					mc.DeletionTimestamp = &metav1.Time{Time: baseTime}
					return mc
				}(),
			},
			timeout:          defaultTimeout,
			now:              baseTime,
			wantRequeueAfter: 0,
			wantNeeded:       false,
		},
		{
			name:     "unjoined member agent: safely ignored (no deadline)",
			outcomes: []selectorOutcome{{counts: resolvedCounts{desired: 1}, matched: []string{"c1"}}},
			clusters: []clusterv1beta1.MemberCluster{
				func() clusterv1beta1.MemberCluster {
					mc := clusterWithMemberAgentHeartbeat("c1", metav1.NewTime(baseTime.Add(-time.Minute)))
					mc.Status.AgentStatus[0].Conditions[0].Status = metav1.ConditionFalse
					return mc
				}(),
			},
			timeout:          defaultTimeout,
			now:              baseTime,
			wantRequeueAfter: 0,
			wantNeeded:       false,
		},
		{
			name:     "missing joined condition: safely ignored (no deadline)",
			outcomes: []selectorOutcome{{counts: resolvedCounts{desired: 1}, matched: []string{"c1"}}},
			clusters: []clusterv1beta1.MemberCluster{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "c1"},
					Status: clusterv1beta1.MemberClusterStatus{
						AgentStatus: []clusterv1beta1.AgentStatus{{
							Type:                  clusterv1beta1.MemberAgent,
							LastReceivedHeartbeat: metav1.NewTime(baseTime.Add(-time.Minute)),
							Conditions: []metav1.Condition{{
								Type:               string(clusterv1beta1.AgentHealthy),
								Status:             metav1.ConditionTrue,
								LastTransitionTime: metav1.NewTime(baseTime.Add(-time.Minute)),
							}},
						}},
					},
				},
			},
			timeout:          defaultTimeout,
			now:              baseTime,
			wantRequeueAfter: 0,
			wantNeeded:       false,
		},
		{
			name:     "missing health condition: safely ignored (no deadline)",
			outcomes: []selectorOutcome{{counts: resolvedCounts{desired: 1}, matched: []string{"c1"}}},
			clusters: []clusterv1beta1.MemberCluster{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "c1"},
					Status: clusterv1beta1.MemberClusterStatus{
						AgentStatus: []clusterv1beta1.AgentStatus{{
							Type:                  clusterv1beta1.MemberAgent,
							LastReceivedHeartbeat: metav1.NewTime(baseTime.Add(-time.Minute)),
							Conditions: []metav1.Condition{{
								Type:               string(clusterv1beta1.AgentJoined),
								Status:             metav1.ConditionTrue,
								LastTransitionTime: metav1.NewTime(baseTime.Add(-time.Minute)),
							}},
						}},
					},
				},
			},
			timeout:          defaultTimeout,
			now:              baseTime,
			wantRequeueAfter: 0,
			wantNeeded:       false,
		},
		{
			name:     "missing agent status: safely ignored",
			outcomes: []selectorOutcome{{counts: resolvedCounts{desired: 1}, matched: []string{"c1"}}},
			clusters: []clusterv1beta1.MemberCluster{
				{ObjectMeta: metav1.ObjectMeta{Name: "c1"}},
			},
			timeout:          defaultTimeout,
			now:              baseTime,
			wantRequeueAfter: 0,
			wantNeeded:       false,
		},
		{
			name:     "agent status has non-member agent type: safely ignored",
			outcomes: []selectorOutcome{{counts: resolvedCounts{desired: 1}, matched: []string{"c1"}}},
			clusters: []clusterv1beta1.MemberCluster{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "c1"},
					Status: clusterv1beta1.MemberClusterStatus{
						AgentStatus: []clusterv1beta1.AgentStatus{{
							Type:                  clusterv1beta1.ServiceExportImportAgent,
							LastReceivedHeartbeat: metav1.NewTime(baseTime.Add(-time.Minute)),
						}},
					},
				},
			},
			timeout:          defaultTimeout,
			now:              baseTime,
			wantRequeueAfter: 0,
			wantNeeded:       false,
		},
		{
			name:     "zero heartbeat timestamp: safely ignored",
			outcomes: []selectorOutcome{{counts: resolvedCounts{desired: 1}, matched: []string{"c1"}}},
			clusters: []clusterv1beta1.MemberCluster{
				clusterWithMemberAgentHeartbeat("c1", metav1.Time{}),
			},
			timeout:          defaultTimeout,
			now:              baseTime,
			wantRequeueAfter: 0,
			wantNeeded:       false,
		},
		{
			name:             "custom timeout: uses configured timeout",
			outcomes:         []selectorOutcome{{counts: resolvedCounts{desired: 1}, matched: []string{"c1"}}},
			clusters:         []clusterv1beta1.MemberCluster{clusterWithMemberAgentHeartbeat("c1", metav1.NewTime(baseTime.Add(-2*time.Minute)))},
			timeout:          15 * time.Minute,
			now:              baseTime,
			wantRequeueAfter: 13*time.Minute + time.Second,
			wantNeeded:       true,
		},
		{
			name:     "unhealthy condition expires before heartbeat: requeues at health check deadline",
			outcomes: []selectorOutcome{{counts: resolvedCounts{desired: 1}, matched: []string{"c1"}}},
			clusters: []clusterv1beta1.MemberCluster{
				clusterWithAgentStatus("c1", metav1.NewTime(baseTime.Add(-1*time.Minute)), metav1.ConditionFalse, metav1.NewTime(baseTime.Add(-3*time.Minute))),
			},
			timeout:          defaultTimeout,
			now:              baseTime,
			wantRequeueAfter: 2*time.Minute + time.Second,
			wantNeeded:       true,
		},
		{
			name:     "heartbeat expires before unhealthy condition: requeues at heartbeat deadline",
			outcomes: []selectorOutcome{{counts: resolvedCounts{desired: 1}, matched: []string{"c1"}}},
			clusters: []clusterv1beta1.MemberCluster{
				clusterWithAgentStatus("c1", metav1.NewTime(baseTime.Add(-4*time.Minute)), metav1.ConditionFalse, metav1.NewTime(baseTime.Add(-1*time.Minute))),
			},
			timeout:          defaultTimeout,
			now:              baseTime,
			wantRequeueAfter: 1*time.Minute + time.Second,
			wantNeeded:       true,
		},
		{
			name:     "healthy condition (ConditionTrue): does not introduce an unnecessary health deadline",
			outcomes: []selectorOutcome{{counts: resolvedCounts{desired: 1}, matched: []string{"c1"}}},
			clusters: []clusterv1beta1.MemberCluster{
				clusterWithAgentStatus("c1", metav1.NewTime(baseTime.Add(-2*time.Minute)), metav1.ConditionTrue, metav1.NewTime(baseTime.Add(-4*time.Minute))),
			},
			timeout:          defaultTimeout,
			now:              baseTime,
			wantRequeueAfter: 3*time.Minute + time.Second,
			wantNeeded:       true,
		},
		{
			name:     "custom heartbeat and health timeouts: respects both custom values",
			outcomes: []selectorOutcome{{counts: resolvedCounts{desired: 1}, matched: []string{"c1"}}},
			clusters: []clusterv1beta1.MemberCluster{
				clusterWithAgentStatus("c1", metav1.NewTime(baseTime.Add(-1*time.Minute)), metav1.ConditionFalse, metav1.NewTime(baseTime.Add(-1*time.Minute))),
			},
			timeout:          10 * time.Minute,
			healthTimeout:    2 * time.Minute,
			now:              baseTime,
			wantRequeueAfter: 1*time.Minute + time.Second,
			wantNeeded:       true,
		},
		{
			name:     "non-True health condition with zero transition time: treated as expired/ineligible",
			outcomes: []selectorOutcome{{counts: resolvedCounts{desired: 1}, matched: []string{"c1"}}},
			clusters: []clusterv1beta1.MemberCluster{
				clusterWithAgentStatus("c1", metav1.NewTime(baseTime.Add(-2*time.Minute)), metav1.ConditionFalse, metav1.Time{}),
			},
			timeout:          defaultTimeout,
			now:              baseTime,
			wantRequeueAfter: 0,
			wantNeeded:       false,
		},
		{
			name:     "healthy condition (ConditionTrue) with zero transition time: respects heartbeat deadline",
			outcomes: []selectorOutcome{{counts: resolvedCounts{desired: 1}, matched: []string{"c1"}}},
			clusters: []clusterv1beta1.MemberCluster{
				clusterWithAgentStatus("c1", metav1.NewTime(baseTime.Add(-2*time.Minute)), metav1.ConditionTrue, metav1.Time{}),
			},
			timeout:          defaultTimeout,
			now:              baseTime,
			wantRequeueAfter: 3*time.Minute + time.Second,
			wantNeeded:       true,
		},
		{
			name: "earliest deadline across multiple counted clusters with mixed health and heartbeat",
			outcomes: []selectorOutcome{{
				counts:  resolvedCounts{desired: 2},
				matched: []string{"c1", "c2"},
			}},
			clusters: []clusterv1beta1.MemberCluster{
				clusterWithAgentStatus("c1", metav1.NewTime(baseTime.Add(-1*time.Minute)), metav1.ConditionTrue, metav1.NewTime(baseTime.Add(-4*time.Minute))),
				clusterWithAgentStatus("c2", metav1.NewTime(baseTime.Add(-2*time.Minute)), metav1.ConditionFalse, metav1.NewTime(baseTime.Add(-4*time.Minute))),
			},
			timeout:          defaultTimeout,
			now:              baseTime,
			wantRequeueAfter: 1*time.Minute + time.Second,
			wantNeeded:       true,
		},
		{
			name: "surplus cluster with imminent health deadline: excluded from deadline",
			outcomes: []selectorOutcome{{
				counts:  resolvedCounts{desired: 1},
				matched: []string{"c1", "c2"}, // c1 selected, c2 surplus
			}},
			clusters: []clusterv1beta1.MemberCluster{
				clusterWithAgentStatus("c1", metav1.NewTime(baseTime.Add(-1*time.Minute)), metav1.ConditionTrue, metav1.NewTime(baseTime.Add(-4*time.Minute))),
				clusterWithAgentStatus("c2", metav1.NewTime(baseTime.Add(-1*time.Minute)), metav1.ConditionFalse, metav1.NewTime(baseTime.Add(-4*time.Minute))),
			},
			timeout:          defaultTimeout,
			now:              baseTime,
			wantRequeueAfter: 4*time.Minute + time.Second,
			wantNeeded:       true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			healthTimeout := tc.healthTimeout
			if healthTimeout == 0 {
				healthTimeout = tc.timeout
			}
			gotAfter, gotNeeded := calculateHeartbeatRequeue(tc.outcomes, tc.clusters, tc.timeout, healthTimeout, tc.now)
			if gotNeeded != tc.wantNeeded {
				t.Fatalf("calculateHeartbeatRequeue() needed = %v, want %v", gotNeeded, tc.wantNeeded)
			}
			if gotAfter != tc.wantRequeueAfter {
				t.Errorf("calculateHeartbeatRequeue() requeueAfter = %v, want %v", gotAfter, tc.wantRequeueAfter)
			}
		})
	}
}
