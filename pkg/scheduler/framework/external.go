package framework

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/scheduler/queue"
	"github.com/kubefleet-dev/kubefleet/pkg/utils"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/controller"
)

const (
	pscNameStateKey      StateKey = "PlacementSchedulingContextName"
	pscDrcStatusStateKey StateKey = "PlacementSchedulingContextDynamicResourceClaimStatus"
)

const (
	drcStatusCondTypeProcessed = "DynamicResourceClaimProcessed"
)

func (f *framework) runExternalFilterAllProcessor(
	ctx context.Context,
	state *CycleState,
	placementKey queue.PlacementKey,
	policy placementv1beta1.PolicySnapshotObj,
	passed []*clusterv1beta1.MemberCluster,
	filtered []*filteredClusterWithStatus,
) ([]*clusterv1beta1.MemberCluster, []*filteredClusterWithStatus, error) {
	schedulingPolicy := policy.GetPolicySnapshotSpec().Policy
	switch {
	case schedulingPolicy == nil:
		klog.V(2).InfoS("No scheduling policy specified; skip calling any external processor")
		return passed, filtered, nil
	case len(schedulingPolicy.DynamicResourceClaims) == 0:
		klog.V(2).InfoS("No dynamic resource claims specified; skip calling any external processor")
		return passed, filtered, nil
	case len(schedulingPolicy.DynamicResourceClaims) > 1:
		klog.V(2).InfoS("Multiple dynamic resource claims specified; skip calling any external processor")
		return passed, filtered, nil
	}

	// Create a placement scheduling context.
	placementNamespace, placementName, err := controller.ExtractNamespaceNameFromKey(placementKey)
	if err != nil {
		klog.ErrorS(err, "Failed to extract namespace and name from placement key", "placementKey", placementKey)
		return nil, nil, fmt.Errorf("failed to extract namespace and name from placement key: %w", err)
	}
	potentialClusters := make([]string, 0, len(passed))
	for idx := range passed {
		c := passed[idx]
		potentialClusters = append(potentialClusters, c.Name)
	}
	pscSpec := &placementv1beta1.PlacementSchedulingContextSpec{
		PotentialClusters: potentialClusters,
		PlacementRef:      placementName,
	}
	pscName, err := f.preparePlacementSchedulingContext(ctx, placementNamespace, placementName, pscSpec)
	if err != nil {
		klog.ErrorS(err, "Failed to prepare placement scheduling context", "placementKey", placementKey)
		return nil, nil, fmt.Errorf("failed to prepare placement scheduling context: %w", err)
	}
	state.Write(pscNameStateKey, pscName)

	// Wait for the scheduling context to be resolved.
	backoff := wait.Backoff{
		Duration: 500 * time.Millisecond,
		Factor:   4,
		Jitter:   0.1,
		Steps:    3,
	}
	drcCount := len(schedulingPolicy.DynamicResourceClaims)
	drcStatusCollection := make([]placementv1beta1.DynamicResourceClaimStatus, 0, drcCount)
	errAfterRetries := retry.OnError(backoff, func(err error) bool {
		return true
	}, func() error {
		var psc placementv1beta1.PlacementSchedulingContextObj
		if placementNamespace == "" {
			crpsc := &placementv1beta1.ClusterResourcePlacementSchedulingContext{}
			if err := f.client.Get(ctx, types.NamespacedName{Namespace: "", Name: pscName}, crpsc); err != nil {
				return fmt.Errorf("failed to get cluster resource placement scheduling context: %w", err)
			}
			psc = crpsc
		} else {
			rppsc := &placementv1beta1.ResourcePlacementSchedulingContext{}
			if err := f.client.Get(ctx, types.NamespacedName{Namespace: placementNamespace, Name: pscName}, rppsc); err != nil {
				return fmt.Errorf("failed to get resource placement scheduling context: %w", err)
			}
			psc = rppsc
		}

		pscStatus := psc.GetPlacementSchedulingContextStatus()
		if len(pscStatus.DynamicResourceClaimStatus) != drcCount {
			return fmt.Errorf("not all dynamic resource claims are processed yet")
		}

		for idx := range pscStatus.DynamicResourceClaimStatus {
			drcStatus := pscStatus.DynamicResourceClaimStatus[idx]
			processedCond := meta.FindStatusCondition(drcStatus.Conditions, drcStatusCondTypeProcessed)
			if processedCond == nil || processedCond.ObservedGeneration != psc.GetGeneration() || processedCond.Status != metav1.ConditionTrue {
				return fmt.Errorf("dynamic resource claim for resource %s has not been fully processed yet or an error has occurred", drcStatus.ResourceName)
			}
		}

		drcStatusCollection = pscStatus.DynamicResourceClaimStatus
		return nil
	})
	if errAfterRetries != nil {
		klog.ErrorS(errAfterRetries, "Failed to wait for placement scheduling context to be resolved", "placementSchedulingCtx", klog.KRef(placementNamespace, pscName))
		return nil, nil, fmt.Errorf("failed to wait for placement scheduling context to be resolved: %w", errAfterRetries)
	}
	state.Write(pscDrcStatusStateKey, drcStatusCollection)

	allPassedClusterNameSet := sets.NewString()
	for k, _ := range drcStatusCollection[0].SuitableClusters {
		allPassedClusterNameSet.Insert(k)
	}
	updatedPassed := make([]*clusterv1beta1.MemberCluster, 0, len(passed))
	for idx := range passed {
		c := passed[idx]
		if allPassedClusterNameSet.Has(c.Name) {
			updatedPassed = append(updatedPassed, c)
		} else {
			filtered = append(filtered, &filteredClusterWithStatus{
				cluster: c,
				status:  NewNonErrorStatus(ClusterUnschedulable, "externalProcessor", "dynamic resource claim cannot be satisfied"),
			})
		}
	}

	return updatedPassed, filtered, nil
}

