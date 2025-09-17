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

package placement

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/condition"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/controller"
)

// extractNamespaceFromResourceSelectors extracts the target namespace name from the
// ClusterResourcePlacement's ResourceSelectors. This function looks for a Namespace
// resource selector and returns its name. Returns empty string if no Namespace
// selector is found.
func extractNamespaceFromResourceSelectors(crp *placementv1beta1.ClusterResourcePlacement) string {
	// CEL validation ensures exactly one Namespace selector exists when NamespaceAccessible.
	for _, selector := range crp.Spec.ResourceSelectors {
		selectorGVK := schema.GroupVersionKind{
			Group:   selector.Group,
			Version: selector.Version,
			Kind:    selector.Kind,
		}
		// Check if this is a namespace selector by comparing with the standard namespace GVK.
		if selectorGVK == utils.NamespaceGVK {
			return selector.Name
		}
	}

	return ""
}

// isNamespaceAccessibleCRP checks if the given placement object is a ClusterResourcePlacement
// with StatusReportingScope set to NamespaceAccessible. Returns true if the placement is
// a CRP with NamespaceAccessible scope, false otherwise.
func isNamespaceAccessibleCRP(placementObj placementv1beta1.PlacementObj) bool {
	crp, ok := placementObj.(*placementv1beta1.ClusterResourcePlacement)
	if !ok {
		klog.V(2).InfoS("Skipped processing RP to create/update ClusterResourcePlacementStatus", "placement", klog.KObj(placementObj))
		return false
	}

	// Only process if StatusReportingScope is NamespaceAccessible.
	if crp.Spec.StatusReportingScope != placementv1beta1.NamespaceAccessible {
		klog.V(2).InfoS("StatusReportingScope is not NamespaceAccessible", "crp", klog.KObj(placementObj))
		return false
	}

	return true
}

// filterStatusSyncedCondition creates a copy of the placement status with the
// ClusterResourcePlacementStatusSynced condition removed. This is used when
// creating ClusterResourcePlacementStatus objects because the StatusSynced
// condition is only relevant for the main CRP object, not the CRPS object.
// Returns a filtered copy of the status with all conditions except StatusSynced.
func filterStatusSyncedCondition(status *placementv1beta1.PlacementStatus) *placementv1beta1.PlacementStatus {
	filteredStatus := status.DeepCopy()

	// Filter out the ClusterResourcePlacementStatusSynced condition.
	filteredConditions := make([]metav1.Condition, 0, len(filteredStatus.Conditions))
	for _, condition := range filteredStatus.Conditions {
		if condition.Type != string(placementv1beta1.ClusterResourcePlacementStatusSyncedConditionType) {
			filteredConditions = append(filteredConditions, condition)
		}
	}
	filteredStatus.Conditions = filteredConditions

	return filteredStatus
}

// buildStatusSyncedCondition creates a StatusSynced condition based on the sync result.
func buildStatusSyncedCondition(generation int64, targetNamespace string, syncErr error) metav1.Condition {
	// Determine condition based on sync result
	if syncErr != nil {
		return metav1.Condition{
			Type:               string(placementv1beta1.ClusterResourcePlacementStatusSyncedConditionType),
			Status:             metav1.ConditionFalse,
			Reason:             condition.StatusSyncFailedReason,
			Message:            fmt.Sprintf("Failed to create or update ClusterResourcePlacementStatus: %v", syncErr),
			ObservedGeneration: generation,
		}
	}

	if targetNamespace == "" {
		return metav1.Condition{
			Type:               string(placementv1beta1.ClusterResourcePlacementStatusSyncedConditionType),
			Status:             metav1.ConditionUnknown,
			Reason:             condition.InvalidResourceSelectorsReason,
			Message:            "NamespaceAccessible ClusterResourcePlacement doesn't specify a resource selector which selects a namespace",
			ObservedGeneration: generation,
		}
	}

	return metav1.Condition{
		Type:               string(placementv1beta1.ClusterResourcePlacementStatusSyncedConditionType),
		Status:             metav1.ConditionTrue,
		Reason:             condition.StatusSyncSucceededReason,
		Message:            fmt.Sprintf("Successfully created or updated ClusterResourcePlacementStatus in namespace '%s'", targetNamespace),
		ObservedGeneration: generation,
	}
}

