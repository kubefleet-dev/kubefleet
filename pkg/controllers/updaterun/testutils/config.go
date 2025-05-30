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

package testutils

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/google/go-cmp/cmp"
	_ "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	prometheusclientmodel "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	placementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1alpha1"
	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils"
	metricsutils "github.com/kubefleet-dev/kubefleet/test/utils/metrics"
)

const (
	regionEastus = "eastus"
	regionWestus = "westus"
)

// TestConfig is a configuration struct for updateRun integration tests.
type TestConfig struct {
	// TargetClusterCount is the number of scheduled clusters.
	TargetClusterCount int
	// UnscheduledClusterCount is the number of unscheduled clusters.
	UnscheduledClusterCount int

	// Timeout is the maximum wait time for Eventually.
	Timeout time.Duration
	// Interval is the time to wait between retries for Eventually and Consistently.
	Interval time.Duration
	// Duration is the time to duration to check for Consistently.
	Duration time.Duration

	// ResourceSnapshotCount is the number of resource snapshots to be created.
	ResourceSnapshotCount int

	k8sClient                   client.Client
	testUpdateRunName           string
	testCRPName                 string
	testResourceSnapshotNames   []string
	testResourceSnapshotIndices []string
	testUpdateStrategyName      string
	testCROName                 string
	updateRunNamespacedName     types.NamespacedName
}

func GenerateDefaultTestConfig(k8sClient client.Client) *TestConfig {
	config := &TestConfig{
		TargetClusterCount:      10,
		UnscheduledClusterCount: 3,
		Timeout:                 time.Second * 10,
		Interval:                time.Millisecond * 250,
		Duration:                time.Second * 20,
		ResourceSnapshotCount:   1,
		k8sClient:               k8sClient,
	}
	config.populateDerivedFields()
	return config
}

func (c *TestConfig) populateDerivedFields() {
	c.testUpdateRunName = "updaterun-" + utils.RandStr()
	c.testCRPName = "crp-" + utils.RandStr()
	for i := range c.ResourceSnapshotCount {
		c.testResourceSnapshotNames = append(c.testResourceSnapshotNames, fmt.Sprintf("%s-%d-snapshot", c.testCRPName, i))
		c.testResourceSnapshotIndices = append(c.testResourceSnapshotIndices, strconv.Itoa(i))
	}
	c.testUpdateStrategyName = "updatestrategy-" + utils.RandStr()
	c.testCROName = "cro-" + utils.RandStr()
	c.updateRunNamespacedName = types.NamespacedName{Name: c.testUpdateRunName}
}

// ValidateUpdateRunMetricsEmitted validates the update run status metrics are emitted and are emitted in the correct order.
func (c *TestConfig) ValidateUpdateRunMetricsEmitted(wantMetrics ...*prometheusclientmodel.Metric) {
	Eventually(func() error {
		metricFamilies, err := ctrlmetrics.Registry.Gather()
		if err != nil {
			return fmt.Errorf("failed to gather metrics: %w", err)
		}
		var gotMetrics []*prometheusclientmodel.Metric
		for _, mf := range metricFamilies {
			if mf.GetName() == "fleet_workload_update_run_status_last_timestamp_seconds" {
				gotMetrics = mf.GetMetric()
			}
		}

		if diff := cmp.Diff(gotMetrics, wantMetrics, metricsutils.MetricsCmpOptions...); diff != "" {
			return fmt.Errorf("update run status metrics mismatch (-got, +want):\n%s", diff)
		}

		return nil
	}, c.Timeout, c.Interval).Should(Succeed(), "failed to validate the update run status metrics")
}

func (c *TestConfig) ValidateUpdateRunIsDeleted(ctx context.Context) {
	Eventually(func() error {
		updateRun := &placementv1beta1.ClusterStagedUpdateRun{}
		if err := c.k8sClient.Get(ctx, c.updateRunNamespacedName, updateRun); !errors.IsNotFound(err) {
			return fmt.Errorf("clusterStagedUpdateRun %s still exists or an unexpected error occurred: %w", c.updateRunNamespacedName, err)
		}
		return nil
	}, c.Timeout, c.Interval).Should(Succeed(), "failed to remove clusterStagedUpdateRun %s ", c.updateRunNamespacedName)
}

