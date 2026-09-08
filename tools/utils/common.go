/*
Copyright (c) Microsoft Corporation.
Licensed under the MIT license.
*/

package utils

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

// CordonTaint is the taint draincluster adds (and uncordoncluster removes) to keep new placements off a member cluster.
var CordonTaint = clusterv1beta1.Taint{
	Key:    "cordon-key",
	Value:  "cordon-value",
	Effect: "NoSchedule",
}

// NewHubClient creates a client for the hub cluster reachable through the given kubeconfig context,
// with all Fleet API types registered.
func NewHubClient(clusterContext string) (client.Client, error) {
	scheme := runtime.NewScheme()
	if err := clusterv1beta1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to create runtime scheme: %w", err)
	}
	if err := placementv1beta1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to create runtime scheme: %w", err)
	}

	// Default loading rules honour $KUBECONFIG (including multi-path lists) and fall back to ~/.kube/config,
	// exactly like kubectl.
	restConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{CurrentContext: clusterContext},
	).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig context %q: %w", clusterContext, err)
	}

	hubClient, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create hub cluster client: %w", err)
	}
	return hubClient, nil
}
