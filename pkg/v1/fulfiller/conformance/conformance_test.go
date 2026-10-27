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

package conformance

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/v1/fulfiller"
)

const subjectEnv = "KUBEFLEET_CONFORMANCE_NONCONFORMANT_SUBJECT"

// nonConformant is a provider with one defect, chosen by name: "misnaming" registers the
// cluster under a name of its own without the ownership labels; "leaky" names and labels
// correctly but its Deprovision leaves the cluster behind.
type nonConformant struct {
	client.Client
	defect string
}

func (p *nonConformant) Provision(ctx context.Context, req fulfiller.Request) error {
	cluster := &clusterv1beta1.MemberCluster{
		ObjectMeta: metav1.ObjectMeta{Name: req.ClusterName, Labels: req.OwnershipLabels()},
		Spec:       clusterv1beta1.MemberClusterSpec{Identity: rbacv1.Subject{Kind: rbacv1.ServiceAccountKind, Name: "sa", Namespace: "fleet-system"}},
	}
	if p.defect == "misnaming" {
		cluster.Name, cluster.Labels = "rogue-"+req.ClusterName[len(req.ClusterName)-12:], nil
	}
	return client.IgnoreAlreadyExists(p.Create(ctx, cluster))
}

func (p *nonConformant) Deprovision(ctx context.Context, req fulfiller.Request) error {
	if p.defect == "leaky" {
		return nil
	}
	return client.IgnoreNotFound(p.Delete(ctx, &clusterv1beta1.MemberCluster{ObjectMeta: metav1.ObjectMeta{Name: req.ClusterName}}))
}

// TestNonConformantSubject runs the suite against the non-conformant provider the environment
// names. It is meant to fail, so it runs only in a child process of
// TestSuiteRejectsNonConformantProviders.
func TestNonConformantSubject(t *testing.T) {
	defect := os.Getenv(subjectEnv)
	if defect == "" {
		t.Skip("only run as the child of TestSuiteRejectsNonConformantProviders")
	}
	Run(t, Subject{
		ProvisionerName: "nonconformant.example.kubefleet.dev",
		New: func(mgr ctrl.Manager) (fulfiller.Provisioner, error) {
			return &nonConformant{Client: mgr.GetClient(), defect: defect}, nil
		},
		Behave:            func(Behaviour) {},
		CRDDirectoryPaths: []string{filepath.Join("..", "..", "..", "..", "config", "crd", "bases")},
	})
}

// TestSuiteRejectsNonConformantProviders proves the suite can fail: each defect is run through
// the subtests that must catch it, and only those, so the children stay short.
func TestSuiteRejectsNonConformantProviders(t *testing.T) {
	testCases := []struct {
		defect    string
		mustFail  []string
		runFilter string
	}{
		{defect: "misnaming", mustFail: []string{"fulfills an approved claim and leaves the freshness marker alone"}, runFilter: "fulfills_an_approved_claim"},
		{defect: "leaky", mustFail: []string{"deprovisions a never-joined cluster on a join timeout", "deprovisions a never-joined cluster on withdrawal", "provisions and deprovisions idempotently, as a restart requires"}, runFilter: "deprovisions|provisions_and_deprovisions"},
	}
	for _, tc := range testCases {
		t.Run(tc.defect, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNonConformantSubject$/^("+tc.runFilter+")", "-test.timeout=10m")
			cmd.Env = append(os.Environ(), subjectEnv+"="+tc.defect)
			out, err := cmd.CombinedOutput()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("the suite passed the %s provider (err=%v); output:\n%s", tc.defect, err, out)
			}
			for _, want := range tc.mustFail {
				if !strings.Contains(string(out), "--- FAIL: TestNonConformantSubject/"+strings.ReplaceAll(want, " ", "_")) {
					t.Errorf("the suite did not fail %q against the %s provider; output:\n%s", want, tc.defect, out)
				}
			}
		})
	}
}
