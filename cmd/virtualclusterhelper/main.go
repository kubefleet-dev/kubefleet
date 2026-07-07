package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/rand"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	"k8s.io/utils/set"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	experimentalv1beta1 "github.com/kubefleet-dev/kubefleet/apis/experimental/v1beta1"
	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

var (
	scheme = runtime.NewScheme()

	hubClient client.Client

	virtualClusters = set.Set[string]{}
)

const (
	controllerName = "virtualclusterhelper"

	clusterRequestCleanupFinalizer = "experimental.kubefleet.dev/cluster-request-cleanup"

	svcAccountName    = "virtual-cluster-helper"
	fleetSystemNSName = "fleet-system"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(placementv1beta1.AddToScheme(scheme))
	utilruntime.Must(clusterv1beta1.AddToScheme(scheme))
	utilruntime.Must(experimentalv1beta1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
	klog.InitFlags(nil)
}

func main() {
	defer klog.Flush()

	// Cancel on interrupt.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	// Create a new Kubernetes client.
	defaultCfg := ctrl.GetConfigOrDie()
	var err error
	hubClient, err = client.New(defaultCfg, client.Options{Scheme: scheme})
	if err != nil {
		klog.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	for {
		select {
		case <-c:
			cancel()
			return
		default:
			processClusterRequests(ctx)
			processHeartbeats(ctx)
			processWorks(ctx)
		}

		time.Sleep(15 * time.Second)
		klog.Info("🕰️ Processing completed; check again in 15 seconds...")
	}
}

func processClusterRequests(ctx context.Context) {
	// List all the cluster requests.
	clusterRequestList := &experimentalv1beta1.ClusterRequestList{}
	if err := hubClient.List(ctx, clusterRequestList); err != nil {
		klog.Errorf("🛑 Failed to list cluster requests: %v", err)
		return
	}
	clusterRequests := clusterRequestList.Items
	var eligibleClusterRequests []*experimentalv1beta1.ClusterRequest
	for idx := range clusterRequests {
		clusterRequest := &clusterRequests[idx]
		completedCond := meta.FindStatusCondition(clusterRequest.Status.Conditions, experimentalv1beta1.ClusterRequestCondTypeCompleted)
		manager, hasLabel := clusterRequest.Labels[experimentalv1beta1.ClusterRequestManagedByLabelKey]
		isManagedByMe := manager == controllerName
		if completedCond == nil && (!hasLabel || isManagedByMe) {
			eligibleClusterRequests = append(eligibleClusterRequests, clusterRequest)
		}
	}
	if len(eligibleClusterRequests) == 0 {
		klog.Info("⚠️ No eligible cluster requests found; check again in 15 seconds...")
		return
	}

	// Sort the requests by creation timestamp in ascending order.
	slices.SortFunc(eligibleClusterRequests, func(a, b *experimentalv1beta1.ClusterRequest) int {
		return a.CreationTimestamp.Compare(b.CreationTimestamp.Time)
	})
	targetClusterRequest := eligibleClusterRequests[0]
	klog.Infof("✅ Found an eligible cluster request %q created at %s; processing it now...", targetClusterRequest.Name, targetClusterRequest.CreationTimestamp.String())

	_, isManaged := targetClusterRequest.Labels[experimentalv1beta1.ClusterRequestManagedByLabelKey]
	if !isManaged {
		// If the request is unmanaged, add a label to claim the ownership of the request.
		if targetClusterRequest.Labels == nil {
			targetClusterRequest.Labels = make(map[string]string)
		}
		targetClusterRequest.Labels[experimentalv1beta1.ClusterRequestManagedByLabelKey] = controllerName

		// Add a finalizer to the request.
		if !controllerutil.ContainsFinalizer(targetClusterRequest, clusterRequestCleanupFinalizer) {
			controllerutil.AddFinalizer(targetClusterRequest, clusterRequestCleanupFinalizer)
		}

		if err := hubClient.Update(ctx, targetClusterRequest); err != nil {
			klog.Errorf("🛑 Failed to claim the ownership of cluster request %q: %v", targetClusterRequest.Name, err)
			return
		}
		klog.Infof("✅ Claimed the ownership of cluster request %q", targetClusterRequest.Name)
	}

	if targetClusterRequest.Status.ProvisionedClusterName == nil {
		// No cluster has been created for the request.
		generatedClusterName := fmt.Sprintf("virtual-cluster-%s", rand.String(8))
		targetClusterRequest.Status.ProvisionedClusterName = &generatedClusterName
		if err := hubClient.Status().Update(ctx, targetClusterRequest); err != nil {
			klog.Errorf("🛑 Failed to update the status of cluster request %q with the provisioned cluster name: %v", targetClusterRequest.Name, err)
			return
		}
		klog.Infof("✅ Updated the status of cluster request %q with the provisioned cluster name %q", targetClusterRequest.Name, generatedClusterName)
	}

	provisionedClusterName := *targetClusterRequest.Status.ProvisionedClusterName
	// Retrieve the member cluster object.
	memberCluster := &clusterv1beta1.MemberCluster{}
	if err := hubClient.Get(ctx, client.ObjectKey{Name: provisionedClusterName}, memberCluster); err != nil {
		if apierrors.IsNotFound(err) {
			memberCluster = nil
		} else {
			klog.Errorf("🛑 Failed to get the provisioned member cluster %q for cluster request %q: %v", provisionedClusterName, targetClusterRequest.Name, err)
			return
		}
	}

	// TO-DO: mark the cluster request as failed if it cannot be fulfilled given the state of the cluster selector.
	wantLabels := map[string]string{}
	if len(targetClusterRequest.Spec.ClusterSelector.Terms) != 1 {
		klog.Errorf("🛑 The cluster request %q cannot be fulfilled by the virtual helper (too many terms)", targetClusterRequest.Name)
		return
	}
	firstTerm := targetClusterRequest.Spec.ClusterSelector.Terms[0]
	if len(firstTerm.MatchLabelExpressions) > 0 {
		klog.Errorf("🛑 The cluster request %q cannot be fulfilled by the virtual helper (matchLabelExpressions are not supported)", targetClusterRequest.Name)
		return
	}
	if len(firstTerm.MatchClusterPropertyExpressions) > 0 {
		klog.Errorf("🛑 The cluster request %q cannot be fulfilled by the virtual helper (matchClusterPropertyExpressions are not supported)", targetClusterRequest.Name)
		return
	}
	wantLabels = firstTerm.MatchLabels

	if memberCluster == nil {
		// The provisioned member cluster does not exist, create it.
		memberCluster = &clusterv1beta1.MemberCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:   provisionedClusterName,
				Labels: wantLabels,
			},
			Spec: clusterv1beta1.MemberClusterSpec{
				Identity: rbacv1.Subject{
					Kind:      rbacv1.ServiceAccountKind,
					Name:      svcAccountName,
					Namespace: fleetSystemNSName,
				},
				HeartbeatPeriodSeconds: 300,
			},
		}
		if err := hubClient.Create(ctx, memberCluster); err != nil {
			klog.Errorf("🛑 Failed to create the provisioned member cluster %q for cluster request %q: %v", provisionedClusterName, targetClusterRequest.Name, err)
			return
		}
		klog.Infof("✅ Created the provisioned member cluster %q for cluster request %q; waiting for the Fleet hub agent to respond", provisionedClusterName, targetClusterRequest.Name)
	}

	// Check to see if the interal member cluster has been created.
	memberClusterReservedNSName := fmt.Sprintf("fleet-member-%s", provisionedClusterName)
	internalMemberCluster := &clusterv1beta1.InternalMemberCluster{}
	if err := hubClient.Get(ctx, client.ObjectKey{Name: provisionedClusterName, Namespace: memberClusterReservedNSName}, internalMemberCluster); err != nil {
		if apierrors.IsNotFound(err) {
			internalMemberCluster = nil
		} else {
			klog.Errorf("🛑 Failed to get the internal member cluster %q for cluster request %q: %v", provisionedClusterName, targetClusterRequest.Name, err)
			return
		}
	}

	if internalMemberCluster == nil {
		klog.Info("⚠️ The internal member cluster has not been created yet; waiting for the Fleet hub agent to respond")
		return
	}

	klog.Infof("✅ The internal member cluster %q has been created for cluster request %q", internalMemberCluster.Name, targetClusterRequest.Name)
	virtualClusters.Insert(provisionedClusterName)

	// Mark the cluster request as completed by setting the Completed condition to True.
	meta.SetStatusCondition(&targetClusterRequest.Status.Conditions, metav1.Condition{
		Type:               experimentalv1beta1.ClusterRequestCondTypeCompleted,
		Status:             metav1.ConditionTrue,
		Reason:             "ProvisioningCompleted",
		Message:            fmt.Sprintf("The cluster request has been completed and the member cluster %q has been provisioned", provisionedClusterName),
		ObservedGeneration: targetClusterRequest.GetGeneration(),
	})
	if err := hubClient.Status().Update(ctx, targetClusterRequest); err != nil {
		klog.Errorf("🛑 Failed to update the status of cluster request %q to mark it as completed: %v", targetClusterRequest.Name, err)
		return
	}
	klog.Infof("✅ Marked cluster request %q as completed", targetClusterRequest.Name)
}

