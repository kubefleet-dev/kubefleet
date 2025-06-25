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

package workapplier

import (
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	fleetv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

// TestWhenWithFullSequence tests the When method.
func TestWhenWithFullSequence(t *testing.T) {
	work := &fleetv1beta1.Work{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: memberReservedNSName,
			Name:      workName,
		},
	}
	bundles := []*manifestProcessingBundle{}
	rateLimiter := NewRequeueFastSlowWithExponentialBackoffRateLimiter(
		5,   // 5 fast attempts.
		20,  // fast delay of 20 seconds.
		1.5, // exponential base of 1.5.
		300, // max delay of 300 seconds.
	)

	testCases := []struct {
		name                    string
		wantRequeueDelaySeconds float64
	}{
		{
			name:                    "attempt #1",
			wantRequeueDelaySeconds: 20,
		},
		{
			name:                    "attempt #2",
			wantRequeueDelaySeconds: 20,
		},
		{
			name:                    "attempt #3",
			wantRequeueDelaySeconds: 20,
		},
		{
			name:                    "attempt #4",
			wantRequeueDelaySeconds: 20,
		},
		{
			name:                    "attempt #5",
			wantRequeueDelaySeconds: 20,
		},
		{
			name:                    "attempt #6",
			wantRequeueDelaySeconds: 30, // 20 * 1.5 = 30
		},
		{
			name:                    "attempt #7",
			wantRequeueDelaySeconds: 45, // 30 * 1.5 = 45
		},
		{
			name:                    "attempt #8",
			wantRequeueDelaySeconds: 67.5, // 45 * 1.5 = 67.5
		},
		{
			name:                    "attempt #9",
			wantRequeueDelaySeconds: 101.25, // 67.5 * 1.5 = 101.25
		},
		{
			name:                    "attempt #10",
			wantRequeueDelaySeconds: 151.875, // 101.25 * 1.5 = 151.875
		},
		{
			name:                    "attempt #11",
			wantRequeueDelaySeconds: 227.8125, // 151.875 * 1.5 = 227.8125
		},
		{
			name:                    "attempt #12",
			wantRequeueDelaySeconds: 300, // 227.8125 * 1.5 = 341.71875, but capped at 300 seconds.
		},
		{
			name:                    "attempt #13",
			wantRequeueDelaySeconds: 300, // Capped at 300 seconds.
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			requeueDelay := rateLimiter.When(work, bundles)
			wantRequeueDelay := time.Duration(tc.wantRequeueDelaySeconds * float64(time.Second))
			if !cmp.Equal(
				requeueDelay, wantRequeueDelay,
				cmpopts.EquateApprox(0.0, 0.0001), // Account for float precision limits.
			) {
				t.Errorf("When() = %v, want %v", requeueDelay, wantRequeueDelay)
			}
		})
	}
}

