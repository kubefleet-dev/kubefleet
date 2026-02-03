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

	"k8s.io/klog/v2"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
)

// reconcileMemberDrivenNamespaceAffinityLabels reconciles namespace affinity labels for a MemberCluster
// based on CRP-namespace associations reported by the member agent through InternalMemberCluster
func (r *Reconciler) reconcileMemberDrivenNamespaceAffinityLabels(
	ctx context.Context,
	mc *clusterv1beta1.MemberCluster,
	imc *clusterv1beta1.InternalMemberCluster,
) error {
	if imc == nil {
		klog.V(4).InfoS("InternalMemberCluster is nil, skipping namespace affinity label reconciliation",
			"memberCluster", mc.Name)
		return nil
	}

	if len(imc.Status.CRPNamespaceAssociations) == 0 {
		klog.V(4).InfoS("No CRP namespace associations found, cleaning up existing labels",
			"memberCluster", mc.Name)
		return r.cleanupAllNamespaceAffinityLabels(ctx, mc)
	}

	klog.V(4).InfoS("Reconciling member-driven namespace affinity labels",
		"memberCluster", mc.Name,
		"crpAssociations", len(imc.Status.CRPNamespaceAssociations))

	// Build expected labels from CRP-namespace associations
	expectedLabels := make(map[string]string)
	totalExpectedLabels := 0

	for crpName, namespaces := range imc.Status.CRPNamespaceAssociations {
		for _, namespace := range namespaces {
			labelKey := BuildNamespaceAffinityLabelKey(namespace)
			expectedLabels[labelKey] = crpName
			totalExpectedLabels++
		}
	}

	// Check if we'd exceed the limit
	if totalExpectedLabels > MaxNamespaceLabelsPerCluster {
		klog.InfoS("Total expected namespace affinity labels exceed limit, will prioritize",
			"memberCluster", mc.Name,
			"expected", totalExpectedLabels,
			"limit", MaxNamespaceLabelsPerCluster)

		// Prioritize labels (simple approach: alphabetical by CRP name, then by namespace)
		expectedLabels = r.prioritizeMemberDrivenNamespaceLabels(imc.Status.CRPNamespaceAssociations, MaxNamespaceLabelsPerCluster)
	}

	// Update the MemberCluster labels
	return r.updateMemberClusterLabelsFromMemberDrivenAssociations(ctx, mc, expectedLabels)
}

// updateMemberClusterLabelsFromMemberDrivenAssociations updates MemberCluster labels based on member-driven associations
func (r *Reconciler) updateMemberClusterLabelsFromMemberDrivenAssociations(
	ctx context.Context,
	mc *clusterv1beta1.MemberCluster,
	expectedLabels map[string]string,
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

	// Add/update labels
	for labelKey, crpName := range expectedLabels {
		if currentValue, exists := mc.Labels[labelKey]; !exists || currentValue != crpName {
			mc.Labels[labelKey] = crpName
			updated = true
			klog.V(4).InfoS("Updated member-driven namespace affinity label",
				"memberCluster", mc.Name, "labelKey", labelKey, "crpName", crpName)
		}
		// Remove from tracking as it's expected
		delete(currentAffinityLabels, labelKey)
	}

	// Remove labels that are no longer needed
	for labelKey := range currentAffinityLabels {
		delete(mc.Labels, labelKey)
		updated = true
		klog.V(4).InfoS("Removed obsolete namespace affinity label",
			"memberCluster", mc.Name, "labelKey", labelKey)
	}

	if !updated {
		return nil
	}

	// Update the MemberCluster
	if err := r.Client.Update(ctx, mc); err != nil {
		return fmt.Errorf("failed to update MemberCluster %s with member-driven namespace affinity labels: %w", mc.Name, err)
	}

	klog.V(2).InfoS("Successfully reconciled member-driven namespace affinity labels",
		"memberCluster", mc.Name, "totalLabels", len(expectedLabels))

	return nil
}

// prioritizeMemberDrivenNamespaceLabels prioritizes namespace labels when the total exceeds the limit
func (r *Reconciler) prioritizeMemberDrivenNamespaceLabels(crpAssociations map[string][]string, maxLabels int) map[string]string {
	prioritized := make(map[string]string)
	count := 0

	// Simple prioritization: process CRPs in alphabetical order
	crpNames := make([]string, 0, len(crpAssociations))
	for crpName := range crpAssociations {
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
		namespaces := crpAssociations[crpName]
		// Sort namespaces for deterministic behavior
		for i := 0; i < len(namespaces)-1; i++ {
			for j := i + 1; j < len(namespaces); j++ {
				if namespaces[i] > namespaces[j] {
					namespaces[i], namespaces[j] = namespaces[j], namespaces[i]
				}
			}
		}

		for _, namespace := range namespaces {
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

// cleanupAllNamespaceAffinityLabels removes all namespace affinity labels from a MemberCluster
func (r *Reconciler) cleanupAllNamespaceAffinityLabels(ctx context.Context, mc *clusterv1beta1.MemberCluster) error {
	if mc.Labels == nil {
		return nil
	}

	updated := false
	labelsToRemove := make([]string, 0)

	// Collect all namespace affinity labels to remove
	for labelKey := range mc.Labels {
		if IsNamespaceAffinityLabel(labelKey) {
			labelsToRemove = append(labelsToRemove, labelKey)
			updated = true
		}
	}

	if !updated {
		return nil // No labels to remove
	}

	// Remove the labels
	for _, labelKey := range labelsToRemove {
		delete(mc.Labels, labelKey)
		klog.V(4).InfoS("Cleaned up namespace affinity label",
			"memberCluster", mc.Name, "labelKey", labelKey)
	}

	// Update the MemberCluster
	if err := r.Client.Update(ctx, mc); err != nil {
		return fmt.Errorf("failed to cleanup namespace affinity labels from MemberCluster %s: %w", mc.Name, err)
	}

	klog.V(2).InfoS("Successfully cleaned up all namespace affinity labels",
		"memberCluster", mc.Name, "removedLabels", len(labelsToRemove))

	return nil
}
