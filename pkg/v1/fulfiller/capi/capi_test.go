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

package capi

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/v1/fulfiller"
)

func request(params map[string]string, terms ...kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm) fulfiller.Request {
	base := map[string]string{ClusterClassParameter: "standard", KubernetesVersionParameter: "v1.33.0", IdentityParameter: "member-sa"}
	for k, v := range params {
		base[k] = v
	}
	return fulfiller.Request{ClaimName: "app-0-abc", ClaimUID: "uid-1", ClusterName: "app-0-abc-0123456789ab", ClusterProviderClassName: "class", Parameters: base, ClusterSelectorTerms: terms}
}

func labels(kv ...string) kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm {
	term := kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{MatchLabels: map[string]string{}}
	for i := 0; i < len(kv); i += 2 {
		term.MatchLabels[kv[i]] = kv[i+1]
	}
	return term
}

func TestRender(t *testing.T) {
	testCases := []struct {
		name          string
		req           fulfiller.Request
		wantTopology  map[string]any
		wantLabels    map[string]string
		wantNamespace string
		wantErr       string
		wantPermanent bool
	}{
		{
			name:          "region maps to the region variable by default, other labels ride along",
			req:           request(nil, labels(defaultRegionLabel, "eastus", "env", "prod")),
			wantTopology:  map[string]any{"class": "standard", "version": "v1.33.0", "variables": []any{map[string]any{"name": "region", "value": "eastus"}}},
			wantLabels:    map[string]string{defaultRegionLabel: "eastus", "env": "prod", kfplacementv1alpha1.FulfilledClaimNameLabel: "app-0-abc", kfplacementv1alpha1.FulfilledClaimUIDLabel: "uid-1"},
			wantNamespace: defaultNamespace,
		},
		{
			name:          "a class maps its own variables and namespace",
			req:           request(map[string]string{NamespaceParameter: "clusters", VariableParameterPrefix + "tier": "env", VariableParameterPrefix + "region": ""}, labels(defaultRegionLabel, "eastus", "env", "prod")),
			wantTopology:  map[string]any{"class": "standard", "version": "v1.33.0", "variables": []any{map[string]any{"name": "tier", "value": "prod"}}},
			wantLabels:    map[string]string{defaultRegionLabel: "eastus", "env": "prod", kfplacementv1alpha1.FulfilledClaimNameLabel: "app-0-abc", kfplacementv1alpha1.FulfilledClaimUIDLabel: "uid-1"},
			wantNamespace: "clusters",
		},
		{
			name:          "no terms renders no variables",
			req:           request(nil),
			wantTopology:  map[string]any{"class": "standard", "version": "v1.33.0"},
			wantLabels:    map[string]string{kfplacementv1alpha1.FulfilledClaimNameLabel: "app-0-abc", kfplacementv1alpha1.FulfilledClaimUIDLabel: "uid-1"},
			wantNamespace: defaultNamespace,
		},
		{
			name:          "a selector term cannot forge the ownership labels",
			req:           request(nil, labels(kfplacementv1alpha1.FulfilledClaimUIDLabel, "forged")),
			wantTopology:  map[string]any{"class": "standard", "version": "v1.33.0"},
			wantLabels:    map[string]string{kfplacementv1alpha1.FulfilledClaimNameLabel: "app-0-abc", kfplacementv1alpha1.FulfilledClaimUIDLabel: "uid-1"},
			wantNamespace: defaultNamespace,
		},
		{name: "two variables mapped to one label is a class error", req: request(map[string]string{VariableParameterPrefix + "a": "env", VariableParameterPrefix + "b": "env"}, labels("env", "prod")), wantErr: `maps the label "env" to both`, wantPermanent: true},
		{
			name:          "a variable may take over the region label from the default",
			req:           request(map[string]string{VariableParameterPrefix + "location": defaultRegionLabel}, labels(defaultRegionLabel, "eastus")),
			wantTopology:  map[string]any{"class": "standard", "version": "v1.33.0", "variables": []any{map[string]any{"name": "location", "value": "eastus"}}},
			wantLabels:    map[string]string{defaultRegionLabel: "eastus", kfplacementv1alpha1.FulfilledClaimNameLabel: "app-0-abc", kfplacementv1alpha1.FulfilledClaimUIDLabel: "uid-1"},
			wantNamespace: defaultNamespace,
		},
		{name: "a property term is unsupported", req: request(nil, kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{MatchClusterPropertyExpressions: []kfplacementv1alpha1.LabelClusterPropertyExpression{{Key: "k", Operator: "Ge", Values: []string{"8"}}}}), wantErr: "unsupported term", wantPermanent: true},
		{name: "a label expression is unsupported", req: request(nil, kfplacementv1alpha1.ClusterLabelAndPropertySelectorTerm{MatchLabelExpressions: []kfplacementv1alpha1.LabelClusterPropertyExpression{{Key: "k", Operator: "In", Values: []string{"a"}}}}), wantErr: "unsupported term", wantPermanent: true},
		{name: "no class name", req: request(map[string]string{ClusterClassParameter: ""}), wantErr: `no "clusterClassName" parameter`, wantPermanent: true},
		{name: "no version", req: request(map[string]string{KubernetesVersionParameter: ""}), wantErr: `no "kubernetesVersion" parameter`, wantPermanent: true},
		{name: "no identity", req: request(map[string]string{IdentityParameter: ""}), wantErr: `no "identity" parameter`, wantPermanent: true},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := render(tc.req)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) || fulfiller.IsPermanent(err) != tc.wantPermanent {
					t.Fatalf("render() = %v (permanent=%t), want an error containing %q, permanent=%t", err, fulfiller.IsPermanent(err), tc.wantErr, tc.wantPermanent)
				}
				return
			}
			if err != nil {
				t.Fatalf("render() = %v, want no error", err)
			}
			topology, _, _ := unstructured.NestedMap(got.cluster.Object, "spec", "topology")
			if diff := cmp.Diff(topology, tc.wantTopology); diff != "" {
				t.Errorf("render() topology mismatch (-got, +want):\n%s", diff)
			}
			if diff := cmp.Diff(got.cluster.GetLabels(), tc.wantLabels); diff != "" {
				t.Errorf("render() labels mismatch (-got, +want):\n%s", diff)
			}
			if got.cluster.GetNamespace() != tc.wantNamespace || got.cluster.GetName() != tc.req.ClusterName || got.cluster.GroupVersionKind() != ClusterGVK {
				t.Errorf("render() = %s %s/%s, want %s %s/%s", got.cluster.GroupVersionKind(), got.cluster.GetNamespace(), got.cluster.GetName(), ClusterGVK, tc.wantNamespace, tc.req.ClusterName)
			}
			if got.identity != "member-sa" {
				t.Errorf("render() identity = %q, want member-sa", got.identity)
			}
		})
	}
}

