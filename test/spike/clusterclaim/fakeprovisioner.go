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

package clusterclaim

import (
	"context"
	"fmt"
	"maps"
	"sync"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	placementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
)

// ProvisionPolicy controls how the FakeProvisioner responds to a claim.
type ProvisionPolicy string

const (
	// PolicyFulfill provisions a MemberCluster whose labels are copied from the
	// claim's first selector term's MatchLabels, then reports completion.
	PolicyFulfill ProvisionPolicy = "Fulfill"
	// PolicyFail reports a terminal provisioning failure.
	PolicyFail ProvisionPolicy = "Fail"
	// PolicyIgnore leaves the claim untouched (simulates no provisioner, or a
	// very slow one).
	PolicyIgnore ProvisionPolicy = "Ignore"
)

// FakeProvisioner is the test stand-in for the platform/cloud-provider
// controller in the FEP-0001 claim contract (the role Meridian's
// ClusterRequest reconciler plays for AKS). It only ever writes the fields
// that side of the contract owns: the Completed status condition and
// status.provisionedClusterName — never the claim spec, and never
// lastObservedMostRecentClusterCreationTimestamp.
type FakeProvisioner struct {
	client.Client

	mu     sync.Mutex
	policy ProvisionPolicy
	// provisioned records claim name -> MemberCluster name for restart-idempotency.
	provisioned map[string]string
}

func NewFakeProvisioner(c client.Client) *FakeProvisioner {
	return &FakeProvisioner{Client: c, policy: PolicyIgnore, provisioned: map[string]string{}}
}

// SetPolicy switches the provisioning behavior for subsequently observed claims.
func (p *FakeProvisioner) SetPolicy(policy ProvisionPolicy) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.policy = policy
}

func (p *FakeProvisioner) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	claim := &placementv1alpha1.ClusterRequest{}
	if err := p.Get(ctx, req.NamespacedName, claim); err != nil {
		// A vanished claim is a normal outcome in this contract: KubeFleet may
		// withdraw at any moment, regardless of provisioning state.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if meta.FindStatusCondition(claim.Status.Conditions, placementv1alpha1.ClusterRequestCondTypeCompleted) != nil {
		return ctrl.Result{}, nil
	}

	p.mu.Lock()
	policy := p.policy
	p.mu.Unlock()

	switch policy {
	case PolicyFulfill:
		return ctrl.Result{}, p.fulfill(ctx, claim)
	case PolicyFail:
		meta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{
			Type:               placementv1alpha1.ClusterRequestCondTypeCompleted,
			Status:             metav1.ConditionFalse,
			Reason:             "Failed",
			Message:            "spike: simulated terminal provisioning failure",
			ObservedGeneration: claim.Generation,
		})
		return ctrl.Result{}, p.Status().Update(ctx, claim)
	default:
		return ctrl.Result{}, nil
	}
}

func (p *FakeProvisioner) fulfill(ctx context.Context, claim *placementv1alpha1.ClusterRequest) error {
	p.mu.Lock()
	clusterName, ok := p.provisioned[claim.Name]
	if !ok {
		clusterName = fmt.Sprintf("provisioned-%s", claim.Name)
		p.provisioned[claim.Name] = clusterName
	}
	p.mu.Unlock()

	memberCluster := &clusterv1beta1.MemberCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:   clusterName,
			Labels: labelsForClaim(claim),
		},
		Spec: clusterv1beta1.MemberClusterSpec{
			Identity: rbacv1.Subject{
				Kind:      "ServiceAccount",
				Name:      clusterName,
				Namespace: "fleet-system",
				APIGroup:  "",
			},
		},
	}
	if err := p.Create(ctx, memberCluster); err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	claim.Status.ProvisionedClusterName = &clusterName
	meta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{
		Type:               placementv1alpha1.ClusterRequestCondTypeCompleted,
		Status:             metav1.ConditionTrue,
		Reason:             "Provisioned",
		Message:            fmt.Sprintf("spike: member cluster %s provisioned", clusterName),
		ObservedGeneration: claim.Generation,
	})
	return p.Status().Update(ctx, claim)
}

// labelsForClaim derives the provisioned cluster's labels from the claim, the
// way a real provisioner would feed selector terms into a cluster blueprint.
func labelsForClaim(claim *placementv1alpha1.ClusterRequest) map[string]string {
	if len(claim.Spec.ClusterSelectorTerms) == 0 {
		return nil
	}
	out := map[string]string{}
	maps.Copy(out, claim.Spec.ClusterSelectorTerms[0].MatchLabels)
	return out
}

func (p *FakeProvisioner) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("spike-fake-provisioner").
		For(&placementv1alpha1.ClusterRequest{}).
		Complete(p)
}