// TestWhenWithGenerationAndProcessingResultChange tests the When method.
func TestWhenWithGenerationAndProcessingResultChange(t *testing.T) {
	rateLimiter := NewRequeueFastSlowWithExponentialBackoffRateLimiter(
		1,  // 1 fast attempt.
		10, // fast delay of 10 seconds.
		2,  // exponential base of 5.
		60, // max delay of 60 seconds.
	)

	testCases := []struct {
		name             string
		work             *fleetv1beta1.Work
		bundles          []*manifestProcessingBundle
		wantRequeueDelay time.Duration
	}{
		{
			name: "first requeue",
			work: &fleetv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: memberReservedNSName,
					Name:      workName,
				},
			},
			bundles:          []*manifestProcessingBundle{},
			wantRequeueDelay: 10 * time.Second, // Fast delay.
		},
		{
			name: "second requeue",
			work: &fleetv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: memberReservedNSName,
					Name:      workName,
				},
			},
			bundles:          []*manifestProcessingBundle{},
			wantRequeueDelay: 20 * time.Second,
		},
		{
			name: "requeue (#3) w/ gen change",
			work: &fleetv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:  memberReservedNSName,
					Name:       workName,
					Generation: 2,
				},
			},
			bundles:          []*manifestProcessingBundle{},
			wantRequeueDelay: 10 * time.Second,
		},
		{
			name: "requeue #4",
			work: &fleetv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:  memberReservedNSName,
					Name:       workName,
					Generation: 2,
				},
			},
			bundles: []*manifestProcessingBundle{},
			// This is the second requeue after the generation change, so it should be 20 seconds.
			wantRequeueDelay: 20 * time.Second,
		},
		{
			name: "requeue #5 w/ processing result change",
			work: &fleetv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:  memberReservedNSName,
					Name:       workName,
					Generation: 2,
				},
			},
			bundles: []*manifestProcessingBundle{
				{
					applyResTyp: ManifestProcessingApplyResultTypeApplied,
				},
			},
			wantRequeueDelay: 10 * time.Second, // Fast delay, since the processing result has changed.
		},
		{
			name: "requeue #6",
			work: &fleetv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:  memberReservedNSName,
					Name:       workName,
					Generation: 2,
				},
			},
			bundles: []*manifestProcessingBundle{
				{
					applyResTyp: ManifestProcessingApplyResultTypeApplied,
				},
			},
			wantRequeueDelay: 20 * time.Second, // Fast delay, since the processing result has changed.
		},
		{
			name: "requeue #7 w/ both gen and processing result change",
			work: &fleetv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:  memberReservedNSName,
					Name:       workName,
					Generation: 3,
				},
			},
			bundles: []*manifestProcessingBundle{
				{
					applyResTyp: ManifestProcessingApplyResultTypeFailedToApply,
				},
			},
			wantRequeueDelay: 10 * time.Second, // Fast delay, since the processing result has changed.
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			requeueDelay := rateLimiter.When(tc.work, tc.bundles)
			if !cmp.Equal(
				requeueDelay, tc.wantRequeueDelay,
				cmpopts.EquateApprox(0.0, 0.0001), // Account for float precision limits.
			) {
				t.Errorf("When() = %v, want %v", requeueDelay, tc.wantRequeueDelay)
			}
		})
	}
}

