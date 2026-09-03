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
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/google/go-cmp/cmp"

	"github.com/kubefleet-dev/kubefleet/pkg/authtoken"
)

// fakeCredential is a minimal azcore.TokenCredential double. GetToken returns tokens[i]/errs[i]
// for the i-th call, clamped to the last entry once tokens is exhausted (errs may be shorter
// than tokens; a call index with no corresponding errs entry returns a nil error).
type fakeCredential struct {
	tokens []azcore.AccessToken
	errs   []error

	calls  int
	scopes [][]string
}

func (f *fakeCredential) GetToken(_ context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	i := f.calls
	if i >= len(f.tokens) {
		i = len(f.tokens) - 1
	}
	f.scopes = append(f.scopes, options.Scopes)
	f.calls++

	var err error
	if i < len(f.errs) {
		err = f.errs[i]
	}
	return f.tokens[i], err
}

// withFakeCredential swaps newAzureCredential for the duration of the test, restoring it on
// cleanup. Pass a non-nil constructErr to simulate a failure to even construct the credential.
func withFakeCredential(t *testing.T, cred *fakeCredential, constructErr error) {
	t.Helper()
	original := newAzureCredential
	newAzureCredential = func(*azidentity.DefaultAzureCredentialOptions) (azcore.TokenCredential, error) {
		if constructErr != nil {
			return nil, constructErr
		}
		return cred, nil
	}
	t.Cleanup(func() { newAzureCredential = original })
}

func TestNew(t *testing.T) {
	tests := []struct {
		name      string
		scope     string
		wantScope string
	}{
		{
			name:      "empty scope defaults to the AKS scope",
			scope:     "",
			wantScope: aksScope,
		},
		{
			name:      "custom scope is preserved as-is",
			scope:     "api://my-app/.default",
			wantScope: "api://my-app/.default",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provider := New(tc.scope)
			got, ok := provider.(*AuthTokenProvider)
			if !ok {
				t.Fatalf("New(%q) returned %T, want *AuthTokenProvider", tc.scope, provider)
			}
			if got.Scope != tc.wantScope {
				t.Errorf("New(%q).Scope = %q, want %q", tc.scope, got.Scope, tc.wantScope)
			}
		})
	}
}

func TestFetchToken_Success(t *testing.T) {
	expiresOn := time.Now().Add(time.Hour).Truncate(0)
	cred := &fakeCredential{
		tokens: []azcore.AccessToken{{Token: "the-token", ExpiresOn: expiresOn}},
	}
	withFakeCredential(t, cred, nil)

	provider := &AuthTokenProvider{Scope: "test-scope"}
	got, err := provider.FetchToken(context.Background())
	if err != nil {
		t.Fatalf("FetchToken() returned error %v, want nil", err)
	}

	want := authtoken.AuthToken{Token: "the-token", ExpiresOn: expiresOn}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("FetchToken() mismatch (-want +got):\n%s", diff)
	}
	if cred.calls != 1 {
		t.Errorf("GetToken called %d time(s), want 1", cred.calls)
	}
	wantScopes := [][]string{{"test-scope"}}
	if diff := cmp.Diff(wantScopes, cred.scopes); diff != "" {
		t.Errorf("GetToken scopes mismatch (-want +got):\n%s", diff)
	}
}

func TestFetchToken_ForcesAzureTokenCredentialsProd(t *testing.T) {
	tests := []struct {
		name    string
		initial string // "" means unset
	}{
		{name: "unset becomes prod", initial: ""},
		{name: "already prod stays prod", initial: "prod"},
		{name: "explicit dev is overridden to prod", initial: "dev"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.initial == "" {
				prev, hadPrev := os.LookupEnv(azureTokenCredentialsEnvVar)
				if err := os.Unsetenv(azureTokenCredentialsEnvVar); err != nil {
					t.Fatalf("os.Unsetenv(%s) returned error %v, want nil", azureTokenCredentialsEnvVar, err)
				}
				t.Cleanup(func() {
					if hadPrev {
						_ = os.Setenv(azureTokenCredentialsEnvVar, prev)
					} else {
						_ = os.Unsetenv(azureTokenCredentialsEnvVar)
					}
				})
			} else {
				t.Setenv(azureTokenCredentialsEnvVar, tc.initial)
			}

			cred := &fakeCredential{
				tokens: []azcore.AccessToken{{Token: "t", ExpiresOn: time.Now()}},
			}
			withFakeCredential(t, cred, nil)

			provider := &AuthTokenProvider{Scope: "test-scope"}
			if _, err := provider.FetchToken(context.Background()); err != nil {
				t.Fatalf("FetchToken() returned error %v, want nil", err)
			}

			if got := os.Getenv(azureTokenCredentialsEnvVar); got != azureTokenCredentialsProd {
				t.Errorf("%s = %q after FetchToken(), want %q", azureTokenCredentialsEnvVar, got, azureTokenCredentialsProd)
			}
		})
	}
}

func TestFetchToken_CredentialConstructionError(t *testing.T) {
	wantErr := errors.New("boom")
	withFakeCredential(t, nil, wantErr)

	provider := &AuthTokenProvider{Scope: "test-scope"}
	_, err := provider.FetchToken(context.Background())
	if err == nil {
		t.Fatal("FetchToken() returned nil error, want non-nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("FetchToken() returned error %v, want it to wrap %v", err, wantErr)
	}
}

func TestFetchToken_RetriesTransientFailuresThenSucceeds(t *testing.T) {
	expiresOn := time.Now().Add(time.Hour).Truncate(0)
	cred := &fakeCredential{
		tokens: []azcore.AccessToken{{}, {}, {Token: "eventual-token", ExpiresOn: expiresOn}},
		errs:   []error{errors.New("transient 1"), errors.New("transient 2")},
	}
	withFakeCredential(t, cred, nil)

	provider := &AuthTokenProvider{Scope: "test-scope"}
	got, err := provider.FetchToken(context.Background())
	if err != nil {
		t.Fatalf("FetchToken() returned error %v, want nil after eventual success", err)
	}

	want := authtoken.AuthToken{Token: "eventual-token", ExpiresOn: expiresOn}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("FetchToken() mismatch (-want +got):\n%s", diff)
	}
	if cred.calls != 3 {
		t.Errorf("GetToken called %d time(s), want 3 (2 failures then a success)", cred.calls)
	}
}

func TestFetchToken_ExhaustsRetriesOnPermanentFailure(t *testing.T) {
	wantErr := errors.New("permanent failure")
	cred := &fakeCredential{
		tokens: []azcore.AccessToken{{}},
		errs:   []error{wantErr},
	}
	withFakeCredential(t, cred, nil)

	provider := &AuthTokenProvider{Scope: "test-scope"}
	_, err := provider.FetchToken(context.Background())
	if err == nil {
		t.Fatal("FetchToken() returned nil error, want non-nil after exhausting retries")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("FetchToken() returned error %v, want it to wrap %v", err, wantErr)
	}
	// retry.DefaultBackoff.Steps is 4: the initial attempt plus three retries.
	if cred.calls != 4 {
		t.Errorf("GetToken called %d time(s), want 4 (retry.DefaultBackoff.Steps)", cred.calls)
	}
}
