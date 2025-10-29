package azure

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

// Important: for simplicity reasons this controller is currently set up to return static
// results. API behavior is for preview and demonstration purposes only, as the capacity support
// is under development and is not yet publicly available.

const (
	azureDynamicResourceProviderControllerName = "azure-dynamic-resource-provider"
)

const (
	azureDynamicResProviderCondTypClaimProcessed                      = "DynamicResourceClaimProcessed"
	azureDynamicResProviderCondTypClaimProcessedReasonResNotSupported = "ResourceNotSupported"
	azureDynamicResProviderCondTypClaimProcessedReasonCompleted       = "ClaimProcessingCompleted"
)

const (
	clusterWithOverridenWeight1 = "model-server-cluster-1"
	clusterWithOverridenWeight2 = "model-server-cluster-2"
	clusterWithOverridenWeight3 = "kind-cluster-1"
	clusterWithOverridenWeight4 = "kind-cluster-2"
	clusterWithOverridenWeight5 = "model-server-cluster-3"
)

var (
	supportedResourceNames = sets.KeySet(map[string]interface{}{
		"kubernetes.azure.com/nodes/additional-capacity": nil,
	})
	clustersWithWeightOverrides = map[string]int64{
		clusterWithOverridenWeight1: 80,
		clusterWithOverridenWeight2: 60,
		clusterWithOverridenWeight3: 40,
		clusterWithOverridenWeight4: 20,
		clusterWithOverridenWeight5: 10,
	}
)

type Reconciler struct {
	HubClient client.Client
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	placementSchedulingCtxRef := klog.KRef(req.Namespace, req.Name)
	startTime := time.Now()
	klog.V(2).InfoS("Reconciliation loop starts", "controller", "azureDynamicResourceProvider", "placementSchedulingContext", placementSchedulingCtxRef)
	defer func() {
		latency := time.Since(startTime).Milliseconds()
		klog.V(2).InfoS("Reconciliation loop ends", "controller", "azureDynamicResourceProvider", "placementSchedulingContext", placementSchedulingCtxRef, "latency", latency)
	}()

	placementSchedulingCtxObj, err := r.retrievePlacementSchedulingContextSpec(ctx, req.Namespace, req.Name)
	if err != nil {
		klog.ErrorS(err, "Failed to retrieve placement scheduling context spec", "placementSchedulingCtx", placementSchedulingCtxRef)
		return ctrl.Result{}, err
	}
	potentialClusters := placementSchedulingCtxObj.GetPlacementSchedulingContextSpec().PotentialClusters

	placementRef := placementSchedulingCtxObj.GetPlacementSchedulingContextSpec().PlacementRef
	dynamicResourceClaims, err := r.retrieveDynamicResourceClaims(ctx, req.Namespace, placementRef)
	if err != nil {
		klog.ErrorS(err, "Failed to retrieve dynamic resource claims", "placementSchedulingCtx", placementSchedulingCtxRef)
		return ctrl.Result{}, err
	}

	drcStatusCollection := []placementv1beta1.DynamicResourceClaimStatus{}
	for idx := range dynamicResourceClaims {
		drc := dynamicResourceClaims[idx]

		if drc.ControllerName == nil || *drc.ControllerName != azureDynamicResourceProviderControllerName {
			klog.V(2).InfoS("Skip processing dynamic resource claim as it is not managed by azure dynamic resource provider", "dynamicResourceName", drc.ResourceName, "placementSchedulingCtx", placementSchedulingCtxRef)
			continue
		}

		drcStatus := placementv1beta1.DynamicResourceClaimStatus{
			ResourceName: drc.ResourceName,
			Conditions:   []metav1.Condition{},
		}

		if !supportedResourceNames.Has(drc.ResourceName) {
			klog.V(2).InfoS("Skip processing dynamic resource claim as the resource name is not supported by azure dynamic resource provider", "dynamicResourceName", drc.ResourceName, "placementSchedulingCtx", placementSchedulingCtxRef)
			meta.SetStatusCondition(&drcStatus.Conditions, metav1.Condition{
				Type:               azureDynamicResProviderCondTypClaimProcessed,
				Status:             metav1.ConditionFalse,
				Reason:             azureDynamicResProviderCondTypClaimProcessedReasonResNotSupported,
				Message:            fmt.Sprintf("Resource name %s in the dynamic resource claim is not supported", drc.ResourceName),
				ObservedGeneration: placementSchedulingCtxObj.GetGeneration(),
			})
			drcStatusCollection = append(drcStatusCollection, drcStatus)
			continue
		}

		suitableClusters := map[string]int64{}
		for _, clusterName := range potentialClusters {
			if weight, isOverriden := clustersWithWeightOverrides[clusterName]; isOverriden {
				suitableClusters[clusterName] = weight
			}
		}
		drcStatus.SuitableClusters = suitableClusters
		meta.SetStatusCondition(&drcStatus.Conditions, metav1.Condition{
			Type:               azureDynamicResProviderCondTypClaimProcessed,
			Status:             metav1.ConditionTrue,
			Reason:             azureDynamicResProviderCondTypClaimProcessedReasonCompleted,
			Message:            fmt.Sprintf("Dynamic resource claim %s has been processed successfully by azure dynamic resource provider", drc.ResourceName),
			ObservedGeneration: placementSchedulingCtxObj.GetGeneration(),
		})
		drcStatusCollection = append(drcStatusCollection, drcStatus)
	}

	// Update the placement scheduling context status.
	if err := r.updatePlacementSchedulingContextStatus(ctx, placementSchedulingCtxObj, drcStatusCollection); err != nil {
		// No need to log the error.
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *Reconciler) retrievePlacementSchedulingContextSpec(ctx context.Context, namespace, name string) (placementv1beta1.PlacementSchedulingContextObj, error) {
	if namespace == "" {
		crpSchedulingCtx := &placementv1beta1.ClusterResourcePlacementSchedulingContext{}
		if err := r.HubClient.Get(ctx, client.ObjectKey{Name: name}, crpSchedulingCtx); err != nil {
			klog.ErrorS(err, "Failed to get cluster resource placement scheduling context", "CRPSchedulingCtx", name)
			return nil, fmt.Errorf("failed to get cluster resource placement scheduling context: %w", err)
		}
		return crpSchedulingCtx, nil
	}

	rpSchedulingCtx := &placementv1beta1.ResourcePlacementSchedulingContext{}
	if err := r.HubClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, rpSchedulingCtx); err != nil {
		klog.ErrorS(err, "Failed to get resource placement scheduling context", "RPSchedulingCtx", klog.KRef(namespace, name))
		return nil, fmt.Errorf("failed to get resource placement scheduling context: %w", err)
	}
	return rpSchedulingCtx, nil
}

