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

package draincluster

import (
	"context"
	"fmt"
	"log"

	"github.com/spf13/cobra"
	k8errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/condition"
	evictionutils "github.com/kubefleet-dev/kubefleet/pkg/utils/eviction"
	toolsutils "github.com/kubefleet-dev/kubefleet/tools/utils"
)

const (
	uuidLength                  = 8
	drainEvictionNameFormat     = "drain-eviction-%s-%s-%s"
	resourceIdentifierKeyFormat = "%s/%s/%s/%s/%s"
)

// drainOptions wraps common cluster connection parameters
type drainOptions struct {
	hubClusterContext string
	clusterName       string

	hubClient client.Client
}

// NewCmdDrainCluster creates a new draincluster command
func NewCmdDrainCluster() *cobra.Command {
	o := &drainOptions{}

	cmd := &cobra.Command{
		Use:   "draincluster",
		Short: "Drain a member cluster",
		Long:  "Drain a member cluster by cordoning it and removing propagated resources",
		RunE: func(command *cobra.Command, args []string) error {
			if err := o.setupClient(); err != nil {
				return err
			}
			return o.runDrain()
		},
	}

	// Add flags specific to drain command
	cmd.Flags().StringVar(&o.hubClusterContext, "hubClusterContext", "", "kubectl context for the hub cluster (required)")
	cmd.Flags().StringVar(&o.clusterName, "clusterName", "", "name of the member cluster (required)")

	// Mark required flags
	_ = cmd.MarkFlagRequired("hubClusterContext")
	_ = cmd.MarkFlagRequired("clusterName")

	return cmd
}

func (o *drainOptions) runDrain() error {
	ctx := context.Background()

	isDrainSuccessful, err := o.drain(ctx)
	if err != nil {
		return fmt.Errorf("failed to drain member cluster %s: %w", o.clusterName, err)
	}

	if isDrainSuccessful {
		log.Printf("drain was successful for cluster %s", o.clusterName)
	} else {
		log.Printf("drain was not successful for cluster %s", o.clusterName)
	}

	log.Printf("retrying drain to ensure all resources propagated from hub cluster are evicted")
	isDrainRetrySuccessful, err := o.drain(ctx)
	if err != nil {
		return fmt.Errorf("failed to drain cluster on retry %s: %w", o.clusterName, err)
	}
	if isDrainRetrySuccessful {
		log.Printf("drain retry was successful for cluster %s", o.clusterName)
	} else {
		log.Printf("drain retry was not successful for cluster %s", o.clusterName)
	}

	log.Printf("reminder: uncordon the cluster %s to remove cordon taint if needed", o.clusterName)
	return nil
}

// setupClient creates and configures the Kubernetes client
func (o *drainOptions) setupClient() error {
	scheme := runtime.NewScheme()

	if err := clusterv1beta1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("failed to add custom APIs (cluster) to the runtime scheme: %w", err)
	}
	if err := placementv1beta1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("failed to add custom APIs (placement) to the runtime scheme: %w", err)
	}

	hubClient, err := toolsutils.GetClusterClientFromClusterContext(o.hubClusterContext, scheme)
	if err != nil {
		return fmt.Errorf("failed to create hub cluster client: %w", err)
	}

	o.hubClient = hubClient
	return nil
}

