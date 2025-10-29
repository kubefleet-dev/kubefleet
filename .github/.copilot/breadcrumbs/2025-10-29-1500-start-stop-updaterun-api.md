# Start/Stop UpdateRun API Implementation

## Date: 2025-10-29-1500

## Requirements

**Primary Objective**: Implement the ability to start and stop an UpdateRun. Currently when we create an updateRun it immediately initializes and executes. Instead when an updateRun is created we initialize but won't execute until user starts the updateRun. And we want the ability to stop the updateRun and if resources are being propagated to a particular cluster when stopped it will complete propagation for the cluster and stop propagation for all other cluster in that stage. And when updateRun is started again it will continue where it left off.

## Understanding Current Implementation

From the codebase analysis:

1. **Current Flow**: CreateUpdateRun → Initialize → Execute immediately 
2. **Controller Logic**: Located in `/pkg/controllers/updaterun/controller.go`
3. **Main Reconcile Loop**: Calls `initialize()` then immediately `execute()`
4. **Conditions**: Uses "Initialized", "Progressing", "Succeeded" conditions
5. **Stage Execution**: Tracks per-stage and per-cluster status in `StageUpdatingStatus`

## Implementation Plan

### Phase 1: API Design - Add Start/Stop Controls
- [x] Task 1.1: Add `Started` field to UpdateRunSpec to control execution start
- [x] Task 1.2: Add new condition type `StagedUpdateRunConditionStarted` 
- [x] Task 1.3: Update UpdateRunSpec validation to handle new field
- [x] Task 1.4: Update kubebuilder comments and validation tags

### Phase 2: Controller Logic Updates
- [ ] Task 2.1: Modify controller.go reconcile loop to check Started field
- [ ] Task 2.2: Separate initialization from execution in controller flow
- [ ] Task 2.3: Add logic to mark UpdateRun as ready but not started after initialization
- [ ] Task 2.4: Handle start transition - when Started changes from false/nil to true
- [ ] Task 2.5: Implement stop transition - when Started changes from true to false

### Phase 3: Graceful Stop Implementation  
- [ ] Task 3.1: Track in-progress cluster updates during stop
- [ ] Task 3.2: Complete current cluster propagation before stopping
- [ ] Task 3.3: Mark stopped stage/clusters appropriately in status
- [ ] Task 3.4: Ensure resume from correct point when restarted

### Phase 4: Status and Condition Management
- [ ] Task 4.1: Add Started condition management functions
- [ ] Task 4.2: Update condition progression: Initialize → Started → Progressing → Succeeded
- [ ] Task 4.3: Handle stop scenarios in condition updates
- [ ] Task 4.4: Update metrics to track start/stop events

### Phase 5: Testing
- [ ] Task 5.1: Write unit tests for new spec field and conditions
- [ ] Task 5.2: Write integration tests for start/stop workflow
- [ ] Task 5.3: Write e2e tests for graceful stop and resume scenarios
- [ ] Task 5.4: Test edge cases (stop during cluster propagation, restart scenarios)

### Phase 6: Documentation and Examples
- [ ] Task 6.1: Update API documentation
- [ ] Task 6.2: Add example YAML files showing start/stop usage
- [ ] Task 6.3: Update user guide with start/stop procedures

## API Design

### UpdateRunSpec Changes
```go
type UpdateRunSpec struct {
    // ... existing fields ...
    
    // Started indicates whether the update run should be started.
    // When false or nil, the update run will initialize but not execute.
    // When true, the update run will begin execution.
    // Changing from true to false will gracefully stop the update run.
    // +kubebuilder:validation:Optional
    Started *bool `json:"started,omitempty"`
}
```

### New Condition Type
```go
const (
    // ... existing conditions ...
    
    // StagedUpdateRunConditionStarted indicates whether the staged update run has been started.
    // Its condition status can be one of the following:
    // - "True": The staged update run has been started and is ready to progress.
    // - "False": The staged update run is stopped or not yet started.
    StagedUpdateRunConditionStarted StagedUpdateRunConditionType = "Started"
)
```

## Success Criteria

1. **✅ Initialize Without Execute**: UpdateRun initializes successfully but waits for start signal
2. **✅ Start Control**: Setting `Started: true` begins execution from correct stage
3. **✅ Graceful Stop**: Setting `Started: false` completes current cluster and stops
4. **✅ Resume Capability**: Restarting continues from exact stopping point
5. **✅ Proper Conditions**: All condition transitions work correctly
6. **✅ Backward Compatibility**: Existing UpdateRuns continue to work (default to started)

## Current Status: Phase 1 Complete ✅

**Completed**: 
- Added `Started *bool` field to UpdateRunSpec with proper kubebuilder validation
- Added `StagedUpdateRunConditionStarted` condition type with proper documentation
- Updated kubebuilder printcolumn annotations to include Started condition in kubectl output
- Updated condition documentation to include "Started" in known conditions list
- Generated CRDs successfully with new API changes

**Next**: Ready to proceed with Phase 2 - Controller Logic Updates