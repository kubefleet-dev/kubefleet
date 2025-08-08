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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

// extractNamespaceFromResourceSelectors extracts the namespace name from ResourceSelectors
// when StatusReportingScope is NamespaceAccessible. Returns empty string if not applicable.
func extractNamespaceFromResourceSelectors(placementObj placementv1beta1.PlacementObj) string {
	spec := placementObj.GetPlacementSpec()

	// Only process if StatusReportingScope is NamespaceAccessible
	if spec.StatusReportingScope != placementv1beta1.NamespaceAccessible {
		return ""
	}

	// CEL validation ensures exactly one Namespace selector exists when NamespaceAccessible
	for _, selector := range spec.ResourceSelectors {
		if selector.Kind == "Namespace" {
			return selector.Name
		}
	}

	// This should never happen due to CEL validation, but defensive programming
	klog.V(2).InfoS("No Namespace selector found despite NamespaceAccessible scope", "placement", klog.KObj(placementObj))
	return ""
}

// syncClusterResourcePlacementStatus creates or updates ClusterResourcePlacementStatus
// object in the target namespace when StatusReportingScope is NamespaceAccessible
func (r *Reconciler) syncClusterResourcePlacementStatus(ctx context.Context, placementObj placementv1beta1.PlacementObj) error {
	// Only sync for ClusterResourcePlacement objects (not ResourcePlacement)
	crp, ok := placementObj.(*placementv1beta1.ClusterResourcePlacement)
	if !ok {
		// This is a ResourcePlacement, not a ClusterResourcePlacement - skip sync
		klog.V(2).InfoS("Skipped processing RP to create/update ClusterResourcePlacementStatus")
		return nil
	}

	// Extract target namespace
	targetNamespace := extractNamespaceFromResourceSelectors(placementObj)
	if targetNamespace == "" {
		// Not NamespaceAccessible or no namespace found - skip sync
		klog.V(2).InfoS("Skipped processing CRP to create/update ClusterResourcePlacementStatus", "crp", klog.KObj(crp))
		return nil
	}

	// Try to get the existing ClusterResourcePlacementStatus
	crpStatus := &placementv1beta1.ClusterResourcePlacementStatus{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: crp.Name, Namespace: targetNamespace}, crpStatus); err != nil {
		if apierrors.IsNotFound(err) {
			// Object doesn't exist, create it.
			crpStatus = &placementv1beta1.ClusterResourcePlacementStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      crp.Name, // Same name as CRP
					Namespace: targetNamespace,
				},
				// Don't set Status here - it will be updated separately after creation
			}

			// Set CRP as owner - this ensures automatic cleanup when CRP is deleted
			if err := controllerutil.SetControllerReference(crp, crpStatus, r.Scheme); err != nil {
				klog.ErrorS(err, "Failed to set controller reference", "crp", klog.KObj(crp), "namespace", targetNamespace)
				return fmt.Errorf("failed to set controller reference: %w", err)
			}

			if err := r.Client.Create(ctx, crpStatus); err != nil {
				klog.ErrorS(err, "Failed to create ClusterResourcePlacementStatus", "crp", klog.KObj(crp), "namespace", targetNamespace)
				return fmt.Errorf("failed to create ClusterResourcePlacementStatus: %w", err)
			}

			klog.V(2).InfoS("Created ClusterResourcePlacementStatus with owner reference", "crp", klog.KObj(crp), "namespace", targetNamespace)

			// Fetch the created object fresh to get the correct metadata for status update
			if err := r.Client.Get(ctx, types.NamespacedName{Name: crp.Name, Namespace: targetNamespace}, crpStatus); err != nil {
				klog.ErrorS(err, "Failed to get ClusterResourcePlacementStatus after creation", "crp", klog.KObj(crp), "namespace", targetNamespace)
				return fmt.Errorf("failed to get ClusterResourcePlacementStatus after creation: %w", err)
			}

			// Now update the status separately
			crpStatus.Status = *crp.Status.DeepCopy()
			if err := r.Client.Status().Update(ctx, crpStatus); err != nil {
				klog.ErrorS(err, "Failed to update ClusterResourcePlacementStatus status after creation", "crp", klog.KObj(crp), "namespace", targetNamespace)
				return fmt.Errorf("failed to update ClusterResourcePlacementStatus status: %w", err)
			}

			klog.V(2).InfoS("Updated ClusterResourcePlacementStatus status after creation", "crp", klog.KObj(crp), "namespace", targetNamespace)
			return nil
		}
		klog.ErrorS(err, "Failed to get ClusterResourcePlacementStatus", "crp", klog.KObj(crp), "namespace", targetNamespace)
		return fmt.Errorf("failed to get ClusterResourcePlacementStatus: %w", err)
	}

	// Object exists, update it.
	crpStatus.Status = *crp.Status.DeepCopy()
	if err := r.Client.Status().Update(ctx, crpStatus); err != nil {
		klog.ErrorS(err, "Failed to update ClusterResourcePlacementStatus", "crp", klog.KObj(crp), "namespace", targetNamespace)
		return fmt.Errorf("failed to update ClusterResourcePlacementStatus: %w", err)
	}

	klog.V(2).InfoS("Updated ClusterResourcePlacementStatus", "crp", klog.KObj(crp), "namespace", targetNamespace)
	return nil
}
