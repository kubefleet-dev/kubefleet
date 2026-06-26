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

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	clusterv1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1"
)

var _ = Describe("Test cluster v1 API validation", func() {
	Context("Test MemberCluster API validation - invalid cases", func() {
		It("should deny creating API with invalid name size", func() {
			var name = "abcdef-123456789-123456789-123456789-123456789-123456789-123456789-123456789"
			// Create the API.
			memberClusterName := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
				},
			}
			By(fmt.Sprintf("expecting denial of CREATE API %s", name))
			var err = hubClient.Create(ctx, memberClusterName)
			var statusErr *k8serrors.StatusError
			Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create API call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8serrors.StatusError{})))
			Expect(statusErr.Status().Message).Should(ContainSubstring("metadata.name max length is 63"))
		})

		It("should deny creating API with invalid name starting with non-alphanumeric character", func() {
			var name = "-abcdef-123456789-123456789-123456789-123456789-123456789"
			// Create the API.
			memberClusterName := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
				},
			}
			By(fmt.Sprintf("expecting denial of CREATE API %s", name))
			err := hubClient.Create(ctx, memberClusterName)
			var statusErr *k8serrors.StatusError
			Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create API call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8serrors.StatusError{})))
			Expect(statusErr.Status().Message).Should(ContainSubstring("a lowercase RFC 1123 subdomain"))
		})

		It("should deny creating API with invalid name ending with non-alphanumeric character", func() {
			var name = "abcdef-123456789-123456789-123456789-123456789-123456789-"
			// Create the API.
			memberClusterName := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
				},
			}
			By(fmt.Sprintf("expecting denial of CREATE API %s", name))
			err := hubClient.Create(ctx, memberClusterName)
			var statusErr *k8serrors.StatusError
			Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create API call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8serrors.StatusError{})))
			Expect(statusErr.Status().Message).Should(ContainSubstring("a lowercase RFC 1123 subdomain"))
		})

		It("should deny creating API with invalid name containing character that is not alphanumeric and not -", func() {
			var name = "a_bcdef-123456789-123456789-123456789-123456789-123456789-123456789-123456789"
			// Create the API.
			memberClusterName := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
				},
			}
			By(fmt.Sprintf("expecting denial of CREATE API %s", name))
			err := hubClient.Create(ctx, memberClusterName)
			var statusErr *k8serrors.StatusError
			Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create API call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8serrors.StatusError{})))
			Expect(statusErr.Status().Message).Should(ContainSubstring("a lowercase RFC 1123 subdomain"))
		})
	})

	Context("Test Member Cluster creation API validation - valid cases", func() {
		It("should allow creating API with valid name size", func() {
			var name = "abc-123456789-123456789-123456789-123456789-123456789-123456789"
			// Create the API.
			memberClusterName := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
				},
			}
			Expect(hubClient.Create(ctx, memberClusterName)).Should(Succeed())
			Expect(hubClient.Delete(ctx, memberClusterName)).Should(Succeed())
		})

		It("should allow creating API with valid name starting with alphabet character", func() {
			var name = "abc-123456789"
			// Create the API.
			memberClusterName := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
				},
			}
			Expect(hubClient.Create(ctx, memberClusterName)).Should(Succeed())
			Expect(hubClient.Delete(ctx, memberClusterName)).Should(Succeed())
		})

		It("should allow creating API with valid name starting with numeric character", func() {
			var name = "123-123456789"
			// Create the API.
			memberClusterName := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
				},
			}
			Expect(hubClient.Create(ctx, memberClusterName)).Should(Succeed())
			Expect(hubClient.Delete(ctx, memberClusterName)).Should(Succeed())
		})

		It("should allow creating API with valid name ending with alphabet character", func() {
			var name = "123456789-abc"
			// Create the API.
			memberClusterName := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
				},
			}
			Expect(hubClient.Create(ctx, memberClusterName)).Should(Succeed())
			Expect(hubClient.Delete(ctx, memberClusterName)).Should(Succeed())
		})

		It("should allow creating API with valid name ending with numeric character", func() {
			var name = "123456789-123"
			// Create the API.
			memberClusterName := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
				},
			}
			Expect(hubClient.Create(ctx, memberClusterName)).Should(Succeed())
			Expect(hubClient.Delete(ctx, memberClusterName)).Should(Succeed())
		})
	})

	Context("Test MemberCluster taint CEL validation - invalid cases", func() {
		It("should deny creating MemberCluster with taint key containing special characters", func() {
			mc := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mc-v1-taint-invalid-key-chars",
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
					Taints: []clusterv1.Taint{
						{Key: "key@123:", Value: "value1", Effect: "NoSchedule"},
					},
				},
			}
			By("expecting denial of CREATE with invalid taint key")
			err := hubClient.Create(ctx, mc)
			var statusErr *k8serrors.StatusError
			Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8serrors.StatusError{})))
			Expect(statusErr.Status().Message).Should(ContainSubstring("taint key name segment must consist of alphanumeric characters"))
		})

		It("should deny creating MemberCluster with taint key starting with non-alphanumeric", func() {
			mc := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mc-v1-taint-key-start-nonalpha",
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
					Taints: []clusterv1.Taint{
						{Key: ".invalid-start", Effect: "NoSchedule"},
					},
				},
			}
			By("expecting denial of CREATE with taint key starting with dot")
			err := hubClient.Create(ctx, mc)
			var statusErr *k8serrors.StatusError
			Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8serrors.StatusError{})))
			Expect(statusErr.Status().Message).Should(ContainSubstring("taint key name segment must consist of alphanumeric characters"))
		})

		It("should deny creating MemberCluster with taint key ending with non-alphanumeric", func() {
			mc := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mc-v1-taint-key-end-nonalpha",
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
					Taints: []clusterv1.Taint{
						{Key: "invalid-end-", Effect: "NoSchedule"},
					},
				},
			}
			By("expecting denial of CREATE with taint key ending with dash")
			err := hubClient.Create(ctx, mc)
			var statusErr *k8serrors.StatusError
			Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8serrors.StatusError{})))
			Expect(statusErr.Status().Message).Should(ContainSubstring("taint key name segment must consist of alphanumeric characters"))
		})

		It("should deny creating MemberCluster with taint key prefix containing uppercase", func() {
			mc := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mc-v1-taint-key-uppercase-pfx",
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
					Taints: []clusterv1.Taint{
						{Key: "Example.COM/my-key", Effect: "NoSchedule"},
					},
				},
			}
			By("expecting denial of CREATE with uppercase prefix in taint key")
			err := hubClient.Create(ctx, mc)
			var statusErr *k8serrors.StatusError
			Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8serrors.StatusError{})))
			Expect(statusErr.Status().Message).Should(ContainSubstring("taint key prefix must be a lowercase DNS subdomain"))
		})

		It("should deny creating MemberCluster with taint key name segment exceeding 63 characters", func() {
			// 64-char name segment with a valid prefix.
			longName := strings.Repeat("a", 64)
			mc := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mc-v1-taint-key-long-name",
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
					Taints: []clusterv1.Taint{
						{Key: "example.com/" + longName, Effect: "NoSchedule"},
					},
				},
			}
			By("expecting denial of CREATE with taint key name segment exceeding 63 characters")
			err := hubClient.Create(ctx, mc)
			var statusErr *k8serrors.StatusError
			Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8serrors.StatusError{})))
			Expect(statusErr.Status().Message).Should(ContainSubstring("taint key name segment must be 63 characters or less"))
		})

		It("should deny creating MemberCluster with taint key prefix exceeding 253 characters", func() {
			// Build a valid DNS prefix that is 254 characters (exceeds 253 limit):
			// 63 + "." + 63 + "." + 63 + "." + 62 = 254
			longPrefix := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 62)
			mc := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mc-v1-taint-key-long-prefix",
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
					Taints: []clusterv1.Taint{
						{Key: longPrefix + "/key", Effect: "NoSchedule"},
					},
				},
			}
			By("expecting denial of CREATE with taint key prefix exceeding 253 characters")
			err := hubClient.Create(ctx, mc)
			var statusErr *k8serrors.StatusError
			Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8serrors.StatusError{})))
			Expect(statusErr.Status().Message).Should(ContainSubstring("taint key prefix must be 253 characters or less"))
		})

		It("should deny creating MemberCluster with taint value containing special characters", func() {
			mc := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mc-v1-taint-invalid-value-chars",
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
					Taints: []clusterv1.Taint{
						{Key: "validkey", Value: "val&123:98_", Effect: "NoSchedule"},
					},
				},
			}
			By("expecting denial of CREATE with invalid taint value")
			err := hubClient.Create(ctx, mc)
			var statusErr *k8serrors.StatusError
			Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8serrors.StatusError{})))
			Expect(statusErr.Status().Message).Should(ContainSubstring("taint value must be a valid label value"))
		})

		It("should deny creating MemberCluster with taint value exceeding 63 characters", func() {
			mc := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mc-v1-taint-value-too-long",
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
					Taints: []clusterv1.Taint{
						{Key: "validkey", Value: strings.Repeat("a", 64), Effect: "NoSchedule"},
					},
				},
			}
			By("expecting denial of CREATE with taint value exceeding 63 characters")
			err := hubClient.Create(ctx, mc)
			var statusErr *k8serrors.StatusError
			Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8serrors.StatusError{})))
			Expect(statusErr.Status().Message).Should(ContainSubstring("may not be more than 63"))
		})

		It("should deny creating MemberCluster with duplicate taints", func() {
			mc := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mc-v1-taint-duplicate",
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
					Taints: []clusterv1.Taint{
						{Key: "key1", Value: "value1", Effect: "NoSchedule"},
						{Key: "key1", Value: "value1", Effect: "NoSchedule"},
					},
				},
			}
			By("expecting denial of CREATE with duplicate taints")
			err := hubClient.Create(ctx, mc)
			var statusErr *k8serrors.StatusError
			Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8serrors.StatusError{})))
			Expect(statusErr.Status().Message).Should(ContainSubstring("taints must be unique"))
		})

		It("should allow creating MemberCluster with taints having same key and effect but different values", func() {
			mc := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mc-v1-taint-same-key-effect",
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
					Taints: []clusterv1.Taint{
						{Key: "key1", Value: "value1", Effect: "NoSchedule"},
						{Key: "key1", Value: "value2", Effect: "NoSchedule"},
					},
				},
			}
			By("expecting success of CREATE with same key+effect but different values")
			Expect(hubClient.Create(ctx, mc)).Should(Succeed())
			Expect(hubClient.Delete(ctx, mc)).Should(Succeed())
		})

		It("should deny creating MemberCluster with taint key prefix having label ending with hyphen", func() {
			mc := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mc-v1-taint-pfx-label-hyphen",
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
					Taints: []clusterv1.Taint{
						{Key: "abc-.def/key", Effect: "NoSchedule"},
					},
				},
			}
			By("expecting denial of CREATE with DNS label ending with hyphen")
			err := hubClient.Create(ctx, mc)
			var statusErr *k8serrors.StatusError
			Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8serrors.StatusError{})))
			Expect(statusErr.Status().Message).Should(ContainSubstring("taint key prefix must be a lowercase DNS subdomain"))
		})

		It("should deny creating MemberCluster with taint key prefix having empty label (consecutive dots)", func() {
			mc := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mc-v1-taint-pfx-empty-label",
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
					Taints: []clusterv1.Taint{
						{Key: "abc..def/key", Effect: "NoSchedule"},
					},
				},
			}
			By("expecting denial of CREATE with consecutive dots in prefix")
			err := hubClient.Create(ctx, mc)
			var statusErr *k8serrors.StatusError
			Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8serrors.StatusError{})))
			Expect(statusErr.Status().Message).Should(ContainSubstring("taint key prefix must be a lowercase DNS subdomain"))
		})

		It("should deny creating MemberCluster with taint key having empty prefix (leading slash)", func() {
			mc := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mc-v1-taint-empty-prefix",
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
					Taints: []clusterv1.Taint{
						{Key: "/name", Effect: "NoSchedule"},
					},
				},
			}
			By("expecting denial of CREATE with leading slash (empty prefix)")
			err := hubClient.Create(ctx, mc)
			var statusErr *k8serrors.StatusError
			Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8serrors.StatusError{})))
			Expect(statusErr.Status().Message).Should(ContainSubstring("taint key prefix must be a lowercase DNS subdomain"))
		})

		It("should deny creating MemberCluster with taint key having empty name segment (trailing slash)", func() {
			mc := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mc-v1-taint-trailing-slash",
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
					Taints: []clusterv1.Taint{
						{Key: "example.com/", Effect: "NoSchedule"},
					},
				},
			}
			By("expecting denial of CREATE with trailing slash (empty name segment)")
			err := hubClient.Create(ctx, mc)
			var statusErr *k8serrors.StatusError
			Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8serrors.StatusError{})))
			Expect(statusErr.Status().Message).Should(ContainSubstring("taint key name segment must consist of alphanumeric characters"))
		})

		It("should deny creating MemberCluster with empty taint key", func() {
			mc := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mc-v1-taint-empty-key",
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
					Taints: []clusterv1.Taint{
						{Key: "", Effect: "NoSchedule"},
					},
				},
			}
			By("expecting denial of CREATE with empty taint key")
			err := hubClient.Create(ctx, mc)
			var statusErr *k8serrors.StatusError
			Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8serrors.StatusError{})))
			// MinLength=1 schema validation rejects empty keys.
			Expect(statusErr.Status().Message).Should(ContainSubstring("should be at least 1 chars long"))
		})

		It("should deny creating MemberCluster with taint key that is only a slash", func() {
			mc := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mc-v1-taint-key-only-slash",
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
					Taints: []clusterv1.Taint{
						{Key: "/", Effect: "NoSchedule"},
					},
				},
			}
			By("expecting denial of CREATE with slash-only taint key")
			err := hubClient.Create(ctx, mc)
			var statusErr *k8serrors.StatusError
			Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8serrors.StatusError{})))
			// Both prefix (empty) and name segment (empty) are invalid.
			Expect(statusErr.Status().Message).Should(ContainSubstring("taint key prefix must be a lowercase DNS subdomain"))
		})

		It("should deny creating MemberCluster with taint value starting with non-alphanumeric", func() {
			mc := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mc-v1-taint-val-start-dot",
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
					Taints: []clusterv1.Taint{
						{Key: "key1", Value: ".starts-with-dot", Effect: "NoSchedule"},
					},
				},
			}
			By("expecting denial of CREATE with taint value starting with dot")
			err := hubClient.Create(ctx, mc)
			var statusErr *k8serrors.StatusError
			Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8serrors.StatusError{})))
			Expect(statusErr.Status().Message).Should(ContainSubstring("taint value must be a valid label value"))
		})

		It("should deny creating MemberCluster with taint value ending with non-alphanumeric", func() {
			mc := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mc-v1-taint-val-end-dash",
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
					Taints: []clusterv1.Taint{
						{Key: "key1", Value: "ends-with-dash-", Effect: "NoSchedule"},
					},
				},
			}
			By("expecting denial of CREATE with taint value ending with dash")
			err := hubClient.Create(ctx, mc)
			var statusErr *k8serrors.StatusError
			Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8serrors.StatusError{})))
			Expect(statusErr.Status().Message).Should(ContainSubstring("taint value must be a valid label value"))
		})

		It("should deny creating MemberCluster with taint key containing multiple slashes", func() {
			mc := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mc-v1-taint-key-multi-slash",
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
					Taints: []clusterv1.Taint{
						{Key: "example.com/sub/key", Effect: "NoSchedule"},
					},
				},
			}
			By("expecting denial of CREATE with multiple slashes in taint key")
			err := hubClient.Create(ctx, mc)
			var statusErr *k8serrors.StatusError
			Expect(errors.As(err, &statusErr)).To(BeTrue(), fmt.Sprintf("Create call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8serrors.StatusError{})))
			// The name segment after the first slash is "sub/key", which contains a slash and fails the name segment regex.
			Expect(statusErr.Status().Message).Should(ContainSubstring("taint key name segment must consist of alphanumeric characters"))
		})
	})

	Context("Test MemberCluster taint CEL validation - valid cases", func() {
		It("should allow creating MemberCluster with valid simple taint key", func() {
			mc := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mc-v1-taint-valid-simple",
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
					Taints: []clusterv1.Taint{
						{Key: "key1", Value: "value1", Effect: "NoSchedule"},
					},
				},
			}
			Expect(hubClient.Create(ctx, mc)).Should(Succeed())
			Expect(hubClient.Delete(ctx, mc)).Should(Succeed())
		})

		It("should allow creating MemberCluster with prefixed taint key", func() {
			mc := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mc-v1-taint-valid-prefix",
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
					Taints: []clusterv1.Taint{
						{Key: "example.com/my-key", Value: "value1", Effect: "NoSchedule"},
					},
				},
			}
			Expect(hubClient.Create(ctx, mc)).Should(Succeed())
			Expect(hubClient.Delete(ctx, mc)).Should(Succeed())
		})

		It("should allow creating MemberCluster with taint having empty value", func() {
			mc := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mc-v1-taint-valid-empty-value",
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
					Taints: []clusterv1.Taint{
						{Key: "key1", Effect: "NoSchedule"},
					},
				},
			}
			Expect(hubClient.Create(ctx, mc)).Should(Succeed())
			Expect(hubClient.Delete(ctx, mc)).Should(Succeed())
		})

		It("should allow creating MemberCluster with multiple unique taints", func() {
			mc := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mc-v1-taint-valid-multiple",
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
					Taints: []clusterv1.Taint{
						{Key: "key1", Value: "value1", Effect: "NoSchedule"},
						{Key: "key2", Value: "value2", Effect: "NoSchedule"},
						{Key: "key3", Effect: "NoSchedule"},
					},
				},
			}
			Expect(hubClient.Create(ctx, mc)).Should(Succeed())
			Expect(hubClient.Delete(ctx, mc)).Should(Succeed())
		})

		It("should allow creating MemberCluster with taint value at max length", func() {
			mc := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mc-v1-taint-valid-max-value",
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
					Taints: []clusterv1.Taint{
						{Key: "key1", Value: strings.Repeat("a", 63), Effect: "NoSchedule"},
					},
				},
			}
			Expect(hubClient.Create(ctx, mc)).Should(Succeed())
			Expect(hubClient.Delete(ctx, mc)).Should(Succeed())
		})

		It("should allow creating MemberCluster with multi-label DNS prefix in taint key", func() {
			mc := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mc-v1-taint-valid-multilabel",
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
					Taints: []clusterv1.Taint{
						{Key: "sub.example.com/my-key", Value: "value1", Effect: "NoSchedule"},
					},
				},
			}
			Expect(hubClient.Create(ctx, mc)).Should(Succeed())
			Expect(hubClient.Delete(ctx, mc)).Should(Succeed())
		})

		It("should allow creating MemberCluster with taint key name segment at max 63 chars", func() {
			mc := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mc-v1-taint-valid-max-name-seg",
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
					Taints: []clusterv1.Taint{
						{Key: "example.com/" + strings.Repeat("a", 63), Effect: "NoSchedule"},
					},
				},
			}
			Expect(hubClient.Create(ctx, mc)).Should(Succeed())
			Expect(hubClient.Delete(ctx, mc)).Should(Succeed())
		})

		It("should allow creating MemberCluster with single-character taint key", func() {
			mc := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mc-v1-taint-valid-single-char",
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
					Taints: []clusterv1.Taint{
						{Key: "a", Effect: "NoSchedule"},
					},
				},
			}
			Expect(hubClient.Create(ctx, mc)).Should(Succeed())
			Expect(hubClient.Delete(ctx, mc)).Should(Succeed())
		})

		It("should allow creating MemberCluster with taint key prefix at max 253 chars", func() {
			// Build a valid 253-char DNS prefix: 63 + "." + 63 + "." + 63 + "." + 61 = 253
			longPrefix := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)
			mc := &clusterv1.MemberCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mc-v1-taint-valid-max-prefix",
				},
				Spec: clusterv1.MemberClusterSpec{
					Identity: rbacv1.Subject{
						Name:      "fleet-member-agent-cluster-1",
						Kind:      "ServiceAccount",
						Namespace: "fleet-system",
						APIGroup:  "",
					},
					Taints: []clusterv1.Taint{
						{Key: longPrefix + "/key", Effect: "NoSchedule"},
					},
				},
			}
			Expect(hubClient.Create(ctx, mc)).Should(Succeed())
			Expect(hubClient.Delete(ctx, mc)).Should(Succeed())
		})
	})
})
