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

package ociartifactcachedlocalfsstore

import (
	"context"
	"sync"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kubefleet-dev/kubefleet/pkg/reimagined/ociartifactconnector"
	kerrors "github.com/kubefleet-dev/kubefleet/pkg/utils/errors"
)

type Manager struct {
	stores map[string]*Store
	mu     sync.RWMutex

	workDir string
}

func NewManager(workDir string) *Manager {
	return &Manager{
		stores:  make(map[string]*Store),
		workDir: workDir,
	}
}

func (m *Manager) GetStore(digest string) (*Store, error) {
	// As a shortcut, check if a store has been created for the given digest; if so, return it immediately.
	m.mu.RLock()
	store, found := m.stores[digest]
	m.mu.RUnlock()

	if found {
		return store, nil
	}

	// No store has been created yet; create a new store for the given digest.
	m.mu.Lock()
	defer m.mu.Unlock()
	// Check again if a store has been created in-between the two lock attempts; if so, return it.
	store, found = m.stores[digest]
	if found {
		return store, nil
	}

	store, err := NewStore(m.workDir, digest)
	if err != nil {
		return nil, kerrors.Wraps(err, "failed to create a new store for the given digest")
	}
	m.stores[digest] = store
	return store, nil
}

func (m *Manager) GetManifests(ctx context.Context, digest string, key *string, connector ociartifactconnector.OCIArtifactConnector, path string) ([]runtime.RawExtension, error) {
	store, err := m.GetStore(digest)
	if err != nil {
		return nil, kerrors.Wraps(err, "failed to get the store for the given digest")
	}

	return store.GetManifests(ctx, key, connector, path)
}
