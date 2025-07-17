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
- [ ] Add `DeletionPolicy` field to `PlacementSpec` in beta API
- [ ] Define enum values: `Delete` (default), `Orphan`
- [ ] Update API documentation and validation

### Phase 2: Controller Logic Updates
- [ ] Update scheduler to respect deletion policy when cleaning up bindings
- [ ] Add logic to skip binding deletion when policy is `Orphan`
- [ ] Update CRP controller to handle policy appropriately

### Phase 3: Testing
- [ ] Add unit tests for new deletion policy options
- [ ] Add integration tests to verify behavior
- [ ] Test both `Delete` and `Orphan` scenarios

### Phase 4: Documentation & Examples
- [ ] Update CRD documentation
- [ ] Add example configurations
- [ ] Update any user-facing documentation

## Success Criteria
- [ ] CRP API has `deletionPolicy` field with `Delete`/`Orphan` options
- [ ] Default behavior (`Delete`) preserves current functionality
- [ ] `Orphan` policy leaves placed resources intact when CRP is deleted
- [ ] All tests pass including new deletion policy tests
- [ ] Changes are minimal and backwards compatible

## Implementation Details

The key insight is that the scheduler's `cleanUpAllBindingsFor` method is what triggers the deletion of placed resources by deleting the bindings. We need to modify this to respect a deletion policy.

Plan:
1. Add `DeletionPolicy` field to `PlacementSpec`
2. Check this policy in scheduler before deleting bindings
3. For `Orphan` policy, remove finalizers but don't delete bindings
4. For `Delete` policy (default), maintain current behavior