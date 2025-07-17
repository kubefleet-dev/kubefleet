# CRP Delete Policy Implementation

## Problem Analysis

The issue requests adding API options to allow customers to choose whether to delete placed resources when a CRP (ClusterResourcePlacement) is deleted.

### Current Deletion Behavior
1. When a CRP is deleted, it has two finalizers:
   - `ClusterResourcePlacementCleanupFinalizer` - handled by CRP controller to delete snapshots
   - `SchedulerCleanupFinalizer` - handled by scheduler to delete bindings
   
2. The deletion flow:
   - CRP controller removes snapshots (ClusterSchedulingPolicySnapshot, ClusterResourceSnapshot)
   - Scheduler removes bindings (ClusterResourceBinding) which triggers cleanup of placed resources
   - Currently there's no option for users to control whether placed resources are deleted

### References
- Kubernetes DeleteOptions has `PropagationPolicy` with values: `Orphan`, `Background`, `Foreground`
- AKS API has `DeletePolicy` pattern (couldn't fetch exact details but following similar pattern)

## Implementation Plan

### Phase 1: API Design
- [x] Add `DeletionPolicy` field to `PlacementSpec` in beta API
- [x] Define enum values: `Delete` (default), `Orphan`
- [x] Update API documentation and validation

### Phase 2: Controller Logic Updates
- [x] Update scheduler to respect deletion policy when cleaning up bindings
- [x] Add logic to skip binding deletion when policy is `Orphan`
- [x] Update CRP controller to handle policy appropriately

### Phase 3: Testing
- [x] Add unit tests for new deletion policy options
- [x] Add integration tests to verify behavior
- [x] Test both `Delete` and `Orphan` scenarios

### Phase 4: Documentation & Examples
- [x] Update CRD documentation
- [ ] Add example configurations
- [x] Update any user-facing documentation

## Success Criteria
- [x] CRP API has `deletionPolicy` field with `Delete`/`Orphan` options
- [x] Default behavior (`Delete`) preserves current functionality
- [x] `Orphan` policy leaves placed resources intact when CRP is deleted
- [x] All tests pass including new deletion policy tests
- [x] Changes are minimal and backwards compatible

## Implementation Details

The key insight is that the scheduler's `cleanUpAllBindingsFor` method is what triggers the deletion of placed resources by deleting the bindings. We need to modify this to respect a deletion policy.

### Changes Made:

1. **API Changes**: Added `DeletionPolicy` enum and field to `PlacementSpec` in `apis/placement/v1beta1/clusterresourceplacement_types.go`
   - Enum with values `Delete` (default) and `Orphan`
   - Field with proper validation and documentation

2. **Scheduler Logic Changes**: Modified `cleanUpAllBindingsFor` method in `pkg/scheduler/scheduler.go`
   - Check deletion policy from placement spec
   - For `Delete` policy: maintain current behavior (delete bindings)
   - For `Orphan` policy: remove finalizers but don't delete bindings (leaves placed resources intact)

3. **Tests Added**: Extended existing test suite in `pkg/scheduler/scheduler_test.go`
   - Test cases for both `Delete` and `Orphan` policies
   - Verified that bindings are deleted with `Delete` policy
   - Verified that bindings are orphaned (finalizers removed but not deleted) with `Orphan` policy

4. **Generated Artifacts**: 
   - Updated CRDs with proper enum validation and default value
   - Regenerated Go code for API types

## Behavior Summary

- **Default (`Delete`)**: When CRP is deleted, all placed resources are removed from member clusters (current behavior)
- **Orphan**: When CRP is deleted, placed resources remain on member clusters but are no longer managed by Fleet

This provides customers with the flexibility to choose between complete cleanup or leaving resources in place when deleting a CRP.