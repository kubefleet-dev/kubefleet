/*
Copyright 2025 The KubeFleet Authors.

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

package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	clusterinventory "sigs.k8s.io/cluster-inventory-api/apis/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	ctrlwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"

	fleetnetworkingv1alpha1 "go.goms.io/fleet-networking/api/v1alpha1"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	placementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1alpha1"
	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/cmd/hubagent/options"
	"github.com/kubefleet-dev/kubefleet/cmd/hubagent/workload"
	mcv1beta1 "github.com/kubefleet-dev/kubefleet/pkg/controllers/membercluster/v1beta1"
	readiness "github.com/kubefleet-dev/kubefleet/pkg/utils/informer/readiness"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/validator"
	"github.com/kubefleet-dev/kubefleet/pkg/webhook"
	// +kubebuilder:scaffold:imports
)

var (
	scheme         = runtime.NewScheme()
	handleExitFunc = func() {
		klog.Flush()
	}

	exitWithErrorFunc = func() {
		handleExitFunc()
		os.Exit(1)
	}
)

const (
	FleetWebhookCertDir = "/tmp/k8s-webhook-server/serving-certs"
	FleetWebhookPort    = 9443
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(placementv1beta1.AddToScheme(scheme))
	utilruntime.Must(clusterv1beta1.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	utilruntime.Must(fleetnetworkingv1alpha1.AddToScheme(scheme))
	utilruntime.Must(placementv1alpha1.AddToScheme(scheme))
	utilruntime.Must(clusterinventory.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
	klog.InitFlags(nil)
}

func main() {
	// The wait group for synchronizing the steps (esp. the exit) of the controller manager and the scheduler.
	var wg sync.WaitGroup

	opts := options.NewOptions()
	opts.AddFlags(flag.CommandLine)

	flag.Parse()
	defer handleExitFunc()

	flag.VisitAll(func(f *flag.Flag) {
		klog.InfoS("flag:", "name", f.Name, "value", f.Value)
	})
	if errs := opts.Validate(); len(errs) != 0 {
		klog.ErrorS(errs.ToAggregate(), "invalid parameter")
		exitWithErrorFunc()
	}

	// Set up controller-runtime logger
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	// Create separate configs for the general access purpose and the leader election purpose.
	//
	// This aims to improve the availability of the hub agent; originally all access to the API
	// server would share the same rate limiting configuration, and under adverse conditions (e.g.,
	// large volume of concurrent placements) controllers would exhause all tokens in the rate limiter,
	// and effectively starve the leader election process (the runtime can no longer renew leases),
	// which would trigger the hub agent to restart even though the system remains functional.
	defaultCfg := ctrl.GetConfigOrDie()
	leaderElectionCfg := rest.CopyConfig(defaultCfg)

	defaultCfg.QPS, defaultCfg.Burst = float32(opts.HubQPS), opts.HubBurst
	leaderElectionCfg.QPS, leaderElectionCfg.Burst = float32(opts.LeaderElectionQPS), opts.LeaderElectionBurst

	mgrOpts := ctrl.Options{
		Scheme: scheme,
		Cache: cache.Options{
			SyncPeriod:       &opts.ResyncPeriod.Duration,
			DefaultTransform: cache.TransformStripManagedFields(),
		},
		LeaderElection:       opts.LeaderElection.LeaderElect,
		LeaderElectionConfig: leaderElectionCfg,
		// If leader election is enabled, the hub agent by default uses a setup
		// with a lease duration of 180 secs, a renew deadline of 120 secs, and a retry period of 5 secs.
		// These values are set significantly higher than the controller-runtime defaults
		// (15 seconds, 10 seconds, and 2 seconds respectively), as under heavy loads the hub agent
		// might have difficulty renewing its lease in time due to API server side latencies, which
		// might further lead to the agent losing the leadership (even when it is the only candidate
		// running) and restarting unexpectedly.
		//
		// Note (chenyu1): a minor side effect with the higher values is that when the agent does restart,
		// (or in the future when we do run multiple hub agent replicas), the new leader might have to wait a bit
		// longer (up to 180 seconds) to acquire the leadership, which should still be acceptable in most scenarios.
		LeaseDuration:              &opts.LeaderElection.LeaseDuration.Duration,
		RenewDeadline:              &opts.LeaderElection.RenewDeadline.Duration,
		RetryPeriod:                &opts.LeaderElection.RetryPeriod.Duration,
		LeaderElectionID:           opts.LeaderElection.ResourceName,
		LeaderElectionNamespace:    opts.LeaderElection.ResourceNamespace,
		LeaderElectionResourceLock: opts.LeaderElection.ResourceLock,
		HealthProbeBindAddress:     opts.HealthProbeAddress,
		Metrics: metricsserver.Options{
			BindAddress: opts.MetricsBindAddress,
		},
		WebhookServer: ctrlwebhook.NewServer(ctrlwebhook.Options{
			Port:    FleetWebhookPort,
			CertDir: FleetWebhookCertDir,
		}),
	}
	if opts.EnablePprof {
		mgrOpts.PprofBindAddress = fmt.Sprintf(":%d", opts.PprofPort)
	}
	mgr, err := ctrl.NewManager(defaultCfg, mgrOpts)
	if err != nil {
		klog.ErrorS(err, "unable to start controller manager.")
		exitWithErrorFunc()
	}

	klog.V(2).InfoS("starting hubagent")
	if opts.EnableV1Beta1APIs {
		klog.Info("Setting up memberCluster v1beta1 controller")
		if err = (&mcv1beta1.Reconciler{
			Client:                  mgr.GetClient(),
			NetworkingAgentsEnabled: opts.NetworkingAgentsEnabled,
			MaxConcurrentReconciles: int(math.Ceil(float64(opts.MaxFleetSizeSupported) / 100)), //one member cluster reconciler routine per 100 member clusters
			ForceDeleteWaitTime:     opts.ForceDeleteWaitTime.Duration,
		}).SetupWithManager(mgr, "membercluster-controller"); err != nil {
			klog.ErrorS(err, "unable to create v1beta1 controller", "controller", "MemberCluster")
			exitWithErrorFunc()
		}
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		klog.ErrorS(err, "unable to set up health check")
		exitWithErrorFunc()
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		klog.ErrorS(err, "unable to set up ready check")
		exitWithErrorFunc()
	}

	if opts.EnableWebhook {
		whiteListedUsers := strings.Split(opts.WhiteListedUsers, ",")
		if err := SetupWebhook(mgr, options.WebhookClientConnectionType(opts.WebhookClientConnectionType), opts.WebhookServiceName, whiteListedUsers,
			opts.EnableGuardRail, opts.EnableV1Beta1APIs, opts.DenyModifyMemberClusterLabels, opts.EnableWorkload, opts.NetworkingAgentsEnabled); err != nil {
			klog.ErrorS(err, "unable to set up webhook")
			exitWithErrorFunc()
		}
	}

	ctx := ctrl.SetupSignalHandler()
	if err := workload.SetupControllers(ctx, &wg, mgr, defaultCfg, opts); err != nil {
		klog.ErrorS(err, "unable to set up controllers")
		exitWithErrorFunc()
	}

	// Add readiness check for dynamic informer cache AFTER controllers are set up.
	// This ensures the discovery cache is populated before the hub agent is marked ready,
	// which is critical for all controllers that rely on dynamic resource discovery.
	// AddReadyzCheck adds additional readiness check instead of replacing the one registered earlier provided the name is different.
	// Both registered checks need to pass for the manager to be considered ready.
	if err := mgr.AddReadyzCheck("informer-cache", readiness.InformerReadinessChecker(validator.ResourceInformer)); err != nil {
		klog.ErrorS(err, "unable to set up informer cache readiness check")
		exitWithErrorFunc()
	}

	// +kubebuilder:scaffold:builder

	wg.Add(1)
	go func() {
		defer wg.Done()

		// Start() is a blocking call and it is set to exit on context cancellation.
		if err := mgr.Start(ctx); err != nil {
			klog.ErrorS(err, "problem starting manager")
			exitWithErrorFunc()
		}

		klog.InfoS("The controller manager has exited")
	}()

	// Wait for the controller manager and the scheduler to exit.
	wg.Wait()
}

// SetupWebhook generates the webhook cert and then set up the webhook configurator.
func SetupWebhook(mgr manager.Manager, webhookClientConnectionType options.WebhookClientConnectionType, webhookServiceName string,
	whiteListedUsers []string, enableGuardRail, isFleetV1Beta1API bool, denyModifyMemberClusterLabels bool, enableWorkload bool, networkingAgentsEnabled bool) error {
	// Generate self-signed key and crt files in FleetWebhookCertDir for the webhook server to start.
	w, err := webhook.NewWebhookConfig(mgr, webhookServiceName, FleetWebhookPort, &webhookClientConnectionType, FleetWebhookCertDir, enableGuardRail, denyModifyMemberClusterLabels, enableWorkload)
	if err != nil {
		klog.ErrorS(err, "fail to generate WebhookConfig")
		return err
	}
	if err = mgr.Add(w); err != nil {
		klog.ErrorS(err, "unable to add WebhookConfig")
		return err
	}
	if err = webhook.AddToManager(mgr, whiteListedUsers, denyModifyMemberClusterLabels, networkingAgentsEnabled); err != nil {
		klog.ErrorS(err, "unable to register webhooks to the manager")
		return err
	}
	return nil
}
