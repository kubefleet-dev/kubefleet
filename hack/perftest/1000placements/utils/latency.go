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

	wg.Go(func() {
		for {
			var attempt latencyTrackAttempt
			var readOK bool
			select {
			case attempt, readOK = <-r.toTrackLatencyChan:
				if !readOK {
					return
				}
				fmt.Printf("latency tracker: CRP %s has latency %v\n", fmt.Sprintf(placementNameFmt, attempt.resIdx), attempt.latency)
				r.placementCompletionLatencyByName[fmt.Sprintf(placementNameFmt, attempt.resIdx)] = attempt.latency
			case <-ctx.Done():
				return
			}
		}
	})
	wg.Wait()
}

func (r *Runner) TallyLatencyQuantiles() {
	latencies := make([]float64, 0, len(r.placementCompletionLatencyByName))
	for _, latency := range r.placementCompletionLatencyByName {
		latencies = append(latencies, float64(latency.Seconds()))
	}
	slices.Sort(latencies)
	q25 := int(math.Floor(float64(len(latencies)) * 0.25))
	q50 := int(math.Floor(float64(len(latencies)) * 0.50))
	q75 := int(math.Floor(float64(len(latencies)) * 0.75))
	q90 := int(math.Floor(float64(len(latencies)) * 0.90))
	q99 := int(math.Floor(float64(len(latencies)) * 0.99))
	fmt.Printf("latencies: 25th=%v, 50th=%v, 75th=%v, 90th=%v, 99th=%v\n",
		latencies[q25], latencies[q50], latencies[q75], latencies[q90], latencies[q99])
}
