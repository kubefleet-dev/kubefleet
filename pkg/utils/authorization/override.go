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
	"fmt"
	"strings"

	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils"
)

// RBAC kinds subject to additional escalate or bind authorization checks.
const (
	roleKind               = "Role"
	clusterRoleKind        = "ClusterRole"
	roleBindingKind        = "RoleBinding"
	clusterRoleBindingKind = "ClusterRoleBinding"
)

// OverrideAuthorizer reviews mutations of resources selected by RO and CRO.
type OverrideAuthorizer struct {
	restMapper meta.RESTMapper
	reviewer   *Reviewer
	inventory  NamespacedResourceInventory
}

// NewOverrideAuthorizer creates an authorizer that inspects policies without reading source objects.
// inventory supplies the namespaced resource types also affected by Namespace CROs.
func NewOverrideAuthorizer(mapper meta.RESTMapper, reviewer *Reviewer, inventory NamespacedResourceInventory) (*OverrideAuthorizer, error) {
	switch {
	case mapper == nil:
		return nil, errors.New("REST mapper must not be nil")
	case reviewer == nil:
		return nil, errors.New("authorization reviewer must not be nil")
	case inventory == nil:
		return nil, errors.New("namespaced resource inventory must not be nil")
	}
	return &OverrideAuthorizer{restMapper: mapper, reviewer: reviewer, inventory: inventory}, nil
}

type overrideTarget struct {
	gvk       schema.GroupVersionKind
	gvr       schema.GroupVersionResource
	namespace string
	name      string
}

type invalidOverrideError string

func (e invalidOverrideError) Error() string { return string(e) }

func overrideErrorResult(err error) Result {
	decision := DecisionIndeterminate
	var invalidErr invalidOverrideError
	if errors.As(err, &invalidErr) {
		decision = DecisionInvalid
	}
	return Result{Decision: decision, Err: err}
}

// Authorize requires mutation access and applicable RBAC privilege-protection permissions.
// The override must have passed CRD and override webhook validation.
func (a *OverrideAuthorizer) Authorize(ctx context.Context, user authenticationv1.UserInfo, override client.Object) Result {
	targets, policy, err := a.resolve(override)
	if err != nil {
		return overrideErrorResult(err)
	}
	checks := mutationChecks(targets, policy)
	rbac, err := a.rbacChecks(targets, policy)
	if err != nil {
		return overrideErrorResult(err)
	}
	return a.reviewer.Authorize(ctx, user, append(checks, rbac...)...)
}

// mutationChecks builds patch/delete authorization attributes for every selected target and active policy verb.
func mutationChecks(targets []overrideTarget, policy *placementv1beta1.OverridePolicy) []authorizationv1.ResourceAttributes {
	verbs := overrideVerbs(policy)
	var checks []authorizationv1.ResourceAttributes
	for _, target := range targets {
		for _, verb := range verbs {
			checks = append(checks, resourceAttributes(target.gvr, verb, target.namespace, target.name))
		}
	}
	return checks
}

// rbacChecks builds additional escalate or bind authorization attributes for active overrides of RBAC roles or bindings.
func (a *OverrideAuthorizer) rbacChecks(targets []overrideTarget, policy *placementv1beta1.OverridePolicy) ([]authorizationv1.ResourceAttributes, error) {
	var checks []authorizationv1.ResourceAttributes
	for _, target := range targets {
		if target.gvk.Group != rbacv1.GroupName {
			continue
		}
		switch target.gvk.Kind {
		case roleKind, clusterRoleKind:
			if hasRolePermissionChanges(policy, target.gvk.Kind == clusterRoleKind) {
				checks = append(checks, resourceAttributes(target.gvr, "escalate", target.namespace, target.name))
			}
		case roleBindingKind, clusterRoleBindingKind:
			if !hasPatchRule(policy) {
				continue
			}
			bindChecks, err := a.bindingChecks(target)
			if err != nil {
				return nil, err
			}
			checks = append(checks, bindChecks...)
		}
	}
	return checks, nil
}

