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

// Package reference is the reference cluster provider: it fulfills a cluster claim by
// registering a MemberCluster on the hub and, when asked, simulating the member agent so that
// the cluster becomes eligible for scheduling without one. It provisions no infrastructure. It
// exists to exercise the fulfillment loop end to end -- in the integration tests, in the kind
// e2e, and as the subject of the conformance suite -- and as the smallest worked example of a
// Provisioner for providers to copy.
//
// The registered cluster's spec.identity names a hub ServiceAccount, from the class parameter
// "identity"; that ServiceAccount and its token Secret must exist already, the way the member
// join script creates them, since this provider creates neither.
//
// Simulating the join means writing a member agent status. Where no hub agent runs, as in
// envtest, it goes on MemberCluster.status directly (JoinMemberCluster). Where a hub agent runs,
// as in a kind cluster, the hub derives MemberCluster.status from the InternalMemberCluster in
// the cluster's fleet-member namespace, so the status goes there (JoinInternalMemberCluster).
// Either write passes through the hub's member-cluster webhook, which admits it only from a
// whitelisted user, so the provider's identity has to be whitelisted on such a hub.
package reference

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils"
	"github.com/kubefleet-dev/kubefleet/pkg/v1/fulfiller"
)

const (
	// ProvisionerName is the spec.provisionerName a ClusterProviderClass names to route its claims
	// here.
	ProvisionerName = "reference.fulfiller.kubefleet.dev"

	// IdentityParameter is the class parameter naming the hub ServiceAccount, in the hub agent's
	// namespace, that the registered cluster's member agent authenticates as. It must exist
	// already, with its token Secret: this provider creates neither.
	IdentityParameter = "identity"

	// SimulatedJoinLabel marks the clusters this provider simulates the member agent for, so that
	// it keeps only its own heartbeats fresh and leaves other providers' clusters alone.
	SimulatedJoinLabel = "kubefleet.dev/simulated-join"

	// defaultHeartbeatInterval keeps simulated heartbeats well within the eligibility checker's
	// five-minute window.
	defaultHeartbeatInterval = time.Minute

	// The reason strings the member agent writes on its own join (see
	// pkg/controllers/internalmembercluster/v1beta1), repeated so that the simulation reads like
	// the real thing without importing the agent.
	joinedReason  = "InternalMemberClusterJoined"
	healthyReason = "InternalMemberClusterHealthy"
)

// Behaviour is what the provider does with a claim; the conformance suite drives it through all
// of them.
type Behaviour string

const (
	// Fulfill registers the cluster and, with SimulateJoin, has it join.
	Fulfill Behaviour = "Fulfill"
	// FailTransient keeps reporting that provisioning is in progress; the framework fails the
	// claim at the class's maxProvisionDuration.
	FailTransient Behaviour = "FailTransient"
	// FailPermanent fails the claim at once.
	FailPermanent Behaviour = "FailPermanent"
	// Ignore does nothing and reports in progress, like a provider that never looks; it differs
	// from FailTransient only in what it stands for.
	Ignore Behaviour = "Ignore"
	// NeverJoin registers the cluster but writes no agent status at all, as a cluster whose
	// member agent never came up: a never-joined cluster carries no Joined condition, true or
	// false.
	NeverJoin Behaviour = "NeverJoin"
)

// JoinTarget is where SimulateJoin writes the member agent's status; see the package doc.
type JoinTarget string

const (
	// JoinMemberCluster stamps MemberCluster.status directly; right where no hub agent runs.
	JoinMemberCluster JoinTarget = "MemberCluster"
	// JoinInternalMemberCluster stamps the InternalMemberCluster in the cluster's fleet-member
	// namespace, which the hub agent creates once it sees the MemberCluster and copies the status
	// from; right wherever a hub agent runs.
	JoinInternalMemberCluster JoinTarget = "InternalMemberCluster"
)

