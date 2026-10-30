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

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils"
)

// NamespacedResourceInventory supplies the namespaced resource types tracked by the placement controller.
type NamespacedResourceInventory interface {
	GetNameSpaceScopedResources() []schema.GroupVersionResource
}

type namespaceScope struct {
	hasSelectiveNamespace bool
	selectedNamespaceName string
}

func (a *PlacementAuthorizer) resolveClusterResourcePlacement(ctx context.Context, crp *placementv1beta1.ClusterResourcePlacement) ([]authorizationv1.ResourceAttributes, error) {
	if crp == nil {
		return nil, errors.New("cluster resource placement must not be nil")
	}
	if crp.Namespace != "" {
		return nil, errors.New("cluster resource placement must not specify a namespace")
	}
	namespaceChecks, scope, err := a.resolveNamespaceSelectors(ctx, crp.Spec.ResourceSelectors)
	if err != nil {
		return nil, err
	}
	resourceChecks, err := a.resolveNonNamespaceSelectors(crp.Spec.ResourceSelectors, scope)
	if err != nil {
		return nil, err
	}
	return append(namespaceChecks, resourceChecks...), nil
}

func (a *PlacementAuthorizer) resolveNamespaceSelectors(ctx context.Context, selectors []placementv1beta1.ResourceSelectorTerm) ([]authorizationv1.ResourceAttributes, namespaceScope, error) {
	var attributes []authorizationv1.ResourceAttributes
	var scope namespaceScope
	namespaceSelectorCount := 0
	for _, selector := range selectors {
		if selectorGVK(selector) != utils.NamespaceGVK {
			continue
		}
		namespaceSelectorCount++
		if selector.SelectionScope == placementv1beta1.NamespaceWithResourceSelectors {
			if scope.hasSelectiveNamespace {
				return nil, namespaceScope{}, errors.New("NamespaceWithResourceSelectors requires exactly one namespace selector")
			}
			scope.hasSelectiveNamespace = true
			scope.selectedNamespaceName = selector.Name
		}
		checks, err := a.resolveNamespaceSelector(ctx, selector)
		if err != nil {
			return nil, namespaceScope{}, err
		}
		attributes = append(attributes, checks...)
	}
	if scope.hasSelectiveNamespace && namespaceSelectorCount != 1 {
		return nil, namespaceScope{}, errors.New("NamespaceWithResourceSelectors requires exactly one namespace selector")
	}
	return attributes, scope, nil
}

func (a *PlacementAuthorizer) resolveNonNamespaceSelectors(selectors []placementv1beta1.ResourceSelectorTerm, scope namespaceScope) ([]authorizationv1.ResourceAttributes, error) {
	var attributes []authorizationv1.ResourceAttributes
	for _, selector := range selectors {
		gvk := selectorGVK(selector)
		if gvk == utils.NamespaceGVK {
			continue
		}
		mapping, err := a.restMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve selector %s: %w", gvk, err)
		}
		namespace := ""
		if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
			if !scope.hasSelectiveNamespace {
				return nil, fmt.Errorf("cluster resource placement requires NamespaceWithResourceSelectors to select namespaced resource %s", gvk)
			}
			namespace = scope.selectedNamespaceName
		}
		if !a.resourceEligibilityChecker.AllowsGVK(gvk) {
			continue
		}
		check, err := a.selectorAttributes(mapping.Resource, namespace, selector)
		if err != nil {
			return nil, err
		}
		attributes = append(attributes, check)
	}
	return attributes, nil
}

func (a *PlacementAuthorizer) namespaceChecksForPlacement(placement placementv1beta1.PlacementObj) ([]authorizationv1.ResourceAttributes, error) {
	crp, ok := placement.(*placementv1beta1.ClusterResourcePlacement)
	if !ok {
		return nil, nil
	}
	if crp == nil {
		return nil, errors.New("cluster resource placement must not be nil")
	}
	if crp.Namespace != "" {
		return nil, errors.New("cluster resource placement must not specify a namespace")
	}
	var attributes []authorizationv1.ResourceAttributes
	for _, selector := range crp.Spec.ResourceSelectors {
		if selectorGVK(selector) != utils.NamespaceGVK {
			continue
		}
		checks, err := a.namespaceSelectorAttributes(selector)
		if err != nil {
			return nil, err
		}
		attributes = append(attributes, checks...)
	}
	return attributes, nil
}

