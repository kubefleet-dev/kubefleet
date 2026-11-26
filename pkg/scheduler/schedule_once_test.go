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

package scheduler

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	fleetv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/scheduler/queue"
)

type trackingPlacementSchedulingQueue struct {
	nextKey     queue.PlacementKey
	rateLimited []queue.PlacementKey
	forgotten   []queue.PlacementKey
}

func (q *trackingPlacementSchedulingQueue) Run() {}

func (q *trackingPlacementSchedulingQueue) Close() {}

func (q *trackingPlacementSchedulingQueue) CloseWithDrain() {}

func (q *trackingPlacementSchedulingQueue) NextPlacementKey() (queue.PlacementKey, bool) {
	return q.nextKey, false
}

func (q *trackingPlacementSchedulingQueue) Done(queue.PlacementKey) {}

func (q *trackingPlacementSchedulingQueue) Add(queue.PlacementKey) {}

func (q *trackingPlacementSchedulingQueue) AddRateLimited(key queue.PlacementKey) {
	q.rateLimited = append(q.rateLimited, key)
}

func (q *trackingPlacementSchedulingQueue) AddAfter(queue.PlacementKey, time.Duration) {}

func (q *trackingPlacementSchedulingQueue) AddBatched(queue.PlacementKey) {}

func (q *trackingPlacementSchedulingQueue) Forget(key queue.PlacementKey) {
	q.forgotten = append(q.forgotten, key)
}

func TestScheduleOncePolicySnapshotLookupErrors(t *testing.T) {
	placementKey := queue.PlacementKey(crpName)
	placement := &fleetv1beta1.ClusterResourcePlacement{
		ObjectMeta: metav1.ObjectMeta{Name: crpName},
	}

	testCases := []struct {
		name            string
		listErr         error
		policySnapshots []client.Object
		wantRateLimited []queue.PlacementKey
		wantForgotten   []queue.PlacementKey
	}{
		{
			name:          "no latest snapshot waits for watcher",
			wantForgotten: []queue.PlacementKey{placementKey},
		},
		{
			name:            "API server error is requeued",
			listErr:         apierrors.NewServiceUnavailable("simulated API server failure"),
			wantRateLimited: []queue.PlacementKey{placementKey},
		},
		{
			name:            "unexpected cache error is requeued",
			listErr:         fmt.Errorf("simulated cache failure"),
			wantRateLimited: []queue.PlacementKey{placementKey},
		},
		{
			name: "multiple latest snapshots are not requeued",
			policySnapshots: []client.Object{
				latestPolicySnapshot("snapshot-1"),
				latestPolicySnapshot("snapshot-2"),
			},
			wantForgotten: []queue.PlacementKey{placementKey},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(placement.DeepCopy())
			if len(tc.policySnapshots) > 0 {
				builder.WithObjects(tc.policySnapshots...)
			}

			var fakeClient client.WithWatch = builder.Build()
			if tc.listErr != nil {
				fakeClient = interceptor.NewClient(fakeClient, interceptor.Funcs{
					List: func(_ context.Context, _ client.WithWatch, _ client.ObjectList, _ ...client.ListOption) error {
						return tc.listErr
					},
				})
			}

			workQueue := &trackingPlacementSchedulingQueue{nextKey: placementKey}
			s := &Scheduler{queue: workQueue, client: fakeClient}
			s.scheduleOnce(context.Background(), 0)

			if diff := cmp.Diff(workQueue.rateLimited, tc.wantRateLimited); diff != "" {
				t.Errorf("rate-limited keys diff (-got, +want):\n%s", diff)
			}
			if diff := cmp.Diff(workQueue.forgotten, tc.wantForgotten); diff != "" {
				t.Errorf("forgotten keys diff (-got, +want):\n%s", diff)
			}
		})
	}
}

func latestPolicySnapshot(name string) client.Object {
	return &fleetv1beta1.ClusterSchedulingPolicySnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				fleetv1beta1.PlacementTrackingLabel: crpName,
				fleetv1beta1.IsLatestSnapshotLabel:  "true",
			},
		},
	}
}
