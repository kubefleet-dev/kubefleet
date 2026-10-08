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

package reference

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/textlogger"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/v1/controllers/placementpolicy"
	"github.com/kubefleet-dev/kubefleet/pkg/v1/fulfiller"
)

const (
	testNamespace = "work"

	eventuallyTimeout    = 30 * time.Second
	consistentlyDuration = 2 * time.Second
	pollInterval         = 250 * time.Millisecond
)

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc

	// provider is the reference provider under test, with the policy controller and the
	// framework running beside it in one manager: the whole fulfillment loop, end to end.
	provider *Provisioner
)

// snapshotStub stands in for the placement resource snapshot manager, as the policy controller's
// own suite does: it hands back a snapshot named after the policy without creating anything.
type snapshotStub struct{}

func (snapshotStub) SnapshotResourcesIfNoSnapshotExists(_ context.Context, policy kfplacementv1alpha1.PlacementPolicyAccessor) ([]kfplacementv1alpha1.PlacementResourceSnapshotAccessor, bool, error) {
	return []kfplacementv1alpha1.PlacementResourceSnapshotAccessor{
		&kfplacementv1alpha1.PlacementResourceSnapshot{ObjectMeta: metav1.ObjectMeta{Name: policy.GetName() + "-snapshot-0"}},
	}, true, nil
}

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Reference Fulfiller Integration Test Suite")
}

var _ = BeforeSuite(func() {
	ctx, cancel = context.WithCancel(context.TODO())

	By("bootstrapping the test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	var err error
	cfg, err = testEnv.Start()
	Expect(err).Should(Succeed())
	Expect(kfplacementv1alpha1.AddToScheme(scheme.Scheme)).Should(Succeed())
	Expect(clusterv1beta1.AddToScheme(scheme.Scheme)).Should(Succeed())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).Should(Succeed())
	Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNamespace}})).Should(Succeed())

	By("starting the policy controller, the framework, and the provider in one manager")
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme.Scheme,
		Metrics: server.Options{BindAddress: "0"},
		Logger:  textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(4))),
	})
	Expect(err).Should(Succeed())

	policies := placementpolicy.NewReconciler(mgr.GetClient(), mgr.GetAPIReader(), snapshotStub{}, mgr.GetEventRecorder("placement-policy-controller"), placementpolicy.WithMaxConcurrentClusterClaims(2))
	Expect(policies.SetupWithManagerForPlacementPolicy(mgr)).Should(Succeed())
	Expect(policies.SetupWithManagerForClusterPlacementPolicy(mgr)).Should(Succeed())

	provider = New(mgr.GetClient(), Options{SimulateJoin: true, HeartbeatInterval: time.Second})
	Expect(mgr.Add(provider)).Should(Succeed())
	framework := fulfiller.New(mgr.GetClient(), fulfiller.Options{
		ProvisionerName: ProvisionerName,
		Provisioner:     provider,
		APIReader:       mgr.GetAPIReader(),
		PollInterval:    pollInterval,
	})
	Expect(framework.SetupWithManager(mgr)).Should(Succeed())

	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(ctx)).Should(Succeed())
	}()
	Expect(mgr.GetCache().WaitForCacheSync(ctx)).Should(BeTrue())
})

var _ = AfterSuite(func() {
	defer klog.Flush()
	cancel()
	Expect(testEnv.Stop()).Should(Succeed())
})
