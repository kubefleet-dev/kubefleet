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

package tests

import (
	"context"
	"flag"
	"path/filepath"
	"sync"
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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	experimentalv1beta1 "github.com/kubefleet-dev/kubefleet/apis/experimental/v1beta1"
	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/reimagined/deploymentwatcher"
	"github.com/kubefleet-dev/kubefleet/pkg/reimagined/placementbinding"
	"github.com/kubefleet-dev/kubefleet/pkg/reimagined/placementmigrationrequest"
	"github.com/kubefleet-dev/kubefleet/pkg/reimagined/placementpolicy"
	"github.com/kubefleet-dev/kubefleet/pkg/reimagined/placementresourcesnapshot"
)

var (
	cfg       *rest.Config
	hubEnv    *envtest.Environment
	hubClient client.Client
	hubMgr    manager.Manager

	snapshotMgr *placementresourcesnapshot.Manager

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
)

const (
	workNSName = "work"
)

func setupNamespaces() {
	for _, name := range []string{
		workNSName,
		"fleet-member-cluster-1",
		"fleet-member-cluster-2",
	} {
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
		}
		Expect(hubClient.Create(ctx, ns)).To(Succeed())
	}
}

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Reimagined Controllers Integration Test Suite")
}

var _ = BeforeSuite(func() {
	ctx, cancel = context.WithCancel(context.TODO())

	By("Setup klog")
	fs := flag.NewFlagSet("klog", flag.ContinueOnError)
	klog.InitFlags(fs)
	Expect(fs.Parse([]string{"--v", "5", "-add_dir_header", "true"})).Should(Succeed())

	logger := zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true))
	klog.SetLogger(logger)
	ctrl.SetLogger(logger)

	By("Bootstrapping the test environment")
	hubEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("../../../", "config", "crd", "bases"),
		},
	}

	var err error
	cfg, err = hubEnv.Start()
	Expect(err).ToNot(HaveOccurred())
	Expect(cfg).ToNot(BeNil())

	err = clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = experimentalv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = placementv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	By("Building the Kubernetes client")
	hubClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).ToNot(HaveOccurred())
	Expect(hubClient).ToNot(BeNil())

	By("Building the dynamic client")
	dynamicClient, err := dynamic.NewForConfig(cfg)
	Expect(err).ToNot(HaveOccurred())
	Expect(dynamicClient).ToNot(BeNil())

	By("Setting up test namespaces")
	setupNamespaces()

	By("Setting up the controller manager")
	hubMgr, err = ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
		Metrics: server.Options{
			BindAddress: "0",
		},
	})
	Expect(err).ToNot(HaveOccurred())

	By("Wiring up the placement resource snapshot manager and controller")
	snapshotMgr = placementresourcesnapshot.NewManager(hubMgr.GetClient(), dynamicClient, 100)
	snapshotReconciler := placementresourcesnapshot.NewPlacementResourceSnapshotReqReconciler(
		hubMgr.GetClient(), snapshotMgr, 30,
	)
	Expect(snapshotReconciler.SetupWithManager(hubMgr)).To(Succeed())

	By("Wiring up the placement policy controller")
	placementReconciler := &placementpolicy.Reconciler{
		HubClient:                        hubMgr.GetClient(),
		PlacementResourceSnapshotManager: snapshotMgr,
		MaxSnapshotCreationWaitTime:      30 * time.Second,
	}
	Expect(placementReconciler.SetupWithManager(hubMgr)).To(Succeed())

	By("Wiring up the placement binding controller")
	bindingReconciler := &placementbinding.Reconciler{
		HubClient: hubMgr.GetClient(),
	}
	Expect(bindingReconciler.SetupWithManager(hubMgr)).To(Succeed())

	By("Wiring up the placement migration request controller")
	migrationReconciler := &placementmigrationrequest.Reconciler{
		HubClient:         hubMgr.GetClient(),
		MaxWaitTimePerRun: 15 * time.Minute,
	}
	Expect(migrationReconciler.SetupWithManager(hubMgr)).To(Succeed())

	By("Wiring up the deployment watcher controller")
	deploymentReconciler := &deploymentwatcher.Reconciler{
		HubClient: hubMgr.GetClient(),
	}
	Expect(deploymentReconciler.SetupWithManager(hubMgr)).To(Succeed())

	wg = sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer GinkgoRecover()
		defer wg.Done()
		Expect(hubMgr.Start(ctx)).To(Succeed())
	}()

	wg.Add(1)
	go func() {
		defer GinkgoRecover()
		defer wg.Done()
		Expect(snapshotMgr.Start(ctx)).To(Succeed())
	}()
})

var _ = AfterSuite(func() {
	defer klog.Flush()

	cancel()
	wg.Wait()
	By("Tearing down the test environment")
	Expect(hubEnv.Stop()).To(Succeed())
})
