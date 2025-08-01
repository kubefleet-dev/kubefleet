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

package clusterresourceplacement

import (
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	fleetv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

const (
	eventuallyDuration   = time.Second * 5
	eventuallyInterval   = time.Millisecond * 250
	consistentlyDuration = time.Second
	consistentlyInterval = time.Millisecond * 200
)

const (
	crpName        = "crp-1"
	noFinalizerCRP = "crp-2"
	rpName         = "rp-1"
	noFinalizerRP  = "rp-2"
	testNamespace  = "test-ns"
)

var (
	resourceSelectors = []fleetv1beta1.ClusterResourceSelector{
		{
			Group:   "core",
			Kind:    "Namespace",
			Version: "v1",
			Name:    "work",
		},
	}
)

var (
	expectedKeySetEnqueuedActual = func() error {
		return isKeyPresent(crpName)
	}

	expectedRPKeySetEnqueuedActual = func() error {
		key := testNamespace + "/" + rpName
		return isKeyPresent(key)
	}

	noKeyEnqueuedActual = func() error {
		if queueLen := keyCollector.Len(); queueLen != 0 {
			return fmt.Errorf("work queue is not empty: current len %d, want 0", queueLen)
		}
		return nil
	}
)

func isKeyPresent(key string) error {
	if isAllPresent, absentKeys := keyCollector.IsPresent(key); !isAllPresent {
		return fmt.Errorf("expected key(s) %v is not found", absentKeys)
	}

	if queueLen := keyCollector.Len(); queueLen != 1 {
		return fmt.Errorf("more than one key is enqueued: current len %d, want 1", queueLen)
	}

	return nil
}

func TestMain(m *testing.M) {
	// Add custom APIs to the runtime scheme.
	if err := fleetv1beta1.AddToScheme(scheme.Scheme); err != nil {
		log.Fatalf("failed to add custom APIs to the runtime scheme: %v", err)
	}

	os.Exit(m.Run())
}

// TODO (ryanzhang): fix tests so that they are not serial and ordered. Each test should be independent and run by itself.
// The whole point of ginkgo is that we can order tests in a way that the common setup/teardown can be pulled together at
// the correct level. There should not be empty nested structs like Describe/Context/It.
// The serial nature of the tests also makes it hard to reason. For example, the CRP gets a finalizer in a previous test
// while the finalizer related test has no mention of it.

var _ = Describe("scheduler cluster resource placement source controller", Serial, Ordered, func() {
	Context("crp created", func() {
		BeforeAll(func() {
			crp := &fleetv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
				Spec: fleetv1beta1.PlacementSpec{
					ResourceSelectors: resourceSelectors,
				},
			}
			Expect(hubClient.Create(ctx, crp)).Should(Succeed(), "Failed to create cluster resource placement")
		})

		It("should not enqueue the CRP when it is created", func() {
			Consistently(noKeyEnqueuedActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "Workqueue is not empty")
		})

		AfterAll(func() {
			keyCollector.Reset()
		})
	})

	Context("crp updated", func() {
		BeforeAll(func() {
			Consistently(noKeyEnqueuedActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "Workqueue is not empty")

			crp := &fleetv1beta1.ClusterResourcePlacement{}
			Expect(hubClient.Get(ctx, client.ObjectKey{Name: crpName}, crp)).Should(Succeed(), "Failed to get cluster resource placement")

			crp.Spec.Policy = &fleetv1beta1.PlacementPolicy{
				PlacementType: fleetv1beta1.PickAllPlacementType,
				Affinity: &fleetv1beta1.Affinity{
					ClusterAffinity: &fleetv1beta1.ClusterAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &fleetv1beta1.ClusterSelector{
							ClusterSelectorTerms: []fleetv1beta1.ClusterSelectorTerm{
								{
									LabelSelector: &metav1.LabelSelector{
										MatchLabels: map[string]string{
											"foo": "bar",
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(hubClient.Update(ctx, crp)).Should(Succeed(), "Failed to update cluster resource placement")
		})

		It("should not enqueue the CRP when it is updated", func() {
			Consistently(noKeyEnqueuedActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "Workqueue is not empty")
		})

		AfterEach(func() {
			keyCollector.Reset()
		})
	})

	Context("crp scheduler cleanup finalizer added", func() {
		BeforeAll(func() {
			Consistently(noKeyEnqueuedActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "Workqueue is not empty")

			crp := &fleetv1beta1.ClusterResourcePlacement{}
			Expect(hubClient.Get(ctx, client.ObjectKey{Name: crpName}, crp)).Should(Succeed(), "Failed to get cluster resource placement")

			crp.Finalizers = append(crp.Finalizers, fleetv1beta1.SchedulerCleanupFinalizer)
			Expect(hubClient.Update(ctx, crp)).Should(Succeed(), "Failed to update cluster resource placement")
		})

		It("should not enqueue the CRP when scheduler cleanup finalizer is added", func() {
			Consistently(noKeyEnqueuedActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "Workqueue is not empty")
		})

		AfterEach(func() {
			keyCollector.Reset()
		})
	})

	Context("crp with finalizer is deleted", func() {
		BeforeAll(func() {
			Consistently(noKeyEnqueuedActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "Workqueue is not empty")

			crp := &fleetv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: crpName,
				},
			}
			Expect(hubClient.Delete(ctx, crp)).Should(Succeed(), "Failed to delete cluster resource placement")
		})

		It("should enqueue the CRP when crp with finalizer is deleted", func() {
			Eventually(expectedKeySetEnqueuedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Workqueue is empty")
			Consistently(expectedKeySetEnqueuedActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "Workqueue is empty")
		})

		AfterEach(func() {
			keyCollector.Reset()
		})
	})

	Context("deleted crp has finalizer removed", func() {
		BeforeAll(func() {
			Consistently(noKeyEnqueuedActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "Workqueue is not empty")

			crp := &fleetv1beta1.ClusterResourcePlacement{}
			Expect(hubClient.Get(ctx, client.ObjectKey{Name: crpName}, crp)).Should(Succeed(), "Failed to get cluster resource placement")

			crp.Finalizers = []string{}
			Expect(hubClient.Update(ctx, crp)).Should(Succeed(), "Failed to update cluster resource placement")
		})

		It("should not enqueue the CRP when finalizer is removed from deleted crp", func() {
			Consistently(noKeyEnqueuedActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "Workqueue is not empty")
		})

		AfterEach(func() {
			keyCollector.Reset()
		})
	})

	Context("crp without finalizer is deleted", func() {
		BeforeAll(func() {
			Consistently(noKeyEnqueuedActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "Workqueue is not empty")

			crp := &fleetv1beta1.ClusterResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name: noFinalizerCRP,
				},
				Spec: fleetv1beta1.PlacementSpec{
					ResourceSelectors: resourceSelectors,
				},
			}
			Expect(hubClient.Create(ctx, crp)).Should(Succeed(), "Failed to create cluster resource placement")
			Expect(hubClient.Delete(ctx, crp)).Should(Succeed(), "Failed to delete cluster resource placement")
		})

		It("should enqueue the CRP when crp with finalizer is deleted", func() {
			Consistently(noKeyEnqueuedActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "Workqueue is not empty")
		})

		AfterEach(func() {
			keyCollector.Reset()
		})
	})
})

var _ = Describe("scheduler resource placement source controller", Serial, Ordered, func() {
	Context("rp created", func() {
		BeforeAll(func() {
			rp := &fleetv1beta1.ResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name:      rpName,
					Namespace: testNamespace,
				},
				Spec: fleetv1beta1.PlacementSpec{
					ResourceSelectors: resourceSelectors,
				},
			}
			Expect(hubClient.Create(ctx, rp)).Should(Succeed(), "Failed to create resource placement")
		})

		It("should not enqueue the RP when it is created", func() {
			Consistently(noKeyEnqueuedActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "Workqueue is not empty")
		})

		AfterAll(func() {
			keyCollector.Reset()
		})
	})

	Context("rp updated", func() {
		BeforeAll(func() {
			Consistently(noKeyEnqueuedActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "Workqueue is not empty")

			rp := &fleetv1beta1.ResourcePlacement{}
			Expect(hubClient.Get(ctx, client.ObjectKey{Name: rpName, Namespace: testNamespace}, rp)).Should(Succeed(), "Failed to get resource placement")

			rp.Spec.Policy = &fleetv1beta1.PlacementPolicy{
				PlacementType: fleetv1beta1.PickAllPlacementType,
				Affinity: &fleetv1beta1.Affinity{
					ClusterAffinity: &fleetv1beta1.ClusterAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &fleetv1beta1.ClusterSelector{
							ClusterSelectorTerms: []fleetv1beta1.ClusterSelectorTerm{
								{
									LabelSelector: &metav1.LabelSelector{
										MatchLabels: map[string]string{
											"foo": "bar",
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(hubClient.Update(ctx, rp)).Should(Succeed(), "Failed to update resource placement")
		})

		It("should not enqueue the RP when it is updated", func() {
			Consistently(noKeyEnqueuedActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "Workqueue is not empty")
		})

		AfterEach(func() {
			keyCollector.Reset()
		})
	})

	Context("rp scheduler cleanup finalizer added", func() {
		BeforeAll(func() {
			Consistently(noKeyEnqueuedActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "Workqueue is not empty")

			rp := &fleetv1beta1.ResourcePlacement{}
			Expect(hubClient.Get(ctx, client.ObjectKey{Name: rpName, Namespace: testNamespace}, rp)).Should(Succeed(), "Failed to get resource placement")

			rp.Finalizers = append(rp.Finalizers, fleetv1beta1.SchedulerCleanupFinalizer)
			Expect(hubClient.Update(ctx, rp)).Should(Succeed(), "Failed to update resource placement")
		})

		It("should not enqueue the RP when scheduler cleanup finalizer is added", func() {
			Consistently(noKeyEnqueuedActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "Workqueue is not empty")
		})

		AfterEach(func() {
			keyCollector.Reset()
		})
	})

	Context("rp with finalizer is deleted", func() {
		BeforeAll(func() {
			Consistently(noKeyEnqueuedActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "Workqueue is not empty")

			rp := &fleetv1beta1.ResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name:      rpName,
					Namespace: testNamespace,
				},
			}
			Expect(hubClient.Delete(ctx, rp)).Should(Succeed(), "Failed to delete resource placement")
		})

		It("should enqueue the RP when rp with finalizer is deleted", func() {
			Eventually(expectedRPKeySetEnqueuedActual, eventuallyDuration, eventuallyInterval).Should(Succeed(), "Workqueue is empty")
			Consistently(expectedRPKeySetEnqueuedActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "Workqueue is empty")
		})

		AfterEach(func() {
			keyCollector.Reset()
		})
	})

	Context("deleted rp has finalizer removed", func() {
		BeforeAll(func() {
			Consistently(noKeyEnqueuedActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "Workqueue is not empty")

			rp := &fleetv1beta1.ResourcePlacement{}
			Expect(hubClient.Get(ctx, client.ObjectKey{Name: rpName, Namespace: testNamespace}, rp)).Should(Succeed(), "Failed to get resource placement")

			rp.Finalizers = []string{}
			Expect(hubClient.Update(ctx, rp)).Should(Succeed(), "Failed to update resource placement")
		})

		It("should not enqueue the RP when finalizer is removed from deleted rp", func() {
			Consistently(noKeyEnqueuedActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "Workqueue is not empty")
		})

		AfterEach(func() {
			keyCollector.Reset()
		})
	})

	Context("rp without finalizer is deleted", func() {
		BeforeAll(func() {
			Consistently(noKeyEnqueuedActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "Workqueue is not empty")

			rp := &fleetv1beta1.ResourcePlacement{
				ObjectMeta: metav1.ObjectMeta{
					Name:      noFinalizerRP,
					Namespace: testNamespace,
				},
				Spec: fleetv1beta1.PlacementSpec{
					ResourceSelectors: resourceSelectors,
				},
			}
			Expect(hubClient.Create(ctx, rp)).Should(Succeed(), "Failed to create resource placement")
			Expect(hubClient.Delete(ctx, rp)).Should(Succeed(), "Failed to delete resource placement")
		})

		It("should not enqueue the RP when rp without finalizer is deleted", func() {
			Consistently(noKeyEnqueuedActual, consistentlyDuration, consistentlyInterval).Should(Succeed(), "Workqueue is not empty")
		})

		AfterEach(func() {
			keyCollector.Reset()
		})
	})
})
