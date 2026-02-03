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

package v1beta1

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

// reconcileNamespaceAffinityLabels reconciles namespace affinity labels for a specific MemberCluster
// by examining all CRPs and their placement status
func (r *Reconciler) reconcileNamespaceAffinityLabels(ctx context.Context, mc *clusterv1beta1.MemberCluster) error {
	// Get all ClusterResourcePlacements
	var crpList placementv1beta1.ClusterResourcePlacementList
	if err := r.Client.List(ctx, &crpList); err != nil {
		return fmt.Errorf("failed to list ClusterResourcePlacements: %w", err)
	}

	klog.V(4).InfoS("Reconciling namespace affinity labels", "cluster", mc.Name, "totalCRPs", len(crpList.Items))

	// Track which CRPs have successfully placed namespaces on this cluster
	successfulCRPs := make(map[string][]string) // CRP name -> list of namespaces

	for _, crp := range crpList.Items {
		// Check if this CRP has successfully placed namespaces on our cluster
		namespaces := r.extractNamespacesForCluster(ctx, &crp, mc.Name)
		if len(namespaces) > 0 {
			successfulCRPs[crp.Name] = namespaces
		}
	}

	// Update labels based on successful placements
	if err := r.updateMemberClusterLabelsFromPlacements(ctx, mc, successfulCRPs); err != nil {
		return fmt.Errorf("failed to update namespace affinity labels: %w", err)
	}

	return nil
}

// extractNamespacesForCluster extracts namespace names that were successfully placed on a specific cluster
func (r *Reconciler) extractNamespacesForCluster(ctx context.Context, crp *placementv1beta1.ClusterResourcePlacement, clusterName string) []string {
	// Find the placement status for this cluster
	var targetStatus *placementv1beta1.PerClusterPlacementStatus
	for i, status := range crp.Status.PerClusterPlacementStatuses {
		if status.ClusterName == clusterName {
			targetStatus = &crp.Status.PerClusterPlacementStatuses[i]
			break
		}
	}

	if targetStatus == nil {
		return nil // Not scheduled on this cluster
	}

	// Check if placement was successful
	if !r.hasSuccessfulNamespacePlacement(*targetStatus) {
		return nil
	}

	// Extract namespaces from the CRP's resource selectors
	return r.extractSuccessfulNamespacesFromStatus(ctx, crp.Name, *targetStatus)
}

// updateMemberClusterLabelsFromPlacements updates namespace affinity labels based on successful placements
func (r *Reconciler) updateMemberClusterLabelsFromPlacements(
	ctx context.Context,
	mc *clusterv1beta1.MemberCluster,
	successfulCRPs map[string][]string,
) error {
	updated := false

	if mc.Labels == nil {
		mc.Labels = make(map[string]string)
	}

	// Get current namespace affinity labels to track what needs cleanup
	currentAffinityLabels := make(map[string]string)
	for k, v := range mc.Labels {
		if IsNamespaceAffinityLabel(k) {
			currentAffinityLabels[k] = v
		}
	}

	// Add new labels for successful placements
	expectedLabels := make(map[string]string)
	totalExpectedLabels := 0

	for crpName, namespaces := range successfulCRPs {
		for _, namespace := range namespaces {
			labelKey := BuildNamespaceAffinityLabelKey(namespace)
			expectedLabels[labelKey] = crpName
			totalExpectedLabels++
		}
	}

	// Check if we'd exceed the limit
	if totalExpectedLabels > MaxNamespaceLabelsPerCluster {
		klog.InfoS("Total expected namespace affinity labels exceed limit, will prioritize by CRP name",
			"cluster", mc.Name, "expected", totalExpectedLabels, "limit", MaxNamespaceLabelsPerCluster)

		// Prioritize labels (simple approach: alphabetical by CRP name)
		expectedLabels = r.prioritizeNamespaceLabels(successfulCRPs, MaxNamespaceLabelsPerCluster)
	}

	// Add/update labels
	for labelKey, crpName := range expectedLabels {
		if currentValue, exists := mc.Labels[labelKey]; !exists || currentValue != crpName {
			mc.Labels[labelKey] = crpName
			updated = true
			klog.V(4).InfoS("Updated namespace affinity label",
				"cluster", mc.Name, "labelKey", labelKey, "crpName", crpName)
		}
		// Remove from tracking as it's expected
		delete(currentAffinityLabels, labelKey)
	}

	// Remove labels that are no longer needed
	for labelKey := range currentAffinityLabels {
		delete(mc.Labels, labelKey)
		updated = true
		klog.V(4).InfoS("Removed obsolete namespace affinity label",
			"cluster", mc.Name, "labelKey", labelKey)
	}

	if !updated {
		return nil
	}

	// Update the MemberCluster
	if err := r.Client.Update(ctx, mc); err != nil {
		return fmt.Errorf("failed to update MemberCluster %s with namespace affinity labels: %w", mc.Name, err)
	}

	klog.V(2).InfoS("Successfully reconciled namespace affinity labels",
		"cluster", mc.Name, "totalLabels", len(expectedLabels))

	return nil
}

