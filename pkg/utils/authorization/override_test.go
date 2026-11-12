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
	"testing"

	"github.com/google/go-cmp/cmp"
	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

func TestNewOverrideAuthorizer(t *testing.T) {
	mapper := meta.NewDefaultRESTMapper(nil)
	reviewer := &Reviewer{}
	inventory := crpResourceInventory{}
	tests := []struct {
		name      string
		mapper    meta.RESTMapper
		reviewer  *Reviewer
		inventory NamespacedResourceInventory
		wantErr   bool
	}{
		{name: "valid dependencies", mapper: mapper, reviewer: reviewer, inventory: inventory, wantErr: false},
		{name: "missing mapper", mapper: nil, reviewer: reviewer, inventory: inventory, wantErr: true},
		{name: "missing reviewer", mapper: mapper, reviewer: nil, inventory: inventory, wantErr: true},
		{name: "nil inventory dependency", mapper: mapper, reviewer: reviewer, inventory: nil, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewOverrideAuthorizer(tt.mapper, tt.reviewer, tt.inventory)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewOverrideAuthorizer() error = %v, want error %t", err, tt.wantErr)
			}
			if tt.wantErr {
				if got != nil {
					t.Errorf("NewOverrideAuthorizer() = %v, want nil", got)
				}
				return
			}
			if got == nil || got.restMapper != mapper || got.reviewer != reviewer {
				t.Fatalf("NewOverrideAuthorizer() = %v, want authorizer with supplied mapper and reviewer", got)
			}
			if diff := cmp.Diff(inventory, got.inventory); diff != "" {
				t.Errorf("NewOverrideAuthorizer() inventory mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBindingChecks(t *testing.T) {
	const bindVerb = "bind"
	dependencyErr := errors.New("resource attributes unavailable")
	tests := []struct {
		name        string
		bindingKind string
		namespace   string
		want        []authorizationv1.ResourceAttributes
		wantErr     bool
	}{
		{
			name:        "RoleBinding requires both role kinds in its namespace",
			bindingKind: roleBindingKind,
			namespace:   rpNamespace,
			want: []authorizationv1.ResourceAttributes{
				{Group: rbacv1.GroupName, Version: "v1", Resource: "clusterroles", Verb: bindVerb, Namespace: rpNamespace},
				{Group: rbacv1.GroupName, Version: "v1", Resource: "roles", Verb: bindVerb, Namespace: rpNamespace},
			},
		},
		{
			name:        "ClusterRoleBinding requires only cluster-wide ClusterRole bind",
			bindingKind: clusterRoleBindingKind,
			want: []authorizationv1.ResourceAttributes{
				{Group: rbacv1.GroupName, Version: "v1", Resource: "clusterroles", Verb: bindVerb},
			},
		},
		{
			name:        "dependency error is propagated without partial checks",
			bindingKind: roleBindingKind,
			namespace:   rpNamespace,
			wantErr:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{rbacv1.SchemeGroupVersion})
			for _, kind := range []string{clusterRoleKind, roleKind} {
				scope := meta.RESTScopeRoot
				if kind == roleKind {
					scope = meta.RESTScopeNamespace
				}
				mapper.Add(rbacv1.SchemeGroupVersion.WithKind(kind), scope)
			}
			var restMapper meta.RESTMapper = mapper
			if tt.wantErr {
				restMapper = bindingChecksErrorMapper{RESTMapper: mapper, err: dependencyErr}
			}
			a := &OverrideAuthorizer{reviewer: &Reviewer{restMapper: restMapper}}
			target := overrideTarget{
				gvk:       rbacv1.SchemeGroupVersion.WithKind(tt.bindingKind),
				namespace: tt.namespace,
				name:      "source",
			}
			got, err := a.bindingChecks(target)
			if (err != nil) != tt.wantErr {
				t.Fatalf("bindingChecks(%v) error = %v, want error %t", target, err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, dependencyErr) {
				t.Errorf("bindingChecks(%v) error = %v, want %v", target, err, dependencyErr)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("bindingChecks(%v) mismatch (-want +got):\n%s", target, diff)
			}
		})
	}
}

func TestIsPatchTargetingField(t *testing.T) {
	const rulesField = "rules"
	tests := []struct {
		name  string
		path  string
		field string
		want  bool
	}{
		{name: "whole field", path: "/rules", field: rulesField, want: true},
		{name: "nested field", path: "/rules/0/verbs", field: rulesField, want: true},
		{name: "array append", path: "/rules/-", field: rulesField, want: true},
		{name: "different field", path: "/subjects", field: rulesField, want: false},
		{name: "shared prefix is not a match", path: "/rulesExtra", field: rulesField, want: false},
		{name: "nested shared prefix is not a match", path: "/rulesExtra/0", field: rulesField, want: false},
		{name: "field name alone is not a match", path: rulesField, field: rulesField, want: false},
		{name: "empty path", path: "", field: rulesField, want: false},
		{name: "field nested under another field", path: "/spec/rules", field: rulesField, want: false},
		{name: "aggregation field", path: "/aggregationRule/clusterRoleSelectors", field: "aggregationRule", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPatchTargetingField(tt.path, tt.field); got != tt.want {
				t.Errorf("isPatchTargetingField(%q, %q) = %t, want %t", tt.path, tt.field, got, tt.want)
			}
		})
	}
}

func TestHasRolePermissionChanges(t *testing.T) {
	activeRule := placementv1beta1.OverrideRule{
		ClusterSelector: &placementv1beta1.ClusterSelector{},
		OverrideType:    placementv1beta1.JSONPatchOverrideType,
		JSONPatchOverrides: []placementv1beta1.JSONPatchOverride{
			{Path: "/rules"},
		},
	}
	inactiveRule := *activeRule.DeepCopy()
	inactiveRule.ClusterSelector = nil
	deleteRule := *activeRule.DeepCopy()
	deleteRule.OverrideType = placementv1beta1.DeleteOverrideType
	aggregationRule := *activeRule.DeepCopy()
	aggregationRule.JSONPatchOverrides = []placementv1beta1.JSONPatchOverride{{Path: "/aggregationRule"}}
	labelsRule := *activeRule.DeepCopy()
	labelsRule.JSONPatchOverrides = []placementv1beta1.JSONPatchOverride{{Path: "/metadata/labels"}}
	aggregationLabelRule := *activeRule.DeepCopy()
	aggregationLabelRule.JSONPatchOverrides = []placementv1beta1.JSONPatchOverride{{
		Path: "/metadata/labels/rbac.authorization.k8s.io~1aggregate-to-admin",
	}}
	tests := []struct {
		name        string
		rules       []placementv1beta1.OverrideRule
		clusterRole bool
		want        bool
	}{
		{name: "inactive rule ignored", rules: []placementv1beta1.OverrideRule{inactiveRule}, clusterRole: false, want: false},
		{name: "delete rule ignored", rules: []placementv1beta1.OverrideRule{deleteRule}, clusterRole: true, want: false},
		{name: "Role rules changed", rules: []placementv1beta1.OverrideRule{activeRule}, clusterRole: false, want: true},
		{name: "aggregation applies to ClusterRole", rules: []placementv1beta1.OverrideRule{aggregationRule}, clusterRole: true, want: true},
		{name: "ClusterRole labels replacement requires escalation", rules: []placementv1beta1.OverrideRule{labelsRule}, clusterRole: true, want: true},
		{name: "ClusterRole aggregation label requires escalation", rules: []placementv1beta1.OverrideRule{aggregationLabelRule}, clusterRole: true, want: true},
		{name: "Role aggregation label does not require escalation", rules: []placementv1beta1.OverrideRule{aggregationLabelRule}, clusterRole: false, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := &placementv1beta1.OverridePolicy{OverrideRules: tt.rules}
			if got := hasRolePermissionChanges(policy, tt.clusterRole); got != tt.want {
				t.Errorf("hasRolePermissionChanges(%v, %t) = %t, want %t", policy, tt.clusterRole, got, tt.want)
			}
		})
	}
}