// Options configure the provider.
type Options struct {
	// Behaviour is the initial behaviour; SetBehaviour changes it at run time.
	Behaviour Behaviour
	// SimulateJoin has the provider stand in for the member agent of every cluster it registers:
	// it stamps a joined, healthy, heartbeating agent status and keeps the heartbeat fresh from
	// Start. On a hub with a member-cluster webhook the provider's identity must be whitelisted
	// for the status write.
	SimulateJoin bool
	// JoinTarget is where the simulated status goes. Defaults to JoinMemberCluster.
	JoinTarget JoinTarget
	// HeartbeatInterval is how often Start refreshes simulated heartbeats. It must stay well
	// inside the eligibility checker's heartbeat window. Defaults to a minute.
	HeartbeatInterval time.Duration
}

// Provisioner is the reference cluster provider.
type Provisioner struct {
	c    client.Client
	opts Options

	mu        sync.RWMutex
	behaviour Behaviour
}

var (
	_ fulfiller.Provisioner = &Provisioner{}
	_ fulfiller.Remediator  = &Provisioner{}
)

// New returns a provider using the given hub client.
func New(c client.Client, opts Options) *Provisioner {
	if opts.Behaviour == "" {
		opts.Behaviour = Fulfill
	}
	if opts.JoinTarget == "" {
		opts.JoinTarget = JoinMemberCluster
	}
	if opts.HeartbeatInterval <= 0 {
		opts.HeartbeatInterval = defaultHeartbeatInterval
	}
	return &Provisioner{c: c, opts: opts, behaviour: opts.Behaviour}
}

// SetBehaviour changes what the provider does with the next claim.
func (p *Provisioner) SetBehaviour(b Behaviour) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.behaviour = b
}

func (p *Provisioner) currentBehaviour() Behaviour {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.behaviour
}

// Provision registers a MemberCluster named by the request, carrying its ownership labels, the
// labels the claim's selector asks for, and the identity the class's parameters name, and
// simulates its join when configured. It is idempotent: the cluster of a previous call is left as
// it is, and only its join is re-asserted.
func (p *Provisioner) Provision(ctx context.Context, req fulfiller.Request) error {
	behaviour := p.currentBehaviour()
	switch behaviour {
	case FailTransient, Ignore:
		return errors.New("provisioning is in progress")
	case FailPermanent:
		return fulfiller.Permanent(errors.New("the reference provider was told to fail"))
	}
	identity, ok := req.Parameters[IdentityParameter]
	if !ok || identity == "" {
		return fulfiller.Permanent(fmt.Errorf("the class %s sets no %q parameter; it must name a hub ServiceAccount for the cluster to authenticate as", req.ClusterProviderClassName, IdentityParameter))
	}
	simulate := p.opts.SimulateJoin && behaviour != NeverJoin
	cluster := &clusterv1beta1.MemberCluster{
		ObjectMeta: metav1.ObjectMeta{Name: req.ClusterName, Labels: labelsFor(req, simulate)},
		Spec: clusterv1beta1.MemberClusterSpec{
			Identity: rbacv1.Subject{Kind: rbacv1.ServiceAccountKind, Name: identity, Namespace: utils.FleetSystemNamespace},
		},
	}
	switch err := p.c.Create(ctx, cluster); {
	case err == nil:
		klog.V(2).InfoS("Registered a member cluster", "cluster", req.ClusterName, "clusterClaim", req.ClaimName)
		if !simulate {
			return nil
		}
		// The object Create returned carries its resourceVersion, so the first stamp needs no
		// read through a cache that may not show the cluster yet.
		if p.opts.JoinTarget == JoinMemberCluster {
			setAgentStatus(&cluster.Status.AgentStatus, p.agentStatus())
			return p.c.Status().Update(ctx, cluster)
		}
	case apierrors.IsAlreadyExists(err):
		// The name is taken: by this claim's own cluster from an earlier call, or by a cluster
		// that is not this claim's at all, which is never touched.
		if owned, err := p.owned(ctx, req); err != nil || !owned {
			return err
		}
	default:
		return err
	}
	if !simulate {
		return nil
	}
	// The join is written through the cache, which may not show the cluster -- or, with a hub
	// agent, its InternalMemberCluster -- yet; the framework retries a transient error, so the
	// claim completes only once the cluster is really joined.
	return p.stampJoined(ctx, req.ClusterName, false)
}

