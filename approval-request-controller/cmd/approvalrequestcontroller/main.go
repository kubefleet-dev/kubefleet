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
	"context"
	"flag"
	"os"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	localv1beta1 "github.com/kubefleet-dev/kubefleet/approval-request-controller/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/approval-request-controller/pkg/controller"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(placementv1beta1.AddToScheme(scheme))
	utilruntime.Must(localv1beta1.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var probeAddr string
	var logLevel int

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.IntVar(&logLevel, "v", 2, "Log level (0-10)")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	klog.InfoS("Starting ApprovalRequest Controller", "logLevel", logLevel)

	config := ctrl.GetConfigOrDie()

	// Check required CRDs are installed before starting
	if err := waitForRequiredCRDs(config); err != nil {
		klog.ErrorS(err, "Required CRDs not found")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(config, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
	})
	if err != nil {
		klog.ErrorS(err, "Unable to create manager")
		os.Exit(1)
	}

	// Setup ApprovalRequest controller
	approvalRequestReconciler := &controller.Reconciler{
		Client: mgr.GetClient(),
	}
	if err = approvalRequestReconciler.SetupWithManagerForApprovalRequest(mgr); err != nil {
		klog.ErrorS(err, "Unable to create controller", "controller", "ApprovalRequest")
		os.Exit(1)
	}

	// Setup ClusterApprovalRequest controller
	clusterApprovalRequestReconciler := &controller.Reconciler{
		Client: mgr.GetClient(),
	}
	if err = clusterApprovalRequestReconciler.SetupWithManagerForClusterApprovalRequest(mgr); err != nil {
		klog.ErrorS(err, "Unable to create controller", "controller", "ClusterApprovalRequest")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		klog.ErrorS(err, "Unable to set up health check")
		os.Exit(1)
	}

	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		klog.ErrorS(err, "Unable to set up ready check")
		os.Exit(1)
	}

	klog.InfoS("Starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		klog.ErrorS(err, "Problem running manager")
		os.Exit(1)
	}
}

// waitForRequiredCRDs checks that all required CRDs are installed
func waitForRequiredCRDs(config *rest.Config) error {
	requiredCRDs := []string{
		"approvalrequests.placement.kubernetes-fleet.io",
		"clusterapprovalrequests.placement.kubernetes-fleet.io",
		"metriccollectors.placement.kubernetes-fleet.io",
		"metriccollectorreports.placement.kubernetes-fleet.io",
		"workloadtrackers.placement.kubernetes-fleet.io",
		"clusterresourceplacements.placement.kubernetes-fleet.io",
		"clusterresourceoverrides.placement.kubernetes-fleet.io",
		"clusterstagedupdateruns.placement.kubernetes-fleet.io",
		"stagedupdateruns.placement.kubernetes-fleet.io",
	}

	klog.InfoS("Checking for required CRDs", "count", len(requiredCRDs))

	c, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}

	ctx := context.Background()

	return wait.PollImmediate(5*time.Second, 2*time.Minute, func() (bool, error) {
		for _, crdName := range requiredCRDs {
			crd := &apiextensionsv1.CustomResourceDefinition{}
			err := c.Get(ctx, client.ObjectKey{Name: crdName}, crd)
			if err != nil {
				klog.V(2).InfoS("CRD not found yet, waiting...", "crd", crdName)
				return false, nil
			}
			klog.V(3).InfoS("CRD found", "crd", crdName)
		}
		klog.InfoS("All required CRDs are installed")
		return true, nil
	})
}
