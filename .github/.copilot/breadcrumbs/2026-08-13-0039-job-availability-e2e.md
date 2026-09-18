# Job Availability E2E

## Plan

- Verify focused unit coverage for completed, running, failed, and suspended Jobs.
- Update hub-workload status expectations for tracked Jobs.
- Cover both completed and long-running Job rollout behavior in the ResourcePlacement E2E test.
- Run focused tests and repository review checks.

## Decisions

- Reuse the safe-rollout status helper to verify that a long-running Job is `NotYetAvailable` on one cluster and blocks rollout to the remaining clusters.
- Keep failed Jobs `NotYetAvailable`; distinct failed-state handling remains out of scope.

## Validation

- `go test ./pkg/controllers/workapplier -run 'TestTrackJobAvailability|TestTrackInMemberClusterObjAvailability' -count=1`
- `go test ./test/e2e -run '^$' -count=1`
