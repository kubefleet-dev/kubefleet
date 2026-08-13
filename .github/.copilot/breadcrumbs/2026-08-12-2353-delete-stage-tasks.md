# Support Delete Stage Tasks

## Overview

Allow staged update strategies to gate the implicit deletion stage with the existing Approval and TimedWait task types.

## Plan

1. Add `deleteStageTasks` to the shared update strategy API with the same validation as after-stage tasks.
2. Snapshot and initialize delete-stage task statuses.
3. Refactor the after-stage task evaluator to accept a stage configuration and status directly, then use it before deleting bindings.
4. Add API validation, unit, and cluster-scoped/namespaced integration coverage.
5. Regenerate API code and CRDs, then run targeted tests and repository quality checks.

## Success Criteria

- [x] Unset delete-stage tasks preserve immediate deletion.
- [x] Approval and timed waits can independently gate deletion.
- [x] Approval and timed waits run concurrently and both must pass.
- [x] Cluster-scoped and namespaced runs retain bindings until their gate passes.
- [x] Generated API and CRD artifacts are current.
- [x] Targeted tests and available repository quality checks pass.

## Approval

Implementation was explicitly requested in the issue task.

## Implementation Notes

- Delete-stage approvals reuse the after-stage task machinery and labels.
- The delete stage uses a label-safe value for approval requests while retaining its canonical status/spec stage name.
- Gates run only before deletion starts; stopping while gated leaves bindings intact.
- Documentation updates are deferred to the separate documentation repository.