// setSyncedConditionAndUpdateStatus is a helper function that sets a StatusSynced condition
// on the placement object and updates its status. This consolidates the common pattern of
// setting StatusSynced conditions and updating placement status throughout the namespace
// status sync functions.
func (r *Reconciler) setSyncedConditionAndUpdateStatus(ctx context.Context, placementObj placementv1beta1.PlacementObj, condition metav1.Condition) error {
	placementKObj := klog.KObj(placementObj)

	// Set the condition on the placement object.
	placementObj.SetConditions(condition)

	// Update the placement status.
	if err := r.Client.Status().Update(ctx, placementObj); err != nil {
		klog.ErrorS(err, "Failed to update the placement status with StatusSynced condition", "crp", placementKObj)
		return controller.NewUpdateIgnoreConflictError(err)
	}

	klog.V(2).InfoS("Updated the placement status with StatusSynced condition", "crp", placementKObj)

	return nil
}

// syncClusterResourcePlacementStatus creates or updates a ClusterResourcePlacementStatus
// object in the target namespace for ClusterResourcePlacements with NamespaceAccessible scope.
// It extracts the target namespace from the CRP's resource selectors, creates/updates a CRPS object
// with filtered status (excluding StatusSynced condition), and sets the CRP as owner for
// automatic cleanup. Returns an error if the operation fails or if the target namespace name is empty.
func (r *Reconciler) syncClusterResourcePlacementStatus(ctx context.Context, placementObj placementv1beta1.PlacementObj) (string, error) {
	placementKObj := klog.KObj(placementObj)
	crp, _ := placementObj.(*placementv1beta1.ClusterResourcePlacement)

	// Extract target namespace from resource selectors.
	targetNamespace := extractNamespaceFromResourceSelectors(crp)
	if targetNamespace == "" {
		klog.ErrorS(controller.NewUnexpectedBehaviorError(fmt.Errorf("no namespace selector found despite NamespaceAccessible scope")),
			"Failed to find valid Namespace selector for NamespaceAccessible scope",
			"crp", placementKObj)
		return "", nil
	}

	crpStatus := &placementv1beta1.ClusterResourcePlacementStatus{
		ObjectMeta: metav1.ObjectMeta{
			Name:      crp.Name,
			Namespace: targetNamespace,
		},
	}
	// Use CreateOrUpdate to handle both creation and update cases.
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, crpStatus, func() error {
		// Set the placement status (excluding StatusSynced condition) and update time.
		crpStatus.PlacementStatus = *filterStatusSyncedCondition(&crp.Status)
		crpStatus.LastUpdatedTime = metav1.Now()

		// Set CRP as owner - this ensures automatic cleanup when CRP is deleted.
		return controllerutil.SetControllerReference(crp, crpStatus, r.Scheme)
	})

	if err != nil {
		klog.ErrorS(err, "Failed to create or update ClusterResourcePlacementStatus", "crp", placementKObj, "namespace", targetNamespace)
		return "", controller.NewAPIServerError(false, fmt.Errorf("failed to create or update ClusterResourcePlacementStatus: %w", err))
	}

	klog.V(2).InfoS("Successfully handled ClusterResourcePlacementStatus", "crp", placementKObj, "namespace", targetNamespace, "operation", op)
	return targetNamespace, nil
}

// handleNamespaceAccessibleCRP handles the complete workflow for ClusterResourcePlacements
// with NamespaceAccessible scope. It syncs the ClusterResourcePlacementStatus object in
// the target namespace, builds a StatusSynced condition based on the sync result, adds
// the condition to the CRP, and updates the CRP status.
func (r *Reconciler) handleNamespaceAccessibleCRP(ctx context.Context, placementObj placementv1beta1.PlacementObj) error {
	// Sync ClusterResourcePlacementStatus object if StatusReportingScope is NamespaceAccessible.
	targetNamespace, syncErr := r.syncClusterResourcePlacementStatus(ctx, placementObj)

	// Build and set the StatusSynced condition based on the sync result, targetNamespace.
	statusSyncCondition := buildStatusSyncedCondition(placementObj.GetGeneration(), targetNamespace, syncErr)
	if err := r.setSyncedConditionAndUpdateStatus(ctx, placementObj, statusSyncCondition); err != nil {
		return err
	}

	if syncErr != nil {
		klog.ErrorS(syncErr, "Failed to sync ClusterResourcePlacementStatus", "placement", klog.KObj(placementObj))
		return syncErr
	}

	return nil
}

