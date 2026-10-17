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

package workapplier

import (
	"fmt"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"

	placementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/condition"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/controller"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/resource"
)

const (
	attemptsWithFixedDelayUpperBound         = 40
	attemptsWithFixedDelayLowerBound         = 1
	fixedDelaySecondsLowerBound              = 2
	initialSlowBackoffDelaySecondsLowerBound = 2
	maxFastBackoffDelaySecondsUpperBound     = 3600 // 1 hour

	// Set caps on the bases to make sure that delay would be monotonically growing and
	// no overflowing would occur.
	exponentialBaseLowerBound = 1.05
	exponentialBaseUpperBound = 100.0

	// With the current set of extremums (attemptsWithFixedDelayUpperBound = 40,
	// initialSlowBackoffDelaySecondsLowerBound = 2, maxFastBackoffDelaySecondsUpperBound = 3600,
	// exponentialBaseLowerBound = 1.05),
	// the number of requeues needed for the requeue delay to reach to the maximum value
	// would be 195. The rate limiter has no need to track number of requeues beyond
	// this value.
	maxNumOfRequeuesToTrack = 200
)

const (
	defaultInitialSlowBackoffDelaySeconds = 2
	defaultBaseForExponentialBackoff      = 1.2
	defaultMaxSlowBackoffDelaySeconds     = 30
	defaultMaxFastBackoffDelaySeconds     = 900 // 15 minutes
)

const (
	gcTriggeringForgetThreshold = 100000
)

const (
	manifestProcessingStateStrTmpl = "%s,%s"
)

var defaultRequeueRateLimiter *MultiStageRequeueWithExponentialBackoffRateLimiter = NewMultiStageRequeueWithExponentialBackoffRateLimiter(
	// Allow 1 attempt of fixed delay; this helps give objects a bit of headroom to get available (or have
	// diffs reported).
	1,
	// Use a fixed delay of 5 seconds for the first attempt.
	//
	// Important (chenyu1): before the introduction of the requeue rate limiter, the work
	// applier uses static requeue intervals, specifically 5 seconds (if the work object is unavailable),
	// and 15 seconds (if the work object is available). There are a number of test cases that
	// implicitly assume this behavior (e.g., a test case might expect that the availability check completes
	// w/in 10 seconds), which is why the rate limiter uses the 5 seconds fast requeue delay by default.
	// If you need to change this value and see that some test cases begin to fail, update the test
	// cases accordingly.
	5,
	// Then switch to slow exponential backoff with a base of 1.2 with an initial delay of 2 seconds
	// and a cap of 15 seconds (12 requeues in total, ~90 seconds in total).
	// This is to allow fast checkups in cases where objects are not yet available or have not yet reported diffs.
	1.2,
	2,
	15,
	// Eventually, switch to a fast exponential backoff with a base of 1.5 with an initial delay of 15 seconds
	// and a cap of 15 minutes (10 requeues in total, ~42 minutes in total).
	1.5,
	900,
	// Allow skipping to the fast exponential backoff stage if the Work object becomes available
	// or has reported diffs.
	true,
	// When the Work object spec does not change and the processing result remains the same (unavailable or failed
	// to report diffs), the requeue pattern is essentially:
	// * 1 attempt of requeue with fixed delays (5 seconds); then
	// * 12 attempts of requeues with slow exponential backoff (factor of 1.2, ~90 seconds in total); then
	// * 10 attempts of requeues with fast exponential backoff (factor of 1.5, ~42 minutes in total);
	// * afterwards, requeue with a delay of 15 minutes indefinitely.
)

