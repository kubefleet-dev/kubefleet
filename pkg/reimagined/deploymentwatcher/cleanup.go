/*
Copyright 2026 The KubeFleet Authors.

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

package deploymentwatcher

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	experimentalv1beta1 "github.com/kubefleet-dev/kubefleet/apis/experimental/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/errors"
)

func (r *Reconciler) deletePlacementPolicyFor(ctx context.Context, deploy *appsv1.Deployment) error {
	if controllerutil.ContainsFinalizer(deploy, deploymentForPlacementFinalizer) {
		// Delete the corresponding placement object (if any).
		placement := experimentalv1beta1.PlacementPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf(placementPolicyNameFmt, deploy.Name),
				Namespace: deploy.Namespace,
			},
		}
		if err := r.HubClient.Delete(ctx, &placement); err != nil && !apierrors.IsNotFound(err) {
			return errors.NewAPIServerError(err, "failed to delete the placement policy for the deployment", false)
		}

		// Remove the finalizer so that the deployment can be deleted.
		controllerutil.RemoveFinalizer(deploy, deploymentForPlacementFinalizer)
		if err := r.HubClient.Update(ctx, deploy); err != nil {
			return errors.NewAPIServerError(err, "failed to remove finalizer from the deployment", false)
		}
	}
	return nil
}
