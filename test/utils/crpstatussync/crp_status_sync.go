package crpstatussync

import (
	"context"
	"fmt"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils"
)

var (
	// Define comparison options for ignoring auto-generated and time-dependent fields.
	crpsCmpOpts = []cmp.Option{
		cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion", "UID", "CreationTimestamp", "Generation", "ManagedFields"),
		cmpopts.IgnoreFields(placementv1beta1.ClusterResourcePlacementStatus{}, "LastUpdatedTime"),
		cmpopts.IgnoreFields(metav1.Condition{}, "LastTransitionTime"),
	}
)

func CRPSStatusMatchesCRPActual(ctx context.Context, client client.Client, crpName, targetNamespace string) func() error {
	return func() error {
		crpStatus := &placementv1beta1.ClusterResourcePlacementStatus{}
		crpStatusKey := types.NamespacedName{
			Name:      crpName,
			Namespace: targetNamespace,
		}

		if err := client.Get(ctx, crpStatusKey, crpStatus); err != nil {
			return fmt.Errorf("failed to get CRPS: %w", err)
		}

		// Get latest CRP status.
		crp := &placementv1beta1.ClusterResourcePlacement{}
		if err := client.Get(ctx, types.NamespacedName{Name: crpName}, crp); err != nil {
			return fmt.Errorf("failed to get CRP: %w", err)
		}

		expectedStatus := namespaceProjectedStatus(&crp.Status, targetNamespace)

		wantCRPS := &placementv1beta1.ClusterResourcePlacementStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name:      crpName,
				Namespace: targetNamespace,
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion:         placementv1beta1.GroupVersion.String(),
						Kind:               "ClusterResourcePlacement",
						Name:               crpName,
						UID:                crp.UID,
						Controller:         ptr.To(true),
						BlockOwnerDeletion: ptr.To(true),
					},
				},
			},
			PlacementStatus: *expectedStatus,
		}

		if err := validateNamespaceProjection(&crpStatus.PlacementStatus, targetNamespace); err != nil {
			return err
		}

		if diff := cmp.Diff(wantCRPS, crpStatus, crpsCmpOpts...); diff != "" {
			return fmt.Errorf("CRPS does not match expected (-want, +got): %s", diff)
		}

		return nil
	}
}

func namespaceProjectedStatus(status *placementv1beta1.PlacementStatus, targetNamespace string) *placementv1beta1.PlacementStatus {
	projectedStatus := status.DeepCopy()
	projectedStatus.SelectedResources = projectResourceIdentifiers(projectedStatus.SelectedResources, targetNamespace)
	for idx := range projectedStatus.PerClusterPlacementStatuses {
		clusterStatus := &projectedStatus.PerClusterPlacementStatuses[idx]
		clusterStatus.ApplicableResourceOverrides = projectResourceOverrides(clusterStatus.ApplicableResourceOverrides, targetNamespace)
		clusterStatus.ApplicableClusterResourceOverrides = nil
		clusterStatus.FailedPlacements = projectFailedPlacements(clusterStatus.FailedPlacements, targetNamespace)
		clusterStatus.DriftedPlacements = projectDriftedPlacements(clusterStatus.DriftedPlacements, targetNamespace)
		clusterStatus.DiffedPlacements = projectDiffedPlacements(clusterStatus.DiffedPlacements, targetNamespace)
	}

	conditions := make([]metav1.Condition, 0, len(projectedStatus.Conditions))
	for _, condition := range projectedStatus.Conditions {
		if condition.Type != string(placementv1beta1.ClusterResourcePlacementStatusSyncedConditionType) {
			conditions = append(conditions, condition)
		}
	}
	projectedStatus.Conditions = conditions
	return projectedStatus
}

func projectResourceIdentifier(identifier placementv1beta1.ResourceIdentifier, targetNamespace string) (placementv1beta1.ResourceIdentifier, bool) {
	inScope := identifier.Namespace == targetNamespace ||
		(identifier.Namespace == "" && identifier.Group == utils.NamespaceGVK.Group && identifier.Version == utils.NamespaceGVK.Version &&
			identifier.Kind == utils.NamespaceGVK.Kind && identifier.Name == targetNamespace)
	if !inScope {
		return placementv1beta1.ResourceIdentifier{}, false
	}
	if identifier.Envelope != nil &&
		(identifier.Envelope.Type == placementv1beta1.ClusterResourceEnvelopeType || identifier.Envelope.Namespace != targetNamespace) {
		identifier.Envelope = nil
	}
	return identifier, true
}

