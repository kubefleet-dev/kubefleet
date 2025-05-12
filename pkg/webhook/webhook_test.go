package webhook

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/kubefleet-dev/kubefleet/cmd/hubagent/options"
	"github.com/kubefleet-dev/kubefleet/pkg/utils"
)

func TestBuildFleetValidatingWebhooks(t *testing.T) {
	url := options.WebhookClientConnectionType("url")
	testCases := map[string]struct {
		config     Config
		wantLength int
	}{
		"valid input": {
			config: Config{
				serviceNamespace:     "test-namespace",
				servicePort:          8080,
				serviceURL:           "test-url",
				clientConnectionType: &url,
			},
			wantLength: 7,
		},
	}

	for testName, testCase := range testCases {
		t.Run(testName, func(t *testing.T) {
			gotResult := testCase.config.buildFleetValidatingWebhooks()
			assert.Equal(t, testCase.wantLength, len(gotResult), utils.TestCaseMsg, testName)
		})
	}
}

func TestBuildFleetGuardRailValidatingWebhooks(t *testing.T) {
	url := options.WebhookClientConnectionType("url")
	testCases := map[string]struct {
		config     Config
		wantLength int
	}{
		"valid input": {
			config: Config{
				serviceNamespace:     "test-namespace",
				servicePort:          8080,
				serviceURL:           "test-url",
				clientConnectionType: &url,
			},
			wantLength: 7,
		},
	}

	for testName, testCase := range testCases {
		t.Run(testName, func(t *testing.T) {
			gotResult := testCase.config.buildFleetGuardRailValidatingWebhooks()
			assert.Equal(t, testCase.wantLength, len(gotResult), utils.TestCaseMsg, testName)
		})
	}
}

func TestNewWebhookConfig(t *testing.T) {
	tests := []struct {
		name                          string
		mgr                           manager.Manager
		webhookServiceName            string
		port                          int32
		clientConnectionType          *options.WebhookClientConnectionType
		certDir                       string
		enableGuardRail               bool
		denyModifyMemberClusterLabels bool
		want                          *Config
		wantErr                       bool
	}{
		{
			name:                          "valid input",
			mgr:                           nil,
			webhookServiceName:            "test-webhook",
			port:                          8080,
			clientConnectionType:          nil,
			certDir:                       "/tmp/cert",
			enableGuardRail:               true,
			denyModifyMemberClusterLabels: true,
			want: &Config{
				serviceNamespace:              "test-namespace",
				serviceName:                   "test-webhook",
				servicePort:                   8080,
				clientConnectionType:          nil,
				enableGuardRail:               true,
				denyModifyMemberClusterLabels: true,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Setenv("POD_NAMESPACE", "test-namespace")
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewWebhookConfig(tt.mgr, tt.webhookServiceName, tt.port, tt.clientConnectionType, tt.certDir, tt.enableGuardRail, tt.denyModifyMemberClusterLabels)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewWebhookConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got == nil || tt.want == nil {
				if got != tt.want {
					t.Errorf("NewWebhookConfig() = %v, want %v", got, tt.want)
				}
				return
			}
			if got.serviceNamespace != tt.want.serviceNamespace ||
				got.serviceName != tt.want.serviceName ||
				got.servicePort != tt.want.servicePort ||
				got.clientConnectionType != tt.want.clientConnectionType ||
				got.enableGuardRail != tt.want.enableGuardRail ||
				got.denyModifyMemberClusterLabels != tt.want.denyModifyMemberClusterLabels {
				t.Errorf("NewWebhookConfig() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
