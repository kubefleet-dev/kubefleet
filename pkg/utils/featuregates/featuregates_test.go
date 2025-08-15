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

package featuregates

import (
	"flag"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	k8sflag "k8s.io/component-base/cli/flag"
	"k8s.io/utils/ptr"
)

const (
	featureName1 = "foo"
	featureName2 = "bar"
	featureName3 = "baz"
)

var (
	featureSpecDefaultToTrue  = FeatureSpec{Default: true}
	featureSpecDefaultToFalse = FeatureSpec{Default: false}
)

// TestAdd tests the Add method of mutableFeatureGate.
func TestAdd(t *testing.T) {
	testCases := []struct {
		name                   string
		fg                     *mutableFeatureGate
		features               map[FeatureName]FeatureSpec
		wantRegisteredFeatures map[FeatureName]FeatureSpec
		wantErrPrefix          string
	}{
		{
			name: "feature gate already closed",
			fg: &mutableFeatureGate{
				closed:             true,
				registeredFeatures: map[FeatureName]FeatureSpec{},
				MapStringBool:      k8sflag.NewMapStringBool(ptr.To(map[string]bool{})),
			},
			features: map[FeatureName]FeatureSpec{
				featureName1: featureSpecDefaultToTrue,
			},
			wantErrPrefix:          "cannot add features to the feature gate as it has been closed",
			wantRegisteredFeatures: map[FeatureName]FeatureSpec{},
		},
		{
			name: "add a new feature",
			fg: &mutableFeatureGate{
				closed:             false,
				registeredFeatures: map[FeatureName]FeatureSpec{},
				MapStringBool:      k8sflag.NewMapStringBool(ptr.To(map[string]bool{})),
			},
			features: map[FeatureName]FeatureSpec{
				featureName1: featureSpecDefaultToFalse,
			},
			wantRegisteredFeatures: map[FeatureName]FeatureSpec{
				featureName1: featureSpecDefaultToFalse,
			},
		},
		{
			name: "add multiple new features",
			fg: &mutableFeatureGate{
				closed:             false,
				registeredFeatures: map[FeatureName]FeatureSpec{},
				MapStringBool:      k8sflag.NewMapStringBool(ptr.To(map[string]bool{})),
			},
			features: map[FeatureName]FeatureSpec{
				featureName1: featureSpecDefaultToTrue,
				featureName2: featureSpecDefaultToFalse,
				featureName3: featureSpecDefaultToTrue,
			},
			wantRegisteredFeatures: map[FeatureName]FeatureSpec{
				featureName1: featureSpecDefaultToTrue,
				featureName2: featureSpecDefaultToFalse,
				featureName3: featureSpecDefaultToTrue,
			},
		},
		{
			name: "add a feature that already exists",
			fg: &mutableFeatureGate{
				closed: false,
				registeredFeatures: map[FeatureName]FeatureSpec{
					featureName1: featureSpecDefaultToTrue,
				},
				MapStringBool: k8sflag.NewMapStringBool(ptr.To(map[string]bool{})),
			},
			features: map[FeatureName]FeatureSpec{
				featureName1: featureSpecDefaultToFalse,
				featureName2: featureSpecDefaultToTrue,
			},
			wantErrPrefix: "feature foo has been added to the feature gate",
			wantRegisteredFeatures: map[FeatureName]FeatureSpec{
				featureName1: featureSpecDefaultToTrue,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.fg.Add(tc.features)

			// Compare the registered features any way to ensure correctness.
			registeredFeatures := tc.fg.registeredFeatures
			if diff := cmp.Diff(registeredFeatures, tc.wantRegisteredFeatures); diff != "" {
				t.Errorf("Add() registered features mismatch (-got, +want):\n%s", diff)
			}

			if tc.wantErrPrefix != "" {
				if err == nil {
					t.Fatalf("Add() = nil, want error with prefix %s", tc.wantErrPrefix)
				}

				if !strings.HasPrefix(err.Error(), tc.wantErrPrefix) {
					t.Fatalf("Add() = %s, want error with prefix %s", err, tc.wantErrPrefix)
				}
				return
			}

			if err != nil {
				t.Fatalf("Add() = %v, want nil", err)
			}
		})
	}
}

