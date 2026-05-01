package validator

import (
	"fmt"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

func TestValidateClusterResourceOverrideResourceLimit(t *testing.T) {
	tests := map[string]struct {
		cro           placementv1beta1.ClusterResourceOverride
		overrideCount int
		wantErrMsg    error
	}{
		"create one cluster resource override for resource foo": {
			cro: placementv1beta1.ClusterResourceOverride{
				ObjectMeta: metav1.ObjectMeta{
					Name: "override-1",
				},
				Spec: placementv1beta1.ClusterResourceOverrideSpec{
					ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
						{
							Group:   "rbac.authorization.k8s.io",
							Version: "v1",
							Kind:    "ClusterRole",
							Name:    "test-cluster-role",
						},
					},
				},
			},
			overrideCount: 1,
			wantErrMsg:    nil,
		},
		"one override, selecting the same resource by other override already": {
			cro: placementv1beta1.ClusterResourceOverride{
				ObjectMeta: metav1.ObjectMeta{
					Name: "override-2",
				},
				Spec: placementv1beta1.ClusterResourceOverrideSpec{
					ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
						{
							Group:   "group",
							Version: "v1",
							Kind:    "kind",
							Name:    "example-0",
						},
						{
							Group:   "group",
							Version: "v1",
							Kind:    "kind",
							Name:    "bar",
						},
					},
				},
			},
			overrideCount: 1,
			wantErrMsg: fmt.Errorf("invalid resource selector %+v: the resource has been selected by both %v and %v, which is not supported",
				placementv1beta1.ResourceSelectorTerm{Group: "group", Version: "v1", Kind: "kind", Name: "example-0"}, "override-2", "override-0"),
		},
		"one override, which exists": {
			cro: placementv1beta1.ClusterResourceOverride{
				ObjectMeta: metav1.ObjectMeta{
					Name: "override-1",
				},
				Spec: placementv1beta1.ClusterResourceOverrideSpec{
					ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
						{
							Group:   "rbac.authorization.k8s.io",
							Version: "v1",
							Kind:    "ClusterRole",
							Name:    "test-cluster-role",
						},
					},
				},
			},
			overrideCount: 2,
			wantErrMsg:    nil,
		},
		"croList is empty": {
			cro: placementv1beta1.ClusterResourceOverride{
				ObjectMeta: metav1.ObjectMeta{
					Name: "override-2",
				},
				Spec: placementv1beta1.ClusterResourceOverrideSpec{
					ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
						{
							Group:   "rbac.authorization.k8s.io",
							Version: "v1",
							Kind:    "ClusterRole",
							Name:    "test-cluster-role",
						},
					},
				},
			},
			overrideCount: 0,
			wantErrMsg:    nil,
		},
	}
	for testName, tt := range tests {
		t.Run(testName, func(t *testing.T) {
			croList := &placementv1beta1.ClusterResourceOverrideList{}
			for i := 0; i < tt.overrideCount; i++ {
				cro := placementv1beta1.ClusterResourceOverride{
					ObjectMeta: metav1.ObjectMeta{
						Name: fmt.Sprintf("override-%d", i),
					},
					Spec: placementv1beta1.ClusterResourceOverrideSpec{
						ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
							{
								Group:   "group",
								Version: "v1",
								Kind:    "kind",
								Name:    fmt.Sprintf("example-%d", i),
							},
						},
					},
				}
				croList.Items = append(croList.Items, cro)
			}
			got := validateClusterResourceOverrideResourceLimit(tt.cro, croList)
			if gotErr, wantErr := got != nil, tt.wantErrMsg != nil; gotErr != wantErr {
				t.Fatalf("validateClusterResourceOverrideResourceLimit() = %v, want %v", got, tt.wantErrMsg)
			}

			if got != nil && !strings.Contains(got.Error(), tt.wantErrMsg.Error()) {
				t.Errorf("validateClusterResourceOverrideResourceLimit() = %v, want %v", got, tt.wantErrMsg)
			}
		})
	}
}

func TestValidateClusterResourceOverride(t *testing.T) {
	tests := map[string]struct {
		cro        placementv1beta1.ClusterResourceOverride
		croList    *placementv1beta1.ClusterResourceOverrideList
		wantErrMsg error
	}{
		"valid cluster resource override": {
			cro: placementv1beta1.ClusterResourceOverride{
				Spec: placementv1beta1.ClusterResourceOverrideSpec{
					ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
						{
							Group:   "rbac.authorization.k8s.io",
							Version: "v1",
							Kind:    "ClusterRole",
							Name:    "test-cluster-role",
						},
					},
				},
			},
			croList:    &placementv1beta1.ClusterResourceOverrideList{},
			wantErrMsg: nil,
		},
		"invalid cluster resource override - fail ValidateClusterResourceOverrideResourceLimit": {
			cro: placementv1beta1.ClusterResourceOverride{
				ObjectMeta: metav1.ObjectMeta{
					Name: "override-1",
				},
				Spec: placementv1beta1.ClusterResourceOverrideSpec{
					ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
						{
							Group:   "group",
							Version: "v1",
							Kind:    "kind",
							Name:    "duplicate-example",
						},
					},
				},
			},
			croList: &placementv1beta1.ClusterResourceOverrideList{
				Items: []placementv1beta1.ClusterResourceOverride{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "override-0"},
						Spec: placementv1beta1.ClusterResourceOverrideSpec{
							ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
								{
									Group:   "group",
									Version: "v1",
									Kind:    "kind",
									Name:    "duplicate-example",
								},
							},
						},
					},
				},
			},
			wantErrMsg: fmt.Errorf("invalid resource selector %+v: the resource has been selected by both %v and %v, which is not supported",
				placementv1beta1.ResourceSelectorTerm{Group: "group", Version: "v1", Kind: "kind", Name: "duplicate-example"}, "override-1", "override-0"),
		},
		"valid cluster resource override - empty croList": {
			cro: placementv1beta1.ClusterResourceOverride{
				Spec: placementv1beta1.ClusterResourceOverrideSpec{
					ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
						{
							Group:   "rbac.authorization.k8s.io",
							Version: "v1",
							Kind:    "ClusterRole",
							Name:    "test-cluster-role",
						},
					},
				},
			},
			croList:    &placementv1beta1.ClusterResourceOverrideList{},
			wantErrMsg: nil,
		},
		"valid cluster resource override - croList nil": {
			cro: placementv1beta1.ClusterResourceOverride{
				Spec: placementv1beta1.ClusterResourceOverrideSpec{
					ClusterResourceSelectors: []placementv1beta1.ResourceSelectorTerm{
						{
							Group:   "rbac.authorization.k8s.io",
							Version: "v1",
							Kind:    "ClusterRole",
							Name:    "test-cluster-role",
						},
					},
				},
			},
			croList:    nil,
			wantErrMsg: nil,
		},
	}
	for testName, tt := range tests {
		t.Run(testName, func(t *testing.T) {
			got := ValidateClusterResourceOverride(tt.cro, tt.croList)
			if gotErr, wantErr := got != nil, tt.wantErrMsg != nil; gotErr != wantErr {
				t.Fatalf("ValidateClusterResourceOverride() = %v, want %v", got, tt.wantErrMsg)
			}

			if got != nil && !strings.Contains(got.Error(), tt.wantErrMsg.Error()) {
				t.Errorf("ValidateClusterResourceOverride() = %v, want %v", got, tt.wantErrMsg)
			}
		})
	}
}
