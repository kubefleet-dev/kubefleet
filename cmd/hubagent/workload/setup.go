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

package workload

import (
	"context"
	"math"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	clusterinventory "sigs.k8s.io/cluster-inventory-api/apis/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	workv1alpha1 "sigs.k8s.io/work-api/pkg/apis/v1alpha1"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	fleetv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/cmd/hubagent/options"
	"github.com/kubefleet-dev/kubefleet/pkg/controllers/clusterinventory/clusterprofile"
	"github.com/kubefleet-dev/kubefleet/pkg/controllers/clusterresourcebindingwatcher"
	"github.com/kubefleet-dev/kubefleet/pkg/controllers/clusterresourceplacement"
	"github.com/kubefleet-dev/kubefleet/pkg/controllers/clusterresourceplacementeviction"
	"github.com/kubefleet-dev/kubefleet/pkg/controllers/clusterresourceplacementwatcher"
	"github.com/kubefleet-dev/kubefleet/pkg/controllers/clusterschedulingpolicysnapshot"
	"github.com/kubefleet-dev/kubefleet/pkg/controllers/memberclusterplacement"
	"github.com/kubefleet-dev/kubefleet/pkg/controllers/overrider"
	"github.com/kubefleet-dev/kubefleet/pkg/controllers/resourcechange"
	"github.com/kubefleet-dev/kubefleet/pkg/controllers/rollout"
	"github.com/kubefleet-dev/kubefleet/pkg/controllers/updaterun"
	"github.com/kubefleet-dev/kubefleet/pkg/controllers/workgenerator"
	"github.com/kubefleet-dev/kubefleet/pkg/resourcewatcher"
	"github.com/kubefleet-dev/kubefleet/pkg/scheduler"
	"github.com/kubefleet-dev/kubefleet/pkg/scheduler/clustereligibilitychecker"
	"github.com/kubefleet-dev/kubefleet/pkg/scheduler/framework"
	"github.com/kubefleet-dev/kubefleet/pkg/scheduler/profile"
	"github.com/kubefleet-dev/kubefleet/pkg/scheduler/queue"
	schedulercrbwatcher "github.com/kubefleet-dev/kubefleet/pkg/scheduler/watchers/clusterresourcebinding"
	schedulercrpwatcher "github.com/kubefleet-dev/kubefleet/pkg/scheduler/watchers/clusterresourceplacement"
	schedulercspswatcher "github.com/kubefleet-dev/kubefleet/pkg/scheduler/watchers/clusterschedulingpolicysnapshot"
	"github.com/kubefleet-dev/kubefleet/pkg/scheduler/watchers/membercluster"
	"github.com/kubefleet-dev/kubefleet/pkg/utils"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/controller"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/informer"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/validator"
)

const (
	crpControllerName         = "cluster-resource-placement-controller"
	crpControllerV1Alpha1Name = crpControllerName + "-v1alpha1"
	crpControllerV1Beta1Name  = crpControllerName + "-v1beta1"

	resourceChangeControllerName = "resource-change-controller"
	mcPlacementControllerName    = "memberCluster-placement-controller"

	schedulerQueueName = "scheduler-queue"
)

