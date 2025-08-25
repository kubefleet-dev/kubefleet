package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math"
	"os"
	"os/exec"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/scheme"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

const (
	concurrentWorkerCount      = 20
	longPollingWorkerCount     = 5
	workerCoolDownPeriod       = time.Second * 2
	pollerRequeueWaitPeriod    = time.Second * 10
	betweenPhaseCoolDownPeriod = time.Second * 30

	configMapValueByteCount = 2
	maxCRPCount             = 2000
)

type latencyTrackAttempt struct {
	latency time.Duration
	resIdx  int
}

var (
	toDeleteChan          = make(chan int, 20)
	toCreateResourcesChan = make(chan int, 20)
	toCreateCRPsChan      = make(chan int, 20)
	longPollingCRPsChan   = make(chan int, maxCRPCount)
	latencyTrackerChan    = make(chan latencyTrackAttempt, maxCRPCount+1)

	longPollingCRPCount = atomic.Int32{}

	triggerPtsForMemProfileDumping = map[int]bool{
		999:  true,
		1999: true,
	}

	availablityCheckLatencyByCRPName = make(map[string]time.Duration, maxCRPCount)
)

var (
	retryOpsBackoff = wait.Backoff{
		Steps:    3,
		Duration: 2 * time.Second,
		Factor:   2.0,
		Jitter:   0.1,
	}
)

