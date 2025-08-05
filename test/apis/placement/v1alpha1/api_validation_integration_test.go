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

package v1alpha1

import (
	"errors"
	"fmt"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	placementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1alpha1"
	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

const (
	croNameTemplate = "test-cro-%d"
	roNameTemplate  = "test-ro-%d"
	testNamespace   = "test-ns"
)

var _ = Describe("Test placement v1alpha1 API validation", func() {
	Context("Test ClusterResourceOverride API validation - valid cases", func() {
		It("should allow creation of ClusterResourceOverride without placement reference", func() {
			cro := placementv1alpha1.ClusterResourceOverride{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf(croNameTemplate, GinkgoParallelProcess()),
				},
				Spec: placementv1alpha1.ClusterResourceOverrideSpec{
					ClusterResourceSelectors: []placementv1beta1.ClusterResourceSelector{
						{
							Group:   "",
							Version: "v1",
							Kind:    "ConfigMap",
							Name:    "test-cm",
						},
					},
					Policy: &placementv1alpha1.OverridePolicy{
						OverrideRules: []placementv1alpha1.OverrideRule{
							{
								OverrideType: placementv1alpha1.JSONPatchOverrideType,
								JSONPatchOverrides: []placementv1alpha1.JSONPatchOverride{
									{
										Operator: placementv1alpha1.JSONPatchOverrideOpAdd,
										Path:     "/metadata/labels/test",
										Value:    apiextensionsv1.JSON{Raw: []byte(`"test-value"`)},
									},
								},
							},
						},
					},
				},
			}
			Expect(hubClient.Create(ctx, &cro)).Should(Succeed())
			Expect(hubClient.Delete(ctx, &cro)).Should(Succeed())
		})

		It("should allow creation of ClusterResourceOverride with cluster-scoped placement reference", func() {
			cro := placementv1alpha1.ClusterResourceOverride{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf(croNameTemplate, GinkgoParallelProcess()),
				},
				Spec: placementv1alpha1.ClusterResourceOverrideSpec{
					Placement: &placementv1alpha1.PlacementRef{
						Name:  "test-placement",
						Scope: placementv1alpha1.ClusterScoped,
					},
					ClusterResourceSelectors: []placementv1beta1.ClusterResourceSelector{
						{
							Group:   "",
							Version: "v1",
							Kind:    "ConfigMap",
							Name:    "test-cm",
						},
					},
					Policy: &placementv1alpha1.OverridePolicy{
						OverrideRules: []placementv1alpha1.OverrideRule{
							{
								OverrideType: placementv1alpha1.JSONPatchOverrideType,
								JSONPatchOverrides: []placementv1alpha1.JSONPatchOverride{
									{
										Operator: placementv1alpha1.JSONPatchOverrideOpAdd,
										Path:     "/metadata/labels/test",
										Value:    apiextensionsv1.JSON{Raw: []byte(`"test-value"`)},
									},
								},
							},
						},
					},
				},
			}
			Expect(hubClient.Create(ctx, &cro)).Should(Succeed())
			Expect(hubClient.Delete(ctx, &cro)).Should(Succeed())
		})

		It("should allow creation of ClusterResourceOverride without specifying scope in placement reference", func() {
			cro := placementv1alpha1.ClusterResourceOverride{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf(croNameTemplate, GinkgoParallelProcess()),
				},
				Spec: placementv1alpha1.ClusterResourceOverrideSpec{
					Placement: &placementv1alpha1.PlacementRef{
						Name: "test-placement",
					},
					ClusterResourceSelectors: []placementv1beta1.ClusterResourceSelector{
						{
							Group:   "",
							Version: "v1",
							Kind:    "ConfigMap",
							Name:    "test-cm",
						},
					},
					Policy: &placementv1alpha1.OverridePolicy{
						OverrideRules: []placementv1alpha1.OverrideRule{
							{
								OverrideType: placementv1alpha1.JSONPatchOverrideType,
								JSONPatchOverrides: []placementv1alpha1.JSONPatchOverride{
									{
										Operator: placementv1alpha1.JSONPatchOverrideOpAdd,
										Path:     "/metadata/labels/test",
										Value:    apiextensionsv1.JSON{Raw: []byte(`"test-value"`)},
									},
								},
							},
						},
					},
				},
			}
			Expect(hubClient.Create(ctx, &cro)).Should(Succeed())
			Expect(hubClient.Delete(ctx, &cro)).Should(Succeed())
		})
	})

	Context("Test ClusterResourceOverride API validation - invalid cases", func() {
		It("should deny creation of ClusterResourceOverride with namespaced placement reference", func() {
			cro := placementv1alpha1.ClusterResourceOverride{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf(croNameTemplate, GinkgoParallelProcess()),
				},
				Spec: placementv1alpha1.ClusterResourceOverrideSpec{
					Placement: &placementv1alpha1.PlacementRef{
						Name:  "test-placement",
						Scope: placementv1alpha1.NamespaceScoped,
					},
					ClusterResourceSelectors: []placementv1beta1.ClusterResourceSelector{
						{
							Group:   "",
							Version: "v1",
							Kind:    "ConfigMap",
							Name:    "test-cm",
						},
					},
					Policy: &placementv1alpha1.OverridePolicy{
						OverrideRules: []placementv1alpha1.OverrideRule{
							{
								OverrideType: placementv1alpha1.JSONPatchOverrideType,
								JSONPatchOverrides: []placementv1alpha1.JSONPatchOverride{
									{
										Operator: placementv1alpha1.JSONPatchOverrideOpAdd,
										Path:     "/metadata/labels/test",
										Value:    apiextensionsv1.JSON{Raw: []byte(`"test-value"`)},
									},
								},
							},
						},
					},
				},
			}
			err := hubClient.Create(ctx, &cro)
			var statusErr *k8sErrors.StatusError
			Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create ClusterResourceOverride call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8sErrors.StatusError{})))
			Expect(statusErr.ErrStatus.Message).Should(MatchRegexp("clusterResourceOverride placement reference cannot be Namespaced scope"))
		})
	})

	Context("Test ResourceOverride API validation - valid cases", func() {
		It("should allow creation of ResourceOverride without placement reference", func() {
			ro := placementv1alpha1.ResourceOverride{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testNamespace,
					Name:      fmt.Sprintf(roNameTemplate, GinkgoParallelProcess()),
				},
				Spec: placementv1alpha1.ResourceOverrideSpec{
					ResourceSelectors: []placementv1alpha1.ResourceSelector{
						{
							Group:   "",
							Version: "v1",
							Kind:    "ConfigMap",
							Name:    "test-cm",
						},
					},
					Policy: &placementv1alpha1.OverridePolicy{
						OverrideRules: []placementv1alpha1.OverrideRule{
							{
								OverrideType: placementv1alpha1.JSONPatchOverrideType,
								JSONPatchOverrides: []placementv1alpha1.JSONPatchOverride{
									{
										Operator: placementv1alpha1.JSONPatchOverrideOpAdd,
										Path:     "/metadata/labels/test",
										Value:    apiextensionsv1.JSON{Raw: []byte(`"test-value"`)},
									},
								},
							},
						},
					},
				},
			}
			Expect(hubClient.Create(ctx, &ro)).Should(Succeed())
			Expect(hubClient.Delete(ctx, &ro)).Should(Succeed())
		})

		It("should allow creation of ResourceOverride with cluster-scoped placement reference", func() {
			ro := placementv1alpha1.ResourceOverride{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testNamespace,
					Name:      fmt.Sprintf(roNameTemplate, GinkgoParallelProcess()),
				},
				Spec: placementv1alpha1.ResourceOverrideSpec{
					Placement: &placementv1alpha1.PlacementRef{
						Name:  "test-placement",
						Scope: placementv1alpha1.ClusterScoped,
					},
					ResourceSelectors: []placementv1alpha1.ResourceSelector{
						{
							Group:   "",
							Version: "v1",
							Kind:    "ConfigMap",
							Name:    "test-cm",
						},
					},
					Policy: &placementv1alpha1.OverridePolicy{
						OverrideRules: []placementv1alpha1.OverrideRule{
							{
								OverrideType: placementv1alpha1.JSONPatchOverrideType,
								JSONPatchOverrides: []placementv1alpha1.JSONPatchOverride{
									{
										Operator: placementv1alpha1.JSONPatchOverrideOpAdd,
										Path:     "/metadata/labels/test",
										Value:    apiextensionsv1.JSON{Raw: []byte(`"test-value"`)},
									},
								},
							},
						},
					},
				},
			}
			Expect(hubClient.Create(ctx, &ro)).Should(Succeed())
			Expect(hubClient.Delete(ctx, &ro)).Should(Succeed())
		})

		It("should allow creation of ResourceOverride without specifying scope in placement reference", func() {
			ro := placementv1alpha1.ResourceOverride{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testNamespace,
					Name:      fmt.Sprintf(roNameTemplate, GinkgoParallelProcess()),
				},
				Spec: placementv1alpha1.ResourceOverrideSpec{
					Placement: &placementv1alpha1.PlacementRef{
						Name: "test-placement",
					},
					ResourceSelectors: []placementv1alpha1.ResourceSelector{
						{
							Group:   "",
							Version: "v1",
							Kind:    "ConfigMap",
							Name:    "test-cm",
						},
					},
					Policy: &placementv1alpha1.OverridePolicy{
						OverrideRules: []placementv1alpha1.OverrideRule{
							{
								OverrideType: placementv1alpha1.JSONPatchOverrideType,
								JSONPatchOverrides: []placementv1alpha1.JSONPatchOverride{
									{
										Operator: placementv1alpha1.JSONPatchOverrideOpAdd,
										Path:     "/metadata/labels/test",
										Value:    apiextensionsv1.JSON{Raw: []byte(`"test-value"`)},
									},
								},
							},
						},
					},
				},
			}
			Expect(hubClient.Create(ctx, &ro)).Should(Succeed())
			Expect(hubClient.Delete(ctx, &ro)).Should(Succeed())
		})

		It("should allow creation of ResourceOverride with namespace-scoped placement reference", func() {
			ro := placementv1alpha1.ResourceOverride{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testNamespace,
					Name:      fmt.Sprintf(roNameTemplate, GinkgoParallelProcess()),
				},
				Spec: placementv1alpha1.ResourceOverrideSpec{
					Placement: &placementv1alpha1.PlacementRef{
						Name:  "test-placement",
						Scope: placementv1alpha1.NamespaceScoped,
					},
					ResourceSelectors: []placementv1alpha1.ResourceSelector{
						{
							Group:   "",
							Version: "v1",
							Kind:    "ConfigMap",
							Name:    "test-cm",
						},
					},
					Policy: &placementv1alpha1.OverridePolicy{
						OverrideRules: []placementv1alpha1.OverrideRule{
							{
								OverrideType: placementv1alpha1.JSONPatchOverrideType,
								JSONPatchOverrides: []placementv1alpha1.JSONPatchOverride{
									{
										Operator: placementv1alpha1.JSONPatchOverrideOpAdd,
										Path:     "/metadata/labels/test",
										Value:    apiextensionsv1.JSON{Raw: []byte(`"test-value"`)},
									},
								},
							},
						},
					},
				},
			}
			Expect(hubClient.Create(ctx, &ro)).Should(Succeed())
			Expect(hubClient.Delete(ctx, &ro)).Should(Succeed())
		})
	})
})