func TestOverrideVerbs(t *testing.T) {
	tests := []struct {
		name  string
		rules []placementv1beta1.OverrideRule
		want  []string
	}{
		{
			name: "nil cluster selector is skipped",
			rules: []placementv1beta1.OverrideRule{{
				ClusterSelector: nil,
				OverrideType:    placementv1beta1.DeleteOverrideType,
			}},
			want: nil,
		},
		{
			name: "delete rule",
			rules: []placementv1beta1.OverrideRule{{
				ClusterSelector: &placementv1beta1.ClusterSelector{},
				OverrideType:    placementv1beta1.DeleteOverrideType,
			}},
			want: []string{"delete"},
		},
		{
			name: "JSONPatch rule",
			rules: []placementv1beta1.OverrideRule{{
				ClusterSelector: &placementv1beta1.ClusterSelector{},
				OverrideType:    placementv1beta1.JSONPatchOverrideType,
			}},
			want: []string{"patch"},
		},
		{
			name: "empty type defaults to patch",
			rules: []placementv1beta1.OverrideRule{{
				ClusterSelector: &placementv1beta1.ClusterSelector{},
				OverrideType:    "",
			}},
			want: []string{"patch"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := &placementv1beta1.OverridePolicy{OverrideRules: tt.rules}
			if got := overrideVerbs(policy); !cmp.Equal(tt.want, got) {
				t.Errorf("overrideVerbs(%v) = %v, want %v", policy, got, tt.want)
			}
		})
	}
}

