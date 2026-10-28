package utils

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sync"
)

func (r *Runner) TrackLatency(ctx context.Context) {
	wg := sync.WaitGroup{}

	// Run the latency tracker.
	wg.Go(func() {
		for {
			var attempt latencyTrackAttempt
			var readOK bool
			select {
			case attempt, readOK = <-r.toTrackLatencyChan:
				if !readOK {
					return
				}
				fmt.Printf("latency tracker: staged update run run-%d has latency %v seconds\n", attempt.resIdx, attempt.latency.Seconds())
				r.stagedUpdateRunCompletionLatencyByRunName[fmt.Sprintf("run-%d", attempt.resIdx)] = attempt.latency
			case <-ctx.Done():
				return
			}
		}
	})
	wg.Wait()
}

func (r *Runner) TallyLatencyQuantiles() {
	latencies := make([]float64, 0, len(r.stagedUpdateRunCompletionLatencyByRunName))
	for _, latency := range r.stagedUpdateRunCompletionLatencyByRunName {
		latencies = append(latencies, latency.Seconds())
	}
	slices.Sort(latencies)

	q25 := int(math.Floor(float64(len(latencies)) * 0.25))
	q50 := int(math.Floor(float64(len(latencies)) * 0.50))
	q75 := int(math.Floor(float64(len(latencies)) * 0.75))
	q90 := int(math.Floor(float64(len(latencies)) * 0.90))
	q99 := int(math.Floor(float64(len(latencies)) * 0.99))
	fmt.Printf("latencies (seconds): 25th=%v, 50th=%v, 75th=%v, 90th=%v, 99th=%v\n",
		latencies[q25], latencies[q50], latencies[q75], latencies[q90], latencies[q99])
}
