package utils

import (
	"context"
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

func (r *Runner) CleanUp(ctx context.Context) {
	wg := sync.WaitGroup{}

	// Run the producer.
	wg.Add(1)
	go func() {
		defer wg.Done()

		for i := 0; i < r.maxCRPToCreateCount; i++ {
			select {
			case r.toDeleteChan <- i:
			case <-ctx.Done():
				close(r.toDeleteChan)
				return
			}
		}

		close(r.toDeleteChan)
	}()

	// Run the workers.
	for i := 0; i < r.resourceSetupWorkerCount; i++ {
		wg.Add(1)
		go func(workerIdx int) {
			defer wg.Done()

			for {
				// Read from the channel.
				var resIdx int
				var readOk bool
				select {
				case resIdx, readOk = <-r.toDeleteChan:
					if !readOk {
						fmt.Printf("worker %d exits\n", workerIdx)
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
				errAfterRetries := retry.OnError(r.retryOpsBackoff, func(err error) bool {
					return err != nil && !errors.IsNotFound(err)
				}, func() error {
					return r.hubClient.Delete(ctx, &crp)
				})
				if errAfterRetries != nil && !errors.IsNotFound(errAfterRetries) {
					fmt.Printf("worker %d: failed to delete CRP crp-%d after retries: %v\n", workerIdx, resIdx, errAfterRetries)
					continue
				}

				// Wait until the CRP is deleted.
				errAfterRetries = retry.OnError(r.retryOpsBackoff, func(err error) bool {
					return err != nil && !errors.IsNotFound(err)
				}, func() error {
					crp := placementv1beta1.ClusterResourcePlacement{}
					err := r.hubClient.Get(ctx, types.NamespacedName{Name: fmt.Sprintf("crp-%d", resIdx)}, &crp)
					if err == nil {
						return fmt.Errorf("CRP crp-%d still exists", resIdx)
					}
					return err
				})
				if !errors.IsNotFound(errAfterRetries) {
					fmt.Printf("worker %d: failed to wait for CRP crp-%d to be deleted after retries: %v\n", workerIdx, resIdx, errAfterRetries)
				} else {
					fmt.Printf("worker %d: deleted CRP crp-%d\n", workerIdx, resIdx)
				}

				// Delete the namespace if it exists.
				namespace := corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: fmt.Sprintf("work-%d", resIdx),
					},
				}
				errAfterRetries = retry.OnError(r.retryOpsBackoff, func(err error) bool {
					return err != nil && !errors.IsNotFound(err)
				}, func() error {
					return r.hubClient.Delete(ctx, &namespace)
				})
				if errAfterRetries != nil && !errors.IsNotFound(errAfterRetries) {
					fmt.Printf("worker %d: failed to delete namespace work-%d after retries: %v\n", workerIdx, resIdx, errAfterRetries)
					continue
				}

				// Wait until the namespace is deleted.
				errAfterRetries = retry.OnError(r.retryOpsBackoff, func(err error) bool {
					return err != nil && !errors.IsNotFound(err)
				}, func() error {
					namespace := corev1.Namespace{}
					err := r.hubClient.Get(ctx, types.NamespacedName{Name: fmt.Sprintf("work-%d", resIdx)}, &namespace)
					if err == nil {
						return fmt.Errorf("namespace work-%d still exists", resIdx)
					}
					return err
				})
				if errAfterRetries == nil || !errors.IsNotFound(errAfterRetries) {
					fmt.Printf("worker %d: failed to wait for namespace work-%d to be deleted after retries: %v\n", workerIdx, resIdx, errAfterRetries)
				} else {
					fmt.Printf("worker %d: deleted namespace work-%d\n", workerIdx, resIdx)
				}
			}
		}(i)
	}
	wg.Wait()

	// Cool down.
	time.Sleep(r.betweenStageCoolDownPeriod)
}
