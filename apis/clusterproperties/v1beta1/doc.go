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

// Note (chenyu1): this package uses the kubernetes code generator instead of the kubebuilder
// code generator. See the README file for more information.

// v1beta1 contains the API schema definitions for the cluster properties API group, version v1beta1.
// +k8s:openapi-gen=true
// +k8s:openapi-model-package=com.github.kubefleet-dev.kubefleet.apis.clusterproperties.v1beta1
// +k8s:deepcopy-gen=package
// +groupName=clusterproperties.kubernetes-fleet.io
package v1beta1