// TestIsEnabled tests the IsEnabled method of mutableFeatureGate.
func TestIsEnabled(t *testing.T) {
	testCases := []struct {
		name          string
		fg            *mutableFeatureGate
		fn            FeatureName
		wantIsEnabled bool
		wantErrPrefix string
	}{
		{
			name: "feature gate not closed yet",
			fg: &mutableFeatureGate{
				closed:             false,
				registeredFeatures: map[FeatureName]FeatureSpec{},
				MapStringBool:      k8sflag.NewMapStringBool(ptr.To(map[string]bool{})),
			},
			fn:            featureName1,
			wantIsEnabled: false,
			wantErrPrefix: "feature gate has not been closed yet; CLI arguments have not been parsed",
		},
		{
			name: "feature not registered",
			fg: &mutableFeatureGate{
				closed: true,
				registeredFeatures: map[FeatureName]FeatureSpec{
					featureName1: featureSpecDefaultToTrue,
				},
				MapStringBool: k8sflag.NewMapStringBool(ptr.To(map[string]bool{})),
			},
			fn:            featureName2,
			wantIsEnabled: false,
			wantErrPrefix: "feature bar has not been registered yet",
		},
		{
			name: "feature enabled via CLI arguments",
			fg: &mutableFeatureGate{
				closed: true,
				registeredFeatures: map[FeatureName]FeatureSpec{
					featureName1: featureSpecDefaultToFalse,
				},
				MapStringBool: k8sflag.NewMapStringBool(ptr.To(map[string]bool{
					string(featureName1): true,
				})),
			},
			fn:            featureName1,
			wantIsEnabled: true,
		},
		{
			name: "feature enabled via default value",
			fg: &mutableFeatureGate{
				closed: true,
				registeredFeatures: map[FeatureName]FeatureSpec{
					featureName1: featureSpecDefaultToTrue,
				},
				MapStringBool: k8sflag.NewMapStringBool(ptr.To(map[string]bool{})),
			},
			fn:            featureName1,
			wantIsEnabled: true,
		},
		{
			name: "feature disabled via CLI arguments",
			fg: &mutableFeatureGate{
				closed: true,
				registeredFeatures: map[FeatureName]FeatureSpec{
					featureName1: featureSpecDefaultToTrue,
				},
				MapStringBool: k8sflag.NewMapStringBool(ptr.To(map[string]bool{
					string(featureName1): false,
				})),
			},
			fn:            featureName1,
			wantIsEnabled: false,
		},
		{
			name: "feature disabled via default value",
			fg: &mutableFeatureGate{
				closed: true,
				registeredFeatures: map[FeatureName]FeatureSpec{
					featureName1: featureSpecDefaultToFalse,
				},
				MapStringBool: k8sflag.NewMapStringBool(ptr.To(map[string]bool{})),
			},
			fn:            featureName1,
			wantIsEnabled: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			isEnabled, err := tc.fg.IsEnabled(tc.fn)

			if tc.wantErrPrefix != "" {
				if err == nil {
					t.Fatalf("IsEnabled() = nil, want error with prefix %s", tc.wantErrPrefix)
				}

				if !strings.HasPrefix(err.Error(), tc.wantErrPrefix) {
					t.Fatalf("IsEnabled() = %s, want error with prefix %s", err, tc.wantErrPrefix)
				}
				return
			}

			if err != nil {
				t.Fatalf("IsEnabled() = %s, want nil", err)
			}

			if isEnabled != tc.wantIsEnabled {
				t.Errorf("IsEnabled() = %v, want %v", isEnabled, tc.wantIsEnabled)
			}
		})
	}
}

func TestParse(t *testing.T) {
	testCases := []struct {
		name                   string
		cliArgs                []string
		registeredFeatures     map[FeatureName]FeatureSpec
		fns                    []FeatureName
		wantIsEnabledForAllFns map[FeatureName]bool
	}{
		{
			name:               "no feature gates specified",
			cliArgs:            []string{},
			registeredFeatures: map[FeatureName]FeatureSpec{},
		},
		{
			name:    "single feature gate specified (enabled)",
			cliArgs: []string{"--features-gates=foo=true"},
			registeredFeatures: map[FeatureName]FeatureSpec{
				"foo": {
					Default: false,
				},
			},
			fns: []FeatureName{"foo"},
			wantIsEnabledForAllFns: map[FeatureName]bool{
				"foo": true,
			},
		},
		{
			name:    "single feature gate specified (disabled)",
			cliArgs: []string{"--features-gates=foo=false"},
			registeredFeatures: map[FeatureName]FeatureSpec{
				"foo": {
					Default: true,
				},
			},
			fns: []FeatureName{"foo"},
			wantIsEnabledForAllFns: map[FeatureName]bool{
				"foo": false,
			},
		},
		{
			name: "multiple feature gates specified",
			cliArgs: []string{
				"--features-gates=foo=true,bar=false,baz=true",
			},
			registeredFeatures: map[FeatureName]FeatureSpec{
				"foo": {
					Default: false,
				},
				"bar": {
					Default: true,
				},
				"baz": {
					Default: false,
				},
			},
			fns: []FeatureName{"foo", "bar", "baz"},
			wantIsEnabledForAllFns: map[FeatureName]bool{
				"foo": true,
				"bar": false,
				"baz": true,
			},
		},
		{
			name: "repeated args",
			cliArgs: []string{
				"--features-gates=foo=true",
				"--features-gates=bar=false",
			},
			registeredFeatures: map[FeatureName]FeatureSpec{
				"foo": {
					Default: false,
				},
				"bar": {
					Default: true,
				},
			},
			fns: []FeatureName{"foo", "bar"},
			wantIsEnabledForAllFns: map[FeatureName]bool{
				"foo": true,
				"bar": false,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet(tc.name, flag.ContinueOnError)
			featureGates := NewFeatureGate()
			if err := featureGates.Add(tc.registeredFeatures); err != nil {
				t.Fatalf("Failed to add features: %v", err)
			}

			featureGates.RegisterAsCLIArgument(fs, "features-gates", "A comma-separated list of features to enable/disable; the format is <feature-name>=<true|false> (e.g., 'foo=true,bar=false').")
			if err := fs.Parse(tc.cliArgs); err != nil {
				t.Fatalf("Failed to parse CLI arguments: %v", err)
			}

			isEnabledForAllFns := make(map[FeatureName]bool)
			for _, fn := range tc.fns {
				isEnabled, err := featureGates.IsEnabled(fn)
				if err != nil {
					t.Errorf("IsEnabled(%s) = %s, want no error", fn, err)
					continue
				}
				isEnabledForAllFns[fn] = isEnabled
			}

			if diff := cmp.Diff(isEnabledForAllFns, tc.wantIsEnabledForAllFns, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("isEnabledForAllFns mismatches (-got, +want):\n%s", diff)
			}
		})
	}
}