// MultiStageRequeueWithExponentialBackoffRateLimiter is a rate limiter that allows requeues of various
// delays in multiple stages; it helps to control the rate of requeues for Work objects while keeping
// the system as responsive as possible.
//
// It will:
//   - first, requeue with a fixed delay for a limited number of attempts;
//     this is a stage where the work objects are expected to be unavailable (e.g., it might be known
//     already that an application takes a while to start up).
//   - then, switch to exponential backoff at a slow rate if the processing results stay consistent,
//     until the delay period reaches a cap;
//     this is a stage where the work objects are expected to become available, and the system will
//     requeue (relatively) fast for a more responsive experience.
//   - at last, switch to exponential backoff at a fast rate with a cap, if the processing results
//     remain consistent;
//     this is a stage where the system no longer expects the work objects to have a status change
//     and chooses to requeue (relatively) slowly to avoid overwhelming the system.
//
// For work objects that are known to be available immediately, the rate
// limiter can be set to skip to the fast backoff stage to save unnecessary fast requeues.
//
// Note that the implementation distinguishes between work objects of different generations and
// processing results, so that work object spec change and/or processing result change
// will reset the requeue counter.
//
// TO-DO (chenyu1): the current implementation tracks processing results as strings, which incurs
// additional overhead when doing comparison (albeit small); evaluate if we need to switch to a more
// performant representation.
type MultiStageRequeueWithExponentialBackoffRateLimiter struct {
	mu                              sync.Mutex
	requeueCounter                  map[types.NamespacedName]int
	lastRequeueDelayTracker         map[types.NamespacedName]time.Duration
	lastTrackedGeneration           map[types.NamespacedName]int64
	lastTrackedProcessingResultHash map[types.NamespacedName]string
	forgets                         int

	attemptsWithFixedDelay int
	fixedDelay             time.Duration

	exponentialBaseForSlowBackoff float64
	initialSlowBackoffDelay       time.Duration
	maxSlowBackoffDelay           time.Duration

	exponentialBaseForFastBackoff float64
	maxFastBackoffDelay           time.Duration

	skipToFastBackoffForAvailableOrDiffReportedWorkObjs bool
}

// NewMultiStageRequeueWithExponentialBackoffRateLimiter creates a MultiStageRequeueWithExponentialBackoffRateLimiter.
func NewMultiStageRequeueWithExponentialBackoffRateLimiter(
	attemptsWithFixedDelay int,
	fixedDelaySeconds int,
	exponentialBaseForSlowBackoff float64,
	initialSlowBackoffDelaySeconds int,
	maxSlowBackoffDelaySeconds int,
	exponentialBaseForFastBackoff float64,
	maxFastBackoffDelaySeconds int,
	skipToFastBackoffForAvailableOrDiffReportedWorkObjs bool,
) *MultiStageRequeueWithExponentialBackoffRateLimiter {
	// Validate the input parameters and reset values as necessary.
	if !(attemptsWithFixedDelay >= attemptsWithFixedDelayLowerBound &&
		attemptsWithFixedDelay <= attemptsWithFixedDelayUpperBound) {
		// Reset the value.
		attemptsWithFixedDelay = attemptsWithFixedDelayLowerBound
		klog.Errorf("attemptsWithFixedDelay is not set to a valid value; set it to the minimum allowed value (%d) instead", attemptsWithFixedDelayLowerBound)
	}
	if fixedDelaySeconds < fixedDelaySecondsLowerBound {
		// Reset the value.
		fixedDelaySeconds = fixedDelaySecondsLowerBound
		klog.Errorf("fixedDelaySeconds is below the minimum allowed value (%d seconds); set it to the minimum allowed value instead", fixedDelaySecondsLowerBound)
	}

	if !(exponentialBaseForSlowBackoff >= exponentialBaseLowerBound &&
		exponentialBaseForSlowBackoff <= exponentialBaseForFastBackoff &&
		exponentialBaseForFastBackoff <= exponentialBaseUpperBound) {
		klog.Errorf(
			"exponentialBaseForSlowBackoff (%f) and exponentialBaseForFastBackoff (%f) are not set to valid values; using the default values instead (%f and %f)",
			exponentialBaseForSlowBackoff, exponentialBaseForFastBackoff,
			defaultBaseForExponentialBackoff, defaultBaseForExponentialBackoff)
		// Reset the values.
		exponentialBaseForSlowBackoff = defaultBaseForExponentialBackoff
		exponentialBaseForFastBackoff = defaultBaseForExponentialBackoff
	}

	if !(initialSlowBackoffDelaySeconds >= initialSlowBackoffDelaySecondsLowerBound &&
		initialSlowBackoffDelaySeconds <= maxSlowBackoffDelaySeconds &&
		maxSlowBackoffDelaySeconds <= maxFastBackoffDelaySeconds &&
		maxFastBackoffDelaySeconds <= maxFastBackoffDelaySecondsUpperBound) {
		klog.Errorf(
			"initialSlowBackoffDelaySeconds (%d), maxSlowBackoffDelaySeconds (%d), and maxFastBackoffDelaySeconds (%d) are not set to valid values; using the default values instead (%d seconds, %d seconds, and %d seconds)",
			initialSlowBackoffDelaySeconds, maxSlowBackoffDelaySeconds, maxFastBackoffDelaySeconds,
			defaultInitialSlowBackoffDelaySeconds, defaultMaxSlowBackoffDelaySeconds, defaultMaxFastBackoffDelaySeconds)
		// Reset the values.
		initialSlowBackoffDelaySeconds = defaultInitialSlowBackoffDelaySeconds
		maxSlowBackoffDelaySeconds = defaultMaxSlowBackoffDelaySeconds
		maxFastBackoffDelaySeconds = defaultMaxFastBackoffDelaySeconds
	}

	return &MultiStageRequeueWithExponentialBackoffRateLimiter{
		requeueCounter:                                      make(map[types.NamespacedName]int),
		lastRequeueDelayTracker:                             make(map[types.NamespacedName]time.Duration),
		lastTrackedGeneration:                               make(map[types.NamespacedName]int64),
		lastTrackedProcessingResultHash:                     make(map[types.NamespacedName]string),
		attemptsWithFixedDelay:                              attemptsWithFixedDelay,
		fixedDelay:                                          time.Duration(fixedDelaySeconds) * time.Second,
		exponentialBaseForSlowBackoff:                       exponentialBaseForSlowBackoff,
		initialSlowBackoffDelay:                             time.Duration(initialSlowBackoffDelaySeconds) * time.Second,
		maxSlowBackoffDelay:                                 time.Duration(maxSlowBackoffDelaySeconds) * time.Second,
		exponentialBaseForFastBackoff:                       exponentialBaseForFastBackoff,
		maxFastBackoffDelay:                                 time.Duration(maxFastBackoffDelaySeconds) * time.Second,
		skipToFastBackoffForAvailableOrDiffReportedWorkObjs: skipToFastBackoffForAvailableOrDiffReportedWorkObjs,
	}
}

