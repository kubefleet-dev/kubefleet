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

package resourcewatcher

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/kubefleet-dev/kubefleet/pkg/controllers/annotationplacement"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/informer"
	testinformer "github.com/kubefleet-dev/kubefleet/test/utils/informer"
)

// TestWatchGeneratedPolicies pins the wiring that makes generated-policy drift repairable: with
// annotation-based placement running, the detector registers an informer for each generated policy
// resource, and with it off, it registers none.
func TestWatchGeneratedPolicies(t *testing.T) {
	testCases := []struct {
		name       string
		controller *recordingController
		want       []informer.APIResourceMeta
	}{
		{
			name:       "the generated policy resources are watched when the feature runs",
			controller: &recordingController{},
			want:       annotationplacement.GeneratedPolicyResources(),
		},
		{
			name:       "nothing is watched when the feature is off",
			controller: nil,
			want:       nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			manager := &testinformer.FakeManager{}
			detector := &ChangeDetector{InformerManager: manager}
			if tc.controller != nil {
				detector.AnnotationPlacementController = tc.controller
			}

			detector.watchGeneratedPolicies()

			// APIResourceMeta has unexported bookkeeping fields; comparing the comparable value as a
			// whole covers the exported identity without reaching into them.
			if diff := cmp.Diff(manager.StaticResources, tc.want, cmpopts.EquateEmpty(), cmpopts.EquateComparable(informer.APIResourceMeta{})); diff != "" {
				t.Errorf("watchGeneratedPolicies() registered resources mismatch (-got, +want):\n%s", diff)
			}
		})
	}
}