func (c *TestConfig) GenerateTestClusterStagedUpdateRun(resourceSnapshotIndex int) *placementv1beta1.ClusterStagedUpdateRun {
	return &placementv1beta1.ClusterStagedUpdateRun{
		ObjectMeta: metav1.ObjectMeta{
			Name: c.testUpdateRunName,
		},
		Spec: placementv1beta1.StagedUpdateRunSpec{
			PlacementName:            c.testCRPName,
			ResourceSnapshotIndex:    c.testResourceSnapshotIndices[resourceSnapshotIndex],
			StagedUpdateStrategyName: c.testUpdateStrategyName,
		},
	}
}

func (c *TestConfig) GenerateTestClusterResourcePlacement() *placementv1beta1.ClusterResourcePlacement {
	return &placementv1beta1.ClusterResourcePlacement{
		ObjectMeta: metav1.ObjectMeta{
			Name: c.testCRPName,
		},
		Spec: placementv1beta1.ClusterResourcePlacementSpec{
			ResourceSelectors: []placementv1beta1.ClusterResourceSelector{
				{
					Group:   "",
					Version: "v1",
					Kind:    "Namespace",
					Name:    "test-namespace",
				},
			},
			Strategy: placementv1beta1.RolloutStrategy{
				Type: placementv1beta1.ExternalRolloutStrategyType,
				ApplyStrategy: &placementv1beta1.ApplyStrategy{
					Type:           placementv1beta1.ApplyStrategyTypeReportDiff,
					WhenToTakeOver: placementv1beta1.WhenToTakeOverTypeIfNoDiff,
				},
			},
		},
	}
}

func (c *TestConfig) GenerateTestClusterSchedulingPolicySnapshot(idx int) *placementv1beta1.ClusterSchedulingPolicySnapshot {
	return &placementv1beta1.ClusterSchedulingPolicySnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf(placementv1beta1.PolicySnapshotNameFmt, c.testCRPName, idx),
			Labels: map[string]string{
				"kubernetes-fleet.io/parent-CRP":         c.testCRPName,
				"kubernetes-fleet.io/is-latest-snapshot": "true",
				"kubernetes-fleet.io/policy-index":       strconv.Itoa(idx),
			},
			Annotations: map[string]string{
				"kubernetes-fleet.io/number-of-clusters": strconv.Itoa(c.TargetClusterCount),
			},
		},
		Spec: placementv1beta1.SchedulingPolicySnapshotSpec{
			Policy: &placementv1beta1.PlacementPolicy{
				PlacementType: placementv1beta1.PickNPlacementType,
			},
			PolicyHash: []byte("hash"),
		},
	}
}

func (c *TestConfig) GenerateTestClustersAndBindings(
	policySnapshotName string,
	resourceSnapshotIndex int,
) ([]*clusterv1beta1.MemberCluster, []*clusterv1beta1.MemberCluster, []*placementv1beta1.ClusterResourceBinding) {
	resourceBindings := make([]*placementv1beta1.ClusterResourceBinding, c.TargetClusterCount+c.UnscheduledClusterCount)
	targetClusters := make([]*clusterv1beta1.MemberCluster, c.TargetClusterCount)
	for i := range targetClusters {
		// split the clusters into 2 regions
		region := regionEastus
		if i%2 == 0 {
			region = regionWestus
		}
		// reserse the order of the clusters by index
		targetClusters[i] = generateTestMemberCluster(c.TargetClusterCount-1-i, "cluster-"+strconv.Itoa(i), map[string]string{"group": "prod", "region": region})
		resourceBindings[i] = c.GenerateTestClusterResourceBinding(resourceSnapshotIndex, policySnapshotName, targetClusters[i].Name, placementv1beta1.BindingStateScheduled)
	}

	unscheduledClusters := make([]*clusterv1beta1.MemberCluster, c.UnscheduledClusterCount)
	for i := range unscheduledClusters {
		unscheduledClusters[i] = generateTestMemberCluster(i, "unscheduled-cluster-"+strconv.Itoa(i), map[string]string{"group": "staging"})
		// update the policySnapshot name so that these clusters are considered to-be-deleted
		resourceBindings[c.TargetClusterCount+i] = c.GenerateTestClusterResourceBinding(0, policySnapshotName+"a", unscheduledClusters[i].Name, placementv1beta1.BindingStateUnscheduled)
	}

	return targetClusters, unscheduledClusters, resourceBindings
}

