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

package clusteraffinity

import (
	"context"
	"fmt"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/scheduler/framework"
)

// PreScore allows the plugin to connect to the PreScore extension point in the scheduling
// framework.
func (p *Plugin) PreScore(
	_ context.Context,
	state framework.CycleStatePluginReadWriter,
	policy placementv1beta1.PolicySnapshotObj,
) (status *framework.Status) {
	noPreferredClusterAffinityTerms := policy.GetPolicySnapshotSpec().Policy == nil ||
		policy.GetPolicySnapshotSpec().Policy.Affinity == nil ||
		policy.GetPolicySnapshotSpec().Policy.Affinity.ClusterAffinity == nil ||
		len(policy.GetPolicySnapshotSpec().Policy.Affinity.ClusterAffinity.PreferredDuringSchedulingIgnoredDuringExecution) == 0
	if noPreferredClusterAffinityTerms {
		// There are no preferred cluster affinity terms specified in the scheduling policy;
		// skip the step.
		//
		// Note that this will also skip the Score() extension point for the plugin.
		return framework.NewNonErrorStatus(framework.Skip, p.Name(), "no preferred cluster affinity terms specified")
	}

	// Prepare the plugin state. Specifically, pre-calculate min. and max. values
	// for properties that require sorting (if any).
	ps, err := preparePluginState(state, policy)
	if err != nil {
		return framework.FromError(err, p.Name(), "failed to prepare plugin state")
	}

	// Save the plugin state.
	state.Write(framework.StateKey(p.Name()), ps)

	// All done.
	return nil
}

// Score allows the plugin to connect to the Score extension point in the scheduling framework.
func (p *Plugin) Score(
	_ context.Context,
	state framework.CycleStatePluginReadWriter,
	policy placementv1beta1.PolicySnapshotObj,
	cluster *clusterv1beta1.MemberCluster,
) (score *framework.ClusterScore, status *framework.Status) {
	// Read the plugin state.
	ps, err := p.readPluginState(state)
	if err != nil {
		// This branch should never be reached, as a state has been set
		// in the PreScore stage.
		return nil, framework.FromError(err, p.Name(), "failed to read plugin state")
	}

	score = &framework.ClusterScore{}
	for _, t := range policy.GetPolicySnapshotSpec().Policy.Affinity.ClusterAffinity.PreferredDuringSchedulingIgnoredDuringExecution {
		if t.Weight != 0 {
			cp := clusterPreference(t)
			ts, err := cp.Scores(ps, cluster)
			if err != nil {
				return nil, framework.FromError(fmt.Errorf("failed to calculate score for cluster %s: %w", cluster.Name, err), p.Name())
			}
			// Multiple preferred affinity terms are OR'd.
			score.AffinityScore += ts
		}
	}

	// All done.
	return score, nil
}
