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
package metrics

import (
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	prometheusclientmodel "github.com/prometheus/client_model/go"
)

var (
	MetricsCmpOptions = []cmp.Option{
		cmpopts.SortSlices(func(a, b *prometheusclientmodel.Metric) bool {
			return a.GetGauge().GetValue() < b.GetGauge().GetValue() // sort by time
		}),
		cmpopts.SortSlices(func(a, b *prometheusclientmodel.LabelPair) bool {
			if a.Name == nil || b.Name == nil {
				return a.Name == nil
			}
			return *a.Name < *b.Name // Sort by label
		}),
		cmp.Comparer(func(a, b *prometheusclientmodel.Gauge) bool {
			return (a.GetValue() > 0) == (b.GetValue() > 0)
		}),
		cmpopts.IgnoreUnexported(prometheusclientmodel.Metric{}, prometheusclientmodel.LabelPair{}, prometheusclientmodel.Gauge{}),
	}
)
