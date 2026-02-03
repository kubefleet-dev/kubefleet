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

package workapplier

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

// CRPNamespaceTracker tracks CRP-namespace associations based on Work object processing
type CRPNamespaceTracker struct {
	hubClient client.Client
}

// NewCRPNamespaceTracker creates a new CRP namespace tracker
func NewCRPNamespaceTracker(hubClient client.Client) *CRPNamespaceTracker {
	return &CRPNamespaceTracker{
		hubClient: hubClient,
	}
}

// UpdateCRPNamespaceAssociations updates the InternalMemberCluster with CRP-namespace associations
// based on successfully processed Work objects
func (t *CRPNamespaceTracker) UpdateCRPNamespaceAssociations(
	ctx context.Context,
	imcNamespace, imcName string,
	work *placementv1beta1.Work,
	successfulNamespaces []string,
) error {
	if len(successfulNamespaces) == 0 {
		return nil // Nothing to update
	}

	// Get CRP name from Work object labels
	crpName := work.Labels[placementv1beta1.PlacementTrackingLabel]
	if crpName == "" {
		// No CRP association, skip
		return nil
	}

	// Get the InternalMemberCluster
	var imc clusterv1beta1.InternalMemberCluster
	if err := t.hubClient.Get(ctx, client.ObjectKey{
		Namespace: imcNamespace,
		Name:      imcName,
	}, &imc); err != nil {
		return fmt.Errorf("failed to get InternalMemberCluster %s/%s: %w", imcNamespace, imcName, err)
	}

	// Initialize the map if nil
	if imc.Status.CRPNamespaceAssociations == nil {
		imc.Status.CRPNamespaceAssociations = make(map[string][]string)
	}

	// Update or add the CRP-namespace associations
	existingNamespaces := imc.Status.CRPNamespaceAssociations[crpName]
	updatedNamespaces := mergeUniqueStrings(existingNamespaces, successfulNamespaces)

	if !equalStringSlices(existingNamespaces, updatedNamespaces) {
		imc.Status.CRPNamespaceAssociations[crpName] = updatedNamespaces

		// Update the InternalMemberCluster
		if err := t.hubClient.Status().Update(ctx, &imc); err != nil {
			return fmt.Errorf("failed to update InternalMemberCluster %s/%s CRP namespace associations: %w",
				imcNamespace, imcName, err)
		}

		klog.V(4).InfoS("Updated CRP namespace associations",
			"internalMemberCluster", fmt.Sprintf("%s/%s", imcNamespace, imcName),
			"crp", crpName,
			"namespaces", updatedNamespaces)
	}

	return nil
}

// RemoveCRPNamespaceAssociations removes namespace associations when a Work object is deleted
func (t *CRPNamespaceTracker) RemoveCRPNamespaceAssociations(
	ctx context.Context,
	imcNamespace, imcName string,
	work *placementv1beta1.Work,
	namespacesToRemove []string,
) error {
	if len(namespacesToRemove) == 0 {
		return nil
	}

	// Get CRP name from Work object labels
	crpName := work.Labels[placementv1beta1.PlacementTrackingLabel]
	if crpName == "" {
		return nil
	}

	// Get the InternalMemberCluster
	var imc clusterv1beta1.InternalMemberCluster
	if err := t.hubClient.Get(ctx, client.ObjectKey{
		Namespace: imcNamespace,
		Name:      imcName,
	}, &imc); err != nil {
		return fmt.Errorf("failed to get InternalMemberCluster %s/%s: %w", imcNamespace, imcName, err)
	}

	if imc.Status.CRPNamespaceAssociations == nil {
		return nil // Nothing to remove
	}

	existingNamespaces := imc.Status.CRPNamespaceAssociations[crpName]
	updatedNamespaces := removeStrings(existingNamespaces, namespacesToRemove)

	if !equalStringSlices(existingNamespaces, updatedNamespaces) {
		if len(updatedNamespaces) == 0 {
			// Remove the CRP entry entirely if no namespaces remain
			delete(imc.Status.CRPNamespaceAssociations, crpName)
		} else {
			imc.Status.CRPNamespaceAssociations[crpName] = updatedNamespaces
		}

		// Update the InternalMemberCluster
		if err := t.hubClient.Status().Update(ctx, &imc); err != nil {
			return fmt.Errorf("failed to update InternalMemberCluster %s/%s CRP namespace associations: %w",
				imcNamespace, imcName, err)
		}

		klog.V(4).InfoS("Removed CRP namespace associations",
			"internalMemberCluster", fmt.Sprintf("%s/%s", imcNamespace, imcName),
			"crp", crpName,
			"removedNamespaces", namespacesToRemove,
			"remainingNamespaces", updatedNamespaces)
	}

	return nil
}

// ExtractNamespacesFromWork extracts namespace names from a Work object's manifests
func ExtractNamespacesFromWork(work *placementv1beta1.Work) []string {
	var namespaces []string

	for _, manifest := range work.Spec.Workload.Manifests {
		obj := &unstructured.Unstructured{}
		if err := obj.UnmarshalJSON(manifest.Raw); err != nil {
			continue // Skip invalid manifests
		}

		// Check if this is a namespace resource
		if obj.GetKind() == "Namespace" &&
			obj.GetAPIVersion() == "v1" &&
			obj.GetName() != "" {
			namespaces = append(namespaces, obj.GetName())
		}
	}

	return namespaces
}

// mergeUniqueStrings merges two string slices, keeping only unique values
func mergeUniqueStrings(existing, new []string) []string {
	seen := make(map[string]bool)
	var result []string

	// Add existing strings
	for _, s := range existing {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}

	// Add new strings
	for _, s := range new {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}

	return result
}

// removeStrings removes specified strings from a slice
func removeStrings(original, toRemove []string) []string {
	removeSet := make(map[string]bool)
	for _, s := range toRemove {
		removeSet[s] = true
	}

	var result []string
	for _, s := range original {
		if !removeSet[s] {
			result = append(result, s)
		}
	}

	return result
}

// equalStringSlices compares two string slices for equality (order matters)
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