func main() {
	ctx := context.Background()

	// Set up the scheme.
	if err := placementv1beta1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("Failed to add placement v1beta1 APIs to the scheme: %v", err))
	}
	if err := corev1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("Failed to add core v1 APIs to the scheme: %v", err))
	}

	// Set up the K8s client for the hub cluster.
	hubClusterConfig := ctrl.GetConfigOrDie()
	hubClusterConfig.QPS = 200
	hubClusterConfig.Burst = 400
	hubClient, err := client.New(hubClusterConfig, client.Options{
		Scheme: scheme.Scheme,
	})
	if err != nil {
		panic(fmt.Sprintf("Failed to create hub client: %v", err))
	}

	// Read the arguments.
	doCleanUp := false
	cleanUpFlag := os.Getenv("CLEANUP")
	if len(cleanUpFlag) != 0 {
		doCleanUp = true
	}

	scaleTestRunName := os.Getenv("RUN_NAME")
	if len(scaleTestRunName) == 0 {
		panic("RUN_NAME environment variable is not set")
	}

	// Clean up any existing resources.
	if doCleanUp {
		wg := sync.WaitGroup{}

		// Run the producer.
		wg.Add(1)
		go func() {
			defer wg.Done()

			for i := 0; i < maxCRPCount; i++ {
				select {
				case toDeleteChan <- i:
				case <-ctx.Done():
					close(toDeleteChan)
					return
				}
			}

			close(toDeleteChan)
		}()

		// Run the workers.
		for i := 0; i < concurrentWorkerCount; i++ {
			wg.Add(1)
			go func(workerIdx int) {
				defer wg.Done()

				for {
					// Read from the channel.
					var resIdx int
					var readOk bool
					select {
					case resIdx, readOk = <-toDeleteChan:
						if !readOk {
							println(fmt.Sprintf("worker %d exits", workerIdx))
							return
						}
					case <-ctx.Done():
						return
					}

					// Delete the CRPs.
					crp := placementv1beta1.ClusterResourcePlacement{
						ObjectMeta: metav1.ObjectMeta{
							Name: fmt.Sprintf("crp-%d", resIdx),
						},
					}
					errAfterReties := retry.OnError(retryOpsBackoff, func(err error) bool {
						return err != nil && !errors.IsNotFound(err)
					}, func() error {
						return hubClient.Delete(ctx, &crp)
					})
					if errAfterReties != nil && !errors.IsNotFound(errAfterReties) {
						println(fmt.Sprintf("worker %d: failed to delete CRP crp-%d after retries: %v", workerIdx, resIdx, errAfterReties))
						continue
					}

					// Wait until the CRP is deleted.
					errAfterReties = retry.OnError(retryOpsBackoff, func(err error) bool {
						return err != nil && !errors.IsNotFound(err)
					}, func() error {
						crp := placementv1beta1.ClusterResourcePlacement{}
						err := hubClient.Get(ctx, types.NamespacedName{Name: fmt.Sprintf("crp-%d", resIdx)}, &crp)
						if err == nil {
							return fmt.Errorf("CRP crp-%d still exists", resIdx)
						}
						return err
					})
					if errAfterReties == nil || !errors.IsNotFound(errAfterReties) {
						println(fmt.Sprintf("worker %d: failed to wait for CRP crp-%d to be deleted after retries: %v", workerIdx, resIdx, errAfterReties))
					} else {
						println(fmt.Sprintf("worker %d: deleted CRP crp-%d", workerIdx, resIdx))
					}

					// Delete the namespace if it exists.
					namespace := corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name: fmt.Sprintf("work-%d", resIdx),
						},
					}
					errAfterReties = retry.OnError(retryOpsBackoff, func(err error) bool {
						return err != nil && !errors.IsNotFound(err)
					}, func() error {
						return hubClient.Delete(ctx, &namespace)
					})
					if errAfterReties != nil && !errors.IsNotFound(errAfterReties) {
						println(fmt.Sprintf("worker %d: failed to delete namespace work-%d after retries: %v", workerIdx, resIdx, errAfterReties))
						continue
					}

					// Wait until the namespace is deleted.
					errAfterReties = retry.OnError(retryOpsBackoff, func(err error) bool {
						return err != nil && !errors.IsNotFound(err)
					}, func() error {
						namespace := corev1.Namespace{}
						err := hubClient.Get(ctx, types.NamespacedName{Name: fmt.Sprintf("work-%d", resIdx)}, &namespace)
						if err == nil {
							return fmt.Errorf("namespace work-%d still exists", resIdx)
						}
						return err
					})
					if errAfterReties == nil || !errors.IsNotFound(errAfterReties) {
						println(fmt.Sprintf("worker %d: failed to wait for namespace work-%d to be deleted after retries: %v", workerIdx, resIdx, errAfterReties))
					} else {
						println(fmt.Sprintf("worker %d: deleted namespace work-%d", workerIdx, resIdx))
					}
				}
			}(i)
		}
		wg.Wait()

		// Cool down.
		time.Sleep(betweenPhaseCoolDownPeriod)
		return
	}

	// Prepare the resources for propagation.
	println("Preparing resources for propagation...")
	wg := sync.WaitGroup{}

	// Run the producer.
	wg.Add(1)
	go func() {
		defer wg.Done()

		for i := 0; i < maxCRPCount; i++ {
			select {
			case toCreateResourcesChan <- i:
			case <-ctx.Done():
				close(toCreateResourcesChan)
				return
			}
		}

		close(toCreateResourcesChan)
	}()

	// Run the workers to create namespaces and configMaps.
	for i := 0; i < concurrentWorkerCount; i++ {
		wg.Add(1)
		go func(workerIdx int) {
			defer wg.Done()

			for {
				// Read from the channel.
				var resIdx int
				var readOk bool
				select {
				case resIdx, readOk = <-toCreateResourcesChan:
					if !readOk {
						println(fmt.Sprintf("worker %d exits", workerIdx))
						return
					}
				case <-ctx.Done():
					return
				}

				// Create the namespace.
				namespace := corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: fmt.Sprintf("work-%d", resIdx),
					},
				}
				errAfterRetries := retry.OnError(retryOpsBackoff, func(err error) bool {
					return err != nil && !errors.IsAlreadyExists(err)
				}, func() error {
					return hubClient.Create(ctx, &namespace)
				})
				if errAfterRetries != nil && !errors.IsAlreadyExists(errAfterRetries) {
					println(fmt.Sprintf("worker %d: failed to create namespace work-%d after retries: %v", workerIdx, resIdx, errAfterRetries))
					continue
				}

				// Create the configMap in the namespace.
				fooValBytes := make([]byte, configMapValueByteCount)
				_, _ = rand.Read(fooValBytes)
				fooValStr := base64.StdEncoding.EncodeToString(fooValBytes)
				configMap := corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("data-%d", resIdx),
						Namespace: fmt.Sprintf("work-%d", resIdx),
					},
					Data: map[string]string{
						"foo": fooValStr,
					},
				}
				errAfterRetries = retry.OnError(retryOpsBackoff, func(err error) bool {
					return err != nil && !errors.IsAlreadyExists(err)
				}, func() error {
					return hubClient.Create(ctx, &configMap)
				})
				if errAfterRetries != nil && !errors.IsAlreadyExists(errAfterRetries) {
					println(fmt.Sprintf("worker %d: failed to create configMap data-%d in namespace work-%d after retries: %v", workerIdx, resIdx, resIdx, errAfterRetries))
					continue
				}

				println(fmt.Sprintf("worker %d: created namespace work-%d and configMap data-%d", workerIdx, resIdx, resIdx))
				time.Sleep(workerCoolDownPeriod)
			}
		}(i)
	}
	wg.Wait()

	// Cool down.
	println("All resources created, cooling down before creating CRPs...")
	time.Sleep(betweenPhaseCoolDownPeriod)

	// Prepare the CRPs for propagation.
	println("Preparing CRPs...")
	wg = sync.WaitGroup{}

	// Run the producer.
	wg.Add(1)
	go func() {
		defer wg.Done()

		for i := 0; i < maxCRPCount; i++ {
			select {
			case toCreateCRPsChan <- i:
			case <-ctx.Done():
				close(toCreateCRPsChan)
				return
			}
		}

		close(toCreateCRPsChan)
	}()

	// Run the workers to create CRPs.
	subWg1 := sync.WaitGroup{}
	for i := 0; i < concurrentWorkerCount; i++ {
		wg.Add(1)
		subWg1.Add(1)

		go func(workerIdx int) {
			defer wg.Done()
			defer subWg1.Done()

			for {
				// Read from the channel.
				var resIdx int
				var readOk bool
				select {
				case resIdx, readOk = <-toCreateCRPsChan:
					if !readOk {
						println(fmt.Sprintf("worker %d exits", workerIdx))
						return
					}
				case <-ctx.Done():
					return
				}

				// Create the CRP.
				crp := placementv1beta1.ClusterResourcePlacement{
					ObjectMeta: metav1.ObjectMeta{
						Name: fmt.Sprintf("crp-%d", resIdx),
					},
					Spec: placementv1beta1.PlacementSpec{
						ResourceSelectors: []placementv1beta1.ClusterResourceSelector{
							{
								Group:   "",
								Kind:    "Namespace",
								Version: "v1",
								Name:    fmt.Sprintf("work-%d", resIdx),
							},
						},
						Strategy: placementv1beta1.RolloutStrategy{
							Type: placementv1beta1.RollingUpdateRolloutStrategyType,
							RollingUpdate: &placementv1beta1.RollingUpdateConfig{
								MaxUnavailable:           ptr.To(intstr.FromString("100%")),
								UnavailablePeriodSeconds: ptr.To(1),
							},
						},
					},
				}
				errAfterRetries := retry.OnError(retryOpsBackoff, func(err error) bool {
					return err != nil && !errors.IsAlreadyExists(err)
				}, func() error {
					return hubClient.Create(ctx, &crp)
				})
				if errAfterRetries != nil && !errors.IsAlreadyExists(errAfterRetries) {
					println(fmt.Sprintf("worker %d: failed to create CRP crp-%d after retries: %v", workerIdx, resIdx, errAfterRetries))
					continue
				}

				println(fmt.Sprintf("worker %d: created CRP crp-%d", workerIdx, resIdx))

				// Wait until the propagation is done.
				errAfterRetries = retry.OnError(retryOpsBackoff, func(err error) bool {
					return err != nil
				}, func() error {
					crp := placementv1beta1.ClusterResourcePlacement{}
					if err := hubClient.Get(ctx, types.NamespacedName{Name: fmt.Sprintf("crp-%d", resIdx)}, &crp); err != nil {
						return fmt.Errorf("worker %d: failed to get CRP crp-%d: %w", workerIdx, resIdx, err)
					}

					availableCond := meta.FindStatusCondition(crp.Status.Conditions, string(placementv1beta1.ClusterResourcePlacementAvailableConditionType))
					if availableCond == nil || availableCond.Status != metav1.ConditionTrue || availableCond.ObservedGeneration != crp.Generation {
						return fmt.Errorf("worker %d: CRP crp-%d is not available yet", workerIdx, resIdx)
					}

					createdTimestamp := crp.GetCreationTimestamp()
					availablityLatency := availableCond.LastTransitionTime.Time.Sub(createdTimestamp.Time)
					latencyTrackerChan <- latencyTrackAttempt{
						latency: availablityLatency,
						resIdx:  resIdx,
					}
					return nil
				})
				if errAfterRetries != nil {
					println(fmt.Sprintf("worker %d: failed to wait for CRP crp-%d to be available after retries: %v", workerIdx, resIdx, errAfterRetries))
					longPollingCRPsChan <- resIdx
					longPollingCRPCount.Add(1)
				} else {
					println(fmt.Sprintf("worker %d: CRP crp-%d is available", workerIdx, resIdx))
				}

				// Dump the memory profile if needed.
				if _, ok := triggerPtsForMemProfileDumping[resIdx]; ok {
					println(fmt.Sprintf("worker %d: retrieving pprof data after CRP %d is created", workerIdx, resIdx))
					retrievePProfProfile(scaleTestRunName, resIdx)
				}
				// Cool down.
				time.Sleep(workerCoolDownPeriod)
			}
		}(i)
	}

	pollerAvailableCRPCount := atomic.Int32{}
	go func() {
		subWg1.Wait()

		for {
			c1 := longPollingCRPCount.Load()
			c2 := pollerAvailableCRPCount.Load()
			if c2 >= c1 {
				println("all CRPs are now available")
				close(longPollingCRPsChan)
				return
			}

			println(fmt.Sprintf("waiting for %d CRPs to be available, %d are available", c1, c2))
			time.Sleep(time.Second * 5)
		}
	}()

	// Run the long pollers.
	subWg2 := sync.WaitGroup{}
	for i := 0; i < longPollingWorkerCount; i++ {
		wg.Add(1)
		subWg2.Add(1)

		go func(pollerIdx int) {
			defer wg.Done()
			defer subWg2.Done()

			for {
				// Read from the channel.
				var resIdx int
				var readOK bool
				select {
				case resIdx, readOK = <-longPollingCRPsChan:
					if !readOK {
						println(fmt.Sprintf("long poller %d exits", pollerIdx))
						return
					}
				case <-ctx.Done():
					return
				}

				// Read the CRP.
				var crp placementv1beta1.ClusterResourcePlacement
				if err := hubClient.Get(ctx, types.NamespacedName{Name: fmt.Sprintf("crp-%d", resIdx)}, &crp); err != nil {
					println(fmt.Sprintf("poller %d: failed to get CRP crp-%d: %v", pollerIdx, resIdx, err))
					// Requeue this CRP.
					time.Sleep(pollerRequeueWaitPeriod)
					longPollingCRPsChan <- resIdx
					continue
				}

				// Check the status of the CRP.
				availableCond := meta.FindStatusCondition(crp.Status.Conditions, string(placementv1beta1.ClusterResourcePlacementAvailableConditionType))
				if availableCond == nil || availableCond.Status != metav1.ConditionTrue || availableCond.ObservedGeneration != crp.Generation {
					println(fmt.Sprintf("poller %d: CRP crp-%d is not available yet", pollerIdx, resIdx))
					// Requeue this CRP.
					time.Sleep(pollerRequeueWaitPeriod)
					longPollingCRPsChan <- resIdx
				} else {
					// The CRP is available.
					println(fmt.Sprintf("poller %d: CRP crp-%d is available", pollerIdx, resIdx))
					createdTimestamp := crp.GetCreationTimestamp()
					availablityLatency := availableCond.LastTransitionTime.Time.Sub(createdTimestamp.Time)
					latencyTrackerChan <- latencyTrackAttempt{
						latency: availablityLatency,
						resIdx:  resIdx,
					}

					pollerAvailableCRPCount.Add(1)
				}

			}
		}(i)
	}

	go func() {
		subWg2.Wait()
		println("All long pollers have completed")
		close(latencyTrackerChan)
	}()

	// Run the latency tracker.
	wg.Add(1)
	go func() {
		defer wg.Done()

		for {
			var attempt latencyTrackAttempt
			var readOK bool
			select {
			case attempt, readOK = <-latencyTrackerChan:
				if !readOK {
					return
				}
				println(fmt.Sprintf("latency tracker: CRP crp-%d has latency %v", attempt.resIdx, attempt.latency))
				availablityCheckLatencyByCRPName[fmt.Sprintf("crp-%d", attempt.resIdx)] = attempt.latency
			case <-ctx.Done():
				return
			}
		}
	}()
	wg.Wait()

	// No need to handle channels that are still open.

	println("retrieving final pprof data")
	retrievePProfProfile(scaleTestRunName, 0)

	// Tally the latencies quantiles.
	latencies := make([]float64, 0, len(availablityCheckLatencyByCRPName))
	for _, latency := range availablityCheckLatencyByCRPName {
		latencies = append(latencies, float64(latency.Milliseconds()))
	}
	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})
	q25 := int(math.Floor(float64(len(latencies)) * 0.25))
	q50 := int(math.Floor(float64(len(latencies)) * 0.50))
	q75 := int(math.Floor(float64(len(latencies)) * 0.75))
	q90 := int(math.Floor(float64(len(latencies)) * 0.90))
	q99 := int(math.Floor(float64(len(latencies)) * 0.99))
	println(fmt.Sprintf("latencies: 25th=%v, 50th=%v, 75th=%v, 90th=%v, 99th=%v",
		latencies[q25], latencies[q50], latencies[q75], latencies[q90], latencies[q99]))

	println("All CRPs created and resources are propagated successfully.")
}

func retrievePProfProfile(runName string, retrievalIdx int) {
	cmd := exec.Command("curl", "-o", fmt.Sprintf("pprof_%s_%d.out", runName, retrievalIdx), "http://localhost:10001/debug/pprof/heap")
	if err := cmd.Run(); err != nil {
		println(fmt.Sprintf("Failed to run curl command to retrieve pprof data: %v", err))
		return
	}
}
