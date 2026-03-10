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

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

var (
	// The runtime scheme that includes all the API types used by the KubeFleet cluster properties
	// extension API server.
	Scheme = runtime.NewScheme()

	// The codec factory for encoding/decoding all the API types in the above scheme.
	CodecFactory = serializer.NewCodecFactory(Scheme)
)

// Set up the scheme.
func init() {
	SchemeBuilder.Register(&MemberClusterProperties{}, &MemberClusterPropertiesList{})

	utilruntime.Must(AddToScheme(Scheme))
	metav1.AddToGroupVersion(Scheme, GroupVersion)

	utilruntime.Must(Scheme.SetVersionPriority(GroupVersion))

	unversioned := schema.GroupVersion{Group: "", Version: "v1"}
	Scheme.AddUnversionedTypes(unversioned,
		&metav1.Status{},
		&metav1.APIVersions{},
		&metav1.APIGroupList{},
		&metav1.APIGroup{},
		&metav1.APIResourceList{},
		&metav1.ListOptions{},
		&metav1.CreateOptions{},
		&metav1.UpdateOptions{},
		&metav1.DeleteOptions{},
		&metav1.PatchOptions{},
	)
}