func processHeartbeats(ctx context.Context) {
	for clusterName := range virtualClusters {
		memberClusterReservedNSName := fmt.Sprintf("fleet-member-%s", clusterName)
		internalMemberCluster := &clusterv1beta1.InternalMemberCluster{}
		if err := hubClient.Get(ctx, client.ObjectKey{Name: clusterName, Namespace: memberClusterReservedNSName}, internalMemberCluster); err != nil {
			klog.Errorf("🛑 Failed to get the internal member cluster %q for heartbeat processing: %v", clusterName, err)
			continue
		}

		// Mark the cluster as joined with the member agent, if it hasn't been so already.
		// This must run before the heartbeat update so that the agent status entry exists.
		joinedCond := internalMemberCluster.GetConditionWithType(clusterv1beta1.MemberAgent, string(clusterv1beta1.AgentJoined))
		if joinedCond == nil || joinedCond.Status != metav1.ConditionTrue {
			internalMemberCluster.SetConditionsWithType(clusterv1beta1.MemberAgent, metav1.Condition{
				Type:               string(clusterv1beta1.AgentJoined),
				Status:             metav1.ConditionTrue,
				Reason:             "VirtualClusterJoined",
				ObservedGeneration: internalMemberCluster.GetGeneration(),
			})
			klog.Infof("✅ Marked internal member cluster %q as joined", clusterName)
		}

		// Update the heartbeat. The agent status entry is guaranteed to exist at this point.
		desiredAgentStatus := internalMemberCluster.GetAgentStatus(clusterv1beta1.MemberAgent)
		if desiredAgentStatus != nil {
			desiredAgentStatus.LastReceivedHeartbeat = metav1.Now()
		}

		if err := hubClient.Status().Update(ctx, internalMemberCluster); err != nil {
			klog.Errorf("🛑 Failed to update the status of internal member cluster %q: %v", clusterName, err)
			continue
		}
		klog.Infof("✅ Updated the status of internal member cluster %q", clusterName)
	}
}