func (c *TestConfig) GenerateTestClusterResourceBinding(
	resourceSnapshotIndex int,
	policySnapshotName, targetCluster string,
	state placementv1beta1.BindingState,
) *placementv1beta1.ClusterResourceBinding {
	binding := &placementv1beta1.ClusterResourceBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "binding-" + c.testResourceSnapshotNames[resourceSnapshotIndex] + "-" + targetCluster,
			Labels: map[string]string{
				placementv1beta1.CRPTrackingLabel: c.testCRPName,
			},
		},
		Spec: placementv1beta1.ResourceBindingSpec{
			State:                        state,
			TargetCluster:                targetCluster,
			SchedulingPolicySnapshotName: policySnapshotName,
		},
	}
	return binding
}

func generateTestMemberCluster(idx int, clusterName string, labels map[string]string) *clusterv1beta1.MemberCluster {
	clusterLabels := make(map[string]string)
	for k, v := range labels {
		clusterLabels[k] = v
	}
	clusterLabels["index"] = strconv.Itoa(idx)
	return &clusterv1beta1.MemberCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:   clusterName,
			Labels: clusterLabels,
		},
		Spec: clusterv1beta1.MemberClusterSpec{
			Identity: rbacv1.Subject{
				Name:      "testUser",
				Kind:      "ServiceAccount",
				Namespace: utils.FleetSystemNamespace,
			},
			HeartbeatPeriodSeconds: 60,
		},
	}
}

func (c *TestConfig) GenerateTestClusterStagedUpdateStrategy() *placementv1beta1.ClusterStagedUpdateStrategy {
	sortingKey := "index"
	return &placementv1beta1.ClusterStagedUpdateStrategy{
		ObjectMeta: metav1.ObjectMeta{
			Name: c.testUpdateStrategyName,
		},
		Spec: placementv1beta1.StagedUpdateStrategySpec{
			Stages: []placementv1beta1.StageConfig{
				{
					Name: "stage1",
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"group":  "prod",
							"region": "eastus",
						},
					},
					SortingLabelKey: &sortingKey,
					AfterStageTasks: []placementv1beta1.AfterStageTask{
						{
							Type: placementv1beta1.AfterStageTaskTypeTimedWait,
							WaitTime: &metav1.Duration{
								Duration: time.Second * 4,
							},
						},
						{
							Type: placementv1beta1.AfterStageTaskTypeApproval,
						},
					},
				},
				{
					Name: "stage2",
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"group":  "prod",
							"region": "westus",
						},
					},
					// no sortingLabelKey, should sort by cluster name
					AfterStageTasks: []placementv1beta1.AfterStageTask{
						{
							Type: placementv1beta1.AfterStageTaskTypeApproval,
						},
						{
							Type: placementv1beta1.AfterStageTaskTypeTimedWait,
							WaitTime: &metav1.Duration{
								Duration: time.Second * 4,
							},
						},
					},
				},
			},
		},
	}
}

func (c *TestConfig) GenerateTestClusterResourceSnapshot(idx int) *placementv1beta1.ClusterResourceSnapshot {
	testNamespace, _ := json.Marshal(corev1.Namespace{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Namespace",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-namespace",
			Labels: map[string]string{
				"fleet.azure.com/name": "test-namespace",
			},
		},
	})
	clusterResourceSnapshot := &placementv1beta1.ClusterResourceSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name: c.testResourceSnapshotNames[idx],
			Labels: map[string]string{
				placementv1beta1.CRPTrackingLabel:      c.testCRPName,
				placementv1beta1.IsLatestSnapshotLabel: strconv.FormatBool(true),
				placementv1beta1.ResourceIndexLabel:    c.testResourceSnapshotIndices[idx],
			},
			Annotations: map[string]string{
				placementv1beta1.ResourceGroupHashAnnotation:         "hash",
				placementv1beta1.NumberOfResourceSnapshotsAnnotation: strconv.Itoa(1),
			},
		},
	}
	rawContents := [][]byte{testNamespace}
	for _, rawContent := range rawContents {
		clusterResourceSnapshot.Spec.SelectedResources = append(clusterResourceSnapshot.Spec.SelectedResources,
			placementv1beta1.ResourceContent{
				RawExtension: runtime.RawExtension{Raw: rawContent},
			},
		)
	}
	return clusterResourceSnapshot
}