// When returns the duration to wait before requeuing the item.
//
// The implementation borrows from the workqueue package's various rate limiter implementations.
func (r *MultiStageRequeueWithExponentialBackoffRateLimiter) When(
	work *placementv1alpha1.Work, manifestProcessingStates []*manifestProcessingState) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()

	namespacedName := types.NamespacedName{
		Namespace: work.Namespace,
		Name:      work.Name,
	}

	// Check if the Work object has been processed before, and check if the rate limiter needs
	// to reset the requeue counter.
	lastTrackedGen := r.lastTrackedGeneration[namespacedName]
	lastTrackedProcessingResHash := r.lastTrackedProcessingResultHash[namespacedName]
	curProcessingResHash, hashErr := computeProcessingResultHash(work, manifestProcessingStates)

	// Reset the requeue counter under the following conditions:
	// * A new generation of the Work object is being processed.
	// * Cannot compute the hash of the processing result; normally this should never occur.
	// * The processing result has changed since last observation.
	if lastTrackedGen != work.Generation ||
		hashErr != nil ||
		lastTrackedProcessingResHash != curProcessingResHash {
		r.requeueCounter[namespacedName] = 0
		r.lastRequeueDelayTracker[namespacedName] = 0
	}

	// Always update the last tracked generation and processing result hash
	// for the Work object, regardless of whether the requeue counter is reset or not.
	r.lastTrackedGeneration[namespacedName] = work.Generation
	r.lastTrackedProcessingResultHash[namespacedName] = curProcessingResHash

	numRequeues := r.requeueCounter[namespacedName]
	lastRequeueDelayWithBackoff := r.lastRequeueDelayTracker[namespacedName]
	var requeueDelay time.Duration

	availableCond := meta.FindStatusCondition(work.Status.Conditions, placementv1alpha1.WorkCondTypeAvailable)

	switch {
	case numRequeues < r.attemptsWithFixedDelay:
		// Requeue with a fixed delay for the first few attempts.
		requeueDelay = r.fixedDelay
	case numRequeues == r.attemptsWithFixedDelay:
		// Start to requeue with backoff.
		requeueDelay = r.initialSlowBackoffDelay
	case lastRequeueDelayWithBackoff >= r.maxFastBackoffDelay:
		// The requeue delay has reached the cap; no need to backoff anymore.
		requeueDelay = r.maxFastBackoffDelay
	case r.skipToFastBackoffForAvailableOrDiffReportedWorkObjs &&
		condition.IsConditionStatusTrue(availableCond, work.Generation):
		// The Work object is already in an available or diff reported state,
		// and the rate limiter is configured to skip to the fast backoff stage under such conditions.
		requeueDelay = time.Duration(float64(lastRequeueDelayWithBackoff) * r.exponentialBaseForFastBackoff)
	case lastRequeueDelayWithBackoff >= r.maxSlowBackoffDelay:
		// The requeue delay has reached the cap for the slow backoff stage;
		// start to fast back off.
		requeueDelay = time.Duration(float64(lastRequeueDelayWithBackoff) * r.exponentialBaseForFastBackoff)
	default:
		// Continue to slow back off.
		requeueDelay = time.Duration(float64(lastRequeueDelayWithBackoff) * r.exponentialBaseForSlowBackoff)
		if requeueDelay > r.maxSlowBackoffDelay {
			// Backing off slowly this time will break the cap for the slow backoff stage;
			// switch to fast back off.
			requeueDelay = time.Duration(float64(lastRequeueDelayWithBackoff) * r.exponentialBaseForFastBackoff)
		}
	}

	// Cap on the requeue delay.
	if requeueDelay > r.maxFastBackoffDelay {
		requeueDelay = r.maxFastBackoffDelay
	}

	// Update the requeue counter and last requeue delay tracker.
	if numRequeues+1 < maxNumOfRequeuesToTrack {
		r.requeueCounter[namespacedName] = numRequeues + 1
	}
	r.lastRequeueDelayTracker[namespacedName] = requeueDelay
	return requeueDelay
}