var (
	v1Alpha1RequiredGVKs = []schema.GroupVersionKind{
		fleetv1alpha1.GroupVersion.WithKind(fleetv1alpha1.MemberClusterKind),
		fleetv1alpha1.GroupVersion.WithKind(fleetv1alpha1.InternalMemberClusterKind),
		fleetv1alpha1.GroupVersion.WithKind(fleetv1alpha1.ClusterResourcePlacementKind),
		workv1alpha1.SchemeGroupVersion.WithKind(workv1alpha1.WorkKind),
	}

	v1Beta1RequiredGVKs = []schema.GroupVersionKind{
		clusterv1beta1.GroupVersion.WithKind(clusterv1beta1.MemberClusterKind),
		clusterv1beta1.GroupVersion.WithKind(clusterv1beta1.InternalMemberClusterKind),
		placementv1beta1.GroupVersion.WithKind(placementv1beta1.ClusterResourcePlacementKind),
		placementv1beta1.GroupVersion.WithKind(placementv1beta1.ClusterResourceBindingKind),
		placementv1beta1.GroupVersion.WithKind(placementv1beta1.ClusterResourceSnapshotKind),
		placementv1beta1.GroupVersion.WithKind(placementv1beta1.ClusterSchedulingPolicySnapshotKind),
		placementv1beta1.GroupVersion.WithKind(placementv1beta1.WorkKind),
		placementv1beta1.GroupVersion.WithKind(placementv1beta1.ClusterResourceOverrideKind),
		placementv1beta1.GroupVersion.WithKind(placementv1beta1.ClusterResourceOverrideSnapshotKind),
		placementv1beta1.GroupVersion.WithKind(placementv1beta1.ResourceOverrideKind),
		placementv1beta1.GroupVersion.WithKind(placementv1beta1.ResourceOverrideSnapshotKind),
	}

	clusterStagedUpdateRunGVKs = []schema.GroupVersionKind{
		placementv1beta1.GroupVersion.WithKind(placementv1beta1.ClusterStagedUpdateRunKind),
		placementv1beta1.GroupVersion.WithKind(placementv1beta1.ClusterStagedUpdateStrategyKind),
		placementv1beta1.GroupVersion.WithKind(placementv1beta1.ClusterApprovalRequestKind),
	}

	clusterInventoryGVKs = []schema.GroupVersionKind{
		clusterinventory.GroupVersion.WithKind("ClusterProfile"),
	}

	evictionGVKs = []schema.GroupVersionKind{
		placementv1beta1.GroupVersion.WithKind(placementv1beta1.ClusterResourcePlacementEvictionKind),
		placementv1beta1.GroupVersion.WithKind(placementv1beta1.ClusterResourcePlacementDisruptionBudgetKind),
	}
)

