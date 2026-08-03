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
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
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
	"github.com/kubefleet-dev/kubefleet/pkg/reimagined/ociartifactcachedlocalfsstore"
	"github.com/kubefleet-dev/kubefleet/pkg/reimagined/orasmanifests"
	"github.com/kubefleet-dev/kubefleet/pkg/reimagined/orasmanifestswatcher"
	"github.com/kubefleet-dev/kubefleet/pkg/reimagined/placementbinding"
	"github.com/kubefleet-dev/kubefleet/pkg/reimagined/placementmigrationrequest"
	"github.com/kubefleet-dev/kubefleet/pkg/reimagined/placementpolicy"
	"github.com/kubefleet-dev/kubefleet/pkg/reimagined/placementresourcesnapshot"
	localregistry "github.com/kubefleet-dev/kubefleet/test/oci"
)

var (
	cfg       *rest.Config
	hubEnv    *envtest.Environment
	hubClient client.Client
	hubMgr    manager.Manager

	snapshotMgr *placementresourcesnapshot.Manager

	ociOutputDir string

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
)

const (
	workNSName = "work"

	clusterName1 = "cluster-1"
	clusterName2 = "cluster-2"
)

const (
	fleetMemberClusterReservedNSNameFmt = "fleet-member-%s"
)

func setupResources() {
	// Create namespaces used by Work objects and tests.
	for _, name := range []string{
		workNSName,
		fmt.Sprintf(fleetMemberClusterReservedNSNameFmt, clusterName1),
		fmt.Sprintf(fleetMemberClusterReservedNSNameFmt, clusterName2),
	} {
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
		}
		Expect(hubClient.Create(ctx, ns)).To(Succeed())
	}

	// Pre-create member clusters used by this suite.
	for _, cluster := range []struct {
		name   string
		region string
	}{
		{name: clusterName1, region: "eastus"},
		{name: clusterName2, region: "centralus"},
	} {
		mc := &clusterv1beta1.MemberCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: cluster.name,
				Labels: map[string]string{
					"topology.kubernetes.io/region": cluster.region,
				},
			},
			Spec: clusterv1beta1.MemberClusterSpec{
				Identity: rbacv1.Subject{
					Kind: rbacv1.ServiceAccountKind,
					Name: "hub-access",
				},
			},
		}
		Expect(hubClient.Create(ctx, mc)).To(Succeed())
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

	By("Bootstrapping the local OCI registry")
	Expect(localregistry.BootstrapLocalRegistry()).To(Succeed())

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
	setupResources()

	By("Setting up the controller manager")
	hubMgr, err = ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
		Metrics: server.Options{
			BindAddress: "0",
		},
	})
	Expect(err).ToNot(HaveOccurred())

	By("Wiring up the placement resource snapshot manager and controller")
	snapshotMgr = placementresourcesnapshot.NewManager(hubMgr.GetClient(), dynamicClient, hubMgr.GetRESTMapper(), 100)
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
	ociOutputDir, err = os.MkdirTemp("", "reimagined-tests-oci-store-*")
	Expect(err).ToNot(HaveOccurred())
	bindingReconciler := &placementbinding.Reconciler{
		HubClient:                     hubMgr.GetClient(),
		OCIArtifactCachedStoreManager: ociartifactcachedlocalfsstore.NewManager(ociOutputDir),
		UseHTTPToConnectToOCIRegistry: true,
	}
	Expect(bindingReconciler.SetupWithManager(hubMgr)).To(Succeed())

	By("Wiring up the ORAS manifests controller")
	orasManifestsReconciler := &orasmanifests.Reconciler{
		HubClient:                     hubMgr.GetClient(),
		UseHTTPToConnectToOCIRegistry: true,
	}
	Expect(orasManifestsReconciler.SetupWithManager(hubMgr)).To(Succeed())

	By("Wiring up the ORAS manifests watcher controller")
	orasManifestsWatcherReconciler := &orasmanifestswatcher.Reconciler{
		HubClient: hubMgr.GetClient(),
	}
	Expect(orasManifestsWatcherReconciler.SetupWithManager(hubMgr)).To(Succeed())

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

	By("Cleaning up the OCI artifact cached store work directory")
	Expect(os.RemoveAll(ociOutputDir)).To(Succeed())

	By("Tearing down the local OCI registry")
	Expect(localregistry.TearDownLocalRegistry()).To(Succeed())
})
