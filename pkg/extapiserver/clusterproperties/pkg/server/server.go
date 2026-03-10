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

package server

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"

	clusterpropertiesv1beta1 "github.com/kubefleet-dev/kubefleet/apis/clusterproperties/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/extapiserver/clusterproperties/pkg/storage"
)

type Server struct {
	GenericAPIServer *genericapiserver.GenericAPIServer
}

func New(
	genericAPIServerCfg *genericapiserver.Config,
) (*Server, error) {
	// TO-DO (chenyu1): add additional, KubeFleet cluster properties extension API server specific
	// metrics.

	// Disable the default metric handlers.
	genericAPIServerCfg.EnableMetrics = false

	// Build the generic API server.
	genericAPIServer, err := genericAPIServerCfg.
		Complete(nil).
		New("kubefleet-cluster-properties-ext-api-server", genericapiserver.NewEmptyDelegate())
	if err != nil {
		return nil, fmt.Errorf("failed to build generic API server: %w", err)
	}

	// Set up the in-memory storage backend.
	APIGroupInfo := genericapiserver.NewDefaultAPIGroupInfo(
		clusterpropertiesv1beta1.GroupVersion.Group,
		clusterpropertiesv1beta1.Scheme,
		metav1.ParameterCodec,
		clusterpropertiesv1beta1.CodecFactory,
	)
	extAPIServerRes := map[string]rest.Storage{
		"memberclusterproperties": &storage.PropertiesStore{},
	}
	APIGroupInfo.VersionedResourcesStorageMap[clusterpropertiesv1beta1.GroupVersion.Version] = extAPIServerRes

	if err := genericAPIServer.InstallAPIGroup(&APIGroupInfo); err != nil {
		return nil, fmt.Errorf("failed to install API group into generic API server: %w", err)
	}

	server := &Server{
		GenericAPIServer: genericAPIServer,
	}
	return server, nil
}

func (s *Server) RunUntil(stopCh <-chan struct{}) error {
	return s.GenericAPIServer.PrepareRun().RunWithContext(wait.ContextForChannel(stopCh))
}
