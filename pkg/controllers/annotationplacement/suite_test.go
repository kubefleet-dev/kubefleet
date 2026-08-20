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

package annotationplacement

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/informer"
)

var (
	configMapGVK = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	configMapGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}
)

var (
	testEnv         *envtest.Environment
	hubClient       client.Client
	informerManager informer.Manager
	reconciler      *Reconciler
	eventRecorder   *record.FakeRecorder
	ctx             context.Context
	cancel          context.CancelFunc
)

// The GVRs the informer manager is told to watch. A resource of a kind with no informer never
// reaches the reconciler at all, so these are exactly the kinds these tests may annotate.
var (
	configMapAPIResource = informer.APIResourceMeta{
		GroupVersionKind:     configMapGVK,
		GroupVersionResource: configMapGVR,
	}
	namespaceAPIResource = informer.APIResourceMeta{
		GroupVersionKind:     namespaceGVK,
		GroupVersionResource: namespaceGVR,
		IsClusterScoped:      true,
	}
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Annotation Based Placement Controller Suite")
}

var _ = BeforeSuite(func() {
	ctx, cancel = context.WithCancel(context.TODO())

	By("bootstrapping the test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("../../../", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := testEnv.Start()
	Expect(err).Should(Succeed())
	Expect(cfg).NotTo(BeNil())

	Expect(kfplacementv1alpha1.AddToScheme(scheme.Scheme)).Should(Succeed())

	hubClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).Should(Succeed())

	dynamicClient, err := dynamic.NewForConfig(cfg)
	Expect(err).Should(Succeed())

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
	Expect(err).Should(Succeed())
	groupResources, err := restmapper.GetAPIGroupResources(discoveryClient)
	Expect(err).Should(Succeed())
	restMapper := restmapper.NewDiscoveryRESTMapper(groupResources)

	By("starting the informers the reconciler reads annotated resources from")
	informerManager = informer.NewInformerManager(dynamicClient, 5*time.Minute, ctx.Done())
	informerManager.CreateInformerForResource(configMapAPIResource)
	informerManager.CreateInformerForResource(namespaceAPIResource)
	informerManager.Start()
	informerManager.WaitForCacheSync()

	// The recorder is buffered generously: an unread event blocks the reconciler that records it,
	// which would surface as a timeout somewhere unrelated rather than as a failed expectation.
	eventRecorder = record.NewFakeRecorder(100)
	reconciler = &Reconciler{
		Client: hubClient,
		// The envtest client reads straight from the API server, so it serves as the uncached reader
		// too; there is no separate cache to fall behind here.
		UncachedReader:  hubClient,
		RestMapper:      restMapper,
		InformerManager: informerManager,
		Recorder:        eventRecorder,
	}
})

var _ = AfterSuite(func() {
	defer func() {
		Expect(testEnv.Stop()).Should(Succeed())
	}()
	cancel()
})
