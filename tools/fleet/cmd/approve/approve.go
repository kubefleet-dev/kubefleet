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
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	toolsutils "github.com/kubefleet-dev/kubefleet/tools/utils"
)

// Kind names (and short aliases) accepted as the positional argument.
const (
	kindClusterApprovalRequest  = "clusterapprovalrequest"
	kindApprovalRequest         = "approvalrequest"
	aliasClusterApprovalRequest = "careq"
	aliasApprovalRequest        = "areq"
)

// approveKind describes one approvable resource kind.
type approveKind struct {
	// kind is the API kind, e.g. "ClusterApprovalRequest"; used in reasons, messages and logs.
	kind       string
	namespaced bool
	// newObj returns an empty object of this kind and a pointer to its status conditions.
	newObj func() (client.Object, *[]metav1.Condition)
}

var (
	clusterApprovalRequestKind = &approveKind{
		kind: "ClusterApprovalRequest",
		newObj: func() (client.Object, *[]metav1.Condition) {
			obj := &placementv1beta1.ClusterApprovalRequest{}
			return obj, &obj.Status.Conditions
		},
	}
	approvalRequestKind = &approveKind{
		kind:       "ApprovalRequest",
		namespaced: true,
		newObj: func() (client.Object, *[]metav1.Condition) {
			obj := &placementv1beta1.ApprovalRequest{}
			return obj, &obj.Status.Conditions
		},
	}
	kinds = map[string]*approveKind{
		kindClusterApprovalRequest:  clusterApprovalRequestKind,
		aliasClusterApprovalRequest: clusterApprovalRequestKind,
		kindApprovalRequest:         approvalRequestKind,
		aliasApprovalRequest:        approvalRequestKind,
	}
)

// resolveKind maps a kind name or alias (case-insensitive) to its approveKind.
func resolveKind(kind string) (*approveKind, error) {
	if kind == "" {
		return nil, fmt.Errorf("resource kind is required")
	}
	k, ok := kinds[strings.ToLower(kind)]
	if !ok {
		return nil, fmt.Errorf("unsupported resource kind %q", kind)
	}
	return k, nil
}

type approveOptions struct {
	hubClusterContext string
	name              string
	namespace         string
	timeout           time.Duration

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
			k, err := resolveKind(args[0])
			if err != nil {
				return err
			}
			if err := o.validate(k); err != nil {
				return err
			}
			if o.hubClient, err = toolsutils.NewHubClient(o.hubClusterContext); err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), o.timeout)
			defer cancel()
			return o.run(ctx, k)
		},
	}

	cmd.Flags().StringVar(&o.hubClusterContext, "hub-cluster-context", "", "The name of the kubeconfig context to use for the hub cluster")
	cmd.Flags().StringVar(&o.name, "name", "", "The name of the resource to approve")
	cmd.Flags().StringVarP(&o.namespace, "namespace", "n", "", "The namespace of the resource to approve (required for namespace-scoped resources)")
	cmd.Flags().DurationVar(&o.timeout, "timeout", 5*time.Minute, "Maximum time to wait for the operation to complete")

	_ = cmd.MarkFlagRequired("hub-cluster-context")
	_ = cmd.MarkFlagRequired("name")

	return cmd
}

// validate checks that the options are valid for the resolved kind.
func (o *approveOptions) validate(k *approveKind) error {
	switch {
	case o.name == "":
		return fmt.Errorf("resource name is required")
	case k.namespaced && o.namespace == "":
		return fmt.Errorf("namespace is required for %s (use --namespace or -n flag)", strings.ToLower(k.kind))
	case !k.namespaced && o.namespace != "":
		return fmt.Errorf("%s is cluster-scoped and does not accept a namespace", strings.ToLower(k.kind))
	}
	return nil
}

// run patches the "Approved" condition onto the resource's status.
func (o *approveOptions) run(ctx context.Context, k *approveKind) error {
	target := fmt.Sprintf("%q", o.name)
	if k.namespaced {
		target += fmt.Sprintf(" in namespace %q", o.namespace)
	}
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		obj, conditions := k.newObj()
		if err := o.hubClient.Get(ctx, types.NamespacedName{Name: o.name, Namespace: o.namespace}, obj); err != nil {
			return fmt.Errorf("failed to get %s %s: %w", k.kind, target, err)
		}
		meta.SetStatusCondition(conditions, metav1.Condition{
			Type:               string(placementv1beta1.ApprovalRequestConditionApproved),
			Status:             metav1.ConditionTrue,
			Reason:             k.kind + "Approved",
			Message:            k.kind + " has been approved",
			ObservedGeneration: obj.GetGeneration(),
		})
		return o.hubClient.Status().Update(ctx, obj)
	})
	if err != nil {
		return fmt.Errorf("failed to approve %s %s: %w", k.kind, target, err)
	}

	log.Printf("%s %s approved successfully", k.kind, target)
	return nil
}