func (f *framework) preparePlacementSchedulingContext(
	ctx context.Context,
	namespace, name string,
	pscSpec *placementv1beta1.PlacementSchedulingContextSpec,
) (string, error) {
	var psc placementv1beta1.PlacementSchedulingContextObj
	nameWithRandomSuffix := fmt.Sprintf("%s-%s", name, utils.RandStr())
	if namespace == "" {
		// Prepare a resource placement scheduling context.
		psc = &placementv1beta1.ClusterResourcePlacementSchedulingContext{
			ObjectMeta: metav1.ObjectMeta{
				Name: nameWithRandomSuffix,
			},
			Spec: *pscSpec,
		}
	} else {
		psc = &placementv1beta1.ResourcePlacementSchedulingContext{
			ObjectMeta: metav1.ObjectMeta{
				Name:      nameWithRandomSuffix,
				Namespace: namespace,
			},
			Spec: *pscSpec,
		}
	}

	if err := f.client.Create(ctx, psc); err != nil {
		klog.ErrorS(err, "Failed to create placement scheduling context", "placementSchedulingCtx", klog.KObj(psc))
		return "", fmt.Errorf("failed to create placement scheduling context: %w", err)
	}
	return nameWithRandomSuffix, nil
}

func (f *framework) runExternalScoreAllProcessor(
	ctx context.Context,
	state *CycleState,
	policy placementv1beta1.PolicySnapshotObj,
	scored ScoredClusters,
) (ScoredClusters, error) {
	schedulingPolicy := policy.GetPolicySnapshotSpec().Policy
	switch {
	case schedulingPolicy == nil:
		klog.V(2).InfoS("No scheduling policy specified; skip calling any external processor")
		return scored, nil
	case len(schedulingPolicy.DynamicResourceClaims) == 0:
		klog.V(2).InfoS("No dynamic resource claims specified; skip calling any external processor")
		return scored, nil
	case len(schedulingPolicy.DynamicResourceClaims) > 1:
		klog.V(2).InfoS("Multiple dynamic resource claims specified; skip calling any external processor")
		return scored, nil
	}

	drc := schedulingPolicy.DynamicResourceClaims[0]
	if drc.Weight == nil || *drc.Weight < 0 {
		klog.V(2).InfoS("Dynamic resource claim has nil, zero, or negative weight; skip calling any external processor")
		return scored, nil
	}

	drcStatusCollectionRaw, err := state.Read(pscDrcStatusStateKey)
	drcStatusCollection, ok := drcStatusCollectionRaw.([]placementv1beta1.DynamicResourceClaimStatus)
	if !ok {
		klog.ErrorS(err, "Failed to read dynamic resource claim status collection")
		return nil, fmt.Errorf("failed to read dynamic resource claim status collection from cycle state")
	}

	drcStatus := drcStatusCollection[0]
	maxInnerWeight := int64(0)
	minInnerWeight := int64(0)
	for _, v := range drcStatus.SuitableClusters {
		if v > maxInnerWeight {
			maxInnerWeight = v
		}
		if v < minInnerWeight {
			minInnerWeight = v
		}
	}
	scoreRange := maxInnerWeight - minInnerWeight
	if scoreRange == 0 {
		klog.V(2).InfoS("All clusters have the same inner weight for the dynamic resource claim; no need to adjust scores")
		return scored, nil
	}

	for idx := range scored {
		scoredCluster := scored[idx]

		innerWeightForCluster, found := drcStatus.SuitableClusters[scoredCluster.Cluster.Name]
		if !found {
			// This should never happen as the filtering phase should have already filtered out
			// clusters that do not have the dynamic resource claim satisfied.
			wrappedErr := fmt.Errorf("failed to find score for cluster")
			klog.ErrorS(wrappedErr, "Failed to adjust scores based on dynamic resource claims", "cluster", scoredCluster.Cluster.Name)
		}

		additionalScore := float64(innerWeightForCluster-minInnerWeight) / float64(scoreRange) * float64(*drc.Weight)
		scoredCluster.Score.AffinityScore += int32(additionalScore)
	}
	return scored, nil
}
