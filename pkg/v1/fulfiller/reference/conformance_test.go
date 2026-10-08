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
	"path/filepath"
	"testing"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/kubefleet-dev/kubefleet/pkg/v1/fulfiller"
	"github.com/kubefleet-dev/kubefleet/pkg/v1/fulfiller/conformance"
)

// TestConformance runs the reference provider through the fulfillment contract's conformance
// suite, as every provider should.
func TestConformance(t *testing.T) {
	var p *Provisioner
	conformance.Run(t, conformance.Subject{
		ProvisionerName: ProvisionerName,
		New: func(mgr ctrl.Manager) (fulfiller.Provisioner, error) {
			// Joins are the suite's to simulate, so it can tell a joined cluster from one that
			// never joined; the provider only registers.
			p = New(mgr.GetClient(), Options{})
			return p, nil
		},
		Behave: func(b conformance.Behaviour) {
			if b == conformance.FailPermanent {
				p.SetBehaviour(FailPermanent)
				return
			}
			p.SetBehaviour(Fulfill)
		},
		Parameters:        map[string]string{IdentityParameter: "member-agent-sa"},
		CRDDirectoryPaths: []string{filepath.Join("..", "..", "..", "..", "config", "crd", "bases")},
	})
}
