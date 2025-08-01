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
package clusterresourceplacement

import (
	"context"
	"flag"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/textlogger"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/cmd/hubagent/options"
	"github.com/kubefleet-dev/kubefleet/pkg/controllers/clusterresourcebindingwatcher"
	"github.com/kubefleet-dev/kubefleet/pkg/controllers/clusterresourceplacementwatcher"
	"github.com/kubefleet-dev/kubefleet/pkg/controllers/clusterschedulingpolicysnapshot"
	"github.com/kubefleet-dev/kubefleet/pkg/utils"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/controller"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/informer"
)

var (
	cfg       *rest.Config
	mgr       manager.Manager
	k8sClient client.Client
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
)

const (
	controllerName = "crp"
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "ClusterResourcePlacement Controller Suite")
}

var _ = BeforeSuite(func() {
	ctx, cancel = context.WithCancel(context.TODO())

	By("Setup klog")
	var err error
	fs := flag.NewFlagSet("klog", flag.ContinueOnError)
	klog.InitFlags(fs)
	Expect(fs.Parse([]string{"--v", "5", "-add_dir_header", "true"})).Should(Succeed())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("../../../", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err = testEnv.Start()
	Expect(err).Should(Succeed())
	Expect(cfg).NotTo(BeNil())

	err = placementv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).Should(Succeed())

	//+kubebuilder:scaffold:scheme
	By("construct the k8s client")
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).Should(Succeed())
	Expect(k8sClient).NotTo(BeNil())

	dynamicClient, err := dynamic.NewForConfig(cfg)
	Expect(err).Should(Succeed())
	Expect(dynamicClient).NotTo(BeNil())

	By("starting the controller manager")
	klog.InitFlags(flag.CommandLine)
	flag.Parse()

	mgr, err = ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
		Logger: textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(4))),
	})
	Expect(err).Should(Succeed(), "failed to create manager")

	reconciler := &Reconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		UncachedReader:  mgr.GetAPIReader(),
		Recorder:        mgr.GetEventRecorderFor(controllerName),
		RestMapper:      mgr.GetRESTMapper(),
		InformerManager: informer.NewInformerManager(dynamicClient, 5*time.Minute, ctx.Done()),
		ResourceConfig:  utils.NewResourceConfig(false),
		SkippedNamespaces: map[string]bool{
			"default": true,
		},
	}
	opts := options.RateLimitOptions{
		RateLimiterBaseDelay:  5 * time.Millisecond,
		RateLimiterMaxDelay:   60 * time.Second,
		RateLimiterQPS:        10,
		RateLimiterBucketSize: 100,
	}
	rateLimiter := options.DefaultControllerRateLimiter(opts)
	crpController := controller.NewController(controllerName, controller.NamespaceKeyFunc, reconciler.Reconcile, rateLimiter)

	// Set up the watchers
	err = (&clusterschedulingpolicysnapshot.Reconciler{
		Client:              mgr.GetClient(),
		PlacementController: crpController,
	}).SetupWithManagerForClusterSchedulingPolicySnapshot(mgr)
	Expect(err).Should(Succeed(), "failed to create clusterSchedulingPolicySnapshot watcher")

	err = (&clusterresourceplacementwatcher.Reconciler{
		PlacementController: crpController,
	}).SetupWithManagerForClusterResourcePlacement(mgr)
	Expect(err).Should(Succeed(), "failed to create clusterResourcePlacement watcher")

	err = (&clusterresourcebindingwatcher.Reconciler{
		Client:              mgr.GetClient(),
		PlacementController: crpController,
	}).SetupWithManagerForClusterResourceBinding(mgr)
	Expect(err).Should(Succeed(), "failed to create clusterResourceBinding watcher")

	ctx, cancel = context.WithCancel(context.TODO())
	// Run the controller manager
	go func() {
		defer GinkgoRecover()
		err := mgr.Start(ctx)
		Expect(err).Should(Succeed(), "failed to run manager")
	}()
	// Run the crp controller
	go func() {
		err := crpController.Run(ctx, 1)
		Expect(err).Should(Succeed(), "failed to run crp controller")
	}()

	By("By creating member clusters namespaces")
	ns := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: member1Namespace,
		},
	}
	Expect(k8sClient.Create(ctx, &ns)).Should(Succeed())

	ns = corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: member2Namespace,
		},
	}
	Expect(k8sClient.Create(ctx, &ns)).Should(Succeed())
})

var _ = AfterSuite(func() {
	defer klog.Flush()
	cancel()

	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})