// validateNamespaceSelectorConsistency validates that ClusterResourcePlacements with
// NamespaceAccessible scope have consistent namespace selectors. This function assumes
// the placement is already confirmed to be NamespaceAccessible. It checks if the
// namespace selector has changed from what was originally selected and sets the
// StatusSynced condition accordingly. Returns an error if validation fails or if
// there are issues updating the placement status.
func (r *Reconciler) validateNamespaceSelectorConsistency(ctx context.Context, placementObj placementv1beta1.PlacementObj) (bool, error) {
	placementKObj := klog.KObj(placementObj)
	crp, _ := placementObj.(*placementv1beta1.ClusterResourcePlacement)

	// Extract target namespace from resource selectors.
	targetNamespaceFromSelector := extractNamespaceFromResourceSelectors(crp)
	if targetNamespaceFromSelector == "" {
		klog.ErrorS(controller.NewUnexpectedBehaviorError(fmt.Errorf("no namespace selector found despite NamespaceAccessible scope")),
			"Failed to find valid Namespace selector for NamespaceAccessible scope",
			"crp", placementKObj)
		statusSyncCondition := buildStatusSyncedCondition(placementObj.GetGeneration(), targetNamespaceFromSelector, nil)
		if err := r.setSyncedConditionAndUpdateStatus(ctx, placementObj, statusSyncCondition); err != nil {
			return false, err
		}
		return false, nil
	}

	placementStatus := placementObj.GetPlacementStatus()
	currentTargetNamespace := ""

	// Extract namespace from selected resources in status.
	for _, resource := range placementStatus.SelectedResources {
		resourceGVK := schema.GroupVersionKind{
			Group:   resource.Group,
			Version: resource.Version,
			Kind:    resource.Kind,
		}
		// Check if this is a namespace resource by comparing with the standard namespace GVK.
		if resourceGVK == utils.NamespaceGVK {
			currentTargetNamespace = resource.Name
			break
		}
	}

	// * If currentTargetNamespace is empty, we skip this check for one of the following reasons,
	//	 - CRP status had not been populated yet.
	//   - CRP is not selecting any namespace (though this should be caught by CEL validation).
	// * If both namespaces exist and they don't match, it means the namespace selector changed.
	if currentTargetNamespace != "" && currentTargetNamespace != targetNamespaceFromSelector {
		klog.V(2).InfoS("Namespace selector has changed for NamespaceAccessible CRP",
			"crp", placementKObj,
			"currentTargetNamespace", currentTargetNamespace,
			"newTargetNamespace", targetNamespaceFromSelector)

		messageFmt := "namespace resource selector is choosing a different namespace '%s' from what was originally picked '%s'. This is not allowed for NamespaceAccessible ClusterResourcePlacements."
		// Build StatusSynced condition set to Unknown with appropriate reason and message.
		statusSyncCondition := metav1.Condition{
			Type:               string(placementv1beta1.ClusterResourcePlacementStatusSyncedConditionType),
			Status:             metav1.ConditionUnknown,
			Reason:             condition.InvalidResourceSelectorsReason,
			Message:            fmt.Sprintf(messageFmt, targetNamespaceFromSelector, currentTargetNamespace),
			ObservedGeneration: placementObj.GetGeneration(),
		}

		// Set condition and update placement status.
		if err := r.setSyncedConditionAndUpdateStatus(ctx, placementObj, statusSyncCondition); err != nil {
			return false, err
		}
		klog.ErrorS(controller.NewUserError(fmt.Errorf(messageFmt, currentTargetNamespace, targetNamespaceFromSelector)),
			"Namespace selector inconsistency detected",
			"crp", placementKObj)
		return false, nil
	}

	// Validation passed.
	return true, nil
}
