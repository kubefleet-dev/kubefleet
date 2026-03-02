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

package v1

// Canary integration tests for v1 API CEL validation rules.
// These verify that the same CEL rules applied to v1beta1 types also work
// on the v1 API version, catching any version-specific regressions.

import (
	"errors"
	"fmt"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	placementv1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1"
)

const (
	crpNameTemplate = "test-crp-v1-%d"
	croNameTemplate = "test-cro-v1-%d"
)

var _ = Describe("v1 API CEL validation canary tests", func() {
	// CRP: PickFixed cluster name DNS validation
	It("should deny v1 CRP with invalid PickFixed cluster name", func() {
		crp := placementv1.ClusterResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess()),
			},
			Spec: placementv1.ClusterResourcePlacementSpec{
				ResourceSelectors: []placementv1.ClusterResourceSelector{{Group: "", Version: "v1", Kind: "Namespace", Name: "test"}},
				Policy: &placementv1.PlacementPolicy{
					PlacementType: placementv1.PickFixedPlacementType,
					ClusterNames:  []string{"INVALID_NAME"},
				},
			},
		}
		err := hubClient.Create(ctx, &crp)
		var statusErr *k8sErrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create CRP call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8sErrors.StatusError{})))
		Expect(statusErr.ErrStatus.Message).Should(MatchRegexp("must be a valid DNS subdomain"))
	})

	// CRP: toleration key format validation
	It("should deny v1 CRP with invalid toleration key", func() {
		crp := placementv1.ClusterResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess()),
			},
			Spec: placementv1.ClusterResourcePlacementSpec{
				ResourceSelectors: []placementv1.ClusterResourceSelector{{Group: "", Version: "v1", Kind: "Namespace", Name: "test"}},
				Policy: &placementv1.PlacementPolicy{
					PlacementType: placementv1.PickAllPlacementType,
					Tolerations: []placementv1.Toleration{
						{Key: "invalid key!"},
					},
				},
			},
		}
		err := hubClient.Create(ctx, &crp)
		var statusErr *k8sErrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create CRP call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8sErrors.StatusError{})))
		Expect(statusErr.ErrStatus.Message).Should(MatchRegexp("toleration key must be a valid qualified name"))
	})

	// CRO: JSON patch path trailing slash validation
	It("should deny v1 CRO with trailing slash in JSON patch path", func() {
		cro := placementv1.ClusterResourceOverride{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf(croNameTemplate, GinkgoParallelProcess()),
			},
			Spec: placementv1.ClusterResourceOverrideSpec{
				ClusterResourceSelectors: []placementv1.ClusterResourceSelector{
					{Group: "", Version: "v1", Kind: "ConfigMap", Name: "test-cm"},
				},
				Policy: &placementv1.OverridePolicy{
					OverrideRules: []placementv1.OverrideRule{
						{
							JSONPatchOverrides: []placementv1.JSONPatchOverride{
								{
									Operator: placementv1.JSONPatchOverrideOpAdd,
									Path:     "/metadata/labels/",
									Value:    apiextensionsv1.JSON{Raw: []byte(`"val"`)},
								},
							},
						},
					},
				},
			},
		}
		err := hubClient.Create(ctx, &cro)
		var statusErr *k8sErrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create CRO call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8sErrors.StatusError{})))
		Expect(statusErr.ErrStatus.Message).Should(MatchRegexp("path cannot have a trailing slash"))
	})

	// CRO: duplicate selector uniqueness validation
	It("should deny v1 CRO with duplicate clusterResourceSelectors", func() {
		cro := placementv1.ClusterResourceOverride{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf(croNameTemplate, GinkgoParallelProcess()),
			},
			Spec: placementv1.ClusterResourceOverrideSpec{
				ClusterResourceSelectors: []placementv1.ClusterResourceSelector{
					{Group: "", Version: "v1", Kind: "ConfigMap", Name: "test-cm"},
					{Group: "", Version: "v1", Kind: "ConfigMap", Name: "test-cm"},
				},
				Policy: &placementv1.OverridePolicy{
					OverrideRules: []placementv1.OverrideRule{
						{OverrideType: placementv1.DeleteOverrideType},
					},
				},
			},
		}
		err := hubClient.Create(ctx, &cro)
		var statusErr *k8sErrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create CRO call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8sErrors.StatusError{})))
		Expect(statusErr.ErrStatus.Message).Should(MatchRegexp("clusterResourceSelectors must be unique"))
	})

	// CRP: labelSelector matchExpressions In with empty values
	It("should deny v1 CRP with In operator and empty values in resource selector", func() {
		crp := placementv1.ClusterResourcePlacement{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf(crpNameTemplate, GinkgoParallelProcess()),
			},
			Spec: placementv1.ClusterResourcePlacementSpec{
				ResourceSelectors: []placementv1.ClusterResourceSelector{
					{
						Group: "", Version: "v1", Kind: "Namespace",
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{Key: "env", Operator: metav1.LabelSelectorOpIn, Values: []string{}},
							},
						},
					},
				},
			},
		}
		err := hubClient.Create(ctx, &crp)
		var statusErr *k8sErrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create CRP call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8sErrors.StatusError{})))
		Expect(statusErr.ErrStatus.Message).Should(MatchRegexp("matchExpressions values must be non-empty when operator is In or NotIn"))
	})
})
