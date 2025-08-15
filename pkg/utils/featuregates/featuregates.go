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
	"fmt"
	"sync"

	k8sflag "k8s.io/component-base/cli/flag"
)

var (
	MemberAgentFeatureGate MutableFeatureGate = NewFeatureGate()
)

// FeatureName is the name of a feature.
type FeatureName string

// FeatureSpec is the enablement setup for a feature.
type FeatureSpec struct {
	// Default is the default enablement state of the feature.
	Default bool
}

// MutableFeatureGate is an interface for mutable features gates that allows adding new features,
// checking if a feature is enabled, and registering a feature gate as a global CLI argument.
type MutableFeatureGate interface {
	// IsEnabled checks if a feature has been enabled.
	IsEnabled(fn FeatureName) (bool, error)

	// Add adds new features to the feature gate.
	Add(features map[FeatureName]FeatureSpec) error

	// RegisterAsCLIArgument registers the feature gate as a CLI argument in a flag set.
	RegisterAsCLIArgument(flagSet *flag.FlagSet, argName string, usage string)
}

// mutableFeatureGate is an implementation of MutableFeatureGate.
type mutableFeatureGate struct {
	// MapStringBool is a Kubernetes implementation of a flag Value that supports
	// multiple comma-separated key-value pairs as CLI arguments.
	//
	// Note (chenyu1):
	*k8sflag.MapStringBool

	closed             bool
	registeredFeatures map[FeatureName]FeatureSpec

	mu sync.Mutex
}

// NewFeatureGate returns a mutableFeatureGate that implements MutableFeatureGate.
func NewFeatureGate() MutableFeatureGate {
	m := &map[string]bool{}
	return &mutableFeatureGate{
		MapStringBool:      k8sflag.NewMapStringBool(m),
		registeredFeatures: make(map[FeatureName]FeatureSpec),
	}
}

// Add adds new features to the feature gate.
func (mfg *mutableFeatureGate) Add(features map[FeatureName]FeatureSpec) error {
	mfg.mu.Lock()
	defer mfg.mu.Unlock()

	if mfg.closed {
		return fmt.Errorf("cannot add features to the feature gate as it has been closed")
	}

	for name := range features {
		if _, exists := mfg.registeredFeatures[name]; exists {
			// Fail all additions if any of them already exists.
			return fmt.Errorf("feature %s has been added to the feature gate", name)
		}
	}
	for name, spec := range features {
		mfg.registeredFeatures[name] = spec
	}
	return nil
}

// IsEnabled checks if a feature has been enabled.
func (mfg *mutableFeatureGate) IsEnabled(fn FeatureName) (bool, error) {
	mfg.mu.Lock()
	defer mfg.mu.Unlock()

	if !mfg.closed {
		return false, fmt.Errorf("feature gate has not been closed yet; CLI arguments have not been parsed")
	}

	featureSpec, ok := mfg.registeredFeatures[fn]
	if !ok {
		return false, fmt.Errorf("feature %s has not been registered yet", fn)
	}
	m := *mfg.MapStringBool.Map
	isFeatureEnabled, found := m[string(fn)]
	if !found {
		return featureSpec.Default, nil
	}
	return isFeatureEnabled, nil
}

// RegisterAsCLIArgument registers the feature gate as a CLI argument in a flag set.
func (mfg *mutableFeatureGate) RegisterAsCLIArgument(fs *flag.FlagSet, argName string, usage string) {
	mfg.mu.Lock()
	defer mfg.mu.Unlock()

	fs.Var(mfg, argName, usage)
	mfg.closed = true
}