func (o *drainOptions) drain(ctx context.Context) (bool, error) {
	if err := o.cordon(ctx); err != nil {
		return false, fmt.Errorf("failed to cordon member cluster %s: %w", o.clusterName, err)
	}
	log.Printf("Successfully cordoned member cluster %s by adding cordon taint", o.clusterName)

	crpNameMap, err := o.fetchClusterResourcePlacementNamesToEvict(ctx)
	if err != nil {
		return false, err
	}

	if len(crpNameMap) == 0 {
		log.Printf("There are currently no resources propagated to %s from fleet using ClusterResourcePlacement resources", o.clusterName)
		return true, nil
	}

	isDrainSuccessful := true
	// create eviction objects for all <crpName, targetCluster>.
	for crpName := range crpNameMap {
		evictionName, err := generateDrainEvictionName(crpName, o.clusterName)
		if err != nil {
			return false, err
		}

		err = retry.OnError(retry.DefaultBackoff, func(err error) bool {
			return k8errors.IsAlreadyExists(err)
		}, func() error {
			eviction := placementv1beta1.ClusterResourcePlacementEviction{
				ObjectMeta: metav1.ObjectMeta{
					Name: evictionName,
				},
				Spec: placementv1beta1.PlacementEvictionSpec{
					PlacementName: crpName,
					ClusterName:   o.clusterName,
				},
			}
			return o.hubClient.Create(ctx, &eviction)
		})

		if err != nil {
			return false, fmt.Errorf("failed to create eviction %s for CRP %s targeting member cluster %s: %w", evictionName, crpName, o.clusterName, err)
		}

		log.Printf("Created eviction %s for CRP %s targeting member cluster %s", evictionName, crpName, o.clusterName)

		// wait until evictions reach a terminal state.
		var eviction placementv1beta1.ClusterResourcePlacementEviction
		err = wait.ExponentialBackoffWithContext(ctx, retry.DefaultBackoff, func(ctx context.Context) (bool, error) {
			if err := o.hubClient.Get(ctx, types.NamespacedName{Name: evictionName}, &eviction); err != nil {
				return false, fmt.Errorf("failed to get eviction %s for CRP %s targeting member cluster %s: %w", evictionName, crpName, o.clusterName, err)
			}
			return evictionutils.IsEvictionInTerminalState(&eviction), nil
		})

		if err != nil {
			return false, fmt.Errorf("failed to wait for eviction %s for CRP %s targeting member cluster %s to reach terminal state: %w", evictionName, crpName, o.clusterName, err)
		}

		// TODO: add safeguards to check if eviction conditions are set to unknown.
		validCondition := eviction.GetCondition(string(placementv1beta1.PlacementEvictionConditionTypeValid))
		if validCondition != nil && validCondition.Status == metav1.ConditionFalse {
			// check to see if CRP is missing or CRP is being deleted or CRB is missing.
			if validCondition.Reason == condition.EvictionInvalidMissingCRPMessage ||
				validCondition.Reason == condition.EvictionInvalidDeletingCRPMessage ||
				validCondition.Reason == condition.EvictionInvalidMissingCRBMessage {
				log.Printf("eviction %s is invalid with reason %s for CRP %s targeting member cluster %s, but drain will succeed", evictionName, validCondition.Reason, crpName, o.clusterName)
				continue
			}
		}
		executedCondition := eviction.GetCondition(string(placementv1beta1.PlacementEvictionConditionTypeExecuted))
		if executedCondition == nil || executedCondition.Status == metav1.ConditionFalse {
			isDrainSuccessful = false
			log.Printf("eviction %s was not executed successfully for CRP %s targeting member cluster %s", evictionName, crpName, o.clusterName)
			continue
		}
		log.Printf("eviction %s was executed successfully for CRP %s targeting member cluster %s", evictionName, crpName, o.clusterName)
		// log each cluster scoped resource evicted for CRP.
		clusterScopedResourceIdentifiers, err := o.collectClusterScopedResourcesSelectedByCRP(ctx, crpName)
		if err != nil {
			log.Printf("failed to collect cluster scoped resources selected by CRP %s: %v", crpName, err)
			continue
		}
		for _, resourceIdentifier := range clusterScopedResourceIdentifiers {
			log.Printf("evicted resource %s propagated by CRP %s targeting member cluster %s", generateResourceIdentifierKey(resourceIdentifier), crpName, o.clusterName)
		}
	}

	return isDrainSuccessful, nil
}

