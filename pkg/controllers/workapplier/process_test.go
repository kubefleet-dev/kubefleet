package workapplier

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	fleetv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

// Note (chenyu1): The fake client Fleet uses for unit tests has trouble processing certain requests
// at the moment; affected test cases will be covered in the integration tests (w/ real clients) instead.

// TestShouldInitiateTakeOverAttempt tests the shouldInitiateTakeOverAttempt function.
func TestShouldInitiateTakeOverAttempt(t *testing.T) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: nsName,
		},
	}
	nsUnstructuredMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(ns)
	if err != nil {
		t.Fatalf("Namespace ToUnstructured() = %v, want no error", err)
	}
	nsUnstructured := &unstructured.Unstructured{Object: nsUnstructuredMap}
	nsUnstructured.SetAPIVersion("v1")
	nsUnstructured.SetKind("Namespace")

	nsWithFleetOwnerUnstructured := nsUnstructured.DeepCopy()
	nsWithFleetOwnerUnstructured.SetOwnerReferences([]metav1.OwnerReference{
		*appliedWorkOwnerRef,
	})

	nsWithNonFleetOwnerUnstructured := nsUnstructured.DeepCopy()
	nsWithNonFleetOwnerUnstructured.SetOwnerReferences([]metav1.OwnerReference{
		dummyOwnerRef,
	})

	testCases := []struct {
		name                        string
		inMemberClusterObj          *unstructured.Unstructured
		applyStrategy               *fleetv1beta1.ApplyStrategy
		expectedAppliedWorkOwnerRef *metav1.OwnerReference
		wantShouldTakeOver          bool
	}{
		{
			name: "no in member cluster object",
			applyStrategy: &fleetv1beta1.ApplyStrategy{
				WhenToTakeOver: fleetv1beta1.WhenToTakeOverTypeAlways,
			},
		},
		{
			name:               "never take over",
			inMemberClusterObj: nsUnstructured,
			applyStrategy: &fleetv1beta1.ApplyStrategy{
				WhenToTakeOver: fleetv1beta1.WhenToTakeOverTypeNever,
			},
			expectedAppliedWorkOwnerRef: appliedWorkOwnerRef,
		},
		{
			name:               "owned by Fleet",
			inMemberClusterObj: nsWithFleetOwnerUnstructured,
			applyStrategy: &fleetv1beta1.ApplyStrategy{
				WhenToTakeOver: fleetv1beta1.WhenToTakeOverTypeAlways,
			},
			expectedAppliedWorkOwnerRef: appliedWorkOwnerRef,
		},
		{
			name:               "no owner, always take over",
			inMemberClusterObj: nsUnstructured,
			applyStrategy: &fleetv1beta1.ApplyStrategy{
				WhenToTakeOver: fleetv1beta1.WhenToTakeOverTypeAlways,
			},
			expectedAppliedWorkOwnerRef: appliedWorkOwnerRef,
			wantShouldTakeOver:          true,
		},
		{
			name:               "not owned by Fleet, take over if no diff",
			inMemberClusterObj: nsWithNonFleetOwnerUnstructured,
			applyStrategy: &fleetv1beta1.ApplyStrategy{
				WhenToTakeOver: fleetv1beta1.WhenToTakeOverTypeIfNoDiff,
			},
			expectedAppliedWorkOwnerRef: appliedWorkOwnerRef,
			wantShouldTakeOver:          true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			shouldTakeOver := shouldInitiateTakeOverAttempt(tc.inMemberClusterObj, tc.applyStrategy, tc.expectedAppliedWorkOwnerRef)
			if shouldTakeOver != tc.wantShouldTakeOver {
				t.Errorf("shouldInitiateTakeOverAttempt() = %v, want %v", shouldTakeOver, tc.wantShouldTakeOver)
			}
		})
	}
}

// TestGetParentCRPNameFromWork tests the getParentCRPNameFromWork function.
func TestGetParentCRPNameFromWork(t *testing.T) {
	testCases := []struct {
		name string
		work *fleetv1beta1.Work
		want string
	}{
		{
			name: "work with parent-CRP label",
			work: &fleetv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-work",
					Namespace: "fleet-member-cluster-1",
					Labels: map[string]string{
						fleetv1beta1.PlacementTrackingLabel: "test-crp",
						"other-label":                       "other-value",
					},
				},
			},
			want: "test-crp",
		},
		{
			name: "work without parent-CRP label",
			work: &fleetv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-work",
					Namespace: "fleet-member-cluster-1",
					Labels: map[string]string{
						"other-label": "other-value",
					},
				},
			},
			want: "",
		},
		{
			name: "work with no labels",
			work: &fleetv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-work",
					Namespace: "fleet-member-cluster-1",
				},
			},
			want: "",
		},
		{
			name: "work with nil labels map",
			work: &fleetv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-work",
					Namespace: "fleet-member-cluster-1",
					Labels:    nil,
				},
			},
			want: "",
		},
		{
			name: "work with empty parent-CRP label value",
			work: &fleetv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-work",
					Namespace: "fleet-member-cluster-1",
					Labels: map[string]string{
						fleetv1beta1.PlacementTrackingLabel: "",
						"other-label":                       "other-value",
					},
				},
			},
			want: "",
		},
		{
			name: "work with complex CRP name",
			work: &fleetv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-work",
					Namespace: "fleet-member-cluster-1",
					Labels: map[string]string{
						fleetv1beta1.PlacementTrackingLabel: "my-app-production-crp",
					},
				},
			},
			want: "my-app-production-crp",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := getParentCRPNameFromWork(tc.work)
			if got != tc.want {
				t.Errorf("getParentCRPNameFromWork() = %v, want %v", got, tc.want)
			}
		})
	}
}
