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
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"oras.land/oras-go/v2"
	orasfile "oras.land/oras-go/v2/content/file"
	"sigs.k8s.io/kustomize/api/krusty"

	"github.com/kubefleet-dev/kubefleet/pkg/reimagined/ociartifactconnector"
	kerrors "github.com/kubefleet-dev/kubefleet/pkg/utils/errors"
)

// decoderBufferSize is the buffer size (in bytes) used when decoding YAML manifest files.
const (
	decoderBufferSize = 4096
)

var _ oras.Target = &Store{}

type Store struct {
	digest string

	orasFileStore *orasfile.Store

	rootFS   *os.Root
	rootPath string

	kustomizer *krusty.Kustomizer

	pulled    bool
	pullMutex sync.RWMutex

	// TO-DO: use a path trie to index the manifests and avoid the I/O scanning overhead.

	generated map[string][]runtime.RawExtension
	genMutex  sync.RWMutex

	creationTimestamp time.Time
}

// Fetch implements the oras.Target interface.
func (s *Store) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	return s.orasFileStore.Fetch(ctx, target)
}

// Push implements the oras.Target interface.
func (s *Store) Push(ctx context.Context, expected ocispec.Descriptor, content io.Reader) error {
	return s.orasFileStore.Push(ctx, expected, content)
}

// Exists implements the oras.Target interface.
func (s *Store) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	return s.orasFileStore.Exists(ctx, target)
}

// Resolve implements the oras.Target interface.
func (s *Store) Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error) {
	return s.orasFileStore.Resolve(ctx, reference)
}

// Tag implements the oras.Target interface.
func (s *Store) Tag(ctx context.Context, desc ocispec.Descriptor, reference string) error {
	return s.orasFileStore.Tag(ctx, desc, reference)
}

func (s *Store) Close() error {
	orasErr := s.orasFileStore.Close()
	rootFSErr := s.rootFS.Close()

	if orasErr != nil {
		orasErr = kerrors.Wraps(orasErr, "failed to close the ORAS file store")
	}
	if rootFSErr != nil {
		rootFSErr = kerrors.Wraps(rootFSErr, "failed to close the root filesystem")
	}

	return errors.Join(orasErr, rootFSErr)
}

func (s *Store) GetManifests(ctx context.Context, key *string, connector ociartifactconnector.OCIArtifactConnector, path string) ([]runtime.RawExtension, error) {
	// Check if the OCI artifact has already been pulled; if not, pull it first.
	s.pullMutex.RLock()
	pulled := s.pulled
	s.pullMutex.RUnlock()

	if !pulled {
		s.pullMutex.Lock()
		// Double-check if the OCI artifact has been pulled in-between the two lock attempts; if so, skip pulling.
		if !s.pulled {
			if _, err := connector.Pull(ctx, s.digest, s); err != nil {
				s.pullMutex.Unlock()
				return nil, kerrors.Wraps(err, "failed to pull the OCI artifact")
			}
			s.pulled = true
		}
		s.pullMutex.Unlock()
	}

	// Check if the manifests have already been generated given the key; if so, return them immediately.
	if key != nil {
		s.genMutex.RLock()
		manifests, found := s.generated[*key]
		s.genMutex.RUnlock()

		if found {
			return manifests, nil
		}
	}

	// No key is provided, or the manifests have never been generated for the given key.
	// Walk the directory tree and generate the manifests under the given path.
	s.genMutex.Lock()
	// Double-check if the manifests have been generated in-between the two lock attempts; if so, return them.
	if key != nil {
		manifests, found := s.generated[*key]
		if found {
			s.genMutex.Unlock()
			return manifests, nil
		}
	}

	walkRoot := filepath.Clean(path)

	// List all the files under the given path. If there are kustomization files
	// (`kustomization.yaml`, `kustomization.yml`, or `Kustomization`), run Kustomize to generate the manifests.
	rootFSys := s.rootFS.FS()
	for _, name := range []string{"kustomization.yaml", "kustomization.yml", "Kustomization"} {
		kustomizationPath := walkRoot + "/" + name
		if _, statErr := fs.Stat(rootFSys, kustomizationPath); statErr == nil {
			manifests, err := s.prepareKustomizeManifests(walkRoot)
			if err != nil {
				s.genMutex.Unlock()
				return nil, kerrors.Wraps(err, "failed to prepare the Kustomize manifests", "kustomizationPath", kustomizationPath)
			}

			// Cache the generated manifests for the given key so that subsequent calls can return them immediately.
			if key != nil {
				s.generated[*key] = manifests
			}
			s.genMutex.Unlock()

			return manifests, nil
		}
	}

	manifests := []runtime.RawExtension{}
	err := fs.WalkDir(rootFSys, walkRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		ext := filepath.Ext(p)
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}

		// Open the file, read its content, and decode it into unstructured K8s objects.
		//
		// Note that a single YAML file might hold multiple documents separated by "---"; each
		// document is decoded into its own unstructured object.
		f, err := rootFSys.Open(p)
		if err != nil {
			return kerrors.Wraps(err, "failed to open the manifest file", "path", p)
		}
		defer f.Close()

		decoder := utilyaml.NewYAMLOrJSONDecoder(f, decoderBufferSize)
		for {
			unstructuredObj := &unstructured.Unstructured{}
			if decErr := decoder.Decode(unstructuredObj); decErr != nil {
				if decErr == io.EOF {
					break
				}
				return kerrors.Wraps(decErr, "failed to decode the manifest file", "path", p)
			}

			// Skip empty documents (e.g., blank documents or documents with comments only).
			if len(unstructuredObj.Object) == 0 {
				continue
			}

			raw, err := unstructuredObj.MarshalJSON()
			if err != nil {
				return kerrors.Wraps(err, "failed to marshal the manifest into JSON", "path", p)
			}
			manifests = append(manifests, runtime.RawExtension{Raw: raw})
		}

		return nil
	})
	if err != nil {
		return nil, kerrors.Wraps(err, "failed to walk the directory tree and generate the manifests", "path", path)
	}

	// Cache the generated manifests for the given key so that subsequent calls can return them immediately.
	if key != nil {
		s.generated[*key] = manifests
	}
	s.genMutex.Unlock()

	return manifests, nil
}

func NewStore(workDir, digest string) (*Store, error) {
	path := strings.TrimRight(workDir, "/") + "/" + digest + "/"
	if err := os.MkdirAll(path, 0700); err != nil && !os.IsExist(err) {
		return nil, kerrors.Wraps(err, "failed to create the directory path")
	}

	rootFS, err := os.OpenRoot(path)
	if err != nil {
		return nil, kerrors.Wraps(err, "failed to open the directory path")
	}

	orasFileStore, err := orasfile.New(path)
	if err != nil {
		// rootFS was opened successfully above; close it to avoid leaking the file descriptor.
		_ = rootFS.Close()
		return nil, kerrors.Wraps(err, "failed to create the ORAS file store")
	}
	// Always disable path traversal; any write outside the work directory is blocked.
	orasFileStore.AllowPathTraversalOnWrite = false
	// Keep unnamed nodes (such as the image manifest itself, if not properly annotated) in the fallback memory store.
	orasFileStore.IgnoreNoName = false

	kustomizer := krusty.MakeKustomizer(krusty.MakeDefaultOptions())

	return &Store{
		digest:            digest,
		orasFileStore:     orasFileStore,
		rootFS:            rootFS,
		rootPath:          path,
		generated:         make(map[string][]runtime.RawExtension),
		creationTimestamp: time.Now(),
		kustomizer:        kustomizer,
	}, nil
}