// resolve converts RO/CRO selectors into resource targets with API mappings and namespaces,
// expands selected Namespaces, and returns the targets alongside the override policy.
func (a *OverrideAuthorizer) resolve(override client.Object) ([]overrideTarget, *placementv1beta1.OverridePolicy, error) {
	var selectors []placementv1beta1.ResourceSelectorTerm
	var namespace string
	var policy *placementv1beta1.OverridePolicy
	switch override := override.(type) {
	case *placementv1beta1.ClusterResourceOverride:
		selectors, policy = override.Spec.ClusterResourceSelectors, override.Spec.Policy
	case *placementv1beta1.ResourceOverride:
		namespace, policy = override.Namespace, override.Spec.Policy
		for _, selector := range override.Spec.ResourceSelectors {
			selectors = append(selectors, placementv1beta1.ResourceSelectorTerm{
				Group: selector.Group, Version: selector.Version, Kind: selector.Kind, Name: selector.Name,
			})
		}
	default:
		return nil, nil, fmt.Errorf("unsupported override type %T", override)
	}
	var targets []overrideTarget
	for _, selector := range selectors {
		gvk := selectorGVK(selector)
		mapping, err := a.restMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to resolve override selector %s: %w", gvk, err)
		}
		namespaced := mapping.Scope.Name() == meta.RESTScopeNameNamespace
		if namespaced != (namespace != "") {
			return nil, nil, invalidOverrideError(fmt.Sprintf("override selector %s has incompatible resource scope", gvk))
		}
		targets = append(targets, overrideTarget{gvk: gvk, gvr: mapping.Resource, namespace: namespace, name: selector.Name})
		if gvk == utils.NamespaceGVK {
			expanded, err := a.expandOverrideNamespace(selector.Name)
			if err != nil {
				return nil, nil, err
			}
			targets = append(targets, expanded...)
		}
	}
	return targets, policy, nil
}

func (a *OverrideAuthorizer) expandOverrideNamespace(namespace string) ([]overrideTarget, error) {
	var targets []overrideTarget
	for _, gvr := range a.inventory.GetNameSpaceScopedResources() {
		gvk, err := a.restMapper.KindFor(gvr)
		switch {
		case meta.IsNoMatchError(err):
			// A removed CRD can linger in the inventory after discovery drops it;
			// skip the stale type because it no longer has a served resource to override.
			klog.V(2).InfoS("Skip override namespace expansion for a resource with no registered kind", "GVR", gvr.String())
			continue
		case err != nil:
			return nil, fmt.Errorf("failed to resolve override resource %s: %w", gvr, err)
		}
		targets = append(targets, overrideTarget{gvk: gvk, gvr: gvr, namespace: namespace})
	}
	return targets, nil
}

func overrideVerbs(policy *placementv1beta1.OverridePolicy) []string {
	var verbs []string
	for _, rule := range policy.OverrideRules {
		if rule.ClusterSelector == nil {
			continue
		}
		switch rule.OverrideType {
		case placementv1beta1.DeleteOverrideType:
			verbs = append(verbs, "delete")
		default:
			verbs = append(verbs, "patch")
		}
	}
	return verbs
}

func hasPatchRule(policy *placementv1beta1.OverridePolicy) bool {
	for _, rule := range policy.OverrideRules {
		if rule.ClusterSelector != nil && rule.OverrideType != placementv1beta1.DeleteOverrideType {
			return true
		}
	}
	return false
}

func hasRolePermissionChanges(policy *placementv1beta1.OverridePolicy, clusterRole bool) bool {
	for _, rule := range policy.OverrideRules {
		if rule.ClusterSelector == nil || rule.OverrideType == placementv1beta1.DeleteOverrideType {
			continue
		}
		for _, patch := range rule.JSONPatchOverrides {
			// Rules grant permissions directly; aggregationRule collects permissions from other ClusterRoles.
			if isPatchTargetingField(patch.Path, "rules") || (clusterRole && isPatchTargetingField(patch.Path, "aggregationRule")) {
				return true
			}
		}
	}
	return false
}

func isPatchTargetingField(path, field string) bool {
	return path == "/"+field || strings.HasPrefix(path, "/"+field+"/")
}

func (a *OverrideAuthorizer) bindingChecks(targetBinding overrideTarget) ([]authorizationv1.ResourceAttributes, error) {
	// Require unrestricted bind access even when patches name a specific role.
	// RoleBindings can reference either kind; ClusterRoleBindings only reference ClusterRoles.
	kinds := []string{clusterRoleKind}
	if targetBinding.gvk.Kind == roleBindingKind {
		kinds = append(kinds, roleKind)
	}
	var checks []authorizationv1.ResourceAttributes
	for _, kind := range kinds {
		check, err := a.reviewer.ResourceAttributesFor(rbacv1.SchemeGroupVersion.WithKind(kind), "bind", targetBinding.namespace, "")
		if err != nil {
			return nil, err
		}
		checks = append(checks, check)
	}
	return checks, nil
}