func TestExpandOverrideNamespace(t *testing.T) {
	firstGVR := schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "firsts"}
	secondGVR := schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "seconds"}
	firstGVK := firstGVR.GroupVersion().WithKind("First")
	secondGVK := secondGVR.GroupVersion().WithKind("Second")
	dependencyErr := errors.New("resource mapping unavailable")
	tests := []struct {
		name      string
		resources []schema.GroupVersionResource
		mapErr    error
		want      []overrideTarget
		wantErr   bool
	}{
		{name: "empty inventory", resources: nil, want: nil, wantErr: false},
		{
			name:      "expand tracked resources",
			resources: []schema.GroupVersionResource{firstGVR, secondGVR},
			want: []overrideTarget{
				{gvk: firstGVK, gvr: firstGVR, namespace: rpNamespace},
				{gvk: secondGVK, gvr: secondGVR, namespace: rpNamespace},
			},
			wantErr: false,
		},
		{
			name:      "no match skips resource and continues",
			resources: []schema.GroupVersionResource{firstGVR, secondGVR, firstGVR},
			mapErr:    &meta.NoResourceMatchError{PartialResource: secondGVR},
			// Expansion preserves duplicates; Reviewer.Authorize deduplicates checks before submitting SARs.
			want: []overrideTarget{
				{gvk: firstGVK, gvr: firstGVR, namespace: rpNamespace},
				{gvk: firstGVK, gvr: firstGVR, namespace: rpNamespace},
			},
			wantErr: false,
		},
		{
			name:      "other errors discard partial targets",
			resources: []schema.GroupVersionResource{firstGVR, secondGVR},
			mapErr:    dependencyErr,
			want:      nil,
			wantErr:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{firstGVR.GroupVersion()})
			mapper.AddSpecific(firstGVK, firstGVR, firstGVR, meta.RESTScopeNamespace)
			mapper.AddSpecific(secondGVK, secondGVR, secondGVR, meta.RESTScopeNamespace)
			a := &OverrideAuthorizer{
				inventory: crpResourceInventory(tt.resources),
				restMapper: namespaceExpansionTestMapper{
					RESTMapper: mapper, errorResource: secondGVR, err: tt.mapErr,
				},
			}
			got, err := a.expandOverrideNamespace(rpNamespace)
			if (err != nil) != tt.wantErr {
				t.Fatalf("expandOverrideNamespace(%q) error = %v, want error %t", rpNamespace, err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, tt.mapErr) {
				t.Errorf("expandOverrideNamespace(%q) error = %v, want %v", rpNamespace, err, tt.mapErr)
			}
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(overrideTarget{})); diff != "" {
				t.Errorf("expandOverrideNamespace(%q) mismatch (-want +got):\n%s", rpNamespace, diff)
			}
		})
	}
}

