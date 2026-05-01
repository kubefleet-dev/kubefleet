package validator

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

func TestValidateOverridePolicy(t *testing.T) {
	tests := map[string]struct {
		policy  *placementv1beta1.OverridePolicy
		wantErr bool
	}{
		"valid policy with add operation and value": {
			policy: &placementv1beta1.OverridePolicy{
				OverrideRules: []placementv1beta1.OverrideRule{
					{
						OverrideType: placementv1beta1.JSONPatchOverrideType,
						JSONPatchOverrides: []placementv1beta1.JSONPatchOverride{
							{
								Operator: placementv1beta1.JSONPatchOverrideOpAdd,
								Path:     "/metadata/labels/foo",
								Value:    apiextensionsv1.JSON{Raw: []byte(`"bar"`)},
							},
						},
					},
				},
			},
			wantErr: false,
		},
		"valid policy with remove operation and no value": {
			policy: &placementv1beta1.OverridePolicy{
				OverrideRules: []placementv1beta1.OverrideRule{
					{
						OverrideType: placementv1beta1.JSONPatchOverrideType,
						JSONPatchOverrides: []placementv1beta1.JSONPatchOverride{
							{
								Operator: placementv1beta1.JSONPatchOverrideOpRemove,
								Path:     "/metadata/labels/foo",
							},
						},
					},
				},
			},
			wantErr: false,
		},
		"invalid policy with remove operation and value": {
			policy: &placementv1beta1.OverridePolicy{
				OverrideRules: []placementv1beta1.OverrideRule{
					{
						OverrideType: placementv1beta1.JSONPatchOverrideType,
						JSONPatchOverrides: []placementv1beta1.JSONPatchOverride{
							{
								Operator: placementv1beta1.JSONPatchOverrideOpRemove,
								Path:     "/metadata/labels/foo",
								Value:    apiextensionsv1.JSON{Raw: []byte(`"bar"`)},
							},
						},
					},
				},
			},
			wantErr: true,
		},
		"valid policy with replace operation and value": {
			policy: &placementv1beta1.OverridePolicy{
				OverrideRules: []placementv1beta1.OverrideRule{
					{
						OverrideType: placementv1beta1.JSONPatchOverrideType,
						JSONPatchOverrides: []placementv1beta1.JSONPatchOverride{
							{
								Operator: placementv1beta1.JSONPatchOverrideOpReplace,
								Path:     "/metadata/labels/foo",
								Value:    apiextensionsv1.JSON{Raw: []byte(`"baz"`)},
							},
						},
					},
				},
			},
			wantErr: false,
		},
		"valid policy with delete override type and no patches": {
			policy: &placementv1beta1.OverridePolicy{
				OverrideRules: []placementv1beta1.OverrideRule{
					{
						OverrideType: placementv1beta1.DeleteOverrideType,
					},
				},
			},
			wantErr: false,
		},
		"empty override rules": {
			policy: &placementv1beta1.OverridePolicy{
				OverrideRules: []placementv1beta1.OverrideRule{},
			},
			wantErr: false,
		},
	}
	for testName, tt := range tests {
		t.Run(testName, func(t *testing.T) {
			got := validateOverridePolicy(tt.policy)
			if gotErr, wantErr := len(got) > 0, tt.wantErr; gotErr != wantErr {
				t.Errorf("validateOverridePolicy() = %v, wantErr %v", got, tt.wantErr)
			}
		})
	}
}

func TestValidateOverridePolicyErrorMessage(t *testing.T) {
	policy := &placementv1beta1.OverridePolicy{
		OverrideRules: []placementv1beta1.OverrideRule{
			{
				OverrideType: placementv1beta1.JSONPatchOverrideType,
				JSONPatchOverrides: []placementv1beta1.JSONPatchOverride{
					{
						Operator: placementv1beta1.JSONPatchOverrideOpRemove,
						Path:     "/metadata/labels/foo",
						Value:    apiextensionsv1.JSON{Raw: []byte(`"bar"`)},
					},
				},
			},
		},
	}
	got := validateOverridePolicy(policy)
	if len(got) != 1 {
		t.Fatalf("validateOverridePolicy() returned %d errors, want 1", len(got))
	}
	want := "overrideRules[0].jsonPatchOverrides[0]: remove operation cannot have a value"
	if diff := cmp.Diff(want, got[0].Error()); diff != "" {
		t.Errorf("validateOverridePolicy() error message mismatch (-want +got):\n%s", diff)
	}
}