// prioritizeNamespaceLabels prioritizes namespace labels when the total exceeds the limit
func (r *Reconciler) prioritizeNamespaceLabels(successfulCRPs map[string][]string, maxLabels int) map[string]string {
	prioritized := make(map[string]string)
	count := 0

	// Simple prioritization: process CRPs in alphabetical order
	crpNames := make([]string, 0, len(successfulCRPs))
	for crpName := range successfulCRPs {
		crpNames = append(crpNames, crpName)
	}

	// Sort to ensure deterministic behavior
	for i := 0; i < len(crpNames)-1; i++ {
		for j := i + 1; j < len(crpNames); j++ {
			if crpNames[i] > crpNames[j] {
				crpNames[i], crpNames[j] = crpNames[j], crpNames[i]
			}
		}
	}

	for _, crpName := range crpNames {
		for _, namespace := range successfulCRPs[crpName] {
			if count >= maxLabels {
				break
			}
			labelKey := BuildNamespaceAffinityLabelKey(namespace)
			prioritized[labelKey] = crpName
			count++
		}
		if count >= maxLabels {
			break
		}
	}

	return prioritized
}

// hasSuccessfulNamespacePlacement checks if a cluster has successfully applied namespace resources
// by examining the conditions in the PerClusterPlacementStatus
func (r *Reconciler) hasSuccessfulNamespacePlacement(status placementv1beta1.PerClusterPlacementStatus) bool {
	// Check for Applied condition = True, which indicates resources were successfully applied
	for _, condition := range status.Conditions {
		if condition.Type == string(placementv1beta1.PerClusterAppliedConditionType) &&
			condition.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}

// extractSuccessfulNamespacesFromStatus determines which namespaces were successfully placed
// on a cluster by examining the CRP's selected resources and applied resources
func (r *Reconciler) extractSuccessfulNamespacesFromStatus(
	ctx context.Context,
	crpName string,
	status placementv1beta1.PerClusterPlacementStatus,
) []string {
	// Get the CRP to examine its selected resources (not resource selectors)
	var crp placementv1beta1.ClusterResourcePlacement
	if err := r.Client.Get(ctx, client.ObjectKey{Name: crpName}, &crp); err != nil {
		klog.ErrorS(err, "Failed to get CRP for namespace extraction",
			"crp", crpName, "cluster", status.ClusterName)
		return nil
	}

	// Extract namespace names from the CRP's SelectedResources (resources that were actually found and selected)
	// This ensures we only consider namespaces that actually exist and were selected for placement
	var namespaces []string
	for _, selectedResource := range crp.Status.SelectedResources {
		if selectedResource.Group == "" && selectedResource.Version == "v1" && selectedResource.Kind == "Namespace" {
			namespaces = append(namespaces, selectedResource.Name)
		}
	}

	// Filter out failed placements - only return namespaces that were successfully applied
	if len(status.FailedPlacements) > 0 {
		failedNamespaces := make(map[string]bool)
		for _, failed := range status.FailedPlacements {
			if failed.Kind == "Namespace" && failed.Group == "" && failed.Version == "v1" {
				failedNamespaces[failed.Name] = true
			}
		}

		// Remove failed namespaces from the successful list
		var successfulNamespaces []string
		for _, ns := range namespaces {
			if !failedNamespaces[ns] {
				successfulNamespaces = append(successfulNamespaces, ns)
			}
		}
		namespaces = successfulNamespaces
	}

	return namespaces
}

// countNamespaceAffinityLabels counts the current number of namespace affinity labels on a MemberCluster
func (r *Reconciler) countNamespaceAffinityLabels(labels map[string]string) int {
	count := 0
	for labelKey := range labels {
		if IsNamespaceAffinityLabel(labelKey) {
			count++
		}
	}
	return count
}