func (a *PlacementAuthorizer) namespaceSelectorAttributes(selector placementv1beta1.ResourceSelectorTerm) ([]authorizationv1.ResourceAttributes, error) {
	if selector.SelectionScope == placementv1beta1.NamespaceWithResourceSelectors && (selector.Name == "" || selector.LabelSelector != nil) {
		return nil, errors.New("NamespaceWithResourceSelectors requires a namespace selected by name")
	}
	mapping, err := a.restMapper.RESTMapping(utils.NamespaceGVK.GroupKind(), utils.NamespaceGVK.Version)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve Namespace mapping: %w", err)
	}
	if !a.resourceEligibilityChecker.AllowsGVK(utils.NamespaceGVK) {
		return nil, nil
	}
	check, err := a.selectorAttributes(mapping.Resource, "", selector)
	if err != nil {
		return nil, err
	}
	return []authorizationv1.ResourceAttributes{check}, nil
}

func (a *PlacementAuthorizer) resolveNamespaceSelector(ctx context.Context, selector placementv1beta1.ResourceSelectorTerm) ([]authorizationv1.ResourceAttributes, error) {
	attributes, err := a.namespaceSelectorAttributes(selector)
	if err != nil {
		return nil, err
	}
	if len(attributes) == 0 {
		return nil, nil
	}
	switch selector.SelectionScope {
	case placementv1beta1.NamespaceOnly:
		return attributes, nil
	case "", placementv1beta1.NamespaceWithResources:
		namespaces, err := a.selectNamespaces(ctx, selector)
		if err != nil {
			return nil, err
		}
		for i := range namespaces {
			namespace := namespaces[i].Name
			if !a.resourceEligibilityChecker.AllowsNamespace(namespace) {
				return nil, fmt.Errorf("namespace %q is not eligible for placement", namespace)
			}
			checks, err := a.expandNamespace(namespace)
			if err != nil {
				return nil, err
			}
			attributes = append(attributes, checks...)
		}
		return attributes, nil
	case placementv1beta1.NamespaceWithResourceSelectors:
		namespaces, err := a.selectNamespaces(ctx, selector)
		if err != nil {
			return nil, err
		}
		if len(namespaces) != 1 {
			return nil, errors.New("NamespaceWithResourceSelectors requires exactly one active namespace")
		}
		if !a.resourceEligibilityChecker.AllowsNamespace(namespaces[0].Name) {
			return nil, fmt.Errorf("namespace %q is not eligible for placement", namespaces[0].Name)
		}
		return attributes, nil
	default:
		return nil, fmt.Errorf("unsupported namespace selection scope %q", selector.SelectionScope)
	}
}

func (a *PlacementAuthorizer) selectNamespaces(ctx context.Context, selector placementv1beta1.ResourceSelectorTerm) ([]corev1.Namespace, error) {
	var namespaces []corev1.Namespace
	if selector.Name != "" {
		var namespace corev1.Namespace
		if err := a.namespaceReader.Get(ctx, types.NamespacedName{Name: selector.Name}, &namespace); err != nil {
			return nil, fmt.Errorf("failed to get selected namespace %q: %w", selector.Name, err)
		}
		namespaces = append(namespaces, namespace)
	} else {
		labelSelector := labels.Everything()
		if selector.LabelSelector != nil {
			var err error
			labelSelector, err = metav1.LabelSelectorAsSelector(selector.LabelSelector)
			if err != nil {
				return nil, fmt.Errorf("invalid namespace label selector: %w", err)
			}
		}
		var list corev1.NamespaceList
		if err := a.namespaceReader.List(ctx, &list, client.MatchingLabelsSelector{Selector: labelSelector}); err != nil {
			return nil, fmt.Errorf("failed to list selected namespaces: %w", err)
		}
		namespaces = list.Items
	}
	active := make([]corev1.Namespace, 0, len(namespaces))
	for i := range namespaces {
		if namespaces[i].DeletionTimestamp == nil {
			active = append(active, namespaces[i])
		}
	}
	return active, nil
}

func (a *PlacementAuthorizer) expandNamespace(namespace string) ([]authorizationv1.ResourceAttributes, error) {
	var attributes []authorizationv1.ResourceAttributes
	for _, gvr := range a.namespacedResources.GetNameSpaceScopedResources() {
		eligible, err := a.resourceEligibilityChecker.AllowsGVR(gvr)
		switch {
		case meta.IsNoMatchError(err):
			klog.V(2).InfoS("Skip namespace expansion authorization for a resource with no registered kind", "GVR", gvr.String())
			continue
		case err != nil:
			return nil, fmt.Errorf("failed to determine eligibility for %s: %w", gvr, err)
		case !eligible:
			continue
		}
		attributes = append(attributes, resourceAttributes(gvr, "list", namespace, ""))
	}
	return attributes, nil
}