func TestOverrideResolve(t *testing.T) {
	const sourceName = "source"
	configGVK := corev1.SchemeGroupVersion.WithKind("ConfigMap")
	configGVR := corev1.SchemeGroupVersion.WithResource("configmaps")
	namespaceGVK := corev1.SchemeGroupVersion.WithKind("Namespace")
	namespaceGVR := corev1.SchemeGroupVersion.WithResource("namespaces")
	clusterRoleGVK := rbacv1.SchemeGroupVersion.WithKind(clusterRoleKind)
	clusterRoleGVR := rbacv1.SchemeGroupVersion.WithResource("clusterroles")
	policy := &placementv1beta1.OverridePolicy{OverrideRules: []placementv1beta1.OverrideRule{{
		OverrideType:    placementv1beta1.DeleteOverrideType,
		ClusterSelector: &placementv1beta1.ClusterSelector{},
	}}}
	resourceOverride := func(gvk schema.GroupVersionKind) client.Object {
		return &placementv1beta1.ResourceOverride{
			ObjectMeta: metav1.ObjectMeta{Namespace: rpNamespace},
			Spec: placementv1beta1.ResourceOverrideSpec{
				ResourceSelectors: []placementv1beta1.ResourceSelector{{
					Group: gvk.Group, Version: gvk.Version, Kind: gvk.Kind, Name: sourceName,
				}},
				Policy: policy,
			},
		}
	}
	clusterOverride := func(gvk schema.GroupVersionKind) client.Object {
		return &placementv1beta1.ClusterResourceOverride{
			Spec: placementv1beta1.ClusterResourceOverrideSpec{
				ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{{
					Group: gvk.Group, Version: gvk.Version, Kind: gvk.Kind, Name: sourceName,
				}},
				Policy: policy,
			},
		}
	}
	dependencyErr := errors.New("mapping unavailable")
	tests := []struct {
		name         string
		override     client.Object
		mappingErr   error
		expansionErr error
		want         []overrideTarget
		wantErr      bool
		wantInvalid  bool
	}{
		{
			name: "RO inherits namespace", override: resourceOverride(configGVK),
			want:    []overrideTarget{{gvk: configGVK, gvr: configGVR, namespace: rpNamespace, name: sourceName}},
			wantErr: false,
		},
		{
			name: "CRO selects cluster-scoped resource", override: clusterOverride(clusterRoleGVK),
			want:    []overrideTarget{{gvk: clusterRoleGVK, gvr: clusterRoleGVR, name: sourceName}},
			wantErr: false,
		},
		{
			name: "CRO namespace expands contents", override: clusterOverride(namespaceGVK),
			want: []overrideTarget{
				{gvk: namespaceGVK, gvr: namespaceGVR, name: sourceName},
				{gvk: configGVK, gvr: configGVR, namespace: sourceName},
			},
			wantErr: false,
		},
		{name: "RO cannot select cluster-scoped kind", override: resourceOverride(clusterRoleGVK), wantErr: true, wantInvalid: true},
		{name: "CRO cannot directly select namespaced kind", override: clusterOverride(configGVK), wantErr: true, wantInvalid: true},
		{name: "selector mapping error", override: resourceOverride(configGVK), mappingErr: dependencyErr, wantErr: true},
		{name: "namespace expansion error", override: clusterOverride(namespaceGVK), expansionErr: dependencyErr, wantErr: true},
		{name: "unsupported object", override: &corev1.ConfigMap{}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{corev1.SchemeGroupVersion, rbacv1.SchemeGroupVersion})
			mapper.AddSpecific(configGVK, configGVR, configGVR, meta.RESTScopeNamespace)
			mapper.AddSpecific(namespaceGVK, namespaceGVR, namespaceGVR, meta.RESTScopeRoot)
			mapper.AddSpecific(clusterRoleGVK, clusterRoleGVR, clusterRoleGVR, meta.RESTScopeRoot)
			var restMapper meta.RESTMapper = namespaceExpansionTestMapper{
				RESTMapper: mapper, errorResource: configGVR, err: tt.expansionErr,
			}
			if tt.mappingErr != nil {
				restMapper = bindingChecksErrorMapper{RESTMapper: mapper, err: tt.mappingErr}
			}
			a := &OverrideAuthorizer{restMapper: restMapper, inventory: crpResourceInventory{configGVR}}
			got, gotPolicy, err := a.resolve(tt.override)
			if (err != nil) != tt.wantErr {
				t.Fatalf("resolve(%v) error = %v, want error %t", tt.override, err, tt.wantErr)
			}
			if tt.wantErr {
				var invalidErr invalidOverrideError
				if gotInvalid := errors.As(err, &invalidErr); gotInvalid != tt.wantInvalid {
					t.Errorf("resolve() invalid error = %t, want %t", gotInvalid, tt.wantInvalid)
				}
				if (tt.mappingErr != nil || tt.expansionErr != nil) && !errors.Is(err, dependencyErr) {
					t.Errorf("resolve() error = %v, want %v", err, dependencyErr)
				}
				if gotPolicy != nil {
					t.Errorf("resolve() policy = %v, want nil on error", gotPolicy)
				}
			} else if diff := cmp.Diff(policy, gotPolicy); diff != "" {
				t.Errorf("resolve() policy mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(overrideTarget{})); diff != "" {
				t.Errorf("resolve() targets mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRBACChecks(t *testing.T) {
	const sourceName = "source"
	const bindVerb = "bind"
	roleTarget := overrideTarget{
		gvk:       rbacv1.SchemeGroupVersion.WithKind(roleKind),
		gvr:       rbacv1.SchemeGroupVersion.WithResource("roles"),
		namespace: rpNamespace, name: sourceName,
	}
	clusterRoleTarget := overrideTarget{
		gvk:  rbacv1.SchemeGroupVersion.WithKind(clusterRoleKind),
		gvr:  rbacv1.SchemeGroupVersion.WithResource("clusterroles"),
		name: sourceName,
	}
	bindingTarget := overrideTarget{
		gvk:       rbacv1.SchemeGroupVersion.WithKind(roleBindingKind),
		namespace: rpNamespace, name: sourceName,
	}
	clusterBindingTarget := overrideTarget{
		gvk:  rbacv1.SchemeGroupVersion.WithKind(clusterRoleBindingKind),
		name: sourceName,
	}
	patchRule := placementv1beta1.OverrideRule{
		ClusterSelector:    &placementv1beta1.ClusterSelector{},
		OverrideType:       placementv1beta1.JSONPatchOverrideType,
		JSONPatchOverrides: []placementv1beta1.JSONPatchOverride{{Path: "/rules"}},
	}
	deleteRule := placementv1beta1.OverrideRule{
		ClusterSelector: &placementv1beta1.ClusterSelector{},
		OverrideType:    placementv1beta1.DeleteOverrideType,
	}
	dependencyErr := errors.New("bind attributes unavailable")
	tests := []struct {
		name    string
		targets []overrideTarget
		rule    placementv1beta1.OverrideRule
		want    []authorizationv1.ResourceAttributes
		wantErr bool
	}{
		{
			name:    "non-RBAC group ignored",
			targets: []overrideTarget{{gvk: schema.GroupVersionKind{Group: "other.example", Version: "v1", Kind: roleKind}}},
			rule:    patchRule, want: nil, wantErr: false,
		},
		{
			name:    "other RBAC kind ignored",
			targets: []overrideTarget{{gvk: rbacv1.SchemeGroupVersion.WithKind("Other")}},
			rule:    patchRule, want: nil, wantErr: false,
		},
		{
			name:    "role targets require escalation",
			targets: []overrideTarget{roleTarget, clusterRoleTarget}, rule: patchRule,
			want: []authorizationv1.ResourceAttributes{
				{Group: rbacv1.GroupName, Version: "v1", Resource: "roles", Verb: "escalate", Namespace: rpNamespace, Name: sourceName},
				{Group: rbacv1.GroupName, Version: "v1", Resource: "clusterroles", Verb: "escalate", Name: sourceName},
			},
			wantErr: false,
		},
		{
			name:    "delete requires no RBAC privilege checks",
			targets: []overrideTarget{roleTarget, clusterRoleTarget, bindingTarget, clusterBindingTarget},
			rule:    deleteRule, want: nil, wantErr: false,
		},
		{
			name:    "binding targets require unrestricted bind",
			targets: []overrideTarget{bindingTarget, clusterBindingTarget}, rule: patchRule,
			// An empty Name requires bind permission without a resource-name restriction.
			want: []authorizationv1.ResourceAttributes{
				{Group: rbacv1.GroupName, Version: "v1", Resource: "clusterroles", Verb: bindVerb, Namespace: rpNamespace, Name: ""},
				{Group: rbacv1.GroupName, Version: "v1", Resource: "roles", Verb: bindVerb, Namespace: rpNamespace, Name: ""},
				{Group: rbacv1.GroupName, Version: "v1", Resource: "clusterroles", Verb: bindVerb, Namespace: "", Name: ""},
			},
			wantErr: false,
		},
		{
			name:    "binding error discards earlier escalation checks",
			targets: []overrideTarget{roleTarget, bindingTarget}, rule: patchRule,
			want: nil, wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{rbacv1.SchemeGroupVersion})
			mapper.Add(rbacv1.SchemeGroupVersion.WithKind(roleKind), meta.RESTScopeNamespace)
			mapper.Add(rbacv1.SchemeGroupVersion.WithKind(clusterRoleKind), meta.RESTScopeRoot)
			var restMapper meta.RESTMapper = mapper
			if tt.wantErr {
				restMapper = bindingChecksErrorMapper{RESTMapper: mapper, err: dependencyErr}
			}
			a := &OverrideAuthorizer{reviewer: &Reviewer{restMapper: restMapper}}
			policy := &placementv1beta1.OverridePolicy{OverrideRules: []placementv1beta1.OverrideRule{tt.rule}}
			got, err := a.rbacChecks(tt.targets, policy)
			if (err != nil) != tt.wantErr {
				t.Fatalf("rbacChecks() error = %v, want error %t", err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, dependencyErr) {
				t.Errorf("rbacChecks() error = %v, want %v", err, dependencyErr)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("rbacChecks() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestMutationChecks(t *testing.T) {
	const roleName = "reader"
	firstTarget := overrideTarget{
		gvr:       corev1.SchemeGroupVersion.WithResource(crpConfigMaps),
		namespace: rpNamespace, name: "source",
	}
	secondTarget := overrideTarget{
		gvr:       rbacv1.SchemeGroupVersion.WithResource("clusterroles"),
		namespace: "", name: roleName,
	}
	patchRule := placementv1beta1.OverrideRule{
		ClusterSelector: &placementv1beta1.ClusterSelector{},
		OverrideType:    placementv1beta1.JSONPatchOverrideType,
	}
	deleteRule := placementv1beta1.OverrideRule{
		ClusterSelector: &placementv1beta1.ClusterSelector{},
		OverrideType:    placementv1beta1.DeleteOverrideType,
	}
	tests := []struct {
		name    string
		targets []overrideTarget
		rules   []placementv1beta1.OverrideRule
		want    []authorizationv1.ResourceAttributes
	}{
		{name: "no targets", targets: nil, rules: []placementv1beta1.OverrideRule{patchRule}, want: nil},
		{name: "no verbs", targets: []overrideTarget{firstTarget}, rules: nil, want: nil},
		{
			name:    "one target and verb",
			targets: []overrideTarget{firstTarget}, rules: []placementv1beta1.OverrideRule{patchRule},
			want: []authorizationv1.ResourceAttributes{
				{Group: "", Version: "v1", Resource: crpConfigMaps, Verb: "patch", Namespace: rpNamespace, Name: "source"},
			},
		},
		{
			name:    "targets and verbs form Cartesian product",
			targets: []overrideTarget{firstTarget, secondTarget},
			rules:   []placementv1beta1.OverrideRule{patchRule, deleteRule},
			want: []authorizationv1.ResourceAttributes{
				{Group: "", Version: "v1", Resource: crpConfigMaps, Verb: "patch", Namespace: rpNamespace, Name: "source"},
				{Group: "", Version: "v1", Resource: crpConfigMaps, Verb: "delete", Namespace: rpNamespace, Name: "source"},
				{Group: rbacv1.GroupName, Version: "v1", Resource: "clusterroles", Verb: "patch", Namespace: "", Name: roleName},
				{Group: rbacv1.GroupName, Version: "v1", Resource: "clusterroles", Verb: "delete", Namespace: "", Name: roleName},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := &placementv1beta1.OverridePolicy{OverrideRules: tt.rules}
			got := mutationChecks(tt.targets, policy)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("mutationChecks() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestOverrideAuthorize(t *testing.T) {
	mappingErr := errors.New("bind mapping unavailable")
	tests := []struct {
		name         string
		wrongScope   bool
		rbacError    bool
		deny         bool
		wantDecision Decision
		wantReviews  bool
	}{
		{name: "resolution error", wrongScope: true, wantDecision: DecisionInvalid, wantReviews: false},
		{name: "RBAC check error", rbacError: true, wantDecision: DecisionIndeterminate, wantReviews: false},
		{name: "combined checks allowed", wantDecision: DecisionAllowed, wantReviews: true},
		{name: "reviewer denial returned", deny: true, wantDecision: DecisionDenied, wantReviews: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{rbacv1.SchemeGroupVersion})
			scope := meta.RESTScopeNamespace
			if tt.wrongScope {
				scope = meta.RESTScopeRoot
			}
			mapper.Add(rbacv1.SchemeGroupVersion.WithKind(roleBindingKind), scope)
			mapper.Add(rbacv1.SchemeGroupVersion.WithKind(roleKind), meta.RESTScopeNamespace)
			mapper.Add(rbacv1.SchemeGroupVersion.WithKind(clusterRoleKind), meta.RESTScopeRoot)
			var reviewerMapper meta.RESTMapper = mapper
			if tt.rbacError {
				reviewerMapper = bindingChecksErrorMapper{RESTMapper: mapper, err: mappingErr}
			}
			user := authenticationv1.UserInfo{Username: rpRequester, UID: "requester-uid", Groups: []string{"developers"}}
			var reviews []authorizationv1.SubjectAccessReviewSpec
			reviewer, err := NewReviewer(&fakeSubjectAccessReviewClient{
				create: func(_ context.Context, review *authorizationv1.SubjectAccessReview) error {
					reviews = append(reviews, *review.Spec.DeepCopy())
					review.Status.Allowed = !tt.deny
					review.Status.Denied = tt.deny
					return nil
				},
			}, reviewerMapper, 1)
			if err != nil {
				t.Fatal(err)
			}
			a := &OverrideAuthorizer{restMapper: mapper, reviewer: reviewer}
			override := &placementv1beta1.ResourceOverride{
				ObjectMeta: metav1.ObjectMeta{Namespace: rpNamespace},
				Spec: placementv1beta1.ResourceOverrideSpec{
					ResourceSelectors: []placementv1beta1.ResourceSelector{{
						Group: rbacv1.GroupName, Version: "v1", Kind: roleBindingKind, Name: "source",
					}},
					Policy: &placementv1beta1.OverridePolicy{OverrideRules: []placementv1beta1.OverrideRule{{
						ClusterSelector: &placementv1beta1.ClusterSelector{},
						OverrideType:    placementv1beta1.JSONPatchOverrideType,
					}}},
				},
			}
			result := a.Authorize(context.Background(), user, override)
			if result.Decision != tt.wantDecision {
				t.Fatalf("Authorize() decision = %v, want %v", result.Decision, tt.wantDecision)
			}
			if tt.wrongScope && result.Err == nil {
				t.Error("Authorize() error = nil, want resolution error")
			}
			if tt.rbacError && !errors.Is(result.Err, mappingErr) {
				t.Errorf("Authorize() error = %v, want %v", result.Err, mappingErr)
			}
			if tt.deny && result.DenialReason == "" {
				t.Error("Authorize() denial reason is empty")
			}
			if !tt.wrongScope && !tt.rbacError && result.Err != nil {
				t.Errorf("Authorize() error = %v, want nil", result.Err)
			}
			var want []authorizationv1.SubjectAccessReviewSpec
			if tt.wantReviews {
				for _, attributes := range []authorizationv1.ResourceAttributes{
					{Group: rbacv1.GroupName, Version: "v1", Resource: "rolebindings", Verb: "patch", Namespace: rpNamespace, Name: "source"},
					{Group: rbacv1.GroupName, Version: "v1", Resource: "clusterroles", Verb: "bind", Namespace: rpNamespace, Name: ""},
					{Group: rbacv1.GroupName, Version: "v1", Resource: "roles", Verb: "bind", Namespace: rpNamespace, Name: ""},
				} {
					want = append(want, authorizationv1.SubjectAccessReviewSpec{
						User: user.Username, UID: user.UID, Groups: user.Groups, ResourceAttributes: &attributes,
					})
				}
			}
			if diff := cmp.Diff(want, reviews); diff != "" {
				t.Errorf("Authorize() reviews mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

type namespaceExpansionTestMapper struct {
	meta.RESTMapper
	errorResource schema.GroupVersionResource
	err           error
}

func (m namespaceExpansionTestMapper) KindFor(resource schema.GroupVersionResource) (schema.GroupVersionKind, error) {
	if resource == m.errorResource && m.err != nil {
		return schema.GroupVersionKind{}, m.err
	}
	return m.RESTMapper.KindFor(resource)
}

type bindingChecksErrorMapper struct {
	meta.RESTMapper
	err error
}

func (m bindingChecksErrorMapper) RESTMapping(schema.GroupKind, ...string) (*meta.RESTMapping, error) {
	return nil, m.err
}
