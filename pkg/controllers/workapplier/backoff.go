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
	"math"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"

	fleetv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/controller"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/resource"
)

const (
	minMaxFastAttempts  = 0
	minFastDelaySeconds = 5
	maxMaxDelaySeconds  = 3600 // 1 hour
	minExponentialBase  = 1
)

const (
	processingResultStrTpl = "%s,%s,%s"
)

// RequeueFastSlowWithExponentialBackoffRateLimiter is a rate limiter that returns the requeue delay
// for each processed Work object.
//
// It will:
// * allow fast requeues for a limited number of attempts; then
// * switch to exponential backoff (with configurable exponential base) for all subsequent requeues with a cap.
//
// Note that the implementation distinguishes between Work objects of different generations and
// processing results, so that Work object spec change and/or processing result change
// will reset the requeue counter.
//
// TO-DO (chenyu1): the current implementation tracks processing results as strings, which incurs
// additional overhead when doing comparison (albeit small); evaluate if we need to switch to a more
// performant representation.
type RequeueFastSlowWithExponentialBackoffRateLimiter struct {
	mu                              sync.Mutex
	requeueCounter                  map[types.NamespacedName]int
	lastTrackedGeneration           map[types.NamespacedName]int64
	lastTrackedProcessingResultHash map[types.NamespacedName]string

	maxFastAttempts int
	fastDelay       time.Duration

	exponentialBase float64
	maxDelay        time.Duration
}

// NewRequeueFastSlowWithExponentialBackoffRateLimiter creates a RequeueFastSlowWithExponentialBackoffRateLimiter.
func NewRequeueFastSlowWithExponentialBackoffRateLimiter(
	maxFastAttempts int,
	fastDelaySeconds float64,
	exponentialBase float64,
	maxDelaySeconds float64,
) *RequeueFastSlowWithExponentialBackoffRateLimiter {
	if maxFastAttempts < minMaxFastAttempts {
		maxFastAttempts = minMaxFastAttempts
		klog.V(2).InfoS("maxFastAttempts is below the minimum value; set it to the minimum value instead", minMaxFastAttempts)
	}

	if fastDelaySeconds < minFastDelaySeconds {
		fastDelaySeconds = minFastDelaySeconds
		klog.V(2).InfoS("fastDelaySeconds is below the minimum value (%d seconds); set it to the minimum value instead", minFastDelaySeconds)
	}

	if maxDelaySeconds < minFastDelaySeconds {
		maxDelaySeconds = minFastDelaySeconds
		klog.V(2).InfoS("maxDelaySeconds is below the minimum value (%d seconds); set it to the minimum value instead", minFastDelaySeconds)
	}
	if maxDelaySeconds > maxMaxDelaySeconds {
		maxDelaySeconds = maxMaxDelaySeconds
		klog.V(2).InfoS("maxDelaySeconds is above the maximum value (%d seconds); set it to the maximum value instead", maxMaxDelaySeconds)
	}

	return &RequeueFastSlowWithExponentialBackoffRateLimiter{
		requeueCounter:                  make(map[types.NamespacedName]int),
		lastTrackedGeneration:           make(map[types.NamespacedName]int64),
		lastTrackedProcessingResultHash: make(map[types.NamespacedName]string),
		maxFastAttempts:                 maxFastAttempts,
		fastDelay:                       time.Duration(fastDelaySeconds * float64(time.Second)),
		exponentialBase:                 exponentialBase,
		maxDelay:                        time.Duration(maxDelaySeconds * float64(time.Second)),
	}
}

