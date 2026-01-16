# Placement Source Annotation Implementation

## Context
Add an annotation to all applied resources in the Work controller to track what source placement resource created them. This will help with debugging, resource tracking, and understanding the origin of deployed resources.

## Analysis Phase

### Current Understanding
- Work controller applies manifests to member clusters through `ApplyWorkReconciler`
- Resources are applied via two strategies: client-side and server-side apply
- Work objects contain a label `fleetv1beta1.CRPTrackingLabel` that tracks the placement
- Applied resources currently get owner references but no source placement annotation

### Implementation Options

#### Option 1: Add annotation during manifest preparation in apply_controller.go
- **Pros**: Centralized location, affects all appliers
- **Cons**: Requires passing placement info through the call chain

#### Option 2: Add annotation in each applier implementation
- **Pros**: More granular control, easier to implement
- **Cons**: Need to modify both client-side and server-side appliers

#### Option 3: Add annotation in the applyManifests function
- **Pros**: Single point of change, before any applier is called
- **Cons**: Need to extract placement info from Work object

### Questions and Decisions
1. **Annotation key**: Use `fleet.azure.com/source-placement` to follow existing pattern in `utils/common.go`
2. **Value format**: Need both the name of the placement and its namespace as we want to support both ResourcePlacement and ClusterResourcePlacement.
3. **Missing label handling**: Log warning and skip annotation if placement label is missing
4. **Placement info location**: Available on Work objects via `fleetv1beta1.PlacementTrackingLabel` label

## Implementation Plan

### Phase 1: Define Annotation Constant

- [x] Add annotation key constant to APIs
- [ ] Decide on annotation format and value

### Phase 2: Extract Placement Information
- [x] Modify applyManifests to extract placement from Work labels
- [x] Handle cases where placement label might be missing

### Phase 3: Add Annotation to Manifests
- [x] Modify manifest objects before applying them
- [x] Ensure annotation is added consistently across both applier types

### Phase 4: Testing
- [x] Write unit tests for annotation addition
- [ ] Test with both client-side and server-side apply strategies
- [ ] Verify annotation appears on deployed resources

### Phase 5: Integration Testing
- [ ] Test end-to-end with actual placements
- [ ] Verify backwards compatibility

## Success Criteria
- All applied resources have the source placement annotation
- Annotation is correctly set for both apply strategies
- No breaking changes to existing functionality
- Tests pass and cover the new functionality
