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
)

const redactedPatchValue = "(redacted for security reasons)"

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

		// Construct expected CRPS.
		// Filter out StatusSynced condition as it's specific to CRP status sync process
		expectedStatus := crp.Status.DeepCopy()
		var filteredConditions []metav1.Condition
		for _, condition := range expectedStatus.Conditions {
			if condition.Type != string(placementv1beta1.ClusterResourcePlacementStatusSyncedConditionType) {
				filteredConditions = append(filteredConditions, condition)
			}
		}
		expectedStatus.Conditions = filteredConditions
		redactPlacementStatusPatchValues(expectedStatus)

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

		// Compare CRPS with expected, ignoring fields that vary.
		if diff := cmp.Diff(wantCRPS, crpStatus, crpsCmpOpts...); diff != "" {
			return fmt.Errorf("CRPS does not match expected (-want, +got): %s", diff)
		}

		return nil
	}
}

func redactPlacementStatusPatchValues(status *placementv1beta1.PlacementStatus) {
	for clusterIdx := range status.PerClusterPlacementStatuses {
		clusterStatus := &status.PerClusterPlacementStatuses[clusterIdx]
		for placementIdx := range clusterStatus.DriftedPlacements {
			redactPatchDetails(clusterStatus.DriftedPlacements[placementIdx].ObservedDrifts)
		}
		for placementIdx := range clusterStatus.DiffedPlacements {
			redactPatchDetails(clusterStatus.DiffedPlacements[placementIdx].ObservedDiffs)
		}
	}
}

func redactPatchDetails(details []placementv1beta1.PatchDetail) {
	for idx := range details {
		if details[idx].ValueInMember != "" {
			details[idx].ValueInMember = redactedPatchValue
		}
		if details[idx].ValueInHub != "" {
			details[idx].ValueInHub = redactedPatchValue
		}
	}
}