func (c *TestConfig) GenerateTestClusterResourceOverride() *placementv1alpha1.ClusterResourceOverrideSnapshot {
	return &placementv1alpha1.ClusterResourceOverrideSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name: c.testCROName,
			Labels: map[string]string{
				placementv1beta1.IsLatestSnapshotLabel: strconv.FormatBool(true),
			},
		},
		Spec: placementv1alpha1.ClusterResourceOverrideSnapshotSpec{
			OverrideSpec: placementv1alpha1.ClusterResourceOverrideSpec{
				ClusterResourceSelectors: []placementv1beta1.ClusterResourceSelector{
					{
						Group:   "",
						Version: "v1",
						Kind:    "Namespace",
						Name:    "test-namespace",
					},
				},
				Policy: &placementv1alpha1.OverridePolicy{
					OverrideRules: []placementv1alpha1.OverrideRule{
						{
							ClusterSelector: &placementv1beta1.ClusterSelector{
								ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
									{
										LabelSelector: &metav1.LabelSelector{
											MatchLabels: map[string]string{
												"region": "eastus",
											},
										},
									},
								},
							},
							JSONPatchOverrides: []placementv1alpha1.JSONPatchOverride{
								{
									Operator: placementv1alpha1.JSONPatchOverrideOpAdd,
									Path:     "/metadata/labels/test",
									Value:    apiextensionsv1.JSON{Raw: []byte(`"test"`)},
								},
							},
						},
					},
				},
			},
			OverrideHash: []byte("hash"),
		},
	}
}

func (c *TestConfig) GenerateTestApprovalRequest(name string) *placementv1beta1.ClusterApprovalRequest {
	return &placementv1beta1.ClusterApprovalRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				placementv1beta1.TargetUpdateRunLabel: c.testUpdateRunName,
			},
		},
	}
}

func (c *TestConfig) ValidateUpdateRunWithFunc(ctx context.Context, f func(updateRun *placementv1beta1.ClusterStagedUpdateRun) error) {
	updateRun := &placementv1beta1.ClusterStagedUpdateRun{}
	Eventually(func() error {
		if err := c.k8sClient.Get(ctx, c.updateRunNamespacedName, updateRun); err != nil {
			return fmt.Errorf("failed to get clusterStagedUpdateRun %s: %w", c.updateRunNamespacedName, err)
		}
		return f(updateRun)
	}, c.Timeout, c.Interval).Should(Succeed(), "failed to validate the clusterStagedUpdateRun %s", c.updateRunNamespacedName)
}

func (c *TestConfig) ValidateUpdateRunWithFuncConsistently(ctx context.Context, f func(updateRun *placementv1beta1.ClusterStagedUpdateRun) error) {
	updateRun := &placementv1beta1.ClusterStagedUpdateRun{}
	Consistently(func() error {
		if err := c.k8sClient.Get(ctx, c.updateRunNamespacedName, updateRun); err != nil {
			return fmt.Errorf("failed to get clusterStagedUpdateRun %s: %w", c.updateRunNamespacedName, err)
		}
		return f(updateRun)
	}, c.Duration, c.Interval).Should(Succeed(), "failed to validate the clusterStagedUpdateRun %s", c.updateRunNamespacedName)
}

func (c *TestConfig) ValidateApprovalRequestCount(ctx context.Context, count int) {
	Eventually(func() (int, error) {
		appReqList := &placementv1beta1.ClusterApprovalRequestList{}
		if err := c.k8sClient.List(ctx, appReqList); err != nil {
			return -1, err
		}
		return len(appReqList.Items), nil
	}, c.Timeout, c.Interval).Should(Equal(count), "approval requests count mismatch")
}
