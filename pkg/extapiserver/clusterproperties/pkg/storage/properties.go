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

package storage

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/registry/rest"

	clusterpropertiesv1beta1 "github.com/kubefleet-dev/kubefleet/apis/clusterproperties/v1beta1"
)

type PropertiesStore struct{}

// Verify that PropertiesStore implements the required interfaces.
var _ rest.Storage = &PropertiesStore{}
var _ rest.SingularNameProvider = &PropertiesStore{}     // Must support singular names.
var _ rest.Scoper = &PropertiesStore{}                   // Must be properly scoped (cluster-scoped in this case).
var _ rest.KindProvider = &PropertiesStore{}             // Must be exposed as a specific Kind.
var _ rest.GroupVersionKindProvider = &PropertiesStore{} // Must be exposed as a specific GVK.

// Verify that PropertiesStore supports specific storage actions.
var _ rest.Getter = &PropertiesStore{} // Must support the GET ops.

// Implement the rest.Storage interface.

// New is a required method for the rest.Storage interface.
//
// It returns an empty object of a specific API type for further manipulation (CREATE/UPDATE ops).
func (s *PropertiesStore) New() runtime.Object {
	return &clusterpropertiesv1beta1.MemberClusterProperties{}
}

// Destroy is a required method for the rest.Storage interface.
//
// It performs cleanup ops as needed on API server shutdown. In the case of KubeFleet cluster properties
// extension API server, since we are using in-memory storage, and all properties are ephemeral by nature,
// this method is a no-op.
func (s *PropertiesStore) Destroy() {}

// Implement the rest.KindProvider interface.

// Kind is a required method for the rest.KindProvider interface.
//
// It returns the Kind name of the API object that this storage manages.
func (s *PropertiesStore) Kind() string {
	return "MemberClusterProperties"
}

// Implement the rest.Scoper interface.

// NamespaceScoped is a required method for the rest.Scoper interface.
//
// It reports whether the API object under the storage's management is namespace-scoped
// or cluster-scoped. All cluster properties are cluster-scoped.
func (s *PropertiesStore) NamespaceScoped() bool {
	return false
}

// Implement the rest.SingularNameProvider interface.

// SingularName is a required method for the rest.SingularNameProvider interface.
//
// It returns the singular name of the API object that this storage manages. In our case,
// there is no singular name available for the cluster properties API object.
func (s *PropertiesStore) GetSingularName() string {
	return ""
}

// Implement the rest.GroupVersionKindProvider interface.

// GroupVersionKind is a required method for the rest.GroupVersionKindProvider interface.
//
// It returns the GroupVersionKind of the API object that this storage manages.
func (s *PropertiesStore) GroupVersionKind(containingGV schema.GroupVersion) schema.GroupVersionKind {
	return clusterpropertiesv1beta1.GroupVersion.WithKind(s.Kind())
}
