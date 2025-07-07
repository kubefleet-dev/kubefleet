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
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	placementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1alpha1"
	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

const (
	croNameTemplate = "test-cro-%d"
	roNameTemplate  = "test-ro-%d"
	nsNameTemplate  = "test-namespace-%d"
)

var (
	validClusterSelector = &placementv1beta1.ClusterSelector{
		ClusterSelectorTerms: []placementv1beta1.ClusterSelectorTerm{
			{
				LabelSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"key": "value",
					},
				},
			},
			{
				LabelSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"key1": "value1",
					},
				},
			},
		},
	}

	validJSONPatchOverrides = []placementv1alpha1.JSONPatchOverride{
		{
			Operator: placementv1alpha1.JSONPatchOverrideOpAdd,
			Path:     "/metadata/labels/new-label",
			Value:    apiextensionsv1.JSON{Raw: []byte(`"new-value"`)},
		},
	}

	invalidJSONPatchOverrides = []placementv1alpha1.JSONPatchOverride{
		{
			Operator: placementv1alpha1.JSONPatchOverrideOpRemove,
			Path:     "/apiVersion",
			Value:    apiextensionsv1.JSON{Raw: []byte(`"new-value"`)},
		},
	}

	policyWithInvalidOverrideRule = &placementv1alpha1.OverridePolicy{
		OverrideRules: []placementv1alpha1.OverrideRule{
			{
				OverrideType:       placementv1alpha1.JSONPatchOverrideType,
				ClusterSelector:    validClusterSelector,
				JSONPatchOverrides: invalidJSONPatchOverrides,
			},
		},
	}
)

