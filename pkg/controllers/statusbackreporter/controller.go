package statusbackreporter

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

type Reconciler struct {
	HubClient        client.Client
	HubDynamicClient dynamic.Interface
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	workRef := klog.KRef(req.Namespace, req.Name)
	startTime := time.Now()
	klog.V(2).InfoS("Reconciliation loop starts", "controller", "statusBackReporter", "work", workRef)
	defer func() {
		latency := time.Since(startTime).Milliseconds()
		klog.V(2).InfoS("Reconciliation loop ends", "controller", "statusBackReporter", "work", workRef, "latency", latency)
	}()

	work := &placementv1beta1.Work{}
	if err := r.HubClient.Get(ctx, req.NamespacedName, work); err != nil {
		klog.ErrorS(err, "Failed to retrieve Work object", "work", workRef)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// TO-DO (chenyu1): verify status back-reporting strategy from the source (CRP or RP objects),
	// rather than using the data copied to the Work object.
	reportBackStrategy := work.Spec.ReportBackStrategy
	switch {
	case reportBackStrategy == nil:
		klog.V(2).InfoS("Skip status back-reporting; the strategy has not been set", "work", workRef)
		return ctrl.Result{}, nil
	case reportBackStrategy.Type != placementv1beta1.ReportBackStrategyTypeMirror:
		klog.V(2).InfoS("Skip status back-reporting; it has been disabled in the strategy", "work", workRef)
		return ctrl.Result{}, nil
	case reportBackStrategy.Destination == nil:
		// This in theory should never occur.
		klog.V(2).InfoS("Skip status back-reporting; destination has not been set in the strategy", "work", workRef)
		return ctrl.Result{}, nil
	case *reportBackStrategy.Destination != placementv1beta1.ReportBackDestinationOriginalResource:
		klog.V(2).InfoS("Skip status back-reporting; destination has been set to the Work API", "work", workRef)
		return ctrl.Result{}, nil
	}

	erred := false
	// TO-DO (chenyu1): parallelize the back-reporting ops.
	for idx := range work.Status.ManifestConditions {
		// TO-DO (chenyu1): handle envelopes gracefully.
		manifestCond := &work.Status.ManifestConditions[idx]
		resIdentifier := manifestCond.Identifier

		applyCond := meta.FindStatusCondition(work.Status.Conditions, placementv1beta1.WorkConditionTypeApplied)
		if applyCond == nil || applyCond.ObservedGeneration != work.Generation || applyCond.Status != metav1.ConditionTrue {
			klog.V(2).InfoS("Skip status back-reporting for the resource; the resource has not been successfully applied yet", "work", workRef, "resourceIdentifier", resIdentifier)
			continue
		}

		// Skip the resource if there is no back-reported status.
		if manifestCond.BackReportedStatus == nil || len(manifestCond.BackReportedStatus.ObservedStatus.Raw) == 0 {
			klog.V(2).InfoS("Skip status back-reporting for the resource; there is no back-reported status", "work", workRef, "resourceIdentifier", resIdentifier)
			continue
		}

		// Note that applied resources should always have a valid identifier set; for simplicity reasons
		// here the back-reporter will no longer validate the data.
		gvr := schema.GroupVersionResource{
			Group:    resIdentifier.Group,
			Version:  resIdentifier.Version,
			Resource: resIdentifier.Resource,
		}
		nsName := resIdentifier.Namespace
		resName := resIdentifier.Name
		unstructured, err := r.HubDynamicClient.Resource(gvr).Namespace(nsName).Get(ctx, resName, metav1.GetOptions{})
		if err != nil {
			klog.ErrorS(err, "Failed to retrieve the target resource for status back-reporting", "work", workRef, "resourceIdentifier", resIdentifier)
			erred = true
			continue
		}

		// Set the back-reported status to the target resource.
		statusWrapper := make(map[string]interface{})
		if err := json.Unmarshal(manifestCond.BackReportedStatus.ObservedStatus.Raw, &statusWrapper); err != nil {
			klog.ErrorS(err, "Failed to unmarshal back-reported status", "work", workRef, "resourceIdentifier", resIdentifier)
			erred = true
			continue
		}

		/**
		if _, ok := unstructured.Object["status"]; !ok {
			err := fmt.Errorf("no status subresource")
			klog.ErrorS(err, "Failed to back-report status to the target resource as it does not have a status subresource", "work", workRef, "resourceIdentifier", resIdentifier)
			continue
		}
		**/

		unstructured.Object["status"] = statusWrapper["status"]
		_, err = r.HubDynamicClient.Resource(gvr).Namespace(nsName).UpdateStatus(ctx, unstructured, metav1.UpdateOptions{})
		if err != nil {
			klog.ErrorS(err, "Failed to update status to the target resource", "work", workRef, "resourceIdentifier", resIdentifier)
			erred = true
			continue
		}
	}

	var sequentialStatusUpdateErr error
	if erred {
		sequentialStatusUpdateErr = fmt.Errorf("failed to back-report status on one or more resources")
	}
	return ctrl.Result{}, sequentialStatusUpdateErr
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("status-back-reporter").
		Watches(&placementv1beta1.Work{}, &handler.EnqueueRequestForObject{}).
		Complete(r)
}
