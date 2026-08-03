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
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/google/go-cmp/cmp"
	"github.com/kubefleet-dev/kubefleet/pkg/reimagined/ociartifactconnector"
	localregistry "github.com/kubefleet-dev/kubefleet/test/oci"
)

var (
	ctx       context.Context
	outputDir string

	connector ociartifactconnector.OCIArtifactConnector
)

var (
	cred = ociartifactconnector.NewStaticRemoteOCIRepositoryCredential(
		localregistry.RegistryURL, localregistry.RegistryUsername, localregistry.RegistryPassword)
)

func TestMain(m *testing.M) {
	ctx = context.Background()

	if err := localregistry.BootstrapLocalRegistry(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to bootstrap local registry: %v\n", err)

		if err := localregistry.TearDownLocalRegistry(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to tear down local registry: %v\n", err)
		}
		os.Exit(1)
	}

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		fmt.Fprintln(os.Stderr, "failed to resolve current test file path")
		os.Exit(1)
	}

	fileDir := filepath.Dir(currentFile)
	// Make a `.staging` sub-directory under the current directory to keep the extracted manifests.
	outputDir = filepath.Join(fileDir, ".staging")
	if err := os.Mkdir(outputDir, 0700); err != nil && !os.IsExist(err) {
		fmt.Fprintf(os.Stderr, "failed to create staging output directory: %v\n", err)
		os.Exit(1)
	}

	var err error
	connector, err = ociartifactconnector.NewRemoteConnectorFromCredential(localregistry.RawManifestsArtifactURL, cred, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create remote connector: %v\n", err)
		//_ = execute(stopScript)
		os.Exit(1)
	}

	exitCode := m.Run()

	// Remove the `.staging` sub-directory after the tests are done.
	if err := os.RemoveAll(outputDir); err != nil {
		fmt.Fprintf(os.Stderr, "failed to remove staging output directory: %v\n", err)
		exitCode = 1
	}

	if err := localregistry.TearDownLocalRegistry(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to tear down local registry: %v\n", err)
	}

	os.Exit(exitCode)
}

func TestStoreGetManifests_Raw(t *testing.T) {
	digest, err := localregistry.Resolve(localregistry.RawManifestsArtifactURL, localregistry.RawManifestsArtifactTag)
	if err != nil {
		t.Fatalf("failed to resolve artifact digest: %v", err)
	}
	store, err := NewStore(outputDir, digest)
	if err != nil {
		t.Fatalf("NewStore() = %v, want no error", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close() = %v, want no error", err)
		}
	})

	manifests, err := store.GetManifests(ctx, ptr.To(""), connector, ".")
	if err != nil {
		t.Fatalf("GetManifests() = %v, want no error", err)
	}

	if !cmp.Equal(len(manifests), 3) {
		t.Fatalf("GetManifests() len() = %d, want %d", len(manifests), 3)
	}

	cm := &corev1.ConfigMap{}
	deploy := &appsv1.Deployment{}
	ns := &corev1.Namespace{}

	// The GetManifests method should return manifests in path-based lexical order.
	if err := json.Unmarshal(manifests[0].Raw, cm); err != nil {
		t.Fatalf("failed to unmarshal manifest 0 into a Kubernetes Namespace: %v", err)
	}
	if err := json.Unmarshal(manifests[1].Raw, deploy); err != nil {
		t.Fatalf("failed to unmarshal manifest 1 into a Kubernetes ConfigMap: %v", err)
	}
	if err := json.Unmarshal(manifests[2].Raw, ns); err != nil {
		t.Fatalf("failed to unmarshal manifest 2 into a Kubernetes Deployment: %v", err)
	}

	wantNS := &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Namespace",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "work",
		},
	}
	wantCM := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web",
			Namespace: "work",
		},
		Data: map[string]string{
			"foo": "bar",
		},
	}
	wantDeploy := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx",
			Namespace: "work",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(1)),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "nginx",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "nginx",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx:stable",
							Ports: []corev1.ContainerPort{
								{ContainerPort: 80},
							},
						},
					},
				},
			},
		},
	}
	if diff := cmp.Diff(cm, wantCM); diff != "" {
		t.Errorf("ConfigMap manifest mismatch (-got, +want):\n%s", diff)
	}
	if diff := cmp.Diff(deploy, wantDeploy); diff != "" {
		t.Errorf("Deployment manifest mismatch (-got, +want):\n%s", diff)
	}
	if diff := cmp.Diff(ns, wantNS); diff != "" {
		t.Errorf("Namespace manifest mismatch (-got, +want):\n%s", diff)
	}
}

