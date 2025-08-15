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

package azure

import (
	"fmt"

	"github.com/kubefleet-dev/kubefleet/pkg/utils/featuregates"
)

const (
	// CollectCostsInAzurePropertyProvider enables the collection of costs in the Azure Property Provider.
	CollectCostsInAzurePropertyProvider = "CollectCostsInAzurePropertyProvider"
	// CollectAvailableResourcesInAzurePropertyProvider enables the collection of available resources in the Azure Property Provider.
	CollectAvailableResourcesInAzurePropertyProvider = "CollectAvailableResourcesInAzurePropertyProvider"
)

var defaultFeatures = map[featuregates.FeatureName]featuregates.FeatureSpec{
	CollectCostsInAzurePropertyProvider:              {Default: true},
	CollectAvailableResourcesInAzurePropertyProvider: {Default: true},
}

func init() {
	if err := featuregates.MemberAgentFeatureGate.Add(defaultFeatures); err != nil {
		panic(fmt.Sprintf("failed to add property provider features to the member agent feature gate: %v", err))
	}
}
