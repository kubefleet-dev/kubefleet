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

	appsv1 "k8s.io/api/apps/v1"

	experimentalv1beta1 "github.com/kubefleet-dev/kubefleet/apis/experimental/v1beta1"
)

func (r *Reconciler) collectAdditionalResourceManifests(
	ctx context.Context, deploy *appsv1.Deployment,
) ([]experimentalv1beta1.SameNamespacedObjectReference, error) {
	res := []experimentalv1beta1.SameNamespacedObjectReference{}

	podTemplateSpec := deploy.Spec.Template.Spec
	volumes := podTemplateSpec.Volumes
	if len(volumes) == 0 {
		return nil, nil
	}

	for idx := range volumes {
		vol := &volumes[idx]

		switch {
		case vol.ConfigMap != nil:
			res = append(res, experimentalv1beta1.SameNamespacedObjectReference{
				Kind:       "ConfigMap",
				Name:       vol.ConfigMap.Name,
				APIGroup:   "",
				APIVersion: "v1",
				Resource:   "configmaps",
			})
		case vol.Secret != nil:
			res = append(res, experimentalv1beta1.SameNamespacedObjectReference{
				Kind:       "Secret",
				Name:       vol.Secret.SecretName,
				APIGroup:   "",
				APIVersion: "v1",
				Resource:   "secrets",
			})
		}
	}
	return res, nil
}