// Forget untracks a work object from the rate limiter.
func (r *MultiStageRequeueWithExponentialBackoffRateLimiter) Forget(work *placementv1alpha1.Work) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Reset the trackers for the item.
	namespacedName := types.NamespacedName{
		Namespace: work.Namespace,
		Name:      work.Name,
	}
	delete(r.requeueCounter, namespacedName)
	delete(r.lastRequeueDelayTracker, namespacedName)
	delete(r.lastTrackedGeneration, namespacedName)
	delete(r.lastTrackedProcessingResultHash, namespacedName)

	r.forgets++
	if r.forgets >= gcTriggeringForgetThreshold {
		// As Go does not GC used space in maps; we manually rebuild the maps to free up memory.
		r.garbageCollect()
		r.forgets = 0
	}
}

// computeProcessingResultHash returns the hash of the result of a work object processing attempt,
// specifically the apply result and the availability check result of each manifest.
//
// Note (chenyu1): there exists a corner case where even though the processing result for a specific
// manifest remains unchanged, the cause of the result have changed (e.g., an apply op first failed
// due to an API server error, then it failed again because a webhook denied it). At this moment
// the rate limiter does not distinguish between the two cases.
func computeProcessingResultHash(work *placementv1alpha1.Work, manifestProcessingStates []*manifestProcessingState) (string, error) {
	// The order of manifests is stable in a bundle.
	manifestProcessingResults := make([]string, 0, len(manifestProcessingStates))
	for _, manifestProcessingState := range manifestProcessingStates {
		manifestProcessingResults = append(manifestProcessingResults,
			fmt.Sprintf(manifestProcessingStateStrTmpl, manifestProcessingState.applyRes, manifestProcessingState.availabilityCheckRes))
	}

	processingResHash, err := resource.HashOf(manifestProcessingResults)
	if err != nil {
		wrappedErr := fmt.Errorf("failed to marshal processing results as JSON: %w", err)
		_ = controller.NewUnexpectedBehaviorError(wrappedErr)
		klog.ErrorS(wrappedErr, "Failed to compute processing result hash", "work", klog.KObj(work))
		return "", wrappedErr
	}
	return processingResHash, nil
}

func (r *MultiStageRequeueWithExponentialBackoffRateLimiter) garbageCollect() {
	rebuiltRequeueCounter := make(map[types.NamespacedName]int, len(r.requeueCounter))
	for k, v := range r.requeueCounter {
		rebuiltRequeueCounter[k] = v
	}
	r.requeueCounter = rebuiltRequeueCounter

	rebuiltLastRequeueDelayTracker := make(map[types.NamespacedName]time.Duration, len(r.lastRequeueDelayTracker))
	for k, v := range r.lastRequeueDelayTracker {
		rebuiltLastRequeueDelayTracker[k] = v
	}
	r.lastRequeueDelayTracker = rebuiltLastRequeueDelayTracker

	rebuiltLastTrackedGeneration := make(map[types.NamespacedName]int64, len(r.lastTrackedGeneration))
	for k, v := range r.lastTrackedGeneration {
		rebuiltLastTrackedGeneration[k] = v
	}
	r.lastTrackedGeneration = rebuiltLastTrackedGeneration

	rebuiltLastTrackedProcessingResultHash := make(map[types.NamespacedName]string, len(r.lastTrackedProcessingResultHash))
	for k, v := range r.lastTrackedProcessingResultHash {
		rebuiltLastTrackedProcessingResultHash[k] = v
	}
	r.lastTrackedProcessingResultHash = rebuiltLastTrackedProcessingResultHash

	r.forgets = 0
}