// SetupControllers set up the customized controllers we developed
func SetupControllers(ctx context.Context, wg *sync.WaitGroup, mgr ctrl.Manager, config *rest.Config, opts *options.Options) error { //nolint:gocyclo
	// TODO: Try to reduce the complexity of this last measured at 33 (failing at > 30) and remove the // nolint:gocyclo
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		klog.ErrorS(err, "unable to create the dynamic client")
		return err
	}

	discoverClient := discovery.NewDiscoveryClientForConfigOrDie(config)
	// AllowedPropagatingAPIs and SkippedPropagatingAPIs are mutually exclusive.
	// If none of them are set, the resourceConfig by default stores a list of skipped propagation APIs.
	resourceConfig := utils.NewResourceConfig(opts.AllowedPropagatingAPIs != "")
	if err = resourceConfig.Parse(opts.AllowedPropagatingAPIs); err != nil {
		// The program will never go here because the parameters have been checked.
		return err
	}
	if err = resourceConfig.Parse(opts.SkippedPropagatingAPIs); err != nil {
		// The program will never go here because the parameters have been checked
		return err
	}

	// setup namespaces we skip propagation
	skippedNamespaces := make(map[string]bool)
	skippedNamespaces["default"] = true
	optionalSkipNS := strings.Split(opts.SkippedPropagatingNamespaces, ";")
	for _, ns := range optionalSkipNS {
		if len(ns) > 0 {
			klog.InfoS("user specified a namespace to skip", "namespace", ns)
			skippedNamespaces[ns] = true
		}
	}

	// the manager for all the dynamically created informers
	dynamicInformerManager := informer.NewInformerManager(dynamicClient, opts.ResyncPeriod.Duration, ctx.Done())
	validator.ResourceInformer = dynamicInformerManager // webhook needs this to check resource scope
	validator.RestMapper = mgr.GetRESTMapper()          // webhook needs this to validate GVK of resource selector

	// Set up  a custom controller to reconcile cluster resource placement
	crpc := &clusterresourceplacement.Reconciler{
		Client:                                  mgr.GetClient(),
		Recorder:                                mgr.GetEventRecorderFor(crpControllerName),
		RestMapper:                              mgr.GetRESTMapper(),
		InformerManager:                         dynamicInformerManager,
		ResourceConfig:                          resourceConfig,
		SkippedNamespaces:                       skippedNamespaces,
		Scheme:                                  mgr.GetScheme(),
		UncachedReader:                          mgr.GetAPIReader(),
		ResourceSnapshotCreationMinimumInterval: opts.ResourceSnapshotCreationMinimumInterval,
		ResourceChangesCollectionDuration:       opts.ResourceChangesCollectionDuration,
	}

	rateLimiter := options.DefaultControllerRateLimiter(opts.RateLimiterOpts)
	var clusterResourcePlacementControllerV1Alpha1 controller.Controller
	var clusterResourcePlacementControllerV1Beta1 controller.Controller
	var memberClusterPlacementController controller.Controller
	if opts.EnableV1Alpha1APIs {
		for _, gvk := range v1Alpha1RequiredGVKs {
			if err = utils.CheckCRDInstalled(discoverClient, gvk); err != nil {
				klog.ErrorS(err, "unable to find the required CRD", "GVK", gvk)
				return err
			}
		}
		klog.Info("Setting up clusterResourcePlacement v1alpha1 controller")
		clusterResourcePlacementControllerV1Alpha1 = controller.NewController(crpControllerV1Alpha1Name, controller.NamespaceKeyFunc, crpc.ReconcileV1Alpha1, rateLimiter)
		klog.Info("Setting up member cluster change controller")
		mcp := &memberclusterplacement.Reconciler{
			InformerManager:     dynamicInformerManager,
			PlacementController: clusterResourcePlacementControllerV1Alpha1,
		}
		memberClusterPlacementController = controller.NewController(mcPlacementControllerName, controller.NamespaceKeyFunc, mcp.Reconcile, rateLimiter)
	}

	if opts.EnableV1Beta1APIs {
		for _, gvk := range v1Beta1RequiredGVKs {
			if err = utils.CheckCRDInstalled(discoverClient, gvk); err != nil {
				klog.ErrorS(err, "unable to find the required CRD", "GVK", gvk)
				return err
			}
		}
		klog.Info("Setting up clusterResourcePlacement v1beta1 controller")
		clusterResourcePlacementControllerV1Beta1 = controller.NewController(crpControllerV1Beta1Name, controller.NamespaceKeyFunc, crpc.Reconcile, rateLimiter)
		klog.Info("Setting up clusterResourcePlacement watcher")
		if err := (&clusterresourceplacementwatcher.Reconciler{
			PlacementController: clusterResourcePlacementControllerV1Beta1,
		}).SetupWithManagerForClusterResourcePlacement(mgr); err != nil {
			klog.ErrorS(err, "Unable to set up the clusterResourcePlacement watcher")
			return err
		}

		klog.Info("Setting up clusterResourceBinding watcher")
		if err := (&clusterresourcebindingwatcher.Reconciler{
			PlacementController: clusterResourcePlacementControllerV1Beta1,
			Client:              mgr.GetClient(),
		}).SetupWithManagerForClusterResourceBinding(mgr); err != nil {
			klog.ErrorS(err, "Unable to set up the clusterResourceBinding watcher")
			return err
		}

		klog.Info("Setting up clusterSchedulingPolicySnapshot watcher")
		if err := (&clusterschedulingpolicysnapshot.Reconciler{
			Client:              mgr.GetClient(),
			PlacementController: clusterResourcePlacementControllerV1Beta1,
		}).SetupWithManagerForClusterSchedulingPolicySnapshot(mgr); err != nil {
			klog.ErrorS(err, "Unable to set up the clusterSchedulingPolicySnapshot watcher")
			return err
		}

		// Set up a new controller to do rollout resources according to CRP rollout strategy
		klog.Info("Setting up rollout controller")
		if err := (&rollout.Reconciler{
			Client:                  mgr.GetClient(),
			UncachedReader:          mgr.GetAPIReader(),
			MaxConcurrentReconciles: int(math.Ceil(float64(opts.MaxFleetSizeSupported)/30) * math.Ceil(float64(opts.MaxConcurrentClusterPlacement)/10)),
			InformerManager:         dynamicInformerManager,
		}).SetupWithManager(mgr); err != nil {
			klog.ErrorS(err, "Unable to set up rollout controller")
			return err
		}

		if opts.EnableEvictionAPIs {
			for _, gvk := range evictionGVKs {
				if err = utils.CheckCRDInstalled(discoverClient, gvk); err != nil {
					klog.ErrorS(err, "Unable to find the required CRD", "GVK", gvk)
					return err
				}
			}
			klog.Info("Setting up cluster resource placement eviction controller")
			if err := (&clusterresourceplacementeviction.Reconciler{
				Client:         mgr.GetClient(),
				UncachedReader: mgr.GetAPIReader(),
			}).SetupWithManager(mgr); err != nil {
				klog.ErrorS(err, "Unable to set up cluster resource placement eviction controller")
				return err
			}
		}

		// Set up a controller to do staged update run, rolling out resources to clusters in a stage by stage manner.
		if opts.EnableStagedUpdateRunAPIs {
			for _, gvk := range clusterStagedUpdateRunGVKs {
				if err = utils.CheckCRDInstalled(discoverClient, gvk); err != nil {
					klog.ErrorS(err, "Unable to find the required CRD", "GVK", gvk)
					return err
				}
			}
			klog.Info("Setting up clusterStagedUpdateRun controller")
			if err = (&updaterun.Reconciler{
				Client:          mgr.GetClient(),
				InformerManager: dynamicInformerManager,
			}).SetupWithManager(mgr); err != nil {
				klog.ErrorS(err, "Unable to set up clusterStagedUpdateRun controller")
				return err
			}
		}

		// Set up the work generator
		klog.Info("Setting up work generator")
		if err := (&workgenerator.Reconciler{
			Client:                  mgr.GetClient(),
			MaxConcurrentReconciles: int(math.Ceil(float64(opts.MaxFleetSizeSupported)/10) * math.Ceil(float64(opts.MaxConcurrentClusterPlacement)/10)),
			InformerManager:         dynamicInformerManager,
		}).SetupWithManager(mgr); err != nil {
			klog.ErrorS(err, "Unable to set up work generator")
			return err
		}

		// Set up the scheduler
		klog.Info("Setting up scheduler")
		defaultProfile := profile.NewDefaultProfile()
		defaultFramework := framework.NewFramework(defaultProfile, mgr)
		defaultSchedulingQueue := queue.NewSimplePlacementSchedulingQueue(
			queue.WithName(schedulerQueueName),
		)
		// we use one scheduler for every 10 concurrent placement
		defaultScheduler := scheduler.NewScheduler("DefaultScheduler", defaultFramework, defaultSchedulingQueue, mgr,
			int(math.Ceil(float64(opts.MaxFleetSizeSupported)/50)*math.Ceil(float64(opts.MaxConcurrentClusterPlacement)/10)))
		klog.Info("Starting the scheduler")
		// Scheduler must run in a separate goroutine as Run() is a blocking call.
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Run() blocks and is set to exit on context cancellation.
			defaultScheduler.Run(ctx)

			klog.InfoS("The scheduler has exited")
		}()

		// Set up the watchers for the controller
		klog.Info("Setting up the clusterResourcePlacement watcher for scheduler")
		if err := (&schedulercrpwatcher.Reconciler{
			Client:             mgr.GetClient(),
			SchedulerWorkQueue: defaultSchedulingQueue,
		}).SetupWithManagerForClusterResourcePlacement(mgr); err != nil {
			klog.ErrorS(err, "Unable to set up clusterResourcePlacement watcher for scheduler")
			return err
		}

		klog.Info("Setting up the clusterSchedulingPolicySnapshot watcher for scheduler")
		if err := (&schedulercspswatcher.Reconciler{
			Client:             mgr.GetClient(),
			SchedulerWorkQueue: defaultSchedulingQueue,
		}).SetupWithManager(mgr); err != nil {
			klog.ErrorS(err, "Unable to set up clusterSchedulingPolicySnapshot watcher for scheduler")
			return err
		}

		klog.Info("Setting up the clusterResourceBinding watcher for scheduler")
		if err := (&schedulercrbwatcher.Reconciler{
			Client:             mgr.GetClient(),
			SchedulerWorkQueue: defaultSchedulingQueue,
		}).SetupWithManagerForClusterResourceBinding(mgr); err != nil {
			klog.ErrorS(err, "Unable to set up clusterResourceBinding watcher for scheduler")
			return err
		}

		klog.Info("Setting up the memberCluster watcher for scheduler")
		if err := (&membercluster.Reconciler{
			Client:                    mgr.GetClient(),
			SchedulerWorkQueue:        defaultSchedulingQueue,
			ClusterEligibilityChecker: clustereligibilitychecker.New(),
		}).SetupWithManager(mgr); err != nil {
			klog.ErrorS(err, "Unable to set up memberCluster watcher for scheduler")
			return err
		}

		// Set up the controllers for overriding resources.
		klog.Info("Setting up the clusterResourceOverride controller")
		if err := (&overrider.ClusterResourceReconciler{
			Reconciler: overrider.Reconciler{
				Client: mgr.GetClient(),
			},
		}).SetupWithManager(mgr); err != nil {
			klog.ErrorS(err, "Unable to set up clusterResourceOverride controller")
			return err
		}

		klog.Info("Setting up the resourceOverride controller")
		if err := (&overrider.ResourceReconciler{
			Reconciler: overrider.Reconciler{
				Client: mgr.GetClient(),
			},
		}).SetupWithManager(mgr); err != nil {
			klog.ErrorS(err, "Unable to set up resourceOverride controller")
			return err
		}

		// Verify cluster inventory CRD installation status.
		if opts.EnableClusterInventoryAPIs {
			for _, gvk := range clusterInventoryGVKs {
				if err = utils.CheckCRDInstalled(discoverClient, gvk); err != nil {
					klog.ErrorS(err, "unable to find the required CRD", "GVK", gvk)
					return err
				}
			}
			klog.Info("Setting up cluster profile controller")
			if err = (&clusterprofile.Reconciler{
				Client:                    mgr.GetClient(),
				ClusterProfileNamespace:   utils.FleetSystemNamespace,
				ClusterUnhealthyThreshold: opts.ClusterUnhealthyThreshold.Duration,
			}).SetupWithManager(mgr); err != nil {
				klog.ErrorS(err, "unable to set up ClusterProfile controller")
				return err
			}
		}
	}

	// Set up a new controller to reconcile any resources in the cluster
	klog.Info("Setting up resource change controller")
	rcr := &resourcechange.Reconciler{
		DynamicClient:               dynamicClient,
		Recorder:                    mgr.GetEventRecorderFor(resourceChangeControllerName),
		RestMapper:                  mgr.GetRESTMapper(),
		InformerManager:             dynamicInformerManager,
		PlacementControllerV1Alpha1: clusterResourcePlacementControllerV1Alpha1,
		PlacementControllerV1Beta1:  clusterResourcePlacementControllerV1Beta1,
	}
	resourceChangeController := controller.NewController(resourceChangeControllerName, controller.ClusterWideKeyFunc, rcr.Reconcile, rateLimiter)

	// Set up a runner that starts all the custom controllers we created above
	resourceChangeDetector := &resourcewatcher.ChangeDetector{
		DiscoveryClient: discoverClient,
		RESTMapper:      mgr.GetRESTMapper(),
		ClusterResourcePlacementControllerV1Alpha1: clusterResourcePlacementControllerV1Alpha1,
		ClusterResourcePlacementControllerV1Beta1:  clusterResourcePlacementControllerV1Beta1,
		ResourceChangeController:                   resourceChangeController,
		MemberClusterPlacementController:           memberClusterPlacementController,
		InformerManager:                            dynamicInformerManager,
		ResourceConfig:                             resourceConfig,
		SkippedNamespaces:                          skippedNamespaces,
		ConcurrentClusterPlacementWorker:           int(math.Ceil(float64(opts.MaxConcurrentClusterPlacement) / 10)),
		ConcurrentResourceChangeWorker:             opts.ConcurrentResourceChangeSyncs,
	}

	if err := mgr.Add(resourceChangeDetector); err != nil {
		klog.ErrorS(err, "Failed to setup resource detector")
		return err
	}
	return nil
}