func projectResourceIdentifiers(identifiers []placementv1beta1.ResourceIdentifier, targetNamespace string) []placementv1beta1.ResourceIdentifier {
	if identifiers == nil {
		return nil
	}
	projected := make([]placementv1beta1.ResourceIdentifier, 0, len(identifiers))
	for _, identifier := range identifiers {
		if projectedIdentifier, ok := projectResourceIdentifier(identifier, targetNamespace); ok {
			projected = append(projected, projectedIdentifier)
		}
	}
	return projected
}

func projectResourceOverrides(overrides []placementv1beta1.NamespacedName, targetNamespace string) []placementv1beta1.NamespacedName {
	if overrides == nil {
		return nil
	}
	projected := make([]placementv1beta1.NamespacedName, 0, len(overrides))
	for _, override := range overrides {
		if override.Namespace == targetNamespace {
			projected = append(projected, override)
		}
	}
	return projected
}

func projectFailedPlacements(placements []placementv1beta1.FailedResourcePlacement, targetNamespace string) []placementv1beta1.FailedResourcePlacement {
	if placements == nil {
		return nil
	}
	projected := make([]placementv1beta1.FailedResourcePlacement, 0, len(placements))
	for _, placement := range placements {
		if identifier, ok := projectResourceIdentifier(placement.ResourceIdentifier, targetNamespace); ok {
			placement.ResourceIdentifier = identifier
			projected = append(projected, placement)
		}
	}
	return projected
}

func projectDriftedPlacements(placements []placementv1beta1.DriftedResourcePlacement, targetNamespace string) []placementv1beta1.DriftedResourcePlacement {
	if placements == nil {
		return nil
	}
	projected := make([]placementv1beta1.DriftedResourcePlacement, 0, len(placements))
	for _, placement := range placements {
		if identifier, ok := projectResourceIdentifier(placement.ResourceIdentifier, targetNamespace); ok {
			placement.ResourceIdentifier = identifier
			projected = append(projected, placement)
		}
	}
	return projected
}

func projectDiffedPlacements(placements []placementv1beta1.DiffedResourcePlacement, targetNamespace string) []placementv1beta1.DiffedResourcePlacement {
	if placements == nil {
		return nil
	}
	projected := make([]placementv1beta1.DiffedResourcePlacement, 0, len(placements))
	for _, placement := range placements {
		if identifier, ok := projectResourceIdentifier(placement.ResourceIdentifier, targetNamespace); ok {
			placement.ResourceIdentifier = identifier
			projected = append(projected, placement)
		}
	}
	return projected
}

func validateNamespaceProjection(status *placementv1beta1.PlacementStatus, targetNamespace string) error {
	validateIdentifier := func(identifier placementv1beta1.ResourceIdentifier) error {
		inScope := identifier.Namespace == targetNamespace ||
			(identifier.Namespace == "" && identifier.Group == utils.NamespaceGVK.Group && identifier.Version == utils.NamespaceGVK.Version &&
				identifier.Kind == utils.NamespaceGVK.Kind && identifier.Name == targetNamespace)
		if !inScope {
			return fmt.Errorf("CRPS reports out-of-scope resource %s/%s %s/%s", identifier.Group, identifier.Kind, identifier.Namespace, identifier.Name)
		}
		if identifier.Envelope != nil &&
			(identifier.Envelope.Type == placementv1beta1.ClusterResourceEnvelopeType || identifier.Envelope.Namespace != targetNamespace) {
			return fmt.Errorf("CRPS reports out-of-scope envelope %s/%s", identifier.Envelope.Namespace, identifier.Envelope.Name)
		}
		return nil
	}

	for _, identifier := range status.SelectedResources {
		if err := validateIdentifier(identifier); err != nil {
			return err
		}
	}
	for _, clusterStatus := range status.PerClusterPlacementStatuses {
		for _, override := range clusterStatus.ApplicableResourceOverrides {
			if override.Namespace != targetNamespace {
				return fmt.Errorf("CRPS reports out-of-scope ResourceOverride %s/%s", override.Namespace, override.Name)
			}
		}
		if len(clusterStatus.ApplicableClusterResourceOverrides) != 0 {
			return fmt.Errorf("CRPS reports cluster-scoped ResourceOverrides: %v", clusterStatus.ApplicableClusterResourceOverrides)
		}
		for _, placement := range clusterStatus.FailedPlacements {
			if err := validateIdentifier(placement.ResourceIdentifier); err != nil {
				return err
			}
		}
		for _, placement := range clusterStatus.DriftedPlacements {
			if err := validateIdentifier(placement.ResourceIdentifier); err != nil {
				return err
			}
		}
		for _, placement := range clusterStatus.DiffedPlacements {
			if err := validateIdentifier(placement.ResourceIdentifier); err != nil {
				return err
			}
		}
	}
	return nil
}
