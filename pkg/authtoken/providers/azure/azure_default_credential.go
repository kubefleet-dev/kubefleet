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
package azure

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"

	"github.com/kubefleet-dev/kubefleet/pkg/authtoken"
)

// The trailing "/.default" is required by the EnvironmentCredential and
// WorkloadIdentityCredential legs of the chain (both go through an OAuth2
// client-credentials/MSAL confidential-client flow, which rejects a bare resource ID as the
// scope: AADSTS1002012 "Client credential flows must have a scope value with /.default suffixed
// to the resource identifier"). ManagedIdentityCredential's IMDS calls need a v1 resource ID
// instead, but azidentity strips the "/.default" suffix internally before making that call, so
// this value works correctly across every credential in the DefaultAzureCredential chain.
const aksScope = "6dae42f8-4368-4678-94ff-3960e28e3630/.default"

// azureTokenCredentialsEnvVar is read by azidentity.NewDefaultAzureCredential itself to decide
// which half of its credential chain to build: "prod" includes only
// EnvironmentCredential/WorkloadIdentityCredential/ManagedIdentityCredential, "dev" includes
// only AzureCLICredential/AzureDeveloperCLICredential. See azureTokenCredentialsProd below.
const azureTokenCredentialsEnvVar = "AZURE_TOKEN_CREDENTIALS"

// azureTokenCredentialsProd is the value FetchToken unconditionally forces AZURE_TOKEN_CREDENTIALS
// to, every call, overriding anything already in the environment. The refresh-token image ships
// no az/azd binary and no cached CLI session, so AzureCLICredential/AzureDeveloperCLICredential
// can never succeed there anyway; excluding them avoids two guaranteed-to-fail attempts (and
// their noise in the aggregated error) on every token fetch. This is deliberately not
// configurable -- the CLI-based legs are not a supported auth mode for this binary.
const azureTokenCredentialsProd = "prod"

// AuthTokenProvider fetches Azure AD access tokens for the fleet hub using
// azidentity.DefaultAzureCredential, which tries the following credential types, in order,
// stopping at the first one that successfully produces a token:
//
//  1. EnvironmentCredential       - service principal via AZURE_CLIENT_ID + AZURE_CLIENT_SECRET,
//     or AZURE_CLIENT_CERTIFICATE_PATH (typically supplied via a mounted Secret's env vars)
//  2. WorkloadIdentityCredential  - federated credential via AZURE_FEDERATED_TOKEN_FILE +
//     AZURE_CLIENT_ID/AZURE_TENANT_ID, as injected by the Azure Workload Identity mutating webhook
//  3. ManagedIdentityCredential   - IMDS; a system-assigned managed identity, or a user-assigned
//     one if AZURE_CLIENT_ID happens to already be set in the environment (e.g. by one of the
//     mechanisms above)
//
// AzureCLICredential and AzureDeveloperCLICredential are always excluded (see
// azureTokenCredentialsProd) since the shipped image has no az/azd binary to shell out to; this
// is not configurable via the environment.
//
// This lets the same binary/image authenticate correctly no matter which of these mechanisms
// the member cluster is actually set up with, without the operator having to pick one Azure
// auth mode up front or KubeFleet needing a dedicated provider per mechanism. There is
// deliberately no client-ID option here: every legitimate way of steering
// DefaultAzureCredential to a specific identity (the workload-identity webhook, an
// EnvironmentCredential Secret) already sets AZURE_CLIENT_ID itself; a flag doing the same
// thing independently would only risk silently clobbering whichever of those actually applies.
type AuthTokenProvider struct {
	Scope string
}

func New(scope string) authtoken.Provider {
	if scope == "" {
		scope = aksScope
	}
	return &AuthTokenProvider{
		Scope: scope,
	}
}

// FetchToken gets a new token to make requests to the associated fleet's hub cluster.
func (a *AuthTokenProvider) FetchToken(ctx context.Context) (authtoken.AuthToken, error) {
	token := authtoken.AuthToken{}

	if err := os.Setenv(azureTokenCredentialsEnvVar, azureTokenCredentialsProd); err != nil {
		return token, fmt.Errorf("failed to set %s: %w", azureTokenCredentialsEnvVar, err)
	}

	httpClient := &http.Client{}
	credential, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{
		ClientOptions: azcore.ClientOptions{
			// Only applies to the credential types that make HTTP calls directly
			// (Environment/WorkloadIdentity/ManagedIdentity); the CLI-based credentials shell
			// out to the az/azd binaries instead and ignore this.
			Transport: httpClient,
		},
	})
	if err != nil {
		return token, fmt.Errorf("failed to create default azure credential: %w", err)
	}

	var azToken azcore.AccessToken
	err = retry.OnError(retry.DefaultBackoff,
		func(err error) bool {
			return ctx.Err() == nil
		}, func() error {
			klog.V(2).InfoS("GetToken start")
			azToken, err = credential.GetToken(ctx, policy.TokenRequestOptions{
				Scopes: []string{a.Scope},
			})
			if err != nil {
				klog.ErrorS(err, "Failed to GetToken", "scope", a.Scope)
				// If the chain falls through to ManagedIdentityCredential, we may race at
				// startup with a sidecar that's still inserting the iptables rule which
				// intercepts IMDS calls. If we get here before that rule exists, we
				// inadvertently talk to the real IMDS, which won't service the request.
				// IMDS doesn't set 'Connection: close' on 4xx errors, and the default Go
				// HTTP client keeps the underlying connection open for reuse regardless of
				// the later iptables change, so all further requests would keep going to the
				// real IMDS and keep failing. Close the connection explicitly so the next
				// attempt re-dials and picks up the (by-then-inserted) redirect. This is a
				// harmless no-op if the failure instead came from a different credential in
				// the chain.
				httpClient.CloseIdleConnections()
			}
			return err
		})
	if err != nil {
		return token, fmt.Errorf("failed to get a token: %w", err)
	}

	token.Token = azToken.Token
	token.ExpiresOn = azToken.ExpiresOn

	return token, nil
}
