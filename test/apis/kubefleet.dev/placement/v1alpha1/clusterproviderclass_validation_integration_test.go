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

package v1alpha1

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	placementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
)

// newClass builds a minimal valid ClusterProviderClass; the caller registers cleanup.
func newClass(name string) *placementv1alpha1.ClusterProviderClass {
	return &placementv1alpha1.ClusterProviderClass{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: placementv1alpha1.ClusterProviderClassSpec{
			ProvisionerName: "example.kubefleet.dev",
		},
	}
}

func createClass(class *placementv1alpha1.ClusterProviderClass) {
	Expect(hubClient.Create(ctx, class)).Should(Succeed())
	DeferCleanup(func() {
		Expect(client.IgnoreNotFound(hubClient.Delete(ctx, class))).Should(Succeed())
	})
}

var _ = Describe("Test ClusterProviderClass API validation", func() {
	It("should accept a minimal class and apply the defaults", func() {
		class := newClass("minimal")
		createClass(class)

		fetched := &placementv1alpha1.ClusterProviderClass{}
		Expect(hubClient.Get(ctx, client.ObjectKeyFromObject(class), fetched)).Should(Succeed())
		Expect(fetched.Spec.Approval).Should(Equal(placementv1alpha1.ClusterClaimApprovalModeManual))
		Expect(fetched.Spec.OnFailure).Should(Equal(placementv1alpha1.ClusterClaimFailureActionHold))
	})

	It("should accept a fully specified class", func() {
		class := newClass("full")
		class.Annotations = map[string]string{placementv1alpha1.IsDefaultClusterProviderClassAnnotation: "true"}
		class.Spec = placementv1alpha1.ClusterProviderClassSpec{
			ProvisionerName: "capi.kubefleet.dev",
			Parameters:      map[string]string{"clusterClassName": "standard", "namespace": "capi"},
			SelectorVocabulary: &placementv1alpha1.SelectorVocabulary{
				LabelKeys: []placementv1alpha1.LabelKeyRule{
					{Key: "topology.kubernetes.io/region", Values: []string{"eastus", "westus"}},
					{Key: "env"},
				},
				PropertyKeys: []placementv1alpha1.PropertyKeyRule{
					{Key: "kubernetes-fleet.io/node-count", Min: ptr.To("1"), Max: ptr.To("100")},
				},
			},
			Approval:             placementv1alpha1.ClusterClaimApprovalModeAutomatic,
			PendingClaimTTL:      &metav1.Duration{Duration: 10 * time.Minute},
			MaxProvisionDuration: &metav1.Duration{Duration: 2 * time.Hour},
			JoinTimeout:          &metav1.Duration{Duration: 20 * time.Minute},
			OnFailure:            placementv1alpha1.ClusterClaimFailureActionRetry,
			RetryAfter:           &metav1.Duration{Duration: 5 * time.Minute},
		}
		createClass(class)
	})

	It("should deny a class without a provisioner name", func() {
		class := newClass("no-provisioner")
		class.Spec.ProvisionerName = ""
		Expect(hubClient.Create(ctx, class)).Should(MatchError(ContainSubstring("should be at least 1 chars")))
	})

	It("should deny changing the provisioner name", func() {
		class := newClass("repointed")
		createClass(class)

		class.Spec.ProvisionerName = "other.kubefleet.dev"
		Expect(hubClient.Update(ctx, class)).Should(MatchError(ContainSubstring("the provisionerName field is immutable")))
	})

	It("should deny an unknown approval mode or failure action", func() {
		class := newClass("bad-approval")
		class.Spec.Approval = "Sometimes"
		Expect(hubClient.Create(ctx, class)).Should(MatchError(ContainSubstring(`Unsupported value: "Sometimes"`)))

		class = newClass("bad-failure")
		class.Spec.OnFailure = "Panic"
		Expect(hubClient.Create(ctx, class)).Should(MatchError(ContainSubstring(`Unsupported value: "Panic"`)))
	})

	It("should deny a zero or negative duration", func() {
		for name, d := range map[string]*time.Duration{"zero-ttl": ptr.To(time.Duration(0)), "negative-retry": ptr.To(-time.Minute)} {
			class := newClass(name)
			if name == "zero-ttl" {
				class.Spec.PendingClaimTTL = &metav1.Duration{Duration: *d}
			} else {
				class.Spec.RetryAfter = &metav1.Duration{Duration: *d}
			}
			Expect(hubClient.Create(ctx, class)).Should(MatchError(ContainSubstring("must be a positive duration")), name)
		}
	})

	It("should bound the parameters and the vocabulary", func() {
		class := newClass("too-many-parameters")
		class.Spec.Parameters = make(map[string]string, 65)
		for i := range 65 {
			class.Spec.Parameters[fmt.Sprintf("k%d", i)] = "v"
		}
		Expect(hubClient.Create(ctx, class)).Should(MatchError(ContainSubstring("must have at most 64 items")))

		class = newClass("too-many-label-keys")
		class.Spec.SelectorVocabulary = &placementv1alpha1.SelectorVocabulary{}
		for i := range 65 {
			class.Spec.SelectorVocabulary.LabelKeys = append(class.Spec.SelectorVocabulary.LabelKeys, placementv1alpha1.LabelKeyRule{Key: fmt.Sprintf("k%d", i)})
		}
		Expect(hubClient.Create(ctx, class)).Should(MatchError(ContainSubstring("must have at most 64 items")))

		class = newClass("empty-label-key")
		class.Spec.SelectorVocabulary = &placementv1alpha1.SelectorVocabulary{LabelKeys: []placementv1alpha1.LabelKeyRule{{Key: ""}}}
		Expect(hubClient.Create(ctx, class)).Should(MatchError(ContainSubstring("should be at least 1 chars")))
	})
})
