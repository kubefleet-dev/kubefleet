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

package approve

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	toolsutils "github.com/kubefleet-dev/kubefleet/tools/utils"
)

const (
	// kindClusterApprovalRequest is the kind for ClusterApprovalRequest.
	kindClusterApprovalRequest = "clusterapprovalrequest"
	// kindApprovalRequest is the kind for ApprovalRequest.
	kindApprovalRequest = "approvalrequest"
	// aliasClusterApprovalRequest is the short alias for ClusterApprovalRequest.
	aliasClusterApprovalRequest = "careq"
	// aliasApprovalRequest is the short alias for ApprovalRequest.
	aliasApprovalRequest = "areq"
)

type approveOptions struct {
	hubClusterContext string
	kind              string
	name              string
	namespace         string

	hubClient client.Client
}

func NewCmdApprove() *cobra.Command {
	o := &approveOptions{}

	cmd := &cobra.Command{
		Use:   "approve <kind>",
		Short: "Approve a resource",
		Long: `Approve a resource by updating its status with an "Approved" condition.

This command updates the approval request status with an "Approved" condition,
allowing staged update runs to proceed to the next stage.

Supported kinds:
  clusterapprovalrequest (careq) - Approve a ClusterApprovalRequest (cluster-scoped)
  approvalrequest (areq)         - Approve an ApprovalRequest (namespace-scoped)

For namespace-scoped resources (approvalrequest), you must also specify the --namespace flag.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.kind = normalizeKind(args[0])
			if err := o.validate(); err != nil {
				return err
			}
			if err := o.setupClient(); err != nil {
				return err
			}
			return o.run(cmd.Context())
		},
	}

	cmd.Flags().StringVar(&o.hubClusterContext, "hubClusterContext", "", "The name of the kubeconfig context to use for the hub cluster")
	cmd.Flags().StringVar(&o.name, "name", "", "The name of the resource to approve")
	cmd.Flags().StringVarP(&o.namespace, "namespace", "n", "", "The namespace of the resource to approve (required for namespace-scoped resources)")

	// Mark required flags.
	_ = cmd.MarkFlagRequired("hubClusterContext")
	_ = cmd.MarkFlagRequired("name")

	return cmd
}

// normalizeKind converts kind aliases to their canonical form.
func normalizeKind(kind string) string {
	switch strings.ToLower(kind) {
	case aliasClusterApprovalRequest:
		return kindClusterApprovalRequest
	case aliasApprovalRequest:
		return kindApprovalRequest
	default:
		return strings.ToLower(kind)
	}
}

// validate checks that the options are valid.
func (o *approveOptions) validate() error {
	if o.kind == "" {
		return fmt.Errorf("resource kind is required")
	}
	if o.name == "" {
		return fmt.Errorf("resource name is required")
	}

	switch o.kind {
	case kindClusterApprovalRequest:
		// Cluster-scoped, no namespace required.
	case kindApprovalRequest:
		if o.namespace == "" {
			return fmt.Errorf("namespace is required for approvalrequest resources (use --namespace or -n flag)")
		}
	default:
		return fmt.Errorf("unsupported resource kind %q, supported kinds are: clusterapprovalrequest (careq), approvalrequest (areq)", o.kind)
	}
	return nil
}

func (o *approveOptions) run(ctx context.Context) error {
	switch o.kind {
	case kindClusterApprovalRequest:
		return o.approveClusterApprovalRequest(ctx)
	case kindApprovalRequest:
		return o.approveApprovalRequest(ctx)
	default:
		return fmt.Errorf("unsupported resource kind %q", o.kind)
	}
}

// approveClusterApprovalRequest approves a ClusterApprovalRequest (cluster-scoped).
func (o *approveOptions) approveClusterApprovalRequest(ctx context.Context) error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var car placementv1beta1.ClusterApprovalRequest
		if err := o.hubClient.Get(ctx, types.NamespacedName{Name: o.name}, &car); err != nil {
			return fmt.Errorf("failed to get ClusterApprovalRequest %q: %w", o.name, err)
		}

		// Add the Approved condition.
		approvedCondition := metav1.Condition{
			Type:               string(placementv1beta1.ApprovalRequestConditionApproved),
			Status:             metav1.ConditionTrue,
			Reason:             "ClusterApprovalRequestApproved",
			Message:            "ClusterApprovalRequest has been approved",
			ObservedGeneration: car.Generation,
		}

		// Update or add the condition.
		meta.SetStatusCondition(&car.Status.Conditions, approvedCondition)

		return o.hubClient.Status().Update(ctx, &car)
	})

	if err != nil {
		return fmt.Errorf("failed to approve ClusterApprovalRequest %q: %w", o.name, err)
	}

	log.Printf("ClusterApprovalRequest %q approved successfully\n", o.name)
	return nil
}

// approveApprovalRequest approves an ApprovalRequest (namespace-scoped).
func (o *approveOptions) approveApprovalRequest(ctx context.Context) error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var ar placementv1beta1.ApprovalRequest
		if err := o.hubClient.Get(ctx, types.NamespacedName{Name: o.name, Namespace: o.namespace}, &ar); err != nil {
			return fmt.Errorf("failed to get ApprovalRequest %q in namespace %q: %w", o.name, o.namespace, err)
		}

		// Add the Approved condition.
		approvedCondition := metav1.Condition{
			Type:               string(placementv1beta1.ApprovalRequestConditionApproved),
			Status:             metav1.ConditionTrue,
			Reason:             "ApprovalRequestApproved",
			Message:            "ApprovalRequest has been approved",
			ObservedGeneration: ar.Generation,
		}

		// Update or add the condition.
		meta.SetStatusCondition(&ar.Status.Conditions, approvedCondition)

		return o.hubClient.Status().Update(ctx, &ar)
	})

	if err != nil {
		return fmt.Errorf("failed to approve ApprovalRequest %q in namespace %q: %w", o.name, o.namespace, err)
	}

	log.Printf("ApprovalRequest %q in namespace %q approved successfully\n", o.name, o.namespace)
	return nil
}

// setupClient creates and configures the Kubernetes client
func (o *approveOptions) setupClient() error {
	scheme := runtime.NewScheme()

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
