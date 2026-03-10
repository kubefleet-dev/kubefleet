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

package storage

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	clusterpropertiesv1beta1 "github.com/kubefleet-dev/kubefleet/apis/clusterproperties/v1beta1"
)

// Implement the rest.Getter interface.

// Get is a required method for the rest.Getter interface.
//
// It retrieves the API object identified by the given name.
func (s *PropertiesStore) Get(ctx context.Context, name string, options *metav1.GetOptions) (runtime.Object, error) {
	// Not yet fully implemented. Return a dummy object for testing purposes.
	return &clusterpropertiesv1beta1.MemberClusterProperties{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			UID:             "12345678-0000-4000-a000-000000000000",
			Generation:      0,
			ResourceVersion: "1",
		},
		Properties: map[clusterpropertiesv1beta1.PropertyName]clusterpropertiesv1beta1.PropertyValue{
			"dummy-property-name": {
				Value: "dummy-property-value",
			},
		},
		ResourceUsage: clusterpropertiesv1beta1.ResourceUsage{},
	}, nil
}
