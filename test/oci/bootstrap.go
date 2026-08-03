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

package oci

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	RegistryURL      = "localhost:9000"
	RegistryUsername = "admin"
	RegistryPassword = "testonly"

	RawManifestsArtifactURL = "localhost:9000/testdata/manifests"
	RawManifestsArtifactTag = "latest"

	KustomizeArtifactURL = "localhost:9000/testdata/kustomize"
	KustomizeArtifactTag = "latest"
)

func BootstrapLocalRegistry() error {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		return fmt.Errorf("failed to resolve current test file path")
	}

	scriptDir := filepath.Dir(currentFile)
	setupScript := filepath.Join(scriptDir, "setup.sh")

	if err := runScript(setupScript); err != nil {
		return fmt.Errorf("failed to run setup script %s: %v", setupScript, err)
	}
	return nil
}

func TearDownLocalRegistry() error {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		return fmt.Errorf("failed to resolve current test file path")
	}

	scriptDir := filepath.Dir(currentFile)
	stopScript := filepath.Join(scriptDir, "stop.sh")

	if err := runScript(stopScript); err != nil {
		return fmt.Errorf("failed to run stop script %s: %v", stopScript, err)
	}
	return nil
}

func Resolve(artifactURL, artifactTag string) (string, error) {
	cmd := exec.Command("oras", "resolve", fmt.Sprintf("%s:%s", artifactURL, artifactTag))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to resolve artifact %s:%s: %v", artifactURL, artifactTag, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func runScript(path string) error {
	cmd := exec.Command("bash", path)
	cmd.Dir = filepath.Dir(path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
