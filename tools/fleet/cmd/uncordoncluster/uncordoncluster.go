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

package uncordoncluster

import (
	"context"
	"fmt"
	"log"
	"slices"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	toolsutils "github.com/kubefleet-dev/kubefleet/tools/utils"
)

// uncordonOptions wraps common cluster connection parameters
type uncordonOptions struct {
	hubClusterContext string
	clusterName       string
	timeout           time.Duration

	hubClient client.Client
}

// NewCmdUncordonCluster creates a new uncordoncluster command
func NewCmdUncordonCluster() *cobra.Command {
	o := &uncordonOptions{}

	cmd := &cobra.Command{
		Use:   "uncordoncluster",
		Short: "Uncordon a member cluster",
		Long:  "Uncordon a previously drained member cluster by removing the cordon taint",
		RunE: func(command *cobra.Command, args []string) error {
			var err error
			if o.hubClient, err = toolsutils.NewHubClient(o.hubClusterContext); err != nil {
				return err
			}
			return o.runUncordon(command.Context())
		},
	}

	// Add flags specific to uncordon command
	cmd.Flags().StringVar(&o.hubClusterContext, "hub-cluster-context", "", "kubectl context for the hub cluster (required)")
	cmd.Flags().StringVar(&o.clusterName, "cluster-name", "", "name of the member cluster (required)")
	cmd.Flags().DurationVar(&o.timeout, "timeout", 5*time.Minute, "Maximum time to wait for the operation to complete")

	// Mark required flags
	_ = cmd.MarkFlagRequired("hub-cluster-context")
	_ = cmd.MarkFlagRequired("cluster-name")

	return cmd
}

func (o *uncordonOptions) runUncordon(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()

	if err := o.uncordon(ctx); err != nil {
		return fmt.Errorf("failed to uncordon cluster %s: %w", o.clusterName, err)
	}

	log.Printf("uncordoned member cluster %s", o.clusterName)
	return nil
}

func (o *uncordonOptions) uncordon(ctx context.Context) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var mc clusterv1beta1.MemberCluster
		if err := o.hubClient.Get(ctx, types.NamespacedName{Name: o.clusterName}, &mc); err != nil {
			return err
		}

		if !slices.Contains(mc.Spec.Taints, toolsutils.CordonTaint) {
			return nil
		}
		mc.Spec.Taints = slices.DeleteFunc(mc.Spec.Taints, func(t clusterv1beta1.Taint) bool { return t == toolsutils.CordonTaint })

		return o.hubClient.Update(ctx, &mc)
	})
}
