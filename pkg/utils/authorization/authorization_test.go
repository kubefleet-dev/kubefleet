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

package authorization

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type fakeSubjectAccessReviewClient struct {
	create func(context.Context, *authorizationv1.SubjectAccessReview) (*authorizationv1.SubjectAccessReview, error)
}

func (f *fakeSubjectAccessReviewClient) Create(ctx context.Context, review *authorizationv1.SubjectAccessReview, _ metav1.CreateOptions) (*authorizationv1.SubjectAccessReview, error) {
	return f.create(ctx, review)
}

func TestResourceAttributesFor(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{gvk.GroupVersion()})
	mapper.AddSpecific(
		gvk,
		schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"},
		schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployment"},
		meta.RESTScopeNamespace,
	)
	reviewer, err := NewReviewer(&fakeSubjectAccessReviewClient{}, mapper, DefaultMaxConcurrentReviews)
	if err != nil {
		t.Fatalf("NewReviewer() returned error %v, want nil", err)
	}

	got, err := reviewer.ResourceAttributesFor(gvk, "get", "workloads", "frontend")
	if err != nil {
		t.Fatalf("ResourceAttributesFor() returned error %v, want nil", err)
	}
	want := authorizationv1.ResourceAttributes{
		Namespace: "workloads",
		Verb:      "get",
		Group:     "apps",
		Version:   "v1",
		Resource:  "deployments",
		Name:      "frontend",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ResourceAttributesFor() mismatch (-want +got):\n%s", diff)
	}
}

func TestResourceAttributesForUnknownKind(t *testing.T) {
	mapper := meta.NewDefaultRESTMapper(nil)
	reviewer, err := NewReviewer(&fakeSubjectAccessReviewClient{}, mapper, DefaultMaxConcurrentReviews)
	if err != nil {
		t.Fatalf("NewReviewer() returned error %v, want nil", err)
	}

	_, err = reviewer.ResourceAttributesFor(
		schema.GroupVersionKind{Group: "unknown.example.io", Version: "v1", Kind: "Unknown"},
		"get",
		"",
		"example",
	)
	if err == nil {
		t.Fatal("ResourceAttributesFor() returned nil error, want mapping error")
	}
}

func TestAuthorizePreservesUserInfo(t *testing.T) {
	attributes := authorizationv1.ResourceAttributes{
		Namespace: "workloads",
		Verb:      "get",
		Group:     "",
		Version:   "v1",
		Resource:  "secrets",
		Name:      "database",
	}
	userInfo := authenticationv1.UserInfo{
		Username: "user@example.com",
		UID:      "requester-uid",
		Groups:   []string{"system:authenticated", "developers"},
		Extra: map[string]authenticationv1.ExtraValue{
			"oid":        {"object-id"},
			"tenant-id":  {"tenant"},
			"multiValue": {"first", "second"},
		},
	}
	var gotSpec authorizationv1.SubjectAccessReviewSpec
	client := &fakeSubjectAccessReviewClient{
		create: func(_ context.Context, review *authorizationv1.SubjectAccessReview) (*authorizationv1.SubjectAccessReview, error) {
			gotSpec = *review.Spec.DeepCopy()
			return &authorizationv1.SubjectAccessReview{
				Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true},
			}, nil
		},
	}
	reviewer := newTestReviewer(t, client, DefaultMaxConcurrentReviews)

	if err := reviewer.Authorize(context.Background(), userInfo, attributes); err != nil {
		t.Fatalf("Authorize() returned error %v, want nil", err)
	}

	wantSpec := authorizationv1.SubjectAccessReviewSpec{
		User:   userInfo.Username,
		UID:    userInfo.UID,
		Groups: userInfo.Groups,
		Extra: map[string]authorizationv1.ExtraValue{
			"oid":        {"object-id"},
			"tenant-id":  {"tenant"},
			"multiValue": {"first", "second"},
		},
		ResourceAttributes: attributes.DeepCopy(),
	}
	if diff := cmp.Diff(wantSpec, gotSpec); diff != "" {
		t.Errorf("Authorize() SubjectAccessReview mismatch (-want +got):\n%s", diff)
	}
}

