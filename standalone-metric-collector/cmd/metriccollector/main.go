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

package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	placementv1beta1 "github.com/kubefleet-dev/standalone-metric-collector/apis/v1beta1"
	metriccollector "github.com/kubefleet-dev/standalone-metric-collector/pkg/controller"
)

var (
	memberQPS         = flag.Int("member-qps", 100, "QPS for member cluster client")
	memberBurst       = flag.Int("member-burst", 200, "Burst for member cluster client")
	hubQPS            = flag.Int("hub-qps", 100, "QPS for hub cluster client")
	hubBurst          = flag.Int("hub-burst", 200, "Burst for hub cluster client")
	metricsAddr       = flag.String("metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	probeAddr         = flag.String("health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	leaderElectionID  = flag.String("leader-election-id", "metric-collector-leader", "The leader election ID.")
	enableLeaderElect = flag.Bool("leader-elect", true, "Enable leader election for controller manager.")
)

func main() {
	klog.InitFlags(nil)
	flag.Parse()

	klog.InfoS("Starting MetricCollector Controller")

	// Get member cluster config (in-cluster)
	memberConfig := ctrl.GetConfigOrDie()
	memberConfig.QPS = float32(*memberQPS)
	memberConfig.Burst = *memberBurst

	// Build hub cluster config
	hubConfig, err := buildHubConfig()
	if err != nil {
		klog.ErrorS(err, "Failed to build hub cluster config")
		os.Exit(1)
	}
	hubConfig.QPS = float32(*hubQPS)
	hubConfig.Burst = *hubBurst

	// Start controller with both clients
	if err := Start(ctrl.SetupSignalHandler(), hubConfig, memberConfig); err != nil {
		klog.ErrorS(err, "Failed to start controller")
		os.Exit(1)
	}
}

// buildHubConfig creates hub cluster config from environment variables
// following the same pattern as member-agent
func buildHubConfig() (*rest.Config, error) {
	hubURL := os.Getenv("HUB_SERVER_URL")
	if hubURL == "" {
		return nil, fmt.Errorf("HUB_SERVER_URL environment variable not set")
	}

	// Check for custom headers
	customHeader := os.Getenv("HUB_KUBE_HEADER")

	// Check TLS insecure flag
	tlsInsecure := os.Getenv("TLS_INSECURE") == "true"

	// Initialize hub config
	hubConfig := &rest.Config{
		Host: hubURL,
		TLSClientConfig: rest.TLSClientConfig{
			Insecure: tlsInsecure,
		},
		WrapTransport: func(rt http.RoundTripper) http.RoundTripper {
			if customHeader != "" {
				return &customHeaderTransport{
					Base:   rt,
					Header: customHeader,
				}
			}
			return rt
		},
	}

	// Check for certificate-based authentication
	identityKey := os.Getenv("IDENTITY_KEY")
	identityCert := os.Getenv("IDENTITY_CERT")
	if identityKey != "" && identityCert != "" {
		klog.InfoS("Using certificate-based authentication for hub cluster")
		// Read certificate files
		certData, err := os.ReadFile(identityCert)
		if err != nil {
			return nil, fmt.Errorf("failed to read identity cert: %w", err)
		}
		keyData, err := os.ReadFile(identityKey)
		if err != nil {
			return nil, fmt.Errorf("failed to read identity key: %w", err)
		}
		hubConfig.CertData = certData
		hubConfig.KeyData = keyData
	} else {
		// Token-based authentication
		klog.InfoS("Using token-based authentication for hub cluster")
		configPath := os.Getenv("CONFIG_PATH")
		if configPath == "" {
			configPath = "/var/run/secrets/hub/token"
		}
		tokenData, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read hub token from %s: %w", configPath, err)
		}
		hubConfig.BearerToken = string(tokenData)
	}

	// Handle CA certificate
	caBundle := os.Getenv("CA_BUNDLE")
	hubCA := os.Getenv("HUB_CERTIFICATE_AUTHORITY")
	if caBundle != "" {
		klog.InfoS("Using CA bundle for hub cluster TLS")
		caData, err := os.ReadFile(caBundle)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA bundle: %w", err)
		}
		hubConfig.CAData = caData
	} else if hubCA != "" {
		klog.InfoS("Using hub certificate authority for hub cluster TLS")
		caData, err := os.ReadFile(hubCA)
		if err != nil {
			return nil, fmt.Errorf("failed to read hub CA: %w", err)
		}
		hubConfig.CAData = caData
	} else {
		// If no CA specified, try to load system CA pool
		klog.InfoS("No CA specified, using insecure connection or system CA pool")
	}

	return hubConfig, nil
}

// customHeaderTransport adds custom headers to requests
type customHeaderTransport struct {
	Base   http.RoundTripper
	Header string
}

func (t *customHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Add("X-Custom-Header", t.Header)
	return t.Base.RoundTrip(req)
}

// Start starts the controller with dual managers for hub and member clusters
func Start(ctx context.Context, hubCfg, memberCfg *rest.Config) error {
	// Create scheme with required APIs
	scheme := runtime.NewScheme()
	if err := placementv1beta1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("failed to add placement API to scheme: %w", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("failed to add core API to scheme: %w", err)
	}

	// Create member cluster manager (where controller runs and watches MetricCollector)
	memberMgr, err := ctrl.NewManager(memberCfg, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: *metricsAddr,
		},
		HealthProbeBindAddress: *probeAddr,
		LeaderElection:         *enableLeaderElect,
		LeaderElectionID:       *leaderElectionID,
	})
	if err != nil {
		return fmt.Errorf("failed to create member manager: %w", err)
	}

	// Create hub cluster client (for writing MetricCollectorReports)
	hubClient, err := client.New(hubCfg, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("failed to create hub client: %w", err)
	}

	// Get Prometheus URL from environment
	prometheusURL := os.Getenv("PROMETHEUS_URL")
	if prometheusURL == "" {
		prometheusURL = "http://prometheus.fleet-system.svc.cluster.local:9090"
		klog.InfoS("PROMETHEUS_URL not set, using default", "url", prometheusURL)
	}

	// Create Prometheus client
	prometheusClient := metriccollector.NewPrometheusClient(prometheusURL, "", nil)

	// Setup MetricCollector controller
	if err := (&metriccollector.Reconciler{
		MemberClient:     memberMgr.GetClient(),
		HubClient:        hubClient,
		PrometheusClient: prometheusClient,
	}).SetupWithManager(memberMgr); err != nil {
		return fmt.Errorf("failed to setup MetricCollector controller: %w", err)
	}

	// Add health checks
	if err := memberMgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return fmt.Errorf("failed to add healthz check: %w", err)
	}
	if err := memberMgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return fmt.Errorf("failed to add readyz check: %w", err)
	}

	klog.InfoS("Starting MetricCollector controller",
		"hubUrl", hubCfg.Host,
		"prometheusUrl", prometheusURL,
		"metricsAddr", *metricsAddr,
		"probeAddr", *probeAddr)

	// Start the manager
	if err := memberMgr.Start(ctx); err != nil {
		return fmt.Errorf("failed to start manager: %w", err)
	}

	return nil
}
