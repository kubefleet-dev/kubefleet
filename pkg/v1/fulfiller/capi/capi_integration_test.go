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

package capi

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/v1/fulfiller"
	"github.com/kubefleet-dev/kubefleet/pkg/v1/fulfiller/conformance"
)

var crdPaths = []string{
	filepath.Join("..", "..", "..", "..", "config", "crd", "bases"),
	filepath.Join("testdata", "v1.10.4"),
}

// capiFinalizer stands in for the finalizer Cluster API holds while it tears infrastructure down.
const capiFinalizer = "cluster.cluster.x-k8s.io"

// fakeInfrastructure stands in for Cluster API's controllers: it watches Cluster objects by
// polling, holds a finalizer on each while it lives and for a moment after its deletion is
// requested, and drives the phase the way the test says -- to Provisioned, or to Failed.
type fakeInfrastructure struct {
	c client.Client

	mu   sync.Mutex
	fail bool
}

func (f *fakeInfrastructure) setFail(fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = fail
}

func (f *fakeInfrastructure) run(ctx context.Context) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			list := &unstructured.UnstructuredList{}
			list.SetGroupVersionKind(ClusterGVK.GroupVersion().WithKind("ClusterList"))
			if err := f.c.List(ctx, list); err != nil {
				continue
			}
			f.mu.Lock()
			fail := f.fail
			f.mu.Unlock()
			for i := range list.Items {
				cluster := &list.Items[i]
				if ts := cluster.GetDeletionTimestamp(); ts != nil {
					// Teardown takes a moment; then the finalizer goes and the object with it.
					if time.Since(ts.Time) > time.Second && controllerutil.RemoveFinalizer(cluster, capiFinalizer) {
						_ = f.c.Update(ctx, cluster)
					}
					continue
				}
				if controllerutil.AddFinalizer(cluster, capiFinalizer) {
					if err := f.c.Update(ctx, cluster); err != nil {
						continue
					}
				}
				if phase, _, _ := unstructured.NestedString(cluster.Object, "status", "phase"); phase != "" {
					continue
				}
				status := map[string]any{"phase": "Provisioned"}
				if fail {
					status = map[string]any{"phase": "Failed", "failureReason": "InvalidConfiguration", "failureMessage": "the fake infrastructure was told to fail"}
				}
				_ = unstructured.SetNestedMap(cluster.Object, status, "status")
				_ = f.c.Status().Update(ctx, cluster)
			}
		}
	}
}