func TestAuthorizeFailsClosed(t *testing.T) {
	apiErr := errors.New("API server unavailable")
	testCases := map[string]struct {
		create  func(context.Context, *authorizationv1.SubjectAccessReview) (*authorizationv1.SubjectAccessReview, error)
		wantErr string
	}{
		"denied": {
			create: func(context.Context, *authorizationv1.SubjectAccessReview) (*authorizationv1.SubjectAccessReview, error) {
				return &authorizationv1.SubjectAccessReview{
					Status: authorizationv1.SubjectAccessReviewStatus{Denied: true, Reason: "RBAC denied the request"},
				}, nil
			},
			wantErr: "denied access",
		},
		"no opinion": {
			create: func(context.Context, *authorizationv1.SubjectAccessReview) (*authorizationv1.SubjectAccessReview, error) {
				return &authorizationv1.SubjectAccessReview{}, nil
			},
			wantErr: "no opinion",
		},
		"evaluation error": {
			create: func(context.Context, *authorizationv1.SubjectAccessReview) (*authorizationv1.SubjectAccessReview, error) {
				return &authorizationv1.SubjectAccessReview{
					Status: authorizationv1.SubjectAccessReviewStatus{
						Allowed:         true,
						EvaluationError: "authorizer failed",
					},
				}, nil
			},
			wantErr: "evaluation error",
		},
		"conflicting decision": {
			create: func(context.Context, *authorizationv1.SubjectAccessReview) (*authorizationv1.SubjectAccessReview, error) {
				return &authorizationv1.SubjectAccessReview{
					Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true, Denied: true},
				}, nil
			},
			wantErr: "conflicting",
		},
		"API error": {
			create: func(context.Context, *authorizationv1.SubjectAccessReview) (*authorizationv1.SubjectAccessReview, error) {
				return nil, apiErr
			},
			wantErr: apiErr.Error(),
		},
		"empty response": {
			create: func(context.Context, *authorizationv1.SubjectAccessReview) (*authorizationv1.SubjectAccessReview, error) {
				return nil, nil
			},
			wantErr: "returned no result",
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			reviewer := newTestReviewer(t, &fakeSubjectAccessReviewClient{create: tc.create}, DefaultMaxConcurrentReviews)
			err := reviewer.Authorize(context.Background(), authenticationv1.UserInfo{Username: "requester"}, authorizationv1.ResourceAttributes{
				Verb:     "get",
				Resource: "namespaces",
				Name:     "workloads",
			})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Authorize() error = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestAuthorizeHonorsContextTimeout(t *testing.T) {
	client := &fakeSubjectAccessReviewClient{
		create: func(ctx context.Context, _ *authorizationv1.SubjectAccessReview) (*authorizationv1.SubjectAccessReview, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	reviewer := newTestReviewer(t, client, DefaultMaxConcurrentReviews)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := reviewer.Authorize(ctx, authenticationv1.UserInfo{Username: "requester"}, authorizationv1.ResourceAttributes{Verb: "get", Resource: "namespaces", Name: "workloads"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Authorize() error = %v, want %v", err, context.DeadlineExceeded)
	}
}

func TestAuthorizeReturnsReviewErrorWhenConcurrencyIsLimited(t *testing.T) {
	client := &fakeSubjectAccessReviewClient{
		create: func(context.Context, *authorizationv1.SubjectAccessReview) (*authorizationv1.SubjectAccessReview, error) {
			return &authorizationv1.SubjectAccessReview{
				Status: authorizationv1.SubjectAccessReviewStatus{Denied: true, Reason: "RBAC denied the request"},
			}, nil
		},
	}
	reviewer := newTestReviewer(t, client, 1)

	err := reviewer.Authorize(
		context.Background(),
		authenticationv1.UserInfo{Username: "requester"},
		authorizationv1.ResourceAttributes{Verb: "get", Resource: "configmaps", Name: "first"},
		authorizationv1.ResourceAttributes{Verb: "get", Resource: "configmaps", Name: "second"},
	)
	if err == nil || !strings.Contains(err.Error(), "RBAC denied the request") {
		t.Errorf("Authorize() error = %v, want original authorization denial", err)
	}
}

func TestAuthorizeDeduplicatesReviews(t *testing.T) {
	var calls atomic.Int32
	client := &fakeSubjectAccessReviewClient{
		create: func(context.Context, *authorizationv1.SubjectAccessReview) (*authorizationv1.SubjectAccessReview, error) {
			calls.Add(1)
			return &authorizationv1.SubjectAccessReview{
				Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true},
			}, nil
		},
	}
	reviewer := newTestReviewer(t, client, DefaultMaxConcurrentReviews)
	attributes := authorizationv1.ResourceAttributes{Verb: "get", Resource: "namespaces", Name: "workloads"}

	if err := reviewer.Authorize(context.Background(), authenticationv1.UserInfo{Username: "requester"}, attributes, attributes); err != nil {
		t.Fatalf("Authorize() returned error %v, want nil", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("SubjectAccessReview calls = %d, want 1", got)
	}
}

func TestAuthorizeDoesNotCacheAcrossCalls(t *testing.T) {
	var calls atomic.Int32
	client := &fakeSubjectAccessReviewClient{
		create: func(context.Context, *authorizationv1.SubjectAccessReview) (*authorizationv1.SubjectAccessReview, error) {
			calls.Add(1)
			return &authorizationv1.SubjectAccessReview{
				Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true},
			}, nil
		},
	}
	reviewer := newTestReviewer(t, client, DefaultMaxConcurrentReviews)
	userInfo := authenticationv1.UserInfo{Username: "requester"}
	attributes := authorizationv1.ResourceAttributes{Verb: "get", Resource: "namespaces", Name: "workloads"}

	if err := reviewer.Authorize(context.Background(), userInfo, attributes); err != nil {
		t.Fatalf("Authorize() returned error %v, want nil", err)
	}
	if err := reviewer.Authorize(context.Background(), userInfo, attributes); err != nil {
		t.Fatalf("Authorize() returned error %v, want nil", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("SubjectAccessReview calls = %d, want 2", got)
	}
}

func TestAuthorizeLimitsConcurrency(t *testing.T) {
	const (
		reviewCount         = 5
		maxConcurrentReview = 2
	)
	started := make(chan struct{}, reviewCount)
	release := make(chan struct{})
	var closeRelease sync.Once
	defer closeRelease.Do(func() { close(release) })

	client := &fakeSubjectAccessReviewClient{
		create: func(context.Context, *authorizationv1.SubjectAccessReview) (*authorizationv1.SubjectAccessReview, error) {
			started <- struct{}{}
			<-release
			return &authorizationv1.SubjectAccessReview{
				Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true},
			}, nil
		},
	}
	reviewer := newTestReviewer(t, client, maxConcurrentReview)
	attributes := make([]authorizationv1.ResourceAttributes, reviewCount)
	for i := range attributes {
		attributes[i] = authorizationv1.ResourceAttributes{
			Verb:     "get",
			Resource: "configmaps",
			Name:     string(rune('a' + i)),
		}
	}

	result := make(chan error, 1)
	go func() {
		result <- reviewer.Authorize(context.Background(), authenticationv1.UserInfo{Username: "requester"}, attributes...)
	}()

	for i := 0; i < maxConcurrentReview; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatalf("Authorize() started %d reviews, want %d", i, maxConcurrentReview)
		}
	}
	select {
	case <-started:
		t.Fatalf("Authorize() exceeded concurrency limit %d", maxConcurrentReview)
	case <-time.After(50 * time.Millisecond):
	}

	closeRelease.Do(func() { close(release) })
	select {
	case err := <-result:
		if err != nil {
			t.Errorf("Authorize() returned error %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Authorize() did not finish after reviews were released")
	}
}

func TestDescribeAttributes(t *testing.T) {
	testCases := map[string]struct {
		attributes authorizationv1.ResourceAttributes
		want       string
	}{
		"core cluster-scoped named resource": {
			attributes: authorizationv1.ResourceAttributes{
				Verb:     "get",
				Resource: "namespaces",
				Name:     "workloads",
			},
			want: `get namespaces "workloads"`,
		},
		"grouped namespaced named resource": {
			attributes: authorizationv1.ResourceAttributes{
				Namespace: "workloads",
				Verb:      "get",
				Group:     "apps",
				Resource:  "deployments",
				Name:      "frontend",
			},
			want: `get deployments.apps "frontend" in namespace "workloads"`,
		},
		"subresource": {
			attributes: authorizationv1.ResourceAttributes{
				Namespace:   "workloads",
				Verb:        "update",
				Group:       "apps",
				Resource:    "deployments",
				Subresource: "status",
				Name:        "frontend",
			},
			want: `update deployments.apps/status "frontend" in namespace "workloads"`,
		},
		"namespaced list": {
			attributes: authorizationv1.ResourceAttributes{
				Namespace: "workloads",
				Verb:      "list",
				Resource:  "secrets",
			},
			want: `list secrets in namespace "workloads"`,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			got := describeAttributes(tc.attributes)
			if got != tc.want {
				t.Errorf("describeAttributes(%v) = %q, want %q", tc.attributes, got, tc.want)
			}
		})
	}
}

func newTestReviewer(t *testing.T, client SubjectAccessReviewClient, maxConcurrentReviews int) *Reviewer {
	t.Helper()
	reviewer, err := NewReviewer(client, meta.NewDefaultRESTMapper(nil), maxConcurrentReviews)
	if err != nil {
		t.Fatalf("NewReviewer() returned error %v, want nil", err)
	}
	return reviewer
}
