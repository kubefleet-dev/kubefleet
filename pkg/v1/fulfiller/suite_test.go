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

package fulfiller

import (
	"context"
	"errors"
	"flag"
	"path/filepath"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
)

const (
	plainProvisioner       = "plain.example.kubefleet.dev"
	remediatingProvisioner = "remediating.example.kubefleet.dev"

	eventuallyTimeout    = 15 * time.Second
	consistentlyDuration = 2 * time.Second
	pollInterval         = 250 * time.Millisecond
)

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc

	// plain and remediating are the two providers under test, driven by two reconcilers in the
	// one manager; only the second implements Remediator.
	plain       *fakeProvisioner
	remediating *fakeRemediator
)

// provisionMode is what fakeProvisioner does on Provision.
type provisionMode string

const (
	modeFulfill   provisionMode = "Fulfill"
	modeTransient provisionMode = "FailTransient"
	modePermanent provisionMode = "FailPermanent"
)

// fakeProvisioner registers a MemberCluster on Provision (or fails as told) and deletes it on
// Deprovision, counting every call by cluster name.
type fakeProvisioner struct {
	mu          sync.Mutex
	mode        provisionMode
	provisions  map[string]int
	deprovision map[string]int
	remediated  map[string]int
}

func newFakeProvisioner() *fakeProvisioner {
	return &fakeProvisioner{mode: modeFulfill, provisions: map[string]int{}, deprovision: map[string]int{}, remediated: map[string]int{}}
}

func (p *fakeProvisioner) setMode(mode provisionMode) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mode = mode
}

func (p *fakeProvisioner) Provision(ctx context.Context, req Request) error {
	p.mu.Lock()
	p.provisions[req.ClusterName]++
	mode := p.mode
	p.mu.Unlock()
	switch mode {
	case modeTransient:
		return errors.New("still provisioning")
	case modePermanent:
		return Permanent(errors.New("quota exceeded"))
	}
	cluster := &clusterv1beta1.MemberCluster{
		ObjectMeta: metav1.ObjectMeta{Name: req.ClusterName, Labels: req.OwnershipLabels()},
		Spec: clusterv1beta1.MemberClusterSpec{
			Identity: rbacv1.Subject{Kind: "ServiceAccount", Name: req.ClusterName, Namespace: "fleet-system"},
		},
	}
	return client.IgnoreAlreadyExists(k8sClient.Create(ctx, cluster))
}

func (p *fakeProvisioner) Deprovision(ctx context.Context, req Request) error {
	p.mu.Lock()
	p.deprovision[req.ClusterName]++
	p.mu.Unlock()
	return client.IgnoreNotFound(k8sClient.Delete(ctx, &clusterv1beta1.MemberCluster{ObjectMeta: metav1.ObjectMeta{Name: req.ClusterName}}))
}

func (p *fakeProvisioner) calls(m map[string]int, cluster string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return m[cluster]
}

// fakeRemediator is a fakeProvisioner that can also repair a dark cluster, by marking it joined.
type fakeRemediator struct {
	*fakeProvisioner
}

func (p *fakeRemediator) Remediate(ctx context.Context, req Request, cluster *clusterv1beta1.MemberCluster) error {
	p.mu.Lock()
	p.remediated[cluster.Name]++
	p.mu.Unlock()
	markJoined(cluster.Name, true)
	return nil
}

// markJoined stamps an agent status that passes (recent heartbeat) or, with recent=false, fails
// (stale heartbeat) the eligibility gate, while Joined=True either way: the "joined then dark"
// shape.
func markJoined(name string, recent bool) {
	mc := &clusterv1beta1.MemberCluster{}
	Expect(k8sClient.Get(ctx, client.ObjectKey{Name: name}, mc)).Should(Succeed())
	heartbeat := metav1.Now()
	if !recent {
		heartbeat = metav1.NewTime(time.Now().Add(-time.Hour))
	}
	mc.Status.AgentStatus = []clusterv1beta1.AgentStatus{{
		Type: clusterv1beta1.MemberAgent,
		Conditions: []metav1.Condition{
			{Type: string(clusterv1beta1.AgentJoined), Status: metav1.ConditionTrue, Reason: "Joined", Message: "test", LastTransitionTime: metav1.Now()},
			{Type: string(clusterv1beta1.AgentHealthy), Status: metav1.ConditionTrue, Reason: "Healthy", Message: "test", LastTransitionTime: metav1.Now()},
		},
		LastReceivedHeartbeat: heartbeat,
	}}
	Expect(k8sClient.Status().Update(ctx, mc)).Should(Succeed())
}

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Cluster Claim Fulfiller Framework Suite")
}

var _ = BeforeSuite(func() {
	ctx, cancel = context.WithCancel(context.TODO())

	fs := flag.NewFlagSet("klog", flag.ContinueOnError)
	klog.InitFlags(fs)
	Expect(fs.Parse([]string{"--v", "4", "-add_dir_header", "true"})).Should(Succeed())

	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	var err error
	cfg, err = testEnv.Start()
	Expect(err).Should(Succeed())
	Expect(kfplacementv1alpha1.AddToScheme(scheme.Scheme)).Should(Succeed())
	Expect(clusterv1beta1.AddToScheme(scheme.Scheme)).Should(Succeed())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).Should(Succeed())

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme.Scheme,
		Metrics: server.Options{BindAddress: "0"},
		Logger:  textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(4))),
	})
	Expect(err).Should(Succeed())

	plain = newFakeProvisioner()
	remediating = &fakeRemediator{fakeProvisioner: newFakeProvisioner()}
	for name, provisioner := range map[string]Provisioner{plainProvisioner: plain, remediatingProvisioner: remediating} {
		r := New(mgr.GetClient(), Options{ProvisionerName: name, Provisioner: provisioner, APIReader: mgr.GetAPIReader(), PollInterval: pollInterval})
		Expect(r.SetupWithManager(mgr)).Should(Succeed())
	}

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

// notFound reports whether a get of the object fails with NotFound.
func notFound(obj client.Object) bool {
	return apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj))
}
