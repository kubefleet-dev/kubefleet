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

package v1

// NamespacedName comprises a resource name, with a mandatory namespace.
type NamespacedName struct {
	// Name is the name of the namespaced scope resource.
	// +required
	Name string `json:"name"`
	// Namespace is namespace of the namespaced scope resource.
	// +required
	Namespace string `json:"namespace"`
}