func (o *drainOptions) cordon(ctx context.Context) error {
	// add taint to member cluster to ensure resources aren't scheduled on it.
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var mc clusterv1beta1.MemberCluster
		if err := o.hubClient.Get(ctx, types.NamespacedName{Name: o.clusterName}, &mc); err != nil {
			return err
		}

		// search to see cordonTaint already exists on the cluster.
		for i := range mc.Spec.Taints {
			if mc.Spec.Taints[i] == toolsutils.CordonTaint {
				return nil
			}
		}

		// add taint to member cluster to cordon.
		mc.Spec.Taints = append(mc.Spec.Taints, toolsutils.CordonTaint)

		return o.hubClient.Update(ctx, &mc)
	})
}

func (o *drainOptions) fetchClusterResourcePlacementNamesToEvict(ctx context.Context) (map[string]bool, error) {
	var crbList placementv1beta1.ClusterResourceBindingList
	if err := o.hubClient.List(ctx, &crbList); err != nil {
		return map[string]bool{}, fmt.Errorf("failed to list cluster resource bindings: %w", err)
	}

	crpNameMap := make(map[string]bool)
	// find all unique CRP names for which eviction needs to occur.
	for i := range crbList.Items {
		crb := crbList.Items[i]
		if crb.Spec.TargetCluster == o.clusterName && crb.DeletionTimestamp == nil {
			crpName, ok := crb.GetLabels()[placementv1beta1.PlacementTrackingLabel]
			if !ok {
				return map[string]bool{}, fmt.Errorf("failed to get CRP name from binding %s", crb.Name)
			}
			crpNameMap[crpName] = true
		}
	}

	return crpNameMap, nil
}

func (o *drainOptions) collectClusterScopedResourcesSelectedByCRP(ctx context.Context, crpName string) ([]placementv1beta1.ResourceIdentifier, error) {
	var crp placementv1beta1.ClusterResourcePlacement
	if err := o.hubClient.Get(ctx, types.NamespacedName{Name: crpName}, &crp); err != nil {
		return nil, fmt.Errorf("failed to get ClusterResourcePlacement %s: %w", crpName, err)
	}

	var resourcesPropagated []placementv1beta1.ResourceIdentifier
	for _, selectedResource := range crp.Status.SelectedResources {
		// only collect cluster scoped resources.
		if len(selectedResource.Namespace) == 0 {
			resourcesPropagated = append(resourcesPropagated, selectedResource)
		}
	}
	return resourcesPropagated, nil
}

func generateDrainEvictionName(crpName, targetCluster string) (string, error) {
	evictionName := fmt.Sprintf(drainEvictionNameFormat, crpName, targetCluster, uuid.NewUUID()[:uuidLength])

	// check to see if eviction name is a valid DNS1123 subdomain name https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#dns-subdomain-names.
	if errs := validation.IsDNS1123Subdomain(evictionName); len(errs) != 0 {
		return "", fmt.Errorf("failed to format a qualified name for drain eviction object with CRP name %s, cluster name %s: %v", crpName, targetCluster, errs)
	}
	return evictionName, nil
}

func generateResourceIdentifierKey(r placementv1beta1.ResourceIdentifier) string {
	if len(r.Group) == 0 && len(r.Namespace) == 0 {
		return fmt.Sprintf(resourceIdentifierKeyFormat, "''", r.Version, r.Kind, "''", r.Name)
	}
	if len(r.Group) == 0 {
		return fmt.Sprintf(resourceIdentifierKeyFormat, "''", r.Version, r.Kind, r.Namespace, r.Name)
	}
	if len(r.Namespace) == 0 {
		return fmt.Sprintf(resourceIdentifierKeyFormat, r.Group, r.Version, r.Kind, "''", r.Name)
	}
	return fmt.Sprintf(resourceIdentifierKeyFormat, r.Group, r.Version, r.Kind, r.Namespace, r.Name)
}
