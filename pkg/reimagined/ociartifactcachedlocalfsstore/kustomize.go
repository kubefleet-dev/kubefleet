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
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	kfs "sigs.k8s.io/kustomize/kyaml/filesys"

	"github.com/kubefleet-dev/kubefleet/pkg/utils/errors"
)

func (s *Store) prepareKustomizeManifests(kustomizeConfigPath string) ([]runtime.RawExtension, error) {
	adaptedFS := &KYAMLFSAdapter{root: s.rootFS}
	m, err := s.kustomizer.Run(adaptedFS, kustomizeConfigPath)
	if err != nil {
		return nil, errors.Wraps(err, "failed to run kustomize builder")
	}
	rawData, err := m.AsYaml()
	if err != nil {
		return nil, errors.Wraps(err, "failed to convert kustomize output to YAML")
	}

	manifests := []runtime.RawExtension{}

	rawDataReader := bytes.NewReader(rawData)
	decoder := utilyaml.NewYAMLOrJSONDecoder(rawDataReader, decoderBufferSize)
	for {
		unstructuredObj := &unstructured.Unstructured{}
		if decErr := decoder.Decode(unstructuredObj); decErr != nil {
			if decErr == io.EOF {
				break
			}
			return nil, errors.Wraps(decErr, "failed to decode the manifest file")
		}

		// Skip empty documents (e.g., blank documents or documents with comments only).
		if len(unstructuredObj.Object) == 0 {
			continue
		}

		raw, err := unstructuredObj.MarshalJSON()
		if err != nil {
			return nil, errors.Wraps(err, "failed to marshal the manifest into JSON")
		}
		manifests = append(manifests, runtime.RawExtension{Raw: raw})
	}

	return manifests, nil
}

type KYAMLFSAdapter struct {
	root *os.Root
}

var _ kfs.FileSystem = &KYAMLFSAdapter{}

func (a *KYAMLFSAdapter) Create(path string) (kfs.File, error) {
	return a.root.Create(path)
}

func (a *KYAMLFSAdapter) Mkdir(path string) error {
	return a.root.Mkdir(path, 0700)
}

func (a *KYAMLFSAdapter) MkdirAll(path string) error {
	return a.root.MkdirAll(path, 0700)
}

func (a *KYAMLFSAdapter) RemoveAll(path string) error {
	return a.root.RemoveAll(path)
}

func (a *KYAMLFSAdapter) Open(path string) (kfs.File, error) {
	return a.root.Open(path)
}

func (a *KYAMLFSAdapter) IsDir(path string) bool {
	info, err := a.root.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func (a *KYAMLFSAdapter) ReadDir(path string) ([]string, error) {
	f, err := a.root.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	entries, err := f.ReadDir(-1)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

func (a *KYAMLFSAdapter) CleanedAbs(p string) (kfs.ConfirmedDir, string, error) {
	cleaned := path.Clean(strings.Trim(p, "/"))
	if cleaned == "" {
		cleaned = "."
	}
	info, err := a.root.Stat(cleaned)
	if err != nil {
		return kfs.ConfirmedDir(""), "", fmt.Errorf("path does not exist: %s", p)
	}
	if info.IsDir() {
		return kfs.ConfirmedDir(cleaned), "", nil
	}
	dir := path.Dir(cleaned)
	dInfo, err := a.root.Stat(dir)
	if err != nil || !dInfo.IsDir() {
		return kfs.ConfirmedDir(""), "", fmt.Errorf("path %q is not in or below a directory", p)
	}
	return kfs.ConfirmedDir(dir), path.Base(cleaned), nil
}

func (a *KYAMLFSAdapter) Exists(path string) bool {
	_, err := a.root.Stat(path)
	return err == nil
}

func (a *KYAMLFSAdapter) Glob(pattern string) ([]string, error) {
	rootFS := a.root.FS()
	var allFiles []string
	err := fs.WalkDir(rootFS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		match, err := path.Match(pattern, p)
		if err != nil {
			return err
		}
		if match {
			allFiles = append(allFiles, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	var result []string
	if kfs.IsHiddenFilePath(pattern) {
		result = allFiles
	} else {
		result = kfs.RemoveHiddenFiles(allFiles)
	}
	sort.Strings(result)
	return result, nil
}

func (a *KYAMLFSAdapter) ReadFile(path string) ([]byte, error) {
	return a.root.ReadFile(path)
}

func (a *KYAMLFSAdapter) WriteFile(path string, data []byte) error {
	return a.root.WriteFile(path, data, 0600)
}

func (a *KYAMLFSAdapter) Walk(path string, walkFn filepath.WalkFunc) error {
	walkRoot := strings.Trim(path, "/")
	if walkRoot == "" {
		walkRoot = "."
	}

	rootFS := a.root.FS()
	return fs.WalkDir(rootFS, walkRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return walkFn(p, nil, err)
		}
		info, ierr := d.Info()
		return walkFn(p, info, ierr)
	})
}
