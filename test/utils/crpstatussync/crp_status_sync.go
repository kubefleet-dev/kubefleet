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

		// Construct expected CRPS and compare the fields unaffected by namespace-safe projection.
		expectedStatus := crp.Status.DeepCopy()
		filteredConditions := make([]metav1.Condition, 0, len(expectedStatus.Conditions))
		for _, condition := range expectedStatus.Conditions {
			if condition.Type != string(placementv1beta1.ClusterResourcePlacementStatusSyncedConditionType) {
				filteredConditions = append(filteredConditions, condition)
			}
		}
		expectedStatus.Conditions = filteredConditions

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

		cmpOpts := append([]cmp.Option{}, crpsCmpOpts...)
		cmpOpts = append(cmpOpts,
			cmpopts.IgnoreFields(placementv1beta1.PlacementStatus{}, "SelectedResources"),
			cmpopts.IgnoreFields(placementv1beta1.PerClusterPlacementStatus{}, "ApplicableResourceOverrides", "ApplicableClusterResourceOverrides", "FailedPlacements", "DriftedPlacements", "DiffedPlacements"),
			cmpopts.IgnoreFields(metav1.Condition{}, "Message"),
		)
		if diff := cmp.Diff(wantCRPS, crpStatus, cmpOpts...); diff != "" {
			return fmt.Errorf("CRPS does not match expected (-want, +got): %s", diff)
		}

		return nil
	}
}

func validateNamespaceProjection(status *placementv1beta1.PlacementStatus, targetNamespace string) error {
	validateIdentifier := func(identifier placementv1beta1.ResourceIdentifier) error {
		inScope := identifier.Namespace == targetNamespace ||
			(identifier.Namespace == "" && identifier.Group == utils.NamespaceGVK.Group && identifier.Version == utils.NamespaceGVK.Version &&
				identifier.Kind == utils.NamespaceGVK.Kind && identifier.Name == targetNamespace)
		if !inScope {
			return fmt.Errorf("CRPS reports out-of-scope resource %s/%s %s/%s", identifier.Group, identifier.Kind, identifier.Namespace, identifier.Name)
		}
		if identifier.Envelope != nil && identifier.Envelope.Namespace != targetNamespace {
			return fmt.Errorf("CRPS reports out-of-scope envelope %s/%s", identifier.Envelope.Namespace, identifier.Envelope.Name)
		}
		return nil
	}

	for _, identifier := range status.SelectedResources {
		if err := validateIdentifier(identifier); err != nil {
			return err
		}
	}
	for _, condition := range status.Conditions {
		if condition.Message != "" {
			return fmt.Errorf("CRPS reports top-level condition message for %q", condition.Type)
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
		for _, condition := range clusterStatus.Conditions {
			if condition.Message != "" {
				return fmt.Errorf("CRPS reports per-cluster condition message for %q", condition.Type)
			}
		}
		for _, placement := range clusterStatus.FailedPlacements {
			if err := validateIdentifier(placement.ResourceIdentifier); err != nil {
				return err
			}
			if placement.Condition.Message != "" {
				return fmt.Errorf("CRPS reports failed placement condition message for %s/%s", placement.Namespace, placement.Name)
			}
		}
		for _, placement := range clusterStatus.DriftedPlacements {
			if err := validateIdentifier(placement.ResourceIdentifier); err != nil {
				return err
			}
			for _, detail := range placement.ObservedDrifts {
				if detail.ValueInMember != "" || detail.ValueInHub != "" {
					return fmt.Errorf("CRPS reports drift values for %s/%s at %s", placement.Namespace, placement.Name, detail.Path)
				}
			}
		}
		for _, placement := range clusterStatus.DiffedPlacements {
			if err := validateIdentifier(placement.ResourceIdentifier); err != nil {
				return err
			}
			for _, detail := range placement.ObservedDiffs {
				if detail.ValueInMember != "" || detail.ValueInHub != "" {
					return fmt.Errorf("CRPS reports diff values for %s/%s at %s", placement.Namespace, placement.Name, detail.Path)
				}
			}
		}
	}
	return nil
}
