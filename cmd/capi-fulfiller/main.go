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

// The capi-fulfiller fulfills KubeFleet cluster claims through Cluster API. It runs against the
// fleet hub (the kubeconfig controller-runtime resolves) and, when the management cluster is
// not the hub, a second kubeconfig for it.
package main

import (
	"flag"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/v1/fulfiller"
	"github.com/kubefleet-dev/kubefleet/pkg/v1/fulfiller/capi"
)

var scheme = runtime.NewScheme()

func init() {
	klog.InitFlags(nil)
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(clusterv1beta1.AddToScheme(scheme))
	utilruntime.Must(kfplacementv1alpha1.AddToScheme(scheme))
}

func main() {
	var (
		managementKubeconfig    = flag.String("management-kubeconfig", "", "Path to the kubeconfig of the Cluster API management cluster; empty when the fleet hub is the management cluster.")
		provisionerName         = flag.String("provisioner-name", capi.ProvisionerName, "The spec.provisionerName of the ClusterProviderClasses this fulfiller serves.")
		pollInterval            = flag.Duration("poll-interval", 30*time.Second, "How often a cluster still provisioning is checked.")
		metricsBindAddress      = flag.String("metrics-bind-address", ":8080", "The address the metrics endpoint binds to.")
		healthProbeBindAddress  = flag.String("health-probe-bind-address", ":8081", "The address the health probe endpoint binds to.")
		leaderElect             = flag.Bool("leader-elect", true, "Enable leader election, so that only one replica fulfills claims at a time.")
		leaderElectionNamespace = flag.String("leader-election-namespace", "fleet-system", "The namespace of the leader election lease.")
	)
	flag.Parse()
	defer klog.Flush()
	ctrl.SetLogger(zap.New(zap.UseDevMode(false)))

	hubConfig := ctrl.GetConfigOrDie()
	mgr, err := ctrl.NewManager(hubConfig, ctrl.Options{
		Scheme:                  scheme,
		Metrics:                 metricsserver.Options{BindAddress: *metricsBindAddress},
		HealthProbeBindAddress:  *healthProbeBindAddress,
		LeaderElection:          *leaderElect,
		LeaderElectionNamespace: *leaderElectionNamespace,
		LeaderElectionID:        "capi-fulfiller.kubefleet.dev",
		// A replica that stops hands the lease over at once rather than letting it expire.
		LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		klog.ErrorS(err, "Failed to create the manager for the hub cluster")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}

	// Cluster API objects are read straight from the management cluster's API server, so that a
	// fulfiller watching only its own claims never has to cache a whole cluster inventory.
	managementConfig := hubConfig
	if *managementKubeconfig == "" {
		klog.InfoS("No management kubeconfig given; Cluster API objects are applied to the fleet hub itself")
	} else {
		if managementConfig, err = clientcmd.BuildConfigFromFlags("", *managementKubeconfig); err != nil {
			klog.ErrorS(err, "Failed to load the management cluster kubeconfig", "path", *managementKubeconfig)
			klog.FlushAndExit(klog.ExitFlushTimeout, 1)
		}
	}
	management, err := client.New(managementConfig, client.Options{Scheme: scheme})
	if err != nil {
		klog.ErrorS(err, "Failed to create the client for the management cluster")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}

	bridge := capi.New(capi.Options{Hub: mgr.GetClient(), HubReader: mgr.GetAPIReader(), Management: management})
	framework := fulfiller.New(mgr.GetClient(), fulfiller.Options{
		ProvisionerName: *provisionerName,
		Provisioner:     bridge,
		APIReader:       mgr.GetAPIReader(),
		PollInterval:    *pollInterval,
		Recorder:        mgr.GetEventRecorder("capi-fulfiller"),
	})
	if err := framework.SetupWithManager(mgr); err != nil {
		klog.ErrorS(err, "Failed to set up the fulfiller")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		klog.ErrorS(err, "Failed to add the health check")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		klog.ErrorS(err, "Failed to add the readiness check")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}
	klog.InfoS("Starting the Cluster API fulfiller", "provisioner", *provisionerName, "managementKubeconfig", *managementKubeconfig)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		klog.ErrorS(err, "The manager stopped with an error")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}
}