// owned reports whether the cluster the request names is this claim's own. The read goes through
// the cache: a cluster not visible yet is a transient error, and one that belongs to someone else
// a permanent one.
func (p *Provisioner) owned(ctx context.Context, req fulfiller.Request) (bool, error) {
	cluster := &clusterv1beta1.MemberCluster{}
	if err := p.c.Get(ctx, client.ObjectKey{Name: req.ClusterName}, cluster); err != nil {
		if apierrors.IsNotFound(err) {
			return false, fmt.Errorf("cluster %s is not visible yet", req.ClusterName)
		}
		return false, err
	}
	if cluster.Labels[kfplacementv1alpha1.FulfilledClaimUIDLabel] != string(req.ClaimUID) {
		return false, fulfiller.Permanent(fmt.Errorf("cluster %s exists but is not this claim's", req.ClusterName))
	}
	return true, nil
}

// labelsFor returns the labels the registered cluster carries: every label the claim's selector
// terms match on, since a provider turns a selector into a cluster that satisfies it -- the terms
// are ORed, so carrying all of them satisfies each -- under the ownership labels the framework
// needs, which always win. What a selector asks for through properties or expressions is not
// something this provider can deliver, and a cluster that falls short of its selector expires as
// NotMatching.
func labelsFor(req fulfiller.Request, simulate bool) map[string]string {
	labels := map[string]string{}
	for _, term := range req.ClusterSelectorTerms {
		maps.Copy(labels, term.MatchLabels)
	}
	maps.Copy(labels, req.OwnershipLabels())
	if simulate {
		labels[SimulatedJoinLabel] = "true"
	}
	return labels
}