// When returns the duration to wait before requeuing the item.
//
// The implementation borrows from the workqueue package's various rate limiter implementations.
func (r *RequeueFastSlowWithExponentialBackoffRateLimiter) When(work *fleetv1beta1.Work, bundles []*manifestProcessingBundle) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()

	namespacedName := types.NamespacedName{
		Namespace: work.Namespace,
		Name:      work.Name,
	}

	// Check if the Work object has been tracked before; if so, verify if its has a generation change
	// or the processing result has changed.
	lastTrackedGen, isTracked := r.lastTrackedGeneration[namespacedName]
	lastTrackedProcessingResHash := r.lastTrackedProcessingResultHash[namespacedName]
	curProcessingResHash, hashErr := computeProcessingResultHash(work, bundles)
	switch {
	case !isTracked:
		// The Work object has never been tracked before. No action is needed.
	case lastTrackedGen != work.Generation:
		// A new generation of the Work object has been observed.
		// Reset the requeue counter.
		r.requeueCounter[namespacedName] = 0
	case hashErr != nil:
		// Cannot compute the hash of the processing result. Normally this should not occur.
		// Reset the requeue counter just in case.
		r.requeueCounter[namespacedName] = 0
	case lastTrackedProcessingResHash != curProcessingResHash:
		// The processing result has changed.
		// Reset the requeue counter.
		r.requeueCounter[namespacedName] = 0
	default:
		// The Work object has been tracked before and its generation and processing result have not changed.
		// No action is needed.
	}

	// Update the last tracked generation and processing result for the Work object.
	r.lastTrackedGeneration[namespacedName] = work.Generation
	r.lastTrackedProcessingResultHash[namespacedName] = curProcessingResHash

	// Increment the requeue counter for the item.
	r.requeueCounter[namespacedName] = r.requeueCounter[namespacedName] + 1

	// If the number of requeues is less than or equal to the max fast attempts,
	// return the fast delay.
	if r.requeueCounter[namespacedName] <= r.maxFastAttempts {
		return r.fastDelay
	}

	exp := r.requeueCounter[namespacedName] - r.maxFastAttempts
	backoff := float64(r.fastDelay.Nanoseconds()) * math.Pow(r.exponentialBase, float64(exp))
	// Add a cap to the backoff to avoid overflowing.
	if backoff > math.MaxInt64 {
		return r.maxDelay
	}
	backoffDuration := time.Duration(backoff)

	// Cap the delay at the maximum allowed delay.
	if backoffDuration > r.maxDelay {
		return r.maxDelay
	}
	return backoffDuration
}

// Forget untracks a Work object from the rate limiter.
func (r *RequeueFastSlowWithExponentialBackoffRateLimiter) Forget(work *fleetv1beta1.Work) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Reset the trackers for the item.
	namespacedName := types.NamespacedName{
		Namespace: work.Namespace,
		Name:      work.Name,
	}
	delete(r.requeueCounter, namespacedName)
	delete(r.lastTrackedGeneration, namespacedName)
	delete(r.lastTrackedProcessingResultHash, namespacedName)
}

// NumRequeues returns the number of times a Work object has been requeued.
func (r *RequeueFastSlowWithExponentialBackoffRateLimiter) NumRequeues(work *fleetv1beta1.Work) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Return the number of times the item has been requeued.
	return r.requeueCounter[types.NamespacedName{
		Namespace: work.Namespace,
		Name:      work.Name,
	}]
}

// computeProcessingResultHash returns the hash of the result of a Work object processing attempt,
// specifically the apply, availability check, and diff reporting results of each manifest.
//
// Note (chenyu1): there exists a corner case where even though the processing result for a specific
// manifest remains unchanged, the cause of the result have changed (e.g., an apply op first failed
// due to an API server error, then it failed again because a webhook denied it). At this moment
// the rate limiter does not distinguish between the two cases.
func computeProcessingResultHash(work *fleetv1beta1.Work, bundles []*manifestProcessingBundle) (string, error) {
	// The order of manifests is stable in a bundle.
	processingResults := make([]string, 0, len(bundles))
	for _, bundle := range bundles {
		processingResults = append(processingResults, fmt.Sprintf(processingResultStrTpl, bundle.applyResTyp, bundle.availabilityResTyp, bundle.reportDiffResTyp))
	}

	processingResHash, err := resource.HashOf(processingResults)
	if err != nil {
		wrappedErr := fmt.Errorf("failed to marshal processing results as JSON: %w", err)
		_ = controller.NewUnexpectedBehaviorError(wrappedErr)
		klog.ErrorS(wrappedErr, "Failed to compute processing result hash", "work", klog.KObj(work))
		return "", wrappedErr
	}
	return processingResHash, nil
}
