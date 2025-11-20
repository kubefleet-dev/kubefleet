# UpdateRun State Management Implementation

## Date
2025-11-14

## Context
Implementing state management for UpdateRun to support start/stop/abandon lifecycle operations. The UpdateRun needs to handle different states with specific behaviors for each state.

## State Transition Diagram
The user provided a state transition diagram showing:

```
NotStarted → Started → Stopped
      ↓          ↓         ↓
      → Abandoned (Terminal) ←
                
Resume: Stopped → Started
```

### Valid State Transitions:
- **NotStarted → Started**: Valid (user sets state to Started)
- **NotStarted → Abandoned**: Valid (abandon before starting)
- **Started → Stopped**: Valid (user sets state to Stopped)
- **Started → Abandoned**: Valid (abandon while running)
- **Stopped → Started**: Valid (Resume - user sets state back to Started)
- **Stopped → Abandoned**: Valid (abandon while stopped)

### Invalid State Transitions:
- **Any State → NotStarted**: Invalid (cannot go back to NotStarted)
- **Abandoned → Any State**: Invalid (Abandoned is terminal)
- CEL validations already exist to prevent these invalid transitions

## Requirements

### 1. NotStarted State
- Only initialize the UpdateRun
- Do NOT execute any cluster updates
- Set up initial status but don't start processing

### 2. Started State
- Execute the UpdateRun normally
- Process cluster updates according to stages and maxConcurrency
- Continue until completion or state changes

### 3. Stopped State
- Stop execution gracefully
- If a cluster is currently updating, let that update complete within the stage
- Do NOT start processing any more clusters for execution
- Update Progressing condition to False with appropriate reason (UpdateRunStoppedReason)
- Can be resumed by transitioning back to Started state

### 4. Abandoned State
- Abandon the UpdateRun immediately
- Update Progressing condition to False with appropriate reason (UpdateRunAbandonedReason)
- This is a TERMINAL state - no further state transitions allowed
- Mark the UpdateRun as complete/failed

## Implementation Plan

### Phase 1: Add Reason Constants
**File**: `pkg/utils/condition/reason.go`
- Add `UpdateRunStoppedReason` constant
- Add `UpdateRunAbandonedReason` constant

**Tasks**:
- [x] Add UpdateRunStoppedReason = "UpdateRunStopped"
- [x] Add UpdateRunAbandonedReason = "UpdateRunAbandoned"

### Phase 2: Modify Controller Logic
**File**: `pkg/controllers/updaterun/controller.go`

**Tasks**:
- [x] Add state checking at the beginning of Reconcile
- [x] Handle NotStarted state - initialize only, don't execute
- [x] Handle Started state - normal execution flow (including resume from Stopped)
- [x] Handle Stopped state - mark as stopped, return without execution
- [x] Handle Abandoned state - mark as abandoned and complete, return

**Implementation Details**:
```go
// In Reconcile function, after validation and before execution:
1. Check updateRun.Spec.UpdateState
2. Switch on state:
   - NotStarted: Initialize status only, return
   - Started: Continue with existing execution logic (works for both new runs and resumed runs)
   - Stopped: Mark progressing=false (UpdateRunStoppedReason), return
   - Abandoned: Mark progressing=false (UpdateRunAbandonedReason), mark as complete, return
```

**Note**: When transitioning from Stopped → Started (resume), the Started state handler will 
automatically pick up where it left off since the status already contains the progress information.

### Phase 3: Modify Execution Logic
**File**: `pkg/controllers/updaterun/execution.go`

**Tasks**:
- [ ] Add state check in executeUpdatingStage
- [ ] When state is Stopped, check if any clusters are currently updating
- [ ] If clusters are updating, let them finish but don't start new ones
- [ ] Return appropriate wait time or completion signal

**Implementation Details**:
```go
// In executeUpdatingStage function:
1. Check updateRun.Spec.UpdateState
2. If Stopped:
   - Count clusters that are already started (ClusterUpdatingConditionStarted == True)
   - Let those complete by checking their status
   - Do NOT start any new clusters (skip the loop that starts updates)
   - Return with wait time if clusters still updating
   - Return completion if all started clusters are done
```

### Phase 4: Add Helper Functions
**File**: `pkg/controllers/updaterun/execution.go`

**Tasks**:
- [x] Add markUpdateRunStopped() function
- [x] Add markUpdateRunAbandoned() function
- [ ] Add isUpdateRunStopped() helper (if needed)
- [ ] Add isUpdateRunAbandoned() helper (if needed)

### Phase 5: Add Unit Tests
**File**: `pkg/controllers/updaterun/execution_test.go`