func newClient(t *testing.T, cfg interface{ Env() *envtest.Environment }) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, clusterv1beta1.AddToScheme, kfplacementv1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	c, err := client.New(cfg.Env().Config, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

type envHolder struct{ env *envtest.Environment }

func (h envHolder) Env() *envtest.Environment { return h.env }

// TestConformance runs the bridge through the conformance suite, with the fake infrastructure
// provisioning every Cluster the bridge applies. The management cluster is the hub itself.
func TestConformance(t *testing.T) {
	var infra *fakeInfrastructure
	conformance.Run(t, conformance.Subject{
		ProvisionerName: ProvisionerName,
		New: func(mgr ctrl.Manager) (fulfiller.Provisioner, error) {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			// The bridge reads Cluster API objects straight from the API server, as the binary does.
			management, err := client.New(mgr.GetConfig(), client.Options{Scheme: mgr.GetScheme()})
			if err != nil {
				return nil, err
			}
			infra = &fakeInfrastructure{c: management}
			go infra.run(ctx)
			return New(Options{Hub: mgr.GetClient(), Management: management}), nil
		},
		Behave: func(b conformance.Behaviour) {
			// The bridge has no behaviours of its own: a transient failure is a cluster that is
			// still provisioning, which the fake never leaves it in, so FailTransient is not
			// representable and is treated as Fulfill; a permanent one is a Failed cluster.
			infra.setFail(b == conformance.FailPermanent)
		},
		Parameters:        map[string]string{ClusterClassParameter: "standard", KubernetesVersionParameter: "v1.33.0", IdentityParameter: "member-sa"},
		CRDDirectoryPaths: crdPaths,
	})
}

// TestBridge pins what the conformance suite cannot see: the Cluster object the bridge renders,
// the kubeconfig hand-off, idempotent re-apply, failure mapping, and deprovisioning both halves.
func TestBridge(t *testing.T) {
	g := gomega.NewWithT(t)
	env := &envtest.Environment{CRDDirectoryPaths: crdPaths, ErrorIfCRDPathMissing: true}
	_, err := env.Start()
	g.Expect(err).NotTo(gomega.HaveOccurred())
	t.Cleanup(func() { _ = env.Stop() })
	c := newClient(t, envHolder{env})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	infra := &fakeInfrastructure{c: c}
	go infra.run(ctx)
	bridge := New(Options{Hub: c, Management: c})

	req := fulfiller.Request{
		ClaimName: "app-0-abc", ClaimUID: types.UID("uid-1"), ClusterName: "app-0-abc-0123456789ab", ClusterProviderClassName: "class",
		Parameters:           map[string]string{ClusterClassParameter: "standard", KubernetesVersionParameter: "v1.33.0", IdentityParameter: "member-sa"},
		ClusterSelectorTerms: []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{{MatchLabels: map[string]string{defaultRegionLabel: "eastus"}}},
	}
	clusterKey := client.ObjectKey{Namespace: defaultNamespace, Name: req.ClusterName}
	getCluster := func() (*unstructured.Unstructured, error) {
		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(ClusterGVK)
		return u, c.Get(ctx, clusterKey, u)
	}

	t.Run("applies the cluster and registers the member once provisioned", func(t *testing.T) {
		g := gomega.NewWithT(t)
		g.Eventually(func() error { return bridge.Provision(ctx, req) }, 30*time.Second, 250*time.Millisecond).Should(gomega.Succeed())
		cluster, err := getCluster()
		g.Expect(err).NotTo(gomega.HaveOccurred())
		variables, _, _ := unstructured.NestedSlice(cluster.Object, "spec", "topology", "variables")
		g.Expect(variables).To(gomega.ConsistOf(map[string]any{"name": "region", "value": "eastus"}))
		g.Expect(cluster.GetLabels()).To(gomega.HaveKeyWithValue(kfplacementv1alpha1.FulfilledClaimUIDLabel, "uid-1"))
		g.Expect(cluster.GetManagedFields()).To(gomega.ContainElement(gomega.HaveField("Manager", FieldManager)))

		member := &clusterv1beta1.MemberCluster{}
		g.Expect(c.Get(ctx, client.ObjectKey{Name: req.ClusterName}, member)).To(gomega.Succeed())
		g.Expect(member.Annotations).To(gomega.HaveKeyWithValue(KubeconfigSecretAnnotation, "default/"+req.ClusterName+"-kubeconfig"))
		g.Expect(member.Labels).To(gomega.HaveKeyWithValue(defaultRegionLabel, "eastus"))
		g.Expect(member.Spec.Identity.Name).To(gomega.Equal("member-sa"))
	})

	t.Run("re-applying after a restart changes nothing", func(t *testing.T) {
		g := gomega.NewWithT(t)
		before, err := getCluster()
		g.Expect(err).NotTo(gomega.HaveOccurred())
		g.Expect(bridge.Provision(ctx, req)).To(gomega.Succeed())
		after, err := getCluster()
		g.Expect(err).NotTo(gomega.HaveOccurred())
		g.Expect(after.GetUID()).To(gomega.Equal(before.GetUID()), "the same Cluster object")
		g.Expect(after.GetGeneration()).To(gomega.Equal(before.GetGeneration()), "an identical apply bumps nothing")
		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(ClusterGVK.GroupVersion().WithKind("ClusterList"))
		g.Expect(c.List(ctx, list)).To(gomega.Succeed())
		g.Expect(list.Items).To(gomega.HaveLen(1), "at most one Cluster per claim")
	})

	t.Run("deprovisions the member and requests the cluster's deletion", func(t *testing.T) {
		g := gomega.NewWithT(t)
		g.Eventually(func() error { return bridge.Deprovision(ctx, req) }, 30*time.Second, 250*time.Millisecond).Should(gomega.Succeed())
		cluster, err := getCluster()
		g.Expect(err).NotTo(gomega.HaveOccurred(), "Cluster API still holds the Cluster while it tears down")
		g.Expect(cluster.GetDeletionTimestamp()).NotTo(gomega.BeNil())
		g.Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Name: req.ClusterName}, &clusterv1beta1.MemberCluster{}))).To(gomega.BeTrue(), "the MemberCluster is gone")
		g.Expect(bridge.Deprovision(ctx, req)).To(gomega.Succeed(), "deprovisioning again is a no-op")
		g.Expect(bridge.Provision(ctx, req)).To(gomega.MatchError(gomega.ContainSubstring("is being deleted")), "a Cluster on its way out is waited for, not applied over")
		g.Eventually(func() bool { _, err := getCluster(); return apierrors.IsNotFound(err) }, 30*time.Second, 250*time.Millisecond).Should(gomega.BeTrue(), "the fake infrastructure finishes the teardown")
	})

	t.Run("maps a failed cluster to a permanent error", func(t *testing.T) {
		g := gomega.NewWithT(t)
		infra.setFail(true)
		t.Cleanup(func() { infra.setFail(false) })
		failing := req
		failing.ClaimUID, failing.ClusterName = "uid-2", "app-0-abc-failing"
		g.Eventually(func() error {
			err := bridge.Provision(ctx, failing)
			if fulfiller.IsPermanent(err) {
				return nil
			}
			return err
		}, 30*time.Second, 250*time.Millisecond).Should(gomega.Succeed())
		g.Expect(fulfiller.IsPermanent(bridge.Provision(ctx, failing))).To(gomega.BeTrue())
		g.Expect(bridge.Provision(ctx, failing).Error()).To(gomega.ContainSubstring("InvalidConfiguration"))
		g.Expect(bridge.Deprovision(ctx, failing)).To(gomega.Succeed(), "a failed cluster's deletion is requested in one call, so the claim can be failed")
	})

	t.Run("refuses a cluster that is not this claim's", func(t *testing.T) {
		g := gomega.NewWithT(t)
		foreign := req
		foreign.ClaimUID, foreign.ClusterName = "uid-3", "app-0-abc-foreign"
		g.Eventually(func() error { return bridge.Provision(ctx, foreign) }, 30*time.Second, 250*time.Millisecond).Should(gomega.Succeed())
		intruder := foreign
		intruder.ClaimUID = "uid-4"
		g.Expect(fulfiller.IsPermanent(bridge.Provision(ctx, intruder))).To(gomega.BeTrue(), "another claim's Cluster under the name is never applied over")
		g.Expect(bridge.Deprovision(ctx, intruder)).To(gomega.Succeed(), "and never deleted")
		cluster, err := getCluster()
		_ = cluster
		g.Expect(err).To(gomega.HaveOccurred()) // the default-named cluster from the first subtest is gone by now
		own := &unstructured.Unstructured{}
		own.SetGroupVersionKind(ClusterGVK)
		g.Expect(c.Get(ctx, client.ObjectKey{Namespace: defaultNamespace, Name: foreign.ClusterName}, own)).To(gomega.Succeed())
		g.Expect(own.GetDeletionTimestamp()).To(gomega.BeNil())
		g.Expect(bridge.Deprovision(ctx, foreign)).To(gomega.Succeed())
	})
}
