package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/client-go/rest"

	"github.com/kubefleet-dev/kubefleet/cmd/memberagent/options"
)

func Test_buildHubConfig(t *testing.T) {
	t.Run("use CA auth, no key file - error", func(t *testing.T) {
		t.Setenv("IDENTITY_KEY", "")
		t.Setenv("IDENTITY_CERT", "/path/to/cert")
		config, err := buildHubConfig("https://hub.domain.com", options.HubConnectivityOptions{UseCertificateAuth: true, UseInsecureTLSClient: false})
		assert.Nil(t, config)
		assert.NotNil(t, err)
	})
	t.Run("use CA auth, no cert file - error", func(t *testing.T) {
		t.Setenv("IDENTITY_KEY", "/path/to/key")
		t.Setenv("IDENTITY_CERT", "")
		config, err := buildHubConfig("https://hub.domain.com", options.HubConnectivityOptions{UseCertificateAuth: true, UseInsecureTLSClient: false})
		assert.Nil(t, config)
		assert.NotNil(t, err)
	})
	t.Run("use CA auth  - success", func(t *testing.T) {
		t.Setenv("IDENTITY_KEY", "/path/to/key")
		t.Setenv("IDENTITY_CERT", "/path/to/cert")
		config, err := buildHubConfig("https://hub.domain.com", options.HubConnectivityOptions{UseCertificateAuth: true, UseInsecureTLSClient: false})
		assert.NotNil(t, config)
		assert.Nil(t, err)
		assert.Equal(t, rest.Config{
			Host: "https://hub.domain.com",
			TLSClientConfig: rest.TLSClientConfig{
				KeyFile:  "/path/to/key",
				CertFile: "/path/to/cert",
			},
		}, *config)
	})
	t.Run("empty CA bundle - error", func(t *testing.T) {
		t.Setenv("IDENTITY_KEY", "/path/to/key")
		t.Setenv("IDENTITY_CERT", "/path/to/cert")
		t.Setenv("CA_BUNDLE", "")
		config, err := buildHubConfig("https://hub.domain.com", options.HubConnectivityOptions{UseCertificateAuth: true, UseInsecureTLSClient: false})
		assert.Nil(t, config)
		assert.NotNil(t, err)
	})
	t.Run("use CA bundle - success", func(t *testing.T) {
		t.Setenv("IDENTITY_KEY", "/path/to/key")
		t.Setenv("IDENTITY_CERT", "/path/to/cert")
		t.Setenv("CA_BUNDLE", "/path/to/ca/bundle")
		config, err := buildHubConfig("https://hub.domain.com", options.HubConnectivityOptions{UseCertificateAuth: true, UseInsecureTLSClient: false})
		assert.NotNil(t, config)
		assert.Nil(t, err)
		assert.Equal(t, rest.Config{
			Host: "https://hub.domain.com",
			TLSClientConfig: rest.TLSClientConfig{
				KeyFile:  "/path/to/key",
				CertFile: "/path/to/cert",
				CAFile:   "/path/to/ca/bundle",
			},
		}, *config)
	})
	t.Run("use CA data - success", func(t *testing.T) {
		t.Setenv("CONFIG_PATH", "./testdata/token")
		t.Setenv("HUB_CERTIFICATE_AUTHORITY", "dGhpcyBpcyBhIGZha2UgY2E=")
		config, err := buildHubConfig("https://hub.domain.com", options.HubConnectivityOptions{UseCertificateAuth: false, UseInsecureTLSClient: false})
		assert.NotNil(t, config)
		assert.Nil(t, err)
		assert.Equal(t, rest.Config{
			Host:            "https://hub.domain.com",
			BearerTokenFile: "./testdata/token",
			TLSClientConfig: rest.TLSClientConfig{
				CAData: []byte("this is a fake ca"),
			},
		}, *config)
	})
	t.Run("empty CA data - error", func(t *testing.T) {
		t.Setenv("CONFIG_PATH", "./testdata/token")
		t.Setenv("HUB_CERTIFICATE_AUTHORITY", "")
		config, err := buildHubConfig("https://hub.domain.com", options.HubConnectivityOptions{UseCertificateAuth: false, UseInsecureTLSClient: false})
		assert.Nil(t, config)
		assert.NotNil(t, err)
	})
	t.Run("both of CA bundle and CA data present - error", func(t *testing.T) {
		t.Setenv("CONFIG_PATH", "./testdata/token")
		t.Setenv("HUB_CERTIFICATE_AUTHORITY", "dGhpcyBpcyBhIGZha2UgY2E=")
		t.Setenv("CA_BUNDLE", "/path/to/ca/bundle")
		config, err := buildHubConfig("https://hub.domain.com", options.HubConnectivityOptions{UseCertificateAuth: false, UseInsecureTLSClient: false})
		assert.Nil(t, config)
		assert.NotNil(t, err)
	})
	t.Run("use token auth, no token path - error", func(t *testing.T) {
		t.Setenv("CONFIG_PATH", "")
		config, err := buildHubConfig("https://hub.domain.com", options.HubConnectivityOptions{UseCertificateAuth: false, UseInsecureTLSClient: false})
		assert.Nil(t, config)
		assert.NotNil(t, err)
	})
	t.Run("use token auth, not exists token path - error", func(t *testing.T) {
		t.Setenv("CONFIG_PATH", "/hot/exists/token/path")
		config, err := buildHubConfig("https://hub.domain.com", options.HubConnectivityOptions{UseCertificateAuth: false, UseInsecureTLSClient: false})
		assert.Nil(t, config)
		assert.NotNil(t, err)
	})
	t.Run("use token auth - success", func(t *testing.T) {
		t.Setenv("CONFIG_PATH", "./testdata/token")
		config, err := buildHubConfig("https://hub.domain.com", options.HubConnectivityOptions{UseCertificateAuth: false, UseInsecureTLSClient: false})
		assert.NotNil(t, config)
		assert.Nil(t, err)
		assert.Equal(t, rest.Config{
			Host:            "https://hub.domain.com",
			BearerTokenFile: "./testdata/token",
		}, *config)
	})
	t.Run("No CA bundle, no Hub CA, not insecure - success", func(t *testing.T) {
		t.Setenv("CONFIG_PATH", "./testdata/token")
		config, err := buildHubConfig("https://hub.domain.com", options.HubConnectivityOptions{UseCertificateAuth: false, UseInsecureTLSClient: false})
		assert.NotNil(t, config)
		assert.Nil(t, err)
		assert.Equal(t, rest.Config{
			Host:            "https://hub.domain.com",
			BearerTokenFile: "./testdata/token",
		}, *config)
	})
	t.Run("use insecure client - success", func(t *testing.T) {
		t.Setenv("CONFIG_PATH", "./testdata/token")
		config, err := buildHubConfig("https://hub.domain.com", options.HubConnectivityOptions{UseCertificateAuth: false, UseInsecureTLSClient: true})
		assert.NotNil(t, config)
		assert.Nil(t, err)
		assert.Equal(t, rest.Config{
			Host:            "https://hub.domain.com",
			BearerTokenFile: "./testdata/token",
			TLSClientConfig: rest.TLSClientConfig{
				Insecure: true,
			},
		}, *config)
	})
	t.Run("use insecure client and custom header - success", func(t *testing.T) {
		t.Setenv("CONFIG_PATH", "./testdata/token")
		t.Setenv("HUB_KUBE_HEADER", "Member-Resource-ID: some-id")
		config, err := buildHubConfig("https://hub.domain.com", options.HubConnectivityOptions{UseCertificateAuth: false, UseInsecureTLSClient: true})
		assert.NotNil(t, config)
		assert.Nil(t, err)
		assert.NotNil(t, config.WrapTransport)
	})
	t.Run("use hub kubeconfig - success", func(t *testing.T) {
		// hubURL is deliberately left empty here, mirroring main()'s behavior of skipping the
		// HUB_SERVER_URL read entirely when UseKubeConfig is set: the kubeconfig's own
		// cluster server URL is authoritative.
		t.Setenv("KUBE_CONFIG_PATH", "./testdata/kubeconfig")
		config, err := buildHubConfig("", options.HubConnectivityOptions{
			UseKubeConfig: true,
		})
		assert.NotNil(t, config)
		assert.Nil(t, err)
		assert.Equal(t, "https://hub.fixture.example.com", config.Host)
		assert.Equal(t, "fixture-bearer-token", config.BearerToken)
	})
	t.Run("use hub kubeconfig, no path - error", func(t *testing.T) {
		config, err := buildHubConfig("", options.HubConnectivityOptions{
			UseKubeConfig: true,
		})
		assert.Nil(t, config)
		assert.NotNil(t, err)
	})
	t.Run("use hub kubeconfig, not exists - error", func(t *testing.T) {
		t.Setenv("KUBE_CONFIG_PATH", "./testdata/does-not-exist")
		config, err := buildHubConfig("", options.HubConnectivityOptions{
			UseKubeConfig: true,
		})
		assert.Nil(t, config)
		assert.NotNil(t, err)
	})
	t.Run("use hub kubeconfig with custom header - success", func(t *testing.T) {
		t.Setenv("HUB_KUBE_HEADER", "Member-Resource-ID: some-id")
		t.Setenv("KUBE_CONFIG_PATH", "./testdata/kubeconfig")
		config, err := buildHubConfig("", options.HubConnectivityOptions{
			UseKubeConfig: true,
		})
		assert.NotNil(t, config)
		assert.Nil(t, err)
		assert.NotNil(t, config.WrapTransport)
	})
}