func processWorks(ctx context.Context) {
	for clusterName := range virtualClusters {
		fleetMemberReservedNSName := fmt.Sprintf("fleet-member-%s", clusterName)

		// List all Work objects in the reserved namespace for this virtual cluster.
		workList := &placementv1beta1.WorkList{}
		if err := hubClient.List(ctx, workList, client.InNamespace(fleetMemberReservedNSName)); err != nil {
			klog.Errorf("🛑 Failed to list works for virtual cluster %q: %v", clusterName, err)
			continue
		}

		for idx := range workList.Items {
			work := &workList.Items[idx]

			appliedCond := meta.FindStatusCondition(work.Status.Conditions, placementv1beta1.WorkConditionTypeApplied)
			availableCond := meta.FindStatusCondition(work.Status.Conditions, placementv1beta1.WorkConditionTypeAvailable)
			if appliedCond != nil && appliedCond.Status == metav1.ConditionTrue &&
				availableCond != nil && availableCond.Status == metav1.ConditionTrue {
				// Already applied and available; nothing to do.
				continue
			}

			// Mark the work as applied and available.
			now := metav1.Now()
			meta.SetStatusCondition(&work.Status.Conditions, metav1.Condition{
				Type:               placementv1beta1.WorkConditionTypeApplied,
				Status:             metav1.ConditionTrue,
				Reason:             "VirtualClusterWorkApplied",
				LastTransitionTime: now,
				ObservedGeneration: work.GetGeneration(),
			})
			meta.SetStatusCondition(&work.Status.Conditions, metav1.Condition{
				Type:               placementv1beta1.WorkConditionTypeAvailable,
				Status:             metav1.ConditionTrue,
				Reason:             "VirtualClusterWorkAvailable",
				LastTransitionTime: now,
				ObservedGeneration: work.GetGeneration(),
			})
			if err := hubClient.Status().Update(ctx, work); err != nil {
				klog.Errorf("🛑 Failed to update work %q/%q for virtual cluster %q: %v", work.Namespace, work.Name, clusterName, err)
				continue
			}
			klog.Infof("✅ Marked work %q/%q as applied and available for virtual cluster %q", work.Namespace, work.Name, clusterName)
		}
	}
}
