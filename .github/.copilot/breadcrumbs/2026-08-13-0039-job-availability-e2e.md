# Job Availability Tracking

## Overview

Track the availability of `batch/v1` Jobs in the work applier, so that a placement reports a Job as available
when it has completed rather than when it has been applied.

## Decisions

- A Job is available when its `Complete` condition is true.
- A failed Job is reported as not yet available, the same as any other workload that fails to become ready;
  it blocks a rolling update from proceeding to more clusters.
- A suspended Job that has not completed or failed is available immediately, as it does not run until it is
  resumed, e.g., by a job queueing system in the member cluster.
- Availability tracking is on by default, the same as the other workload types. A running Job blocks a rolling
  update until it completes. This reverses the removal of the Job availability tracking in 2024, which
  considered a Job available as soon as it had one ready or succeeded pod.

## Validation

- Unit tests for all the Job states, plus the work applier integration tests.
- The CRP and RP rollout E2E tests verify that a completed Job is available, and that a long-running Job is
  not yet available and blocks the rollout.

## Follow-ups

- Update the Job section of the safe rollout docs (website repository) to match.