// TestForget tests the Forget method.
func TestForget(t *testing.T) {
	workNamespacedName1 := types.NamespacedName{
		Namespace: memberReservedNSName,
		Name:      fmt.Sprintf(workNameTemplate, "1"),
	}
	workNamespacedName2 := types.NamespacedName{
		Namespace: memberReservedNSName,
		Name:      fmt.Sprintf(workNameTemplate, "2"),
	}
	workNamespacedName3 := types.NamespacedName{
		Namespace: memberReservedNSName,
		Name:      fmt.Sprintf(workNameTemplate, "3"),
	}

	bundles := []*manifestProcessingBundle{}

	defaultMaxFastAttempts := 3
	defaultFastDelay := 5 * time.Second
	defaultExponentialBase := 2.0
	defaultMaxDelay := 60 * time.Second

	testCases := []struct {
		name            string
		rateLimiter     *RequeueFastSlowWithExponentialBackoffRateLimiter
		work            *fleetv1beta1.Work
		wantRateLimiter *RequeueFastSlowWithExponentialBackoffRateLimiter
	}{
		{
			name: "forget tracked work",
			rateLimiter: &RequeueFastSlowWithExponentialBackoffRateLimiter{
				requeueCounter: map[types.NamespacedName]int{
					workNamespacedName1: 1,
					workNamespacedName2: 5,
					workNamespacedName3: 9,
				},
				lastTrackedGeneration: map[types.NamespacedName]int64{
					workNamespacedName1: 1,
					workNamespacedName2: 2,
					workNamespacedName3: 3,
				},
				lastTrackedProcessingResultHash: map[types.NamespacedName]string{
					workNamespacedName1: "hash-1",
					workNamespacedName2: "hash-2",
					workNamespacedName3: "hash-3",
				},
				maxFastAttempts: defaultMaxFastAttempts,
				fastDelay:       defaultFastDelay,
				exponentialBase: defaultExponentialBase,
				maxDelay:        defaultMaxDelay,
			},
			work: &fleetv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: memberReservedNSName,
					Name:      workNamespacedName2.Name,
				},
			},
			wantRateLimiter: &RequeueFastSlowWithExponentialBackoffRateLimiter{
				requeueCounter: map[types.NamespacedName]int{
					workNamespacedName1: 1,
					workNamespacedName3: 9,
				},
				lastTrackedGeneration: map[types.NamespacedName]int64{
					workNamespacedName1: 1,
					workNamespacedName3: 3,
				},
				lastTrackedProcessingResultHash: map[types.NamespacedName]string{
					workNamespacedName1: "hash-1",
					workNamespacedName3: "hash-3",
				},
				maxFastAttempts: defaultMaxFastAttempts,
				fastDelay:       defaultFastDelay,
				exponentialBase: defaultExponentialBase,
				maxDelay:        defaultMaxDelay,
			},
		},
		{
			name: "forget untracked work",
			rateLimiter: &RequeueFastSlowWithExponentialBackoffRateLimiter{
				requeueCounter: map[types.NamespacedName]int{
					workNamespacedName1: 1,
					workNamespacedName2: 5,
					workNamespacedName3: 9,
				},
				lastTrackedGeneration: map[types.NamespacedName]int64{
					workNamespacedName1: 1,
					workNamespacedName2: 2,
					workNamespacedName3: 3,
				},
				lastTrackedProcessingResultHash: map[types.NamespacedName]string{
					workNamespacedName1: "hash-1",
					workNamespacedName2: "hash-2",
					workNamespacedName3: "hash-3",
				},
				maxFastAttempts: defaultMaxFastAttempts,
				fastDelay:       defaultFastDelay,
				exponentialBase: defaultExponentialBase,
				maxDelay:        defaultMaxDelay,
			},
			work: &fleetv1beta1.Work{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: memberReservedNSName,
					Name:      workNamespacedName3.Name,
				},
			},
			wantRateLimiter: &RequeueFastSlowWithExponentialBackoffRateLimiter{
				requeueCounter: map[types.NamespacedName]int{
					workNamespacedName1: 1,
					workNamespacedName2: 5,
				},
				lastTrackedGeneration: map[types.NamespacedName]int64{
					workNamespacedName1: 1,
					workNamespacedName2: 2,
				},
				lastTrackedProcessingResultHash: map[types.NamespacedName]string{
					workNamespacedName1: "hash-1",
					workNamespacedName2: "hash-2",
				},
				maxFastAttempts: defaultMaxFastAttempts,
				fastDelay:       defaultFastDelay,
				exponentialBase: defaultExponentialBase,
				maxDelay:        defaultMaxDelay,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.rateLimiter.Forget(tc.work)

			if diff := cmp.Diff(
				tc.rateLimiter, tc.wantRateLimiter,
				cmpopts.IgnoreFields(RequeueFastSlowWithExponentialBackoffRateLimiter{}, "mu"),
				cmp.AllowUnexported(RequeueFastSlowWithExponentialBackoffRateLimiter{})); diff != "" {
				t.Errorf("Forget() mismatch (-got +want):\n%s", diff)
			}

			// Ensure that after forgetting the work, the rate limiter will return
			// an expected delay when the work is requeued again.
			requeueDelay := tc.rateLimiter.When(tc.work, bundles)
			wantRequeueDelay := defaultFastDelay
			// Account for float precision limits.
			if !cmp.Equal(requeueDelay, wantRequeueDelay, cmpopts.EquateApprox(0.0, 0.0001)) {
				t.Errorf("When() after Forget() = %v, want %v", requeueDelay, wantRequeueDelay)
			}
		})
	}
}

