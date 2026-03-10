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

package v1beta1

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

const (
	celCRONameTemplate     = "test-cro-cel-%d"
	celCRORuleNameTemplate = "test-cro-cel-rule-%d"
	celCROPathNameTemplate = "test-cro-cel-path-%d"
	celRORuleNameTemplate  = "test-ro-cel-rule-%d"
	celROPathNameTemplate  = "test-ro-cel-path-%d"
)

// expectCELValidationError asserts that the given error is a StatusError whose message matches the given regex.
func expectCELValidationError(err error, messageRegex string) {
	Expect(err).Should(HaveOccurred())
	var statusErr *k8sErrors.StatusError
	Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("error type = %s, want %s", reflect.TypeOf(err), reflect.TypeFor[*k8sErrors.StatusError]()))
	Expect(statusErr.ErrStatus.Message).Should(MatchRegexp(messageRegex))
}

var _ = Describe("Test Override CEL validation", func() {
	Context("ClusterResourceOverride - placement scope validation", func() {
		It("should deny CRO with Namespaced placement scope", func() {
			cro := createValidClusterResourceOverride(
				fmt.Sprintf(celCRONameTemplate, GinkgoParallelProcess()),
				&placementv1beta1.PlacementRef{
					Name:  "test-placement",
					Scope: placementv1beta1.NamespaceScoped,
				},
			)
			expectCELValidationError(hubClient.Create(ctx, &cro), "clusterResourceOverride placement reference cannot be Namespaced scope")
		})

		It("should allow CRO with Cluster placement scope", func() {
			cro := createValidClusterResourceOverride(
				fmt.Sprintf(celCRONameTemplate+"-valid", GinkgoParallelProcess()),
				&placementv1beta1.PlacementRef{
					Name:  "test-placement",
					Scope: placementv1beta1.ClusterScoped,
				},
			)
			Expect(hubClient.Create(ctx, &cro)).Should(Succeed())
			Expect(hubClient.Delete(ctx, &cro)).Should(Succeed())
		})
	})

	Context("ClusterResourceOverride - OverrideRule type validation", func() {
		It("should deny OverrideRule with Delete type and non-empty jsonPatchOverrides", func() {
			cro := createValidClusterResourceOverride(
				fmt.Sprintf(celCRORuleNameTemplate, GinkgoParallelProcess()),
				nil,
			)
			cro.Spec.Policy.OverrideRules = []placementv1beta1.OverrideRule{
				{
					OverrideType: placementv1beta1.DeleteOverrideType,
					JSONPatchOverrides: []placementv1beta1.JSONPatchOverride{
						{
							Operator: placementv1beta1.JSONPatchOverrideOpAdd,
							Path:     "/metadata/labels/test",
							Value:    apiextensionsv1.JSON{Raw: []byte(`"val"`)},
						},
					},
				},
			}
			expectCELValidationError(hubClient.Create(ctx, &cro), "jsonPatchOverrides must be empty when overrideType is Delete")
		})

		It("should deny OverrideRule with JSONPatch type and empty jsonPatchOverrides", func() {
			cro := createValidClusterResourceOverride(
				fmt.Sprintf(celCRORuleNameTemplate+"-2", GinkgoParallelProcess()),
				nil,
			)
			cro.Spec.Policy.OverrideRules = []placementv1beta1.OverrideRule{
				{
					OverrideType:       placementv1beta1.JSONPatchOverrideType,
					JSONPatchOverrides: []placementv1beta1.JSONPatchOverride{},
				},
			}
			expectCELValidationError(hubClient.Create(ctx, &cro), "jsonPatchOverrides must not be empty when overrideType is JSONPatch")
		})

		It("should allow OverrideRule with Delete type and no jsonPatchOverrides", func() {
			cro := createValidClusterResourceOverride(
				fmt.Sprintf(celCRORuleNameTemplate+"-delete", GinkgoParallelProcess()),
				nil,
			)
			cro.Spec.Policy.OverrideRules = []placementv1beta1.OverrideRule{
				{
					OverrideType: placementv1beta1.DeleteOverrideType,
				},
			}
			Expect(hubClient.Create(ctx, &cro)).Should(Succeed())
			Expect(hubClient.Delete(ctx, &cro)).Should(Succeed())
		})
	})

	Context("ClusterResourceOverride - JSONPatchOverride path validation", func() {
		It("should deny path targeting /kind", func() {
			cro := createValidClusterResourceOverride(
				fmt.Sprintf(celCROPathNameTemplate+"-1", GinkgoParallelProcess()),
				nil,
			)
			cro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/kind"
			expectCELValidationError(hubClient.Create(ctx, &cro), "cannot override typeMeta field kind")
		})

		It("should deny path targeting /apiVersion", func() {
			cro := createValidClusterResourceOverride(
				fmt.Sprintf(celCROPathNameTemplate+"-2", GinkgoParallelProcess()),
				nil,
			)
			cro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/apiVersion"
			expectCELValidationError(hubClient.Create(ctx, &cro), "cannot override typeMeta field apiVersion")
		})

		It("should deny path targeting /status", func() {
			cro := createValidClusterResourceOverride(
				fmt.Sprintf(celCROPathNameTemplate+"-3", GinkgoParallelProcess()),
				nil,
			)
			cro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/status"
			expectCELValidationError(hubClient.Create(ctx, &cro), "cannot override status fields")
		})

		It("should deny path targeting /metadata directly", func() {
			cro := createValidClusterResourceOverride(
				fmt.Sprintf(celCROPathNameTemplate+"-4", GinkgoParallelProcess()),
				nil,
			)
			cro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/metadata"
			expectCELValidationError(hubClient.Create(ctx, &cro), "cannot override metadata fields except annotations and labels")
		})

		It("should deny path with empty segments (double slash)", func() {
			cro := createValidClusterResourceOverride(
				fmt.Sprintf(celCROPathNameTemplate+"-5", GinkgoParallelProcess()),
				nil,
			)
			cro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/spec//field"
			expectCELValidationError(hubClient.Create(ctx, &cro), "path cannot contain empty segments")
		})

		It("should deny path with trailing slash", func() {
			cro := createValidClusterResourceOverride(
				fmt.Sprintf(celCROPathNameTemplate+"-6", GinkgoParallelProcess()),
				nil,
			)
			cro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/spec/field/"
			expectCELValidationError(hubClient.Create(ctx, &cro), "path cannot have a trailing slash")
		})

		It("should deny path with whitespace-only segment", func() {
			cro := createValidClusterResourceOverride(
				fmt.Sprintf(celCROPathNameTemplate+"-6b", GinkgoParallelProcess()),
				nil,
			)
			cro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/spec/ /field"
			expectCELValidationError(hubClient.Create(ctx, &cro), "path segment cannot contain only whitespace")
		})

		It("should deny path exceeding max length of 512", func() {
			cro := createValidClusterResourceOverride(
				fmt.Sprintf(celCROPathNameTemplate+"-9", GinkgoParallelProcess()),
				nil,
			)
			cro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/" + strings.Repeat("a", 512)
			expectCELValidationError(hubClient.Create(ctx, &cro), "Too long")
		})

		It("should allow path targeting /metadata/labels", func() {
			cro := createValidClusterResourceOverride(
				fmt.Sprintf(celCROPathNameTemplate+"-7", GinkgoParallelProcess()),
				nil,
			)
			cro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/metadata/labels/my-label"
			Expect(hubClient.Create(ctx, &cro)).Should(Succeed())
			Expect(hubClient.Delete(ctx, &cro)).Should(Succeed())
		})

		It("should allow path targeting /metadata/annotations", func() {
			cro := createValidClusterResourceOverride(
				fmt.Sprintf(celCROPathNameTemplate+"-8", GinkgoParallelProcess()),
				nil,
			)
			cro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/metadata/annotations/my-annotation"
			Expect(hubClient.Create(ctx, &cro)).Should(Succeed())
			Expect(hubClient.Delete(ctx, &cro)).Should(Succeed())
		})
	})

	Context("ResourceOverride - OverrideRule type validation", func() {
		It("should deny RO OverrideRule with Delete type and non-empty jsonPatchOverrides", func() {
			ro := createValidResourceOverride(
				testNamespace,
				fmt.Sprintf(celRORuleNameTemplate, GinkgoParallelProcess()),
				nil,
			)
			ro.Spec.Policy.OverrideRules = []placementv1beta1.OverrideRule{
				{
					OverrideType: placementv1beta1.DeleteOverrideType,
					JSONPatchOverrides: []placementv1beta1.JSONPatchOverride{
						{
							Operator: placementv1beta1.JSONPatchOverrideOpAdd,
							Path:     "/metadata/labels/test",
							Value:    apiextensionsv1.JSON{Raw: []byte(`"val"`)},
						},
					},
				},
			}
			expectCELValidationError(hubClient.Create(ctx, &ro), "jsonPatchOverrides must be empty when overrideType is Delete")
		})

		It("should deny RO OverrideRule with JSONPatch type and empty jsonPatchOverrides", func() {
			ro := createValidResourceOverride(
				testNamespace,
				fmt.Sprintf(celRORuleNameTemplate+"-2", GinkgoParallelProcess()),
				nil,
			)
			ro.Spec.Policy.OverrideRules = []placementv1beta1.OverrideRule{
				{
					OverrideType:       placementv1beta1.JSONPatchOverrideType,
					JSONPatchOverrides: []placementv1beta1.JSONPatchOverride{},
				},
			}
			expectCELValidationError(hubClient.Create(ctx, &ro), "jsonPatchOverrides must not be empty when overrideType is JSONPatch")
		})

		It("should allow RO OverrideRule with Delete type and no jsonPatchOverrides", func() {
			ro := createValidResourceOverride(
				testNamespace,
				fmt.Sprintf(celRORuleNameTemplate+"-delete", GinkgoParallelProcess()),
				nil,
			)
			ro.Spec.Policy.OverrideRules = []placementv1beta1.OverrideRule{
				{
					OverrideType: placementv1beta1.DeleteOverrideType,
				},
			}
			Expect(hubClient.Create(ctx, &ro)).Should(Succeed())
			Expect(hubClient.Delete(ctx, &ro)).Should(Succeed())
		})
	})

	Context("ResourceOverride - JSONPatchOverride path validation", func() {
		It("should deny RO path targeting /kind", func() {
			ro := createValidResourceOverride(
				testNamespace,
				fmt.Sprintf(celROPathNameTemplate+"-1", GinkgoParallelProcess()),
				nil,
			)
			ro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/kind"
			expectCELValidationError(hubClient.Create(ctx, &ro), "cannot override typeMeta field kind")
		})

		It("should deny RO path targeting /apiVersion", func() {
			ro := createValidResourceOverride(
				testNamespace,
				fmt.Sprintf(celROPathNameTemplate+"-2", GinkgoParallelProcess()),
				nil,
			)
			ro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/apiVersion"
			expectCELValidationError(hubClient.Create(ctx, &ro), "cannot override typeMeta field apiVersion")
		})

		It("should deny RO path targeting /status", func() {
			ro := createValidResourceOverride(
				testNamespace,
				fmt.Sprintf(celROPathNameTemplate+"-3", GinkgoParallelProcess()),
				nil,
			)
			ro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/status"
			expectCELValidationError(hubClient.Create(ctx, &ro), "cannot override status fields")
		})

		It("should deny RO path targeting /metadata directly", func() {
			ro := createValidResourceOverride(
				testNamespace,
				fmt.Sprintf(celROPathNameTemplate+"-4", GinkgoParallelProcess()),
				nil,
			)
			ro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/metadata"
			expectCELValidationError(hubClient.Create(ctx, &ro), "cannot override metadata fields except annotations and labels")
		})

		It("should deny RO path with empty segments (double slash)", func() {
			ro := createValidResourceOverride(
				testNamespace,
				fmt.Sprintf(celROPathNameTemplate+"-5", GinkgoParallelProcess()),
				nil,
			)
			ro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/spec//field"
			expectCELValidationError(hubClient.Create(ctx, &ro), "path cannot contain empty segments")
		})

		It("should deny RO path with trailing slash", func() {
			ro := createValidResourceOverride(
				testNamespace,
				fmt.Sprintf(celROPathNameTemplate+"-6", GinkgoParallelProcess()),
				nil,
			)
			ro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/spec/field/"
			expectCELValidationError(hubClient.Create(ctx, &ro), "path cannot have a trailing slash")
		})

		It("should deny RO path with whitespace-only segment", func() {
			ro := createValidResourceOverride(
				testNamespace,
				fmt.Sprintf(celROPathNameTemplate+"-6b", GinkgoParallelProcess()),
				nil,
			)
			ro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/spec/ /field"
			expectCELValidationError(hubClient.Create(ctx, &ro), "path segment cannot contain only whitespace")
		})

		It("should deny RO path exceeding max length of 512", func() {
			ro := createValidResourceOverride(
				testNamespace,
				fmt.Sprintf(celROPathNameTemplate+"-9", GinkgoParallelProcess()),
				nil,
			)
			ro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/" + strings.Repeat("a", 512)
			expectCELValidationError(hubClient.Create(ctx, &ro), "Too long")
		})

		It("should allow RO path targeting /metadata/labels", func() {
			ro := createValidResourceOverride(
				testNamespace,
				fmt.Sprintf(celROPathNameTemplate+"-7", GinkgoParallelProcess()),
				nil,
			)
			ro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/metadata/labels/my-label"
			Expect(hubClient.Create(ctx, &ro)).Should(Succeed())
			Expect(hubClient.Delete(ctx, &ro)).Should(Succeed())
		})

		It("should allow RO path targeting /metadata/annotations", func() {
			ro := createValidResourceOverride(
				testNamespace,
				fmt.Sprintf(celROPathNameTemplate+"-8", GinkgoParallelProcess()),
				nil,
			)
			ro.Spec.Policy.OverrideRules[0].JSONPatchOverrides[0].Path = "/metadata/annotations/my-annotation"
			Expect(hubClient.Create(ctx, &ro)).Should(Succeed())
			Expect(hubClient.Delete(ctx, &ro)).Should(Succeed())
		})
	})
})
