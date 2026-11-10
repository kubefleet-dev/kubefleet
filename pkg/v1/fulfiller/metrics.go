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

package fulfiller

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// claimOutcomes counts the claims a provider brought to an end, by outcome: fulfilled or failed.
// Each claim is counted once: complete and fail each run on the round that makes the transition,
// and later rounds return before them on a fulfilled or terminal claim.
var claimOutcomes = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "fleet_cluster_claim_fulfiller_outcomes_total",
	Help: "Number of cluster claims a fulfiller brought to an end, by provisioner and outcome.",
}, []string{"provisioner", "outcome"})

// The metrics register on controller-runtime's registry here rather than with the hub agent's
// metrics in pkg/metrics/hub: a provider runs in its own process and must not register the hub's
// series.
func init() {
	metrics.Registry.MustRegister(claimOutcomes)
}
