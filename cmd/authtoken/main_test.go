package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kubefleet-dev/kubefleet/pkg/authtoken/providers/azure"
)

func TestParseArgs(t *testing.T) {
	t.Run("all arguments", func(t *testing.T) {
		os.Args = []string{"refreshtoken", "azure", "--scope=test-scope"}
		t.Cleanup(func() {
			os.Args = nil
		})
		tokenProvider, err := parseArgs()
		assert.NotNil(t, tokenProvider)
		assert.Nil(t, err)

		azTokenProvider, ok := tokenProvider.(*azure.AuthTokenProvider)
		assert.Equal(t, true, ok)
		assert.Equal(t, "test-scope", azTokenProvider.Scope)
	})
	t.Run("no optional arguments", func(t *testing.T) {
		os.Args = []string{"refreshtoken", "azure"}
		t.Cleanup(func() {
			os.Args = nil
		})
		tokenProvider, err := parseArgs()
		assert.NotNil(t, tokenProvider)
		assert.Nil(t, err)

		azTokenProvider, ok := tokenProvider.(*azure.AuthTokenProvider)
		assert.Equal(t, true, ok)
		assert.Equal(t, "6dae42f8-4368-4678-94ff-3960e28e3630/.default", azTokenProvider.Scope)
	})
	t.Run("deprecated clientid flag is accepted as a no-op", func(t *testing.T) {
		os.Args = []string{"refreshtoken", "azure", "--scope=test-scope", "--clientid=some-client-id"}
		t.Cleanup(func() {
			os.Args = nil
		})
		tokenProvider, err := parseArgs()
		if err != nil {
			t.Fatalf("parseArgs() error = %v, want nil", err)
		}

		azTokenProvider, ok := tokenProvider.(*azure.AuthTokenProvider)
		if !ok {
			t.Fatalf("parseArgs() provider type = %T, want *azure.AuthTokenProvider", tokenProvider)
		}
		if azTokenProvider.Scope != "test-scope" {
			t.Errorf("parseArgs() Scope = %v, want %v", azTokenProvider.Scope, "test-scope")
		}
	})
}
