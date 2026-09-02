package workapplier

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"

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

func TestHideDiffValuesUnlessOwned(t *testing.T) {
	ownedObj := &unstructured.Unstructured{}
	ownedObj.SetOwnerReferences([]metav1.OwnerReference{*appliedWorkOwnerRef})

	ownedByAnotherAppliedWorkObj := &unstructured.Unstructured{}
	ownedByAnotherAppliedWorkObj.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion: appliedWorkOwnerRef.APIVersion,
			Kind:       appliedWorkOwnerRef.Kind,
			Name:       "another-work",
			UID:        "another-uid",
		},
	})

	testCases := []struct {
		name               string
		inMemberClusterObj *unstructured.Unstructured
		wantDiffs          []fleetv1beta1.PatchDetail
	}{
		{
			name:               "owned by expected AppliedWork",
			inMemberClusterObj: ownedObj,
			wantDiffs: []fleetv1beta1.PatchDetail{
				{Path: "/spec/replicas", ValueInMember: "1", ValueInHub: "2"},
			},
		},
		{
			name: "object does not exist",
			wantDiffs: []fleetv1beta1.PatchDetail{
				{Path: "/spec/replicas"},
			},
		},
		{
			name:               "object is not owned",
			inMemberClusterObj: &unstructured.Unstructured{},
			wantDiffs: []fleetv1beta1.PatchDetail{
				{Path: "/spec/replicas"},
			},
		},
		{
			name:               "object is owned by another AppliedWork",
			inMemberClusterObj: ownedByAnotherAppliedWorkObj,
			wantDiffs: []fleetv1beta1.PatchDetail{
				{Path: "/spec/replicas"},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			bundle := &manifestProcessingBundle{
				inMemberClusterObj: tc.inMemberClusterObj,
				diffs: []fleetv1beta1.PatchDetail{
					{Path: "/spec/replicas", ValueInMember: "1", ValueInHub: "2"},
				},
			}

			hideDiffValuesUnlessOwned(bundle, appliedWorkOwnerRef)

			if diff := cmp.Diff(bundle.diffs, tc.wantDiffs); diff != "" {
				t.Errorf("hideDiffValuesUnlessOwned() diff mismatch (-got, +want):\n%s", diff)
			}
		})
	}
}

func TestProcessOneManifestHidesDiffValuesForUnownedObject(t *testing.T) {
	testCases := []struct {
		name       string
		strategy   *fleetv1beta1.ApplyStrategy
		wantResult ManifestProcessingApplyOrReportDiffResultType
	}{
		{
			name: "report diff without takeover",
			strategy: &fleetv1beta1.ApplyStrategy{
				Type:             fleetv1beta1.ApplyStrategyTypeReportDiff,
				WhenToTakeOver:   fleetv1beta1.WhenToTakeOverTypeNever,
				ComparisonOption: fleetv1beta1.ComparisonOptionTypeFullComparison,
			},
			wantResult: ApplyOrReportDiffResTypeFoundDiff,
		},
		{
			name: "diff blocks takeover",
			strategy: &fleetv1beta1.ApplyStrategy{
				Type:             fleetv1beta1.ApplyStrategyTypeClientSideApply,
				WhenToTakeOver:   fleetv1beta1.WhenToTakeOverTypeIfNoDiff,
				ComparisonOption: fleetv1beta1.ComparisonOptionTypeFullComparison,
			},
			wantResult: ApplyOrReportDiffResTypeFailedToTakeOver,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			inMemberClusterObj := nsUnstructured.DeepCopy()
			inMemberClusterObj.SetLabels(map[string]string{"sensitive": "member-value"})
			r := &Reconciler{
				spokeDynamicClient: dynamicfake.NewSimpleDynamicClient(scheme.Scheme, inMemberClusterObj),
			}
			bundle := &manifestProcessingBundle{
				manifestObj: nsUnstructured.DeepCopy(),
				gvr:         &nsGVR,
			}
			work := &fleetv1beta1.Work{
				Spec: fleetv1beta1.WorkSpec{ApplyStrategy: tc.strategy},
			}

			r.processOneManifest(t.Context(), bundle, work, appliedWorkOwnerRef)

			if bundle.applyOrReportDiffResTyp != tc.wantResult {
				t.Fatalf("processOneManifest() result = %s, want %s", bundle.applyOrReportDiffResTyp, tc.wantResult)
			}
			if len(bundle.diffs) == 0 {
				t.Fatal("processOneManifest() reported no diffs, want at least one path")
			}
			for _, diff := range bundle.diffs {
				if diff.ValueInMember != "" || diff.ValueInHub != "" {
					t.Errorf("processOneManifest() diff at path %q contains values: member=%q, hub=%q", diff.Path, diff.ValueInMember, diff.ValueInHub)
				}
			}
		})
	}
}