func TestStoreGetManifests_Kustomize(t *testing.T) {
	digest, err := localregistry.Resolve(localregistry.KustomizeArtifactURL, localregistry.KustomizeArtifactTag)
	if err != nil {
		t.Fatalf("failed to resolve artifact digest: %v", err)
	}
	kustomizeConnector, err := ociartifactconnector.NewRemoteConnectorFromCredential(localregistry.KustomizeArtifactURL, cred, true)
	if err != nil {
		t.Fatalf("NewRemoteConnectorFromCredential() = %v, want no error", err)
	}
	store, err := NewStore(outputDir, digest)
	if err != nil {
		t.Fatalf("NewStore() = %v, want no error", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close() = %v, want no error", err)
		}
	})

	manifests, err := store.GetManifests(ctx, nil, kustomizeConnector, "./overlays/team-red")
	if err != nil {
		t.Fatalf("GetManifests() = %v, want no error", err)
	}

	if !cmp.Equal(len(manifests), 3) {
		t.Fatalf("GetManifests() len() = %d, want %d", len(manifests), 3)
	}

	// Kustomize does not guarantee a stable output ordering, so sort the manifests by their
	// raw bytes to get a consistent order before unmarshaling. Any consistent order works.
	sort.Slice(manifests, func(i, j int) bool {
		return bytes.Compare(manifests[i].Raw, manifests[j].Raw) < 0
	})

	ns := &corev1.Namespace{}
	cm := &corev1.ConfigMap{}
	deploy := &appsv1.Deployment{}

	for i := range manifests {
		typeMeta := &metav1.TypeMeta{}
		if err := json.Unmarshal(manifests[i].Raw, typeMeta); err != nil {
			t.Fatalf("failed to unmarshal manifest %d into a TypeMeta: %v", i, err)
		}
		switch typeMeta.Kind {
		case "Namespace":
			if err := json.Unmarshal(manifests[i].Raw, ns); err != nil {
				t.Fatalf("failed to unmarshal manifest %d into a Kubernetes Namespace: %v", i, err)
			}
		case "ConfigMap":
			if err := json.Unmarshal(manifests[i].Raw, cm); err != nil {
				t.Fatalf("failed to unmarshal manifest %d into a Kubernetes ConfigMap: %v", i, err)
			}
		case "Deployment":
			if err := json.Unmarshal(manifests[i].Raw, deploy); err != nil {
				t.Fatalf("failed to unmarshal manifest %d into a Kubernetes Deployment: %v", i, err)
			}
		default:
			t.Fatalf("unexpected manifest kind %q in manifest %d", typeMeta.Kind, i)
		}
	}

	// The team-red overlay composes the base (Namespace + Deployment) with the overlay ConfigMap
	// and applies the common label team=red to every resource.
	wantNS := &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Namespace",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "work",
			Labels: map[string]string{
				"team": "red",
			},
		},
	}
	wantCM := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web",
			Namespace: "work",
			Labels: map[string]string{
				"team": "red",
			},
		},
		Data: map[string]string{
			"foo": "bar",
		},
	}
	wantDeploy := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx",
			Namespace: "work",
			Labels: map[string]string{
				"team": "red",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(1)),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "nginx",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "nginx",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx:stable",
							Ports: []corev1.ContainerPort{
								{ContainerPort: 80},
							},
						},
					},
				},
			},
		},
	}
	if diff := cmp.Diff(ns, wantNS); diff != "" {
		t.Errorf("Namespace manifest mismatch (-got, +want):\n%s", diff)
	}
	if diff := cmp.Diff(cm, wantCM); diff != "" {
		t.Errorf("ConfigMap manifest mismatch (-got, +want):\n%s", diff)
	}
	if diff := cmp.Diff(deploy, wantDeploy); diff != "" {
		t.Errorf("Deployment manifest mismatch (-got, +want):\n%s", diff)
	}

	// The team-blue overlay composes only the base (Namespace + Deployment) and applies the
	// common label team=blue to every resource; it does not include the overlay ConfigMap.
	blueManifests, err := store.GetManifests(ctx, nil, kustomizeConnector, "./overlays/team-blue")
	if err != nil {
		t.Fatalf("GetManifests() = %v, want no error", err)
	}

	if !cmp.Equal(len(blueManifests), 2) {
		t.Fatalf("GetManifests() len() = %d, want %d", len(blueManifests), 2)
	}

	sort.Slice(blueManifests, func(i, j int) bool {
		return bytes.Compare(blueManifests[i].Raw, blueManifests[j].Raw) < 0
	})

	blueNS := &corev1.Namespace{}
	blueDeploy := &appsv1.Deployment{}

	for i := range blueManifests {
		typeMeta := &metav1.TypeMeta{}
		if err := json.Unmarshal(blueManifests[i].Raw, typeMeta); err != nil {
			t.Fatalf("failed to unmarshal manifest %d into a TypeMeta: %v", i, err)
		}
		switch typeMeta.Kind {
		case "Namespace":
			if err := json.Unmarshal(blueManifests[i].Raw, blueNS); err != nil {
				t.Fatalf("failed to unmarshal manifest %d into a Kubernetes Namespace: %v", i, err)
			}
		case "Deployment":
			if err := json.Unmarshal(blueManifests[i].Raw, blueDeploy); err != nil {
				t.Fatalf("failed to unmarshal manifest %d into a Kubernetes Deployment: %v", i, err)
			}
		default:
			t.Fatalf("unexpected manifest kind %q in manifest %d", typeMeta.Kind, i)
		}
	}

	wantBlueNS := &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Namespace",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "work",
			Labels: map[string]string{
				"team": "blue",
			},
		},
	}
	wantBlueDeploy := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx",
			Namespace: "work",
			Labels: map[string]string{
				"team": "blue",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(1)),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "nginx",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "nginx",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx:stable",
							Ports: []corev1.ContainerPort{
								{ContainerPort: 80},
							},
						},
					},
				},
			},
		},
	}
	if diff := cmp.Diff(blueNS, wantBlueNS); diff != "" {
		t.Errorf("Namespace manifest mismatch (-got, +want):\n%s", diff)
	}
	if diff := cmp.Diff(blueDeploy, wantBlueDeploy); diff != "" {
		t.Errorf("Deployment manifest mismatch (-got, +want):\n%s", diff)
	}
}