var _ = Describe("Test placement v1alpha1 API validation", func() {
	Context("Test ClusterResourceOverride API validation Cases", Ordered, func() {
		croName := fmt.Sprintf(croNameTemplate, GinkgoParallelProcess())
		validClusterResourceSelector := placementv1beta1.ClusterResourceSelector{
			Group:   "rbac.authorization.k8s.io",
			Version: "v1",
			Kind:    "ClusterRole",
			Name:    "test-cluster-role",
		}

		Context("Test ClusterResourceOverride API validation - invalid cases", func() {
			It("Should deny creation of ClusterResourceOverride with delete override and jsonPatchOverrides", func() {
				cro := placementv1alpha1.ClusterResourceOverride{
					ObjectMeta: metav1.ObjectMeta{
						Name: croName,
					},
					Spec: placementv1alpha1.ClusterResourceOverrideSpec{
						ClusterResourceSelectors: []placementv1beta1.ClusterResourceSelector{
							validClusterResourceSelector,
						},
						Policy: &placementv1alpha1.OverridePolicy{
							OverrideRules: []placementv1alpha1.OverrideRule{
								{
									OverrideType:       placementv1alpha1.DeleteOverrideType,
									ClusterSelector:    validClusterSelector,
									JSONPatchOverrides: validJSONPatchOverrides,
								},
							},
						},
					},
				}
				err := hubClient.Create(ctx, &cro)
				var statusErr *k8sErrors.StatusError
				Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create ClusterResourceOverride call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8sErrors.StatusError{})))
				Expect(statusErr.Status().Message).Should(MatchRegexp("jsonPatchOverrides cannot be set when the override type is Delete."))
			})

			It("Should deny creation of ClusterResourceOverride with jsonPatch override and no jsonPatchOverrides", func() {
				cro := placementv1alpha1.ClusterResourceOverride{
					ObjectMeta: metav1.ObjectMeta{
						Name: croName,
					},
					Spec: placementv1alpha1.ClusterResourceOverrideSpec{
						ClusterResourceSelectors: []placementv1beta1.ClusterResourceSelector{
							validClusterResourceSelector,
						},
						Policy: &placementv1alpha1.OverridePolicy{
							OverrideRules: []placementv1alpha1.OverrideRule{
								{
									OverrideType:    placementv1alpha1.JSONPatchOverrideType,
									ClusterSelector: validClusterSelector,
								},
							},
						},
					},
				}
				err := hubClient.Create(ctx, &cro)
				var statusErr *k8sErrors.StatusError
				Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create ClusterResourceOverride call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8sErrors.StatusError{})))
				Expect(statusErr.Status().Message).Should(MatchRegexp("jsonPatchOverrides must be set when the override type is JSONPatch."))
			})

			It("Should deny creation of ClusterResourceOverride with invalid jsonPatchOverrides Paths (/apiVersion)", func() {
				cro := placementv1alpha1.ClusterResourceOverride{
					ObjectMeta: metav1.ObjectMeta{
						Name: croName,
					},
					Spec: placementv1alpha1.ClusterResourceOverrideSpec{
						ClusterResourceSelectors: []placementv1beta1.ClusterResourceSelector{
							validClusterResourceSelector,
						},
						Policy: policyWithInvalidOverrideRule,
					},
				}
				err := hubClient.Create(ctx, &cro)
				var statusErr *k8sErrors.StatusError
				Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create ClusterResourceOverride call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8sErrors.StatusError{})))
				Expect(statusErr.Status().Message).Should(ContainSubstring("Path cannot target typeMeta fields (kind, apiVersion)"))
			})

			It("Should deny creation of ClusterResourceOverride with invalid jsonPatchOverrides Paths (/kind)", func() {
				cro := placementv1alpha1.ClusterResourceOverride{
					ObjectMeta: metav1.ObjectMeta{
						Name: croName,
					},
					Spec: placementv1alpha1.ClusterResourceOverrideSpec{
						ClusterResourceSelectors: []placementv1beta1.ClusterResourceSelector{
							validClusterResourceSelector,
						},
						Policy: policyWithInvalidOverrideRule,
					},
				}
				// Modify the path to target 'kind'
				cro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/kind"
				err := hubClient.Create(ctx, &cro)
				var statusErr *k8sErrors.StatusError
				Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create ClusterResourceOverride call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8sErrors.StatusError{})))
				Expect(statusErr.Status().Message).Should(ContainSubstring("Path cannot target typeMeta fields (kind, apiVersion)"))
			})

			It("Should deny creation of ClusterResourceOverride with invalid jsonPatchOverrides Paths (/metadata)", func() {
				cro := placementv1alpha1.ClusterResourceOverride{
					ObjectMeta: metav1.ObjectMeta{
						Name: croName,
					},
					Spec: placementv1alpha1.ClusterResourceOverrideSpec{
						ClusterResourceSelectors: []placementv1beta1.ClusterResourceSelector{
							validClusterResourceSelector,
						},
						Policy: policyWithInvalidOverrideRule,
					},
				}
				// Modify the path to target 'metadata'
				cro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/metadata"
				err := hubClient.Create(ctx, &cro)
				var statusErr *k8sErrors.StatusError
				Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create ClusterResourceOverride call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8sErrors.StatusError{})))
				Expect(statusErr.Status().Message).Should(ContainSubstring("Path can only target annotations and labels under metadata"))
			})

			It("Should deny creation of ClusterResourceOverride with invalid jsonPatchOverrides Paths (/metadata/name)", func() {
				cro := placementv1alpha1.ClusterResourceOverride{
					ObjectMeta: metav1.ObjectMeta{
						Name: croName,
					},
					Spec: placementv1alpha1.ClusterResourceOverrideSpec{
						ClusterResourceSelectors: []placementv1beta1.ClusterResourceSelector{
							validClusterResourceSelector,
						},
						Policy: policyWithInvalidOverrideRule,
					},
				}
				// Modify the path to target 'metadata.name'
				cro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/metadata/name"
				err := hubClient.Create(ctx, &cro)
				var statusErr *k8sErrors.StatusError
				Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create ClusterResourceOverride call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8sErrors.StatusError{})))
				Expect(statusErr.Status().Message).Should(ContainSubstring("Path can only target annotations and labels under metadata"))
			})

			It("Should deny creation of ClusterResourceOverride with invalid jsonPatchOverrides Paths (/status)", func() {
				cro := placementv1alpha1.ClusterResourceOverride{
					ObjectMeta: metav1.ObjectMeta{
						Name: croName,
					},
					Spec: placementv1alpha1.ClusterResourceOverrideSpec{
						ClusterResourceSelectors: []placementv1beta1.ClusterResourceSelector{
							validClusterResourceSelector,
						},
						Policy: policyWithInvalidOverrideRule,
					},
				}
				// Modify the path to target 'status'
				cro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/status/conditions/0/type"
				err := hubClient.Create(ctx, &cro)
				var statusErr *k8sErrors.StatusError
				Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create ClusterResourceOverride call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8sErrors.StatusError{})))
				Expect(statusErr.Status().Message).Should(ContainSubstring("Path cannot target status fields"))
			})
		})

		Context("Test ClusterResourceOverride API validation - valid cases", func() {
			It("Should allow creation of ClusterResourceOverride  with valid jsonPatchOverrides Paths (/metadata/labels)", func() {
				cro := placementv1alpha1.ClusterResourceOverride{
					ObjectMeta: metav1.ObjectMeta{
						Name: croName,
					},
					Spec: placementv1alpha1.ClusterResourceOverrideSpec{
						ClusterResourceSelectors: []placementv1beta1.ClusterResourceSelector{
							validClusterResourceSelector,
						},
						Policy: policyWithInvalidOverrideRule,
					},
				}
				// Modify the path to target 'metadata.name'
				cro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/metadata/labels/label"
				Expect(hubClient.Create(ctx, &cro)).Should(Succeed(), "Create ClusterResourceOverride call produced error")
			})
		})
	})

	Context("Test ResourceOverride API validation Cases", Ordered, func() {
		roName := fmt.Sprintf(roNameTemplate, GinkgoParallelProcess())
		nsName := fmt.Sprintf(nsNameTemplate, GinkgoParallelProcess())
		validResourceSelector := placementv1alpha1.ResourceSelector{
			Group:   "apps",
			Version: "v1",
			Kind:    "Deployment",
			Name:    "test-deployment",
		}

		BeforeAll(func() {
			By("Create the test namespace")
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: nsName,
				},
			}
			Expect(hubClient.Create(ctx, ns)).Should(Succeed(), "Failed to create test namespace for ResourceOverride tests")
		})

		AfterAll(func() {
			By("Delete the test namespace")
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: nsName,
				},
			}
			Expect(hubClient.Delete(ctx, ns)).Should(Succeed(), "Failed to delete test namespace for ResourceOverride tests")
		})

		Context("Test ResourceOverride API validation - valid cases", func() {
			AfterEach(func() {
				// Clean up ResourceOverride after each test
				ro := &placementv1alpha1.ResourceOverride{
					ObjectMeta: metav1.ObjectMeta{
						Name:      roName,
						Namespace: nsName,
					},
				}
				Expect(hubClient.Delete(ctx, ro)).Should(Succeed(), "Failed to delete ResourceOverride after test")
			})

			It("Should allow creation of ResourceOverride  with valid jsonPatchOverrides Paths (/metadata/labels)", func() {
				ro := placementv1alpha1.ResourceOverride{
					ObjectMeta: metav1.ObjectMeta{
						Name:      roName,
						Namespace: nsName,
					},
					Spec: placementv1alpha1.ResourceOverrideSpec{
						ResourceSelectors: []placementv1alpha1.ResourceSelector{
							validResourceSelector,
						},
						Policy: policyWithInvalidOverrideRule,
					},
				}
				// Modify the path to target 'metadata.name'
				ro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/metadata/labels/label"
				Expect(hubClient.Create(ctx, &ro)).Should(Succeed(), "Create ResourceOverride call produced error")
			})

			It("Should allow creation of ResourceOverride with delete override and no jsonPatchOverrides", func() {
				ro := placementv1alpha1.ResourceOverride{
					ObjectMeta: metav1.ObjectMeta{
						Name:      roName,
						Namespace: nsName,
					},
					Spec: placementv1alpha1.ResourceOverrideSpec{
						ResourceSelectors: []placementv1alpha1.ResourceSelector{
							validResourceSelector,
						},
						Policy: &placementv1alpha1.OverridePolicy{
							OverrideRules: []placementv1alpha1.OverrideRule{
								{
									OverrideType:    placementv1alpha1.DeleteOverrideType,
									ClusterSelector: validClusterSelector,
								},
							},
						},
					},
				}
				Expect(hubClient.Create(ctx, &ro)).Should(Succeed(), "Create ResourceOverride call produced error")
			})
		})

		Context("Test ResourceOverride API validation - invalid cases", func() {
			It("Should deny creation of ResourceOverride with delete override and jsonPatchOverrides", func() {
				ro := placementv1alpha1.ResourceOverride{
					ObjectMeta: metav1.ObjectMeta{
						Name:      roName,
						Namespace: nsName,
					},
					Spec: placementv1alpha1.ResourceOverrideSpec{
						ResourceSelectors: []placementv1alpha1.ResourceSelector{
							validResourceSelector,
						},
						Policy: &placementv1alpha1.OverridePolicy{
							OverrideRules: []placementv1alpha1.OverrideRule{
								{
									OverrideType:       placementv1alpha1.DeleteOverrideType,
									ClusterSelector:    validClusterSelector,
									JSONPatchOverrides: validJSONPatchOverrides,
								},
							},
						},
					},
				}
				err := hubClient.Create(ctx, &ro)
				var statusErr *k8sErrors.StatusError
				Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create ResourceOverride call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8sErrors.StatusError{})))
				Expect(statusErr.Status().Message).Should(MatchRegexp("jsonPatchOverrides cannot be set when the override type is Delete."))
			})

			It("Should deny creation of ResourceOverride with jsonPatch override and no jsonPatchOverrides", func() {
				ro := placementv1alpha1.ResourceOverride{
					ObjectMeta: metav1.ObjectMeta{
						Name:      roName,
						Namespace: nsName,
					},
					Spec: placementv1alpha1.ResourceOverrideSpec{
						ResourceSelectors: []placementv1alpha1.ResourceSelector{
							validResourceSelector,
						},
						Policy: &placementv1alpha1.OverridePolicy{
							OverrideRules: []placementv1alpha1.OverrideRule{
								{
									OverrideType:    placementv1alpha1.JSONPatchOverrideType,
									ClusterSelector: validClusterSelector,
								},
							},
						},
					},
				}
				err := hubClient.Create(ctx, &ro)
				var statusErr *k8sErrors.StatusError
				Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create ResourceOverride call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8sErrors.StatusError{})))
				Expect(statusErr.Status().Message).Should(MatchRegexp("jsonPatchOverrides must be set when the override type is JSONPatch."))
			})

			It("Should deny creation of ResourceOverride with invalid jsonPatchOverrides Paths (/apiVersion)", func() {
				ro := placementv1alpha1.ResourceOverride{
					ObjectMeta: metav1.ObjectMeta{
						Name:      roName,
						Namespace: nsName,
					},
					Spec: placementv1alpha1.ResourceOverrideSpec{
						ResourceSelectors: []placementv1alpha1.ResourceSelector{
							validResourceSelector,
						},
						Policy: policyWithInvalidOverrideRule,
					},
				}
				// Modify the path to target 'apiVersion'
				ro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/apiVersion"
				err := hubClient.Create(ctx, &ro)
				var statusErr *k8sErrors.StatusError
				Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create esourceOverride call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8sErrors.StatusError{})))
				Expect(statusErr.Status().Message).Should(ContainSubstring("Path cannot target typeMeta fields (kind, apiVersion)"))
			})

			It("Should deny creation of ResourceOverride with invalid jsonPatchOverrides Paths (/kind)", func() {
				ro := placementv1alpha1.ResourceOverride{
					ObjectMeta: metav1.ObjectMeta{
						Name:      roName,
						Namespace: nsName,
					},
					Spec: placementv1alpha1.ResourceOverrideSpec{
						ResourceSelectors: []placementv1alpha1.ResourceSelector{
							validResourceSelector,
						},
						Policy: policyWithInvalidOverrideRule,
					},
				}
				// Modify the path to target 'kind'
				ro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/kind"
				err := hubClient.Create(ctx, &ro)
				var statusErr *k8sErrors.StatusError
				Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create ResourceOverride call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8sErrors.StatusError{})))
				Expect(statusErr.Status().Message).Should(ContainSubstring("Path cannot target typeMeta fields (kind, apiVersion)"))
			})

			It("Should deny creation of ResourceOverride with invalid jsonPatchOverrides Paths (/metadata)", func() {
				ro := placementv1alpha1.ResourceOverride{
					ObjectMeta: metav1.ObjectMeta{
						Name:      roName,
						Namespace: nsName,
					},
					Spec: placementv1alpha1.ResourceOverrideSpec{
						ResourceSelectors: []placementv1alpha1.ResourceSelector{
							validResourceSelector,
						},
						Policy: policyWithInvalidOverrideRule,
					},
				}
				// Modify the path to target 'metadata'
				ro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/metadata"
				err := hubClient.Create(ctx, &ro)
				var statusErr *k8sErrors.StatusError
				Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create ResourceOverride call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8sErrors.StatusError{})))
				Expect(statusErr.Status().Message).Should(ContainSubstring("Path can only target annotations and labels under metadata"))
			})

			It("Should deny creation of ResourceOverride with invalid jsonPatchOverrides Paths (/metadata/name)", func() {
				ro := placementv1alpha1.ResourceOverride{
					ObjectMeta: metav1.ObjectMeta{
						Name:      roName,
						Namespace: nsName,
					},
					Spec: placementv1alpha1.ResourceOverrideSpec{
						ResourceSelectors: []placementv1alpha1.ResourceSelector{
							validResourceSelector,
						},
						Policy: policyWithInvalidOverrideRule,
					},
				}
				// Modify the path to target 'metadata.name'
				ro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/metadata/name"
				err := hubClient.Create(ctx, &ro)
				var statusErr *k8sErrors.StatusError
				Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create ResourceOverride call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8sErrors.StatusError{})))
				Expect(statusErr.Status().Message).Should(ContainSubstring("Path can only target annotations and labels under metadata"))
			})

			It("Should deny creation of ResourceOverride with invalid jsonPatchOverrides Paths (/status)", func() {
				ro := placementv1alpha1.ResourceOverride{
					ObjectMeta: metav1.ObjectMeta{
						Name:      roName,
						Namespace: nsName,
					},
					Spec: placementv1alpha1.ResourceOverrideSpec{
						ResourceSelectors: []placementv1alpha1.ResourceSelector{
							validResourceSelector,
						},
						Policy: policyWithInvalidOverrideRule,
					},
				}
				// Modify the path to target 'status'
				ro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/status/conditions/0/type"
				err := hubClient.Create(ctx, &ro)
				var statusErr *k8sErrors.StatusError
				Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create ResourceOverride call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8sErrors.StatusError{})))
				Expect(statusErr.Status().Message).Should(ContainSubstring("Path cannot target status fields"))
			})
		})
	})
})
