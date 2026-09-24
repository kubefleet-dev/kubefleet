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

// Package metrics provides utilities for metrics.
package metrics

import (
	"sort"
	"strings"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	prometheusclientmodel "github.com/prometheus/client_model/go"
)

var (
	// MetricsCmpOptions defines comparison options for Prometheus metric structures.
	// - Sorting metrics by their label identity for deterministic ordering (labels
	//   uniquely identify a metric, whereas gauge values, e.g. persisted transition
	//   timestamps, only have second-level precision once round-tripped through the
	//   API server and so can legitimately tie across distinct metrics),
	// - Sorting metric labels for deterministic ordering,
	// - Comparing gauge values based on whether they were meaningfully set (i.e., > 0),
	//   which stays permissive for consumers (e.g. the placement package) that still
	//   derive their expected gauge value from time.Now() rather than a persisted
	//   condition timestamp,
	// - Ignoring unexported fields to avoid false mismatches due to internal state.
	MetricsCmpOptions = []cmp.Option{
		cmpopts.SortSlices(func(a, b *prometheusclientmodel.Metric) bool {
			return metricLabelKey(a) < metricLabelKey(b)
		}),
		cmpopts.SortSlices(func(a, b *prometheusclientmodel.LabelPair) bool {
			return a.GetName() < b.GetName() // Sort by label
		}),
		cmp.Comparer(func(a, b *prometheusclientmodel.Gauge) bool {
			return (a.GetValue() > 0) == (b.GetValue() > 0)
		}),
		cmpopts.IgnoreUnexported(prometheusclientmodel.Metric{}, prometheusclientmodel.LabelPair{}, prometheusclientmodel.Gauge{}),
	}
)

// metricLabelKey builds a stable, deterministic key from a metric's label pairs
// (sorted by name) so that metrics can be sorted, and thus paired for comparison,
// by identity rather than by their gauge value, which may tie across distinct
// metrics (e.g. two condition transitions persisted within the same second).
func metricLabelKey(m *prometheusclientmodel.Metric) string {
	labels := m.GetLabel()
	pairs := make([]string, 0, len(labels))
	for _, l := range labels {
		pairs = append(pairs, l.GetName()+"="+l.GetValue())
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ";")
}
