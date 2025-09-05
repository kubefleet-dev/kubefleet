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

// extractNamespaceFromResourceSelectors extracts the namespace name from ResourceSelectors
// when StatusReportingScope is NamespaceAccessible. Returns empty string if not applicable.
func extractNamespaceFromResourceSelectors(placementObj placementv1beta1.PlacementObj) string {
	spec := placementObj.GetPlacementSpec()

	// Only process if StatusReportingScope is NamespaceAccessible.
	if spec.StatusReportingScope != placementv1beta1.NamespaceAccessible {
		klog.V(2).InfoS("StatusReportingScope is not NamespaceAccessible", "placement", klog.KObj(placementObj))
		return ""
	}

	// CEL validation ensures exactly one Namespace selector exists when NamespaceAccessible.
	for _, selector := range spec.ResourceSelectors {
		selectorGVK := schema.GroupVersionKind{
			Group:   selector.Group,
			Version: selector.Version,
			Kind:    selector.Kind,
		}
		// Check if this is a namespace selector by comparing with the standard namespace GVK
		if selectorGVK == utils.NamespaceGVK {
			return selector.Name
		}
	}

	// This should never happen due to CEL validation, but defensive programming.
	klog.ErrorS(controller.NewUnexpectedBehaviorError(fmt.Errorf("no namespace selector found despite NamespaceAccessible scope")),
		"Failed to find valid Namespace selector for NamespaceAccessible scope",
		"placement", klog.KObj(placementObj))
	return ""
}

// isNamespaceAccessibleCRP checks if the placement object is a ClusterResourcePlacement
// with NamespaceAccessible scope and returns the target namespace.
// isNamespaceAccessible is true only for CRP objects with NamespaceAccessible scope.
// targetNamespace is the namespace where CRPS should be created (empty if not applicable).
func isNamespaceAccessibleCRP(placementObj placementv1beta1.PlacementObj) (bool, string) {
	_, ok := placementObj.(*placementv1beta1.ClusterResourcePlacement)
	if !ok {
		return false, ""
	}

	// Extract target namespace using the same logic as syncClusterResourcePlacementStatus.
	targetNamespace := extractNamespaceFromResourceSelectors(placementObj)
	if targetNamespace == "" {
		// Not NamespaceAccessible or no namespace found - no sync needed.
		return false, ""
	}

	return true, targetNamespace
}

// filterStatusSyncedCondition removes the ClusterResourcePlacementStatusSynced condition
// from the placement status conditions since it doesn't apply to CRPS objects.
func filterStatusSyncedCondition(status placementv1beta1.PlacementStatus) placementv1beta1.PlacementStatus {
	filteredStatus := status.DeepCopy()

	// Filter out the ClusterResourcePlacementStatusSynced condition
	filteredConditions := make([]metav1.Condition, 0, len(filteredStatus.Conditions))
	for _, condition := range filteredStatus.Conditions {
		if condition.Type != string(placementv1beta1.ClusterResourcePlacementStatusSyncedConditionType) {
			filteredConditions = append(filteredConditions, condition)
		}
	}
	filteredStatus.Conditions = filteredConditions

	return *filteredStatus
}

// syncClusterResourcePlacementStatus creates or updates ClusterResourcePlacementStatus
// object in the target namespace when StatusReportingScope is NamespaceAccessible.
func (r *Reconciler) syncClusterResourcePlacementStatus(ctx context.Context, placementObj placementv1beta1.PlacementObj) error {
	isNamespaceAccessible, targetNamespace := isNamespaceAccessibleCRP(placementObj)
	if !isNamespaceAccessible {
		// Not a NamespaceAccessible CRP - skip sync.
		klog.V(2).InfoS("Skipped processing placement to create/update ClusterResourcePlacementStatus", "placement", klog.KObj(placementObj))
		return nil
	}

	crp, _ := placementObj.(*placementv1beta1.ClusterResourcePlacement)
	crpStatus := &placementv1beta1.ClusterResourcePlacementStatus{
		ObjectMeta: metav1.ObjectMeta{
			Name:      crp.Name, // Same name as CRP.
			Namespace: targetNamespace,
		},
	}
	// Use CreateOrUpdate to handle both creation and update cases.
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, crpStatus, func() error {
		// Set the placement status (excluding StatusSynced condition) and update time.
		crpStatus.PlacementStatus = filterStatusSyncedCondition(crp.Status)
		crpStatus.LastUpdatedTime = metav1.Now()

		// Set CRP as owner - this ensures automatic cleanup when CRP is deleted.
		return controllerutil.SetControllerReference(crp, crpStatus, r.Scheme)
	})

	if err != nil {
		klog.ErrorS(err, "Failed to create or update ClusterResourcePlacementStatus", "crp", klog.KObj(crp), "namespace", targetNamespace)
		return controller.NewAPIServerError(false, fmt.Errorf("failed to create or update ClusterResourcePlacementStatus: %w", err))
	}

	klog.V(2).InfoS("Successfully handled ClusterResourcePlacementStatus", "crp", klog.KObj(crp), "namespace", targetNamespace, "operation", op)
	return nil
}

// validateNamespaceSelectorConsistency validates that the namespace selector hasn't changed
// for NamespaceAccessible CRPs. Returns a condition if validation fails, nil if validation passes.
func validateNamespaceSelectorConsistency(placementObj placementv1beta1.PlacementObj) *metav1.Condition {
	// Check if it's a NamespaceAccessible CRP
	isNamespaceAccessible, targetNamespaceFromSelector := isNamespaceAccessibleCRP(placementObj)
	if !isNamespaceAccessible {
		return nil
	}

	// Get the current target namespace from CRP status selected resources
	placementStatus := placementObj.GetPlacementStatus()
	currentTargetNamespace := ""

	// Extract namespace from selected resources in status
	for _, resource := range placementStatus.SelectedResources {
		if resource.Kind == "Namespace" && resource.Group == "" && resource.Version == "v1" {
			currentTargetNamespace = resource.Name
			break
		}
	}

	// If both namespaces exist and they don't match, it means the namespace selector changed
	if currentTargetNamespace != "" && currentTargetNamespace != targetNamespaceFromSelector {
		klog.V(2).InfoS("Namespace selector has changed for NamespaceAccessible CRP",
			"placement", klog.KObj(placementObj),
			"currentTargetNamespace", currentTargetNamespace,
			"newTargetNamespace", targetNamespaceFromSelector)

		// Return StatusSynced condition set to Unknown with appropriate reason and message
		return &metav1.Condition{
			Type:               string(placementv1beta1.ClusterResourcePlacementStatusSyncedConditionType),
			Status:             metav1.ConditionUnknown,
			Reason:             condition.InvalidResourceSelectorsReason,
			Message:            fmt.Sprintf("Namespace resource selector is choosing a different namespace '%s' from what was originally picked '%s'. This is not allowed for NamespaceAccessible ClusterResourcePlacements.", targetNamespaceFromSelector, currentTargetNamespace),
			ObservedGeneration: placementObj.GetGeneration(),
		}
	}

	return nil
}
