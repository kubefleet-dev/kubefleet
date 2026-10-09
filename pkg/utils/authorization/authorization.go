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

// Package authorization provides authorization checks for resources selected by Fleet APIs.
package authorization

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"golang.org/x/sync/errgroup"
	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	// DefaultMaxConcurrentReviews limits the number of SubjectAccessReviews an admission request issues concurrently.
	DefaultMaxConcurrentReviews = 10
)

// SubjectAccessReviewClient creates SubjectAccessReviews.
type SubjectAccessReviewClient interface {
	Create(context.Context, *authorizationv1.SubjectAccessReview, metav1.CreateOptions) (*authorizationv1.SubjectAccessReview, error)
}

// Reviewer resolves resource kinds and reviews selected-resource access.
type Reviewer struct {
	client               SubjectAccessReviewClient
	restMapper           meta.RESTMapper
	maxConcurrentReviews int
}

// NewReviewer creates a selected-resource authorization reviewer.
func NewReviewer(client SubjectAccessReviewClient, restMapper meta.RESTMapper, maxConcurrentReviews int) (*Reviewer, error) {
	if client == nil {
		return nil, errors.New("subject access review client must not be nil")
	}
	if restMapper == nil {
		return nil, errors.New("REST mapper must not be nil")
	}
	if maxConcurrentReviews < 1 {
		return nil, errors.New("maximum concurrent reviews must be greater than zero")
	}
	return &Reviewer{
		client:               client,
		restMapper:           restMapper,
		maxConcurrentReviews: maxConcurrentReviews,
	}, nil
}

// ResourceAttributesFor resolves a resource kind to the plural resource used by Kubernetes authorization.
func (r *Reviewer) ResourceAttributesFor(gvk schema.GroupVersionKind, verb, namespace, name string) (authorizationv1.ResourceAttributes, error) {
	mapping, err := r.restMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return authorizationv1.ResourceAttributes{}, fmt.Errorf("failed to resolve resource mapping for %s: %w", gvk.String(), err)
	}
	return authorizationv1.ResourceAttributes{
		Namespace: namespace,
		Verb:      verb,
		Group:     mapping.Resource.Group,
		Version:   mapping.Resource.Version,
		Resource:  mapping.Resource.Resource,
		Name:      name,
	}, nil
}

// Authorize verifies that the request user is allowed to perform all the supplied resource actions.
func (r *Reviewer) Authorize(ctx context.Context, userInfo authenticationv1.UserInfo, attributes ...authorizationv1.ResourceAttributes) error {
	userInfo = copyUserInfo(userInfo)
	uniqueAttributes := make([]authorizationv1.ResourceAttributes, 0, len(attributes))
	seen := make(map[string]struct{}, len(attributes))
	for i := range attributes {
		key, err := reviewKey(attributes[i])
		if err != nil {
			return fmt.Errorf("failed to build authorization review key: %w", err)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		uniqueAttributes = append(uniqueAttributes, attributes[i])
	}

	reviews, reviewCtx := errgroup.WithContext(ctx)
	reviews.SetLimit(r.maxConcurrentReviews)
	var submissionErr error
	for i := range uniqueAttributes {
		attributes := uniqueAttributes[i]
		if err := reviewCtx.Err(); err != nil {
			submissionErr = err
			break
		}
		reviews.Go(func() error {
			return r.review(reviewCtx, userInfo, attributes)
		})
	}
	if err := reviews.Wait(); err != nil {
		return err
	}
	return submissionErr
}

func (r *Reviewer) review(ctx context.Context, userInfo authenticationv1.UserInfo, attributes authorizationv1.ResourceAttributes) error {
	review := &authorizationv1.SubjectAccessReview{
		Spec: authorizationv1.SubjectAccessReviewSpec{
			User:               userInfo.Username,
			UID:                userInfo.UID,
			Groups:             append([]string(nil), userInfo.Groups...),
			Extra:              authorizationExtra(userInfo.Extra),
			ResourceAttributes: attributes.DeepCopy(),
		},
	}
	result, err := r.client.Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create SubjectAccessReview for %s: %w", describeAttributes(attributes), err)
	}
	if result == nil {
		return fmt.Errorf("SubjectAccessReview for %s returned no result", describeAttributes(attributes))
	}
	if result.Status.EvaluationError != "" {
		return fmt.Errorf("SubjectAccessReview for %s reported an evaluation error: %s", describeAttributes(attributes), result.Status.EvaluationError)
	}
	if result.Status.Allowed && result.Status.Denied {
		return fmt.Errorf("SubjectAccessReview for %s returned conflicting allowed and denied decisions", describeAttributes(attributes))
	}
	if !result.Status.Allowed {
		decision := "no opinion"
		if result.Status.Denied {
			decision = "denied"
		}
		if result.Status.Reason == "" {
			return fmt.Errorf("SubjectAccessReview %s access to %s", decision, describeAttributes(attributes))
		}
		return fmt.Errorf("SubjectAccessReview %s access to %s: %s", decision, describeAttributes(attributes), result.Status.Reason)
	}
	return nil
}

func reviewKey(attributes authorizationv1.ResourceAttributes) (string, error) {
	key, err := json.Marshal(attributes)
	if err != nil {
		return "", err
	}
	return string(key), nil
}

func copyUserInfo(userInfo authenticationv1.UserInfo) authenticationv1.UserInfo {
	copied := authenticationv1.UserInfo{
		Username: userInfo.Username,
		UID:      userInfo.UID,
		Groups:   append([]string(nil), userInfo.Groups...),
	}
	if userInfo.Extra != nil {
		copied.Extra = make(map[string]authenticationv1.ExtraValue, len(userInfo.Extra))
		for key, values := range userInfo.Extra {
			copied.Extra[key] = append(authenticationv1.ExtraValue(nil), values...)
		}
	}
	return copied
}

func authorizationExtra(extra map[string]authenticationv1.ExtraValue) map[string]authorizationv1.ExtraValue {
	if extra == nil {
		return nil
	}
	converted := make(map[string]authorizationv1.ExtraValue, len(extra))
	for key, values := range extra {
		converted[key] = append(authorizationv1.ExtraValue(nil), values...)
	}
	return converted
}

func describeAttributes(attributes authorizationv1.ResourceAttributes) string {
	resource := attributes.Resource
	if attributes.Group != "" {
		resource = resource + "." + attributes.Group
	}
	if attributes.Subresource != "" {
		resource = resource + "/" + attributes.Subresource
	}
	target := resource
	if attributes.Name != "" {
		target = fmt.Sprintf("%s %q", resource, attributes.Name)
	}
	if attributes.Namespace != "" {
		target = fmt.Sprintf("%s in namespace %q", target, attributes.Namespace)
	}
	return fmt.Sprintf("%s %s", attributes.Verb, target)
}