// TestComputeProcessingResultHash tests the computeProcessingResultHash function.
func TestComputeProcessingResultHash(t *testing.T) {
	work := &fleetv1beta1.Work{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: memberReservedNSName,
			Name:      workName,
		},
	}

	testCases := []struct {
		name     string
		bundles  []*manifestProcessingBundle
		wantHash string
	}{
		{
			// This is a case that normally should not occur.
			name:     "no manifest",
			bundles:  []*manifestProcessingBundle{},
			wantHash: "4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945",
		},
		{
			// This is a case that normally should not occur.
			name: "single manifest, no result of any type",
			bundles: []*manifestProcessingBundle{
				{},
			},
			wantHash: "ec6e5a3a69851e2b956b6f682bad1d2355faa874e635b4d2f3e33ce84a8f788a",
		},
		{
			name: "single manifest, apply op failure (pre-processing)",
			bundles: []*manifestProcessingBundle{
				{
					applyResTyp: ManifestProcessingApplyResultTypeDecodingErred,
				},
			},
			wantHash: "a4cce45a59ced1c0b218b7e2b07920e6515a0bd4e80141f114cf29a1e2062790",
		},
		{
			name: "single manifest, apply op failure (processing, no error message)",
			bundles: []*manifestProcessingBundle{
				{
					applyResTyp: ManifestProcessingApplyResultTypeFailedToApply,
				},
			},
			wantHash: "f4610fbac163e867a62672a3e95547e8321fa09709ecac73308dfff8fde49511",
		},
		{
			name: "single manifest, apply op failure (processing, with error message)",
			bundles: []*manifestProcessingBundle{
				{
					applyResTyp: ManifestProcessingApplyResultTypeFailedToApply,
					applyErr:    fmt.Errorf("failed to apply manifest"),
				},
			},
			// Note that this expected hash value is the same as the previous one.
			wantHash: "f4610fbac163e867a62672a3e95547e8321fa09709ecac73308dfff8fde49511",
		},
		{
			name: "single manifest, availability check failure",
			bundles: []*manifestProcessingBundle{
				{
					applyResTyp:        ManifestProcessingApplyResultTypeApplied,
					availabilityResTyp: ManifestProcessingAvailabilityResultTypeNotYetAvailable,
				},
			},
			wantHash: "9110cc26c9559ba84e909593a089fd495eb6e86479c9430d5673229ebe2d1275",
		},
		{
			name: "single manifest, apply op + availability check success",
			bundles: []*manifestProcessingBundle{
				{
					applyResTyp:        ManifestProcessingApplyResultTypeApplied,
					availabilityResTyp: ManifestProcessingAvailabilityResultTypeAvailable,
				},
			},
			wantHash: "d922098ce1f87b79fc26fad06355ea4eba77cc5a86e742e9159c58cce5bd4a31",
		},
		{
			name: "single manifest, diff reporting failure",
			bundles: []*manifestProcessingBundle{
				{
					reportDiffResTyp: ManifestProcessingReportDiffResultTypeFailed,
				},
			},
			wantHash: "dd541a034eb568cf92da960b884dece6d136460399ab68958ce8fc6730c91d45",
		},
		{
			name: "single manifest, diff reporting success",
			bundles: []*manifestProcessingBundle{
				{
					reportDiffResTyp: ManifestProcessingReportDiffResultTypeNoDiffFound,
				},
			},
			wantHash: "f9b66724190d196e1cf19247a0447a6ed0d71697dcb8016c0bc3b3726a757e1a",
		},
		{
			name: "multiple manifests (assorted)",
			bundles: []*manifestProcessingBundle{
				{
					applyResTyp: ManifestProcessingApplyResultTypeFailedToApply,
					applyErr:    fmt.Errorf("failed to apply manifest"),
				},
				{
					applyResTyp:        ManifestProcessingApplyResultTypeApplied,
					availabilityResTyp: ManifestProcessingAvailabilityResultTypeAvailable,
				},
				{
					applyResTyp:        ManifestProcessingApplyResultTypeApplied,
					availabilityResTyp: ManifestProcessingAvailabilityResultTypeNotTrackable,
				},
			},
			wantHash: "09c6195d94bfc84cdbb365bb615d3461a457a355b9f74049488a1db38e979018",
		},
		{
			name: "multiple manifests (assorted, different order)",
			bundles: []*manifestProcessingBundle{
				{
					applyResTyp:        ManifestProcessingApplyResultTypeApplied,
					availabilityResTyp: ManifestProcessingAvailabilityResultTypeAvailable,
				},
				{
					applyResTyp: ManifestProcessingApplyResultTypeFailedToApply,
					applyErr:    fmt.Errorf("failed to apply manifest"),
				},
				{
					applyResTyp:        ManifestProcessingApplyResultTypeApplied,
					availabilityResTyp: ManifestProcessingAvailabilityResultTypeNotTrackable,
				},
			},
			// Note that different orders of the manifests result in different hashes.
			wantHash: "ef1a6e8d207f5b86a8c7f39417eede40abc6e4f1d5ef9feceb5797f14a834f58",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hash, err := computeProcessingResultHash(work, tc.bundles)
			if err != nil {
				t.Fatalf("computeProcessingResultHash() = %v, want no error", err)
			}
			if hash != tc.wantHash {
				t.Errorf("computeProcessingResultHash() = %v, want %v", hash, tc.wantHash)
			}
		})
	}
}