func (r *Reconciler) retrieveDynamicResourceClaims(ctx context.Context, placementNamespace, placementName string) ([]*placementv1beta1.DynamicResourceClaim, error) {
	dynamicResourceClaims := []*placementv1beta1.DynamicResourceClaim{}

	if placementNamespace == "" {
		crp := &placementv1beta1.ClusterResourcePlacement{}
		crpName := placementName
		if err := r.HubClient.Get(ctx, client.ObjectKey{Name: crpName}, crp); err != nil {
			klog.ErrorS(err, "Failed to get cluster resource placement", "CRP", crpName)
			return nil, fmt.Errorf("failed to get cluster resource placement: %w", err)
		}

		if crp.Spec.Policy == nil {
			return dynamicResourceClaims, nil
		}

		for idx := range crp.Spec.Policy.DynamicResourceClaims {
			drc := &crp.Spec.Policy.DynamicResourceClaims[idx]
			dynamicResourceClaims = append(dynamicResourceClaims, drc)
		}
		return dynamicResourceClaims, nil
	}

	rp := &placementv1beta1.ResourcePlacement{}
	rpName := placementName
	if err := r.HubClient.Get(ctx, client.ObjectKey{Namespace: placementNamespace, Name: rpName}, rp); err != nil {
		klog.ErrorS(err, "Failed to get resource placement", "RP", klog.KRef(placementNamespace, rpName))
		return nil, fmt.Errorf("failed to get resource placement: %w", err)
	}

	if rp.Spec.Policy == nil {
		return dynamicResourceClaims, nil
	}

	for idx := range rp.Spec.Policy.DynamicResourceClaims {
		drc := &rp.Spec.Policy.DynamicResourceClaims[idx]
		dynamicResourceClaims = append(dynamicResourceClaims, drc)
	}
	return dynamicResourceClaims, nil
}

func (r *Reconciler) updatePlacementSchedulingContextStatus(ctx context.Context, placementSchedulingContext placementv1beta1.PlacementSchedulingContextObj, drcStatusCollection []placementv1beta1.DynamicResourceClaimStatus) error {
	placementSchedulingContextStatus := placementv1beta1.PlacementSchedulingContextStatus{
		DynamicResourceClaimStatus: drcStatusCollection,
	}
	placementSchedulingContext.SetPlacementSchedulingContextStatus(&placementSchedulingContextStatus)

	if err := r.HubClient.Status().Update(ctx, placementSchedulingContext); err != nil {
		klog.ErrorS(err, "Failed to update placement scheduling context status", "placementSchedulingCtx", klog.KObj(placementSchedulingContext))
		return fmt.Errorf("failed to update placement scheduling context status: %w", err)
	}
	return nil
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(azureDynamicResourceProviderControllerName).
		Watches(&placementv1beta1.ResourcePlacementSchedulingContext{}, &handler.EnqueueRequestForObject{}).
		Watches(&placementv1beta1.ClusterResourcePlacementSchedulingContext{}, &handler.EnqueueRequestForObject{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Complete(r)
}