**Tasks**:
- [ ] Test NotStarted state behavior
- [ ] Test Started state execution
- [ ] Test Stopped state - graceful stop with in-progress clusters
- [ ] Test Stopped state - no clusters in progress
- [ ] Test Abandoned state handling
- [ ] Test Resume (Stopped → Started) transition

### Phase 6: Add E2E Tests
**Files**: 
- `test/e2e/cluster_staged_updaterun_test.go`
- `test/e2e/staged_updaterun_test.go`

**Tasks**:
- [ ] Test stop operation during execution
- [ ] Test resume operation after stop
- [ ] Test abandon operation
- [ ] Test that abandoned state is terminal

## Technical Details

### Condition Updates

**For Stopped State**:
```go
meta.SetStatusCondition(&updateRunStatus.Conditions, metav1.Condition{
    Type:               string(placementv1beta1.StagedUpdateRunConditionProgressing),
    Status:             metav1.ConditionFalse,
    ObservedGeneration: updateRun.GetGeneration(),
    Reason:             condition.UpdateRunStoppedReason,
    Message:            "The update run has been stopped by user request",
})
```

**For Abandoned State**:
```go
meta.SetStatusCondition(&updateRunStatus.Conditions, metav1.Condition{
    Type:               string(placementv1beta1.StagedUpdateRunConditionProgressing),
    Status:             metav1.ConditionFalse,
    ObservedGeneration: updateRun.GetGeneration(),
    Reason:             condition.UpdateRunAbandonedReason,
    Message:            "The update run has been abandoned and cannot be resumed",
})
```

### Graceful Stop Logic

When Stopped state is detected:
1. Don't start any NEW cluster updates
2. Check for in-progress clusters (ClusterUpdatingConditionStarted == True && ClusterUpdatingConditionSucceeded != True)
3. Continue monitoring those clusters until they complete
4. Once all in-progress clusters are done, mark stage/run as stopped

### Key Considerations

1. **Idempotency**: State handling should be idempotent - multiple reconciles in the same state should be safe
2. **Graceful Handling**: Stopped state should allow current operations to complete
3. **Terminal State**: Abandoned state must prevent any further execution
4. **Status Updates**: All state transitions should update conditions appropriately
5. **Metrics**: Consider adding metrics for state transitions
6. **Requeue**: Appropriate requeue times for different states (e.g., wait while clusters complete in Stopped state)

## Questions & Decisions

### Q1: Should Stopped state wait for ALL clusters in a stage or just currently updating ones?
**Decision**: Wait only for clusters that have already started (ClusterUpdatingConditionStarted == True). Don't start any new clusters.

### Q2: What happens to after-stage tasks when stopped?
**Decision**: If a stage is stopped, don't execute after-stage tasks. They should be handled when/if the updateRun is resumed.

### Q3: Can we stop during after-stage tasks (approval/wait)?
**Decision**: Yes, stopped state should be checked even during after-stage task execution. If stopped during approval wait, just mark as stopped and don't wait for approval.

### Q4: Should abandoned state do any cleanup?
**Decision**: Abandoned state should mark the updateRun as complete with failed condition. No need to update individual cluster statuses - they remain as-is showing the last known state.

## Success Criteria

1. ✅ UpdateRun in NotStarted state only initializes, doesn't execute
2. ✅ UpdateRun in Started state executes normally
3. ✅ UpdateRun in Stopped state stops gracefully (completes current cluster updates)
4. ✅ UpdateRun in Abandoned state terminates immediately and is terminal
5. ✅ Resume (Stopped → Started) works correctly
6. ✅ Invalid state transitions are prevented by CEL validation
7. ✅ All unit tests pass
8. ✅ All E2E tests pass
9. ✅ Appropriate conditions are set for each state
10. ✅ Metrics are updated for state transitions

## Related Files

- `apis/placement/v1beta1/stageupdate_types.go` - UpdateRun type definitions
- `pkg/controllers/updaterun/controller.go` - Main reconciliation logic
- `pkg/controllers/updaterun/execution.go` - Execution logic
- `pkg/utils/condition/reason.go` - Condition reason constants
- `test/e2e/cluster_staged_updaterun_test.go` - E2E tests
- `test/e2e/staged_updaterun_test.go` - E2E tests

## Notes

- CEL validation already exists to prevent invalid state transitions (going back to NotStarted, transitioning from Abandoned)
- The implementation should be careful not to break existing functionality for updateRuns without explicit state set
- Consider backward compatibility - what happens to existing updateRuns that don't have state field set?

## Next Steps

1. Start with Phase 1 - add reason constants
2. Implement controller logic changes (Phase 2)
3. Implement execution logic changes (Phase 3)
4. Add helper functions (Phase 4)
5. Write comprehensive unit tests (Phase 5)
6. Write E2E tests (Phase 6)
7. Test thoroughly including edge cases
8. Update documentation if needed