// Deprovision removes the cluster the request names, if it is this claim's, and is done once it
// is gone. A cluster of another claim under that name is left alone.
func (p *Provisioner) Deprovision(ctx context.Context, req fulfiller.Request) error {
	cluster := &clusterv1beta1.MemberCluster{}
	switch err := p.c.Get(ctx, client.ObjectKey{Name: req.ClusterName}, cluster); {
	case apierrors.IsNotFound(err):
		return nil
	case err != nil:
		return err
	case cluster.Labels[kfplacementv1alpha1.FulfilledClaimUIDLabel] != string(req.ClaimUID):
		klog.V(2).InfoS("Leaving a cluster that is not this claim's in place", "cluster", req.ClusterName, "clusterClaim", req.ClaimName)
		return nil
	}
	if err := p.c.Delete(ctx, cluster, client.Preconditions{UID: &cluster.UID}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if err := p.c.Get(ctx, client.ObjectKey{Name: req.ClusterName}, cluster); err == nil {
		return errors.New("the member cluster is still being removed")
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	klog.V(2).InfoS("Removed a member cluster", "cluster", req.ClusterName, "clusterClaim", req.ClaimName)
	return nil
}

// Remediate brings a registered cluster of this provider back by re-stamping its simulated
// agent status; a cluster whose join is not simulated has nothing this provider can repair.
func (p *Provisioner) Remediate(ctx context.Context, _ fulfiller.Request, cluster *clusterv1beta1.MemberCluster) error {
	if cluster.Labels[SimulatedJoinLabel] != "true" {
		return fulfiller.Permanent(fmt.Errorf("cluster %s does not simulate its member agent, so there is nothing to repair", cluster.Name))
	}
	return p.stampJoined(ctx, cluster.Name, true)
}

// Start keeps the simulated heartbeats of this provider's clusters fresh until ctx is done; the
// manager runs it as a Runnable. Without SimulateJoin it returns at once.
func (p *Provisioner) Start(ctx context.Context) error {
	if !p.opts.SimulateJoin {
		return nil
	}
	ticker := time.NewTicker(p.opts.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			clusters := &clusterv1beta1.MemberClusterList{}
			if err := p.c.List(ctx, clusters, client.MatchingLabels{SimulatedJoinLabel: "true"}); err != nil {
				klog.ErrorS(err, "Failed to list the clusters with a simulated member agent")
				continue
			}
			for i := range clusters.Items {
				if !clusters.Items[i].DeletionTimestamp.IsZero() {
					continue
				}
				// A cluster deleted or still being created between the list and the write is
				// picked up on the next tick.
				if err := p.stampJoined(ctx, clusters.Items[i].Name, true); err != nil {
					klog.ErrorS(err, "Failed to refresh a simulated heartbeat", "cluster", clusters.Items[i].Name)
				}
			}
		}
	}
}

// stampJoined writes a joined, healthy, heartbeating member agent status where the options say.
// An object that cannot be found is an error unless tolerateMissing: Provision wants to retry
// until the cluster (or, with a hub agent, its InternalMemberCluster) exists, while a heartbeat
// refresh and a remediation of a cluster that went away have nothing to do.
func (p *Provisioner) stampJoined(ctx context.Context, clusterName string, tolerateMissing bool) error {
	agent := p.agentStatus()
	missing := func(err error) error {
		if tolerateMissing && apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	switch p.opts.JoinTarget {
	case JoinInternalMemberCluster:
		imc := &clusterv1beta1.InternalMemberCluster{}
		key := client.ObjectKey{Namespace: fmt.Sprintf(utils.NamespaceNameFormat, clusterName), Name: clusterName}
		if err := p.c.Get(ctx, key, imc); err != nil {
			return missing(err)
		}
		setAgentStatus(&imc.Status.AgentStatus, agent)
		return missing(p.c.Status().Update(ctx, imc))
	default:
		mc := &clusterv1beta1.MemberCluster{}
		if err := p.c.Get(ctx, client.ObjectKey{Name: clusterName}, mc); err != nil {
			return missing(err)
		}
		setAgentStatus(&mc.Status.AgentStatus, agent)
		return missing(p.c.Status().Update(ctx, mc))
	}
}

// agentStatus is the member agent status the simulation writes: joined, healthy, heartbeating now.
func (p *Provisioner) agentStatus() clusterv1beta1.AgentStatus {
	now := metav1.Now()
	return clusterv1beta1.AgentStatus{
		Type:                  clusterv1beta1.MemberAgent,
		LastReceivedHeartbeat: now,
		Conditions: []metav1.Condition{
			{Type: string(clusterv1beta1.AgentJoined), Status: metav1.ConditionTrue, Reason: joinedReason, Message: "simulated by the reference provider", LastTransitionTime: now},
			{Type: string(clusterv1beta1.AgentHealthy), Status: metav1.ConditionTrue, Reason: healthyReason, Message: "simulated by the reference provider", LastTransitionTime: now},
		},
	}
}

// setAgentStatus replaces the member agent's entry, preserving the transition times of
// conditions that do not change so a heartbeat refresh is not mistaken for a re-join.
func setAgentStatus(statuses *[]clusterv1beta1.AgentStatus, agent clusterv1beta1.AgentStatus) {
	for i := range *statuses {
		if (*statuses)[i].Type != agent.Type {
			continue
		}
		existing := &(*statuses)[i]
		for _, cond := range agent.Conditions {
			meta.SetStatusCondition(&existing.Conditions, cond)
		}
		existing.LastReceivedHeartbeat = agent.LastReceivedHeartbeat
		return
	}
	*statuses = append(*statuses, agent)
}
