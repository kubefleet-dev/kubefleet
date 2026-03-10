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

package integration

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"

	clusterpropertiesv1beta1 "github.com/kubefleet-dev/kubefleet/apis/clusterproperties/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/extapiserver/clusterproperties/cmd/options"
)

var (
	defaultHostname     = "localhost"
	defaultAlternateIPs = []net.IP{net.ParseIP("127.0.0.1")}
)

var (
	ctx    context.Context
	cancel context.CancelFunc

	wg sync.WaitGroup

	scheme           = runtime.NewScheme()
	hubClient        client.Client
	hubDynamicClient dynamic.Interface
	hubHTTPClient    *http.Client
)

func TestMain(m *testing.M) {
	// Add built-in APIs to the scheme.
	if err := k8sscheme.AddToScheme(scheme); err != nil {
		log.Fatalf("failed to add built-in APIs to scheme: %v", err)
	}

	// Add the cluster properties API to the scheme.
	if err := clusterpropertiesv1beta1.AddToScheme(scheme); err != nil {
		log.Fatalf("failed to add cluster properties API to scheme: %v", err)
	}

	os.Exit(m.Run())
}

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "KubeFleet Cluster Properties Extension API Server Test Suite")
}

var _ = BeforeSuite(func() {
	ctx, cancel = context.WithCancel(context.Background())

	opts := options.NewWithDefaultValues()
	opts.DisableAuthnAuthz = true

	server, err := opts.BuildServer(defaultHostname, nil, defaultAlternateIPs)
	Expect(err).ToNot(HaveOccurred())

	wg = sync.WaitGroup{}
	wg.Add(1)

	go func() {
		defer GinkgoRecover()
		defer wg.Done()

		Expect(server.RunUntil(ctx.Done())).ToNot(HaveOccurred())
	}()

	hubClusterAPICfg := clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			"hub": {
				Server:                "https://localhost:443",
				InsecureSkipTLSVerify: true,
			},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"test-only": {},
		},
		Contexts: map[string]*clientcmdapi.Context{
			"hub": {
				Cluster:  "hub",
				AuthInfo: "test-only",
			},
		},
		CurrentContext: "hub",
	}
	hubClusterCfg := clientcmd.NewDefaultClientConfig(hubClusterAPICfg, nil)
	hubClusterRESTCfg, err := hubClusterCfg.ClientConfig()
	Expect(err).ToNot(HaveOccurred())

	hubClient, err = client.New(hubClusterRESTCfg, client.Options{Scheme: scheme})
	Expect(err).ToNot(HaveOccurred())

	hubDynamicClient, err = dynamic.NewForConfig(hubClusterRESTCfg)
	Expect(err).ToNot(HaveOccurred())

	hubHTTPClient, err = http.DefaultClient, nil
	Expect(err).ToNot(HaveOccurred())
})

var _ = AfterSuite(func() {
	cancel()
	wg.Wait()
})