func TestPhaseOutcome(t *testing.T) {
	withStatus := func(fields map[string]any) *unstructured.Unstructured {
		u := &unstructured.Unstructured{Object: map[string]any{}}
		if fields != nil {
			u.Object["status"] = fields
		}
		return u
	}
	testCases := []struct {
		name          string
		status        map[string]any
		wantErr       bool
		wantPermanent bool
	}{
		{name: "no status yet", status: nil, wantErr: true},
		{name: "pending", status: map[string]any{"phase": "Pending"}, wantErr: true},
		{name: "provisioning", status: map[string]any{"phase": "Provisioning"}, wantErr: true},
		{name: "provisioned", status: map[string]any{"phase": "Provisioned"}},
		{name: "deleting", status: map[string]any{"phase": "Deleting"}, wantErr: true},
		{name: "unknown", status: map[string]any{"phase": "Unknown"}, wantErr: true},
		{name: "failed", status: map[string]any{"phase": "Failed"}, wantErr: true, wantPermanent: true},
		{name: "a failure reason is fatal in any phase", status: map[string]any{"phase": "Provisioning", "failureReason": "InvalidConfiguration", "failureMessage": "no such class"}, wantErr: true, wantPermanent: true},
		{name: "a failure message alone is fatal too", status: map[string]any{"phase": "Deleting", "failureMessage": "no such class"}, wantErr: true, wantPermanent: true},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := phaseOutcome(withStatus(tc.status))
			if (err != nil) != tc.wantErr || fulfiller.IsPermanent(err) != tc.wantPermanent {
				t.Errorf("phaseOutcome(%v) = %v (permanent=%t), want error=%t permanent=%t", tc.status, err, fulfiller.IsPermanent(err), tc.wantErr, tc.wantPermanent)
			}
		})
	}
}
