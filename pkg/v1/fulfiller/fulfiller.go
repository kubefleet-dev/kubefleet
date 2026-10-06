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

// Package fulfiller is the framework a cluster provider builds on to fulfill KubeFleet cluster
// claims. A provider implements Provision and Deprovision (and may implement Remediate); the
// framework does the rest of the contract FEP-0001 describes: it watches the claims of the
// provider's ClusterProviderClass, refuses unapproved and stale claims, accepts a claim with its
// finalizer, decides whether a claim needs a new cluster or already has one, drives Provision
// until the cluster is registered, writes the claim's status with the claim's own identity, turns
// transient errors permanent once the class's maxProvisionDuration has elapsed, and, on withdrawal
// or expiry, deprovisions only a cluster that never joined the fleet.
//
// This is the CSI external-provisioner split: the framework owns the choreography, the provider
// owns one thing.
package fulfiller

import (
	"context"
	"errors"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/scheduler/clustereligibilitychecker"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/naming"
)

// Request is what the framework hands a provider for one claim.
type Request struct {
	// ClaimName and ClaimUID identify the claim. KubeFleet re-issues a claim under the same name
	// when it needs one more cluster for the same selector, so the UID is what tells two claims
	// apart.
	ClaimName string
	ClaimUID  types.UID
	// ClusterName is the name the provider must register the cluster under: the name of the
	// MemberCluster object, a DNS label of at most 49 characters (it has to fit the hub's
	// fleet-member-<name> namespace). The framework derives it from the claim and records it on
	// the claim before the first Provision call, so it is the same on every call for one claim
	// and a provider restarted mid-flight resumes rather than duplicates. Providers use this
	// value and never derive one of their own.
	ClusterName string
	// ClusterSelectorTerms are the claim's selector terms, within the class's vocabulary.
	ClusterSelectorTerms []kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm
	// ClusterProviderClassName and Parameters come from the claim's class.
	ClusterProviderClassName string
	Parameters               map[string]string
}

// Provisioner is what a cluster provider implements.
//
// Provision must be idempotent and is called repeatedly for one request, every PollInterval,
// until it returns nil: that means the cluster is registered with the fleet, i.e. a MemberCluster
// named Request.ClusterName exists and carries the labels in Request.OwnershipLabels. Registered
// is not joined; whether the member agent reports in is observed by KubeFleet afterwards. A
// transient error means provisioning is still in progress; a transient error is any error not
// marked with Permanent, and the framework retries it until the class's maxProvisionDuration has
// elapsed since acceptance, then fails the claim. Provision must return when ctx is done.
//
// Deprovision must be idempotent too: it removes whatever Provision created for the request,
// including the MemberCluster, and returns nil once nothing is left. The framework calls it only
// for a cluster that has not joined the fleet (see the package doc); a failed claim is cleaned up
// when it fails and again, as a backstop, when it is withdrawn.
type Provisioner interface {
	Provision(ctx context.Context, req Request) error
	Deprovision(ctx context.Context, req Request) error
}

// Remediator is implemented, optionally, by a provider that can repair a registered cluster that
// has stopped being eligible for scheduling (its member agent has gone dark); the framework finds
// it by a type assertion on the Provisioner. Remediate must be idempotent: the framework judges
// the repair by the cluster's eligibility on a later round, within the class's
// maxProvisionDuration, and calls it again while the cluster stays ineligible. Without a
// Remediator, such a cluster fails the claim, and an operator has to delete the MemberCluster
// before a replacement is provisioned.
type Remediator interface {
	Remediate(ctx context.Context, req Request, cluster *clusterv1beta1.MemberCluster) error
}

type permanentError struct{ error }

func (e permanentError) Unwrap() error { return e.error }

// Permanent marks an error from Provision as not worth retrying: the framework fails the claim at
// once instead of retrying until maxProvisionDuration. Permanent(nil) is nil.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err}
}

// IsPermanent reports whether err, or any error it wraps, was marked with Permanent.
func IsPermanent(err error) bool {
	var p permanentError
	return errors.As(err, &p)
}

// Options configure a Reconciler.
type Options struct {
	// ProvisionerName is the name the framework matches against spec.provisionerName of a claim's
	// ClusterProviderClass; claims of other classes are ignored.
	ProvisionerName string
	// Provisioner is the provider.
	Provisioner Provisioner
	// Eligibility are the options of the cluster eligibility checker, which must match the ones
	// the hub's placement policy controller runs with so that both sides judge a cluster alike.
	Eligibility []clustereligibilitychecker.Option
	// APIReader reads the claim at the start of a round, and the member cluster a deprovision
	// decision rests on, from the API server rather than the cache; pass the manager's
	// GetAPIReader. Without it the framework still works, through the cached client, but a
	// round can then start on a view that lags the previous round's write and repeat a provider
	// call, which the one-call-per-round property relies on it not doing. The member cluster
	// list that identity and freshness are judged on stays cached.
	APIReader client.Reader
	// PollInterval is how often an in-progress provision is retried. Defaults to 30 seconds.
	PollInterval time.Duration
	// Recorder, when set, receives an event for every acceptance, progress report, fulfillment,
	// failure, and cleanup problem. The events rule in the framework's RBAC is needed only then.
	Recorder record.EventRecorder
}

// Reconciler fulfills the cluster claims of one provider.
type Reconciler struct {
	client.Client

	reader client.Reader

	provisionerName string
	provisioner     Provisioner
	eligibility     *clustereligibilitychecker.ClusterEligibilityChecker
	pollInterval    time.Duration
	recorder        record.EventRecorder
}

const defaultPollInterval = 30 * time.Second

// New returns a Reconciler that drives the given provider with the hub client.
func New(hubClient client.Client, opts Options) *Reconciler {
	if opts.PollInterval <= 0 {
		opts.PollInterval = defaultPollInterval
	}
	if opts.APIReader == nil {
		opts.APIReader = hubClient
	}
	return &Reconciler{
		Client:          hubClient,
		reader:          opts.APIReader,
		provisionerName: opts.ProvisionerName,
		provisioner:     opts.Provisioner,
		eligibility:     clustereligibilitychecker.New(opts.Eligibility...),
		pollInterval:    opts.PollInterval,
		recorder:        opts.Recorder,
	}
}

// SetupWithManager registers the reconciler on the hub manager. The controller is named after
// the provider, so that two providers in one process do not collide. Only claims are watched: a
// provision in progress is polled every PollInterval rather than watched through MemberClusters,
// which keeps the framework to one informer at the cost of up to one interval of latency.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("fulfiller-" + naming.Sanitize(r.provisionerName)).
		For(&kfplacementv1alpha1.ClusterClaim{}).
		Complete(r)
}
