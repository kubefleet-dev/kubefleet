# Namespaced StagedUpdateRun API Implementation

## Requirements

Create namespaced API variants for the staged update functionality, similar to how `ClusterResourcePlacement` and `ResourcePlacement` are structured. This includes adding namespace-scoped versions of `ClusterStagedUpdateRun`, `ClusterStagedUpdateStrategy`, and `ClusterApprovalRequest` with proper interface abstractions.

### Current State Analysis
- ✅ `ClusterStagedUpdateRun` exists in `apis/placement/v1beta1/stageupdate_types.go` (cluster-scoped)
- ✅ `ClusterStagedUpdateStrategy` exists in `apis/placement/v1beta1/stageupdate_types.go` (cluster-scoped)  
- ✅ `ClusterApprovalRequest` exists in `apis/placement/v1beta1/stageupdate_types.go` (cluster-scoped)
- ✅ Namespace-scoped `StagedUpdateRun` implemented
- ✅ Namespace-scoped `StagedUpdateStrategy` implemented
- ✅ Namespace-scoped `ApprovalRequest` implemented
- ✅ Interface abstractions (`UpdateRunObj`, `UpdateStrategyObj`, `ApprovalRequestObj`) implemented

### Required Implementation
1. ✅ Add namespace-scoped `StagedUpdateRun` type following the same pattern as `ResourcePlacement`
2. ✅ Add namespace-scoped `StagedUpdateStrategy` type following the same pattern
3. ✅ Add namespace-scoped `ApprovalRequest` type following the same pattern
4. ✅ Create interface abstractions for all three types using the naming convention: `UpdateRunObj`, `UpdateStrategyObj`, `ApprovalRequestObj`
5. ✅ Ensure proper kubebuilder annotations for CRD generation
6. ✅ Follow existing v1beta1 API patterns and conventions established by `PlacementObj` interface

## Additional comments from user

User requested to use `UpdateRunObj` interface naming instead of `StagedUpdateRunObj` to avoid confusion with the concrete `StagedUpdateRun` type. This follows a cleaner naming pattern similar to how `PlacementObj` works with both `ClusterResourcePlacement` and `ResourcePlacement`.

## Implementation Summary

### Phase 1: Interface Design ✅
- ✅ Studied the `PlacementObj` interface pattern in `clusterresourceplacement_types.go`
- ✅ Analyzed shared spec/status pattern between `ClusterResourcePlacement` and `ResourcePlacement`
- ✅ Designed `UpdateRunObj`, `UpdateStrategyObj`, and `ApprovalRequestObj` interfaces
- ✅ Designed corresponding list interfaces: `UpdateRunObjList`, `UpdateStrategyObjList`, `ApprovalRequestObjList`

### Phase 2: Type Implementation ✅
- ✅ Added `StagedUpdateRun` type with proper kubebuilder annotations (namespace-scoped)
- ✅ Added `StagedUpdateRunList` type
- ✅ Added `StagedUpdateStrategy` type with proper kubebuilder annotations (namespace-scoped)
- ✅ Added `StagedUpdateStrategyList` type  
- ✅ Added `ApprovalRequest` type with proper kubebuilder annotations (namespace-scoped)
- ✅ Added `ApprovalRequestList` type

### Phase 3: Interface Methods ✅
- ✅ Implemented getter/setter methods for `StagedUpdateRun` (spec and status)
- ✅ Implemented getter/setter methods for `StagedUpdateStrategy` (spec)
- ✅ Implemented getter/setter methods for `ApprovalRequest` (spec and status)
- ✅ Implemented `GetUpdateRunObjs()` method for list types
- ✅ Added type assertions to ensure interface compliance
- ✅ Updated `init()` function to register new types

### Phase 4: CRD Generation ✅
- ✅ Ran `make generate` to update generated code
- ✅ Ran `make manifests` to generate CRDs
- ✅ Validated CRD generation and kubebuilder annotations
- ✅ Confirmed proper namespace-scoped CRDs were created:
  - `placement.kubernetes-fleet.io_stagedupdateruns.yaml`
  - `placement.kubernetes-fleet.io_stagedupdatestrategies.yaml`
  - `placement.kubernetes-fleet.io_approvalrequests.yaml`

### Phase 5: Documentation and Examples ✅
- ✅ Created example YAML files for the new namespace-scoped types:
  - `stagedUpdateRun.yaml` - namespace-scoped StagedUpdateRun example
  - `stagedUpdateStrategy.yaml` - namespace-scoped StagedUpdateStrategy example
  - `namespaced-approvalRequest.yaml` - namespace-scoped ApprovalRequest example
- ✅ Created comprehensive README.md explaining both cluster and namespace-scoped variants
- ✅ Verified consistency with domain knowledge about cluster vs namespace scoped resources

## Interface Design Details

### UpdateRunObj Interface
```go
// UpdateRunObj offers the functionality to work with staged update run objects
type UpdateRunObj interface {
    apis.ConditionedObj
    UpdateRunSpecGetterSetter
    UpdateRunStatusGetterSetter
}
```

### UpdateStrategyObj Interface  
```go
// UpdateStrategyObj offers the functionality to work with staged update strategy objects
type UpdateStrategyObj interface {
    client.Object  // Note: NOT apis.ConditionedObj (strategies don't have status/conditions)
    UpdateStrategySpecGetterSetter
}
```

### ApprovalRequestObj Interface
```go
// ApprovalRequestObj offers the functionality to work with approval request objects
type ApprovalRequestObj interface {
    apis.ConditionedObj
    ApprovalRequestSpecGetterSetter
    ApprovalRequestStatusGetterSetter
}
```

## Key Design Decisions

1. **Interface Naming**: Used `UpdateRunObj` instead of `StagedUpdateRunObj` to avoid confusion with concrete types
2. **Spec/Status Sharing**: All namespace and cluster-scoped variants share the same spec/status types (`StagedUpdateRunSpec`, `StagedUpdateRunStatus`, etc.)
3. **Strategy Design**: `UpdateStrategyObj` does NOT extend `apis.ConditionedObj` because strategy types are configuration-only and don't have status/conditions
4. **CRD Annotations**: Proper kubebuilder annotations for namespace-scoped resources using `scope=Namespaced`
5. **Short Names**: Used meaningful short names (`sur`, `sus`, `areq`) for namespace-scoped types
6. **Pattern Consistency**: Followed the exact same pattern as `ClusterResourcePlacement`/`ResourcePlacement`

## Validation Results

- ✅ All new types compile without errors
- ✅ Interface type assertions pass
- ✅ CRDs generate successfully with proper scope
- ✅ Code generation (`make generate`) creates DeepCopy methods
- ✅ Manifest generation (`make manifests`) creates namespace-scoped CRDs
- ✅ Example YAML files demonstrate proper usage

## Success Criteria - ALL MET ✅

- ✅ All namespace-scoped types (`StagedUpdateRun`, `StagedUpdateStrategy`, `ApprovalRequest`) are properly defined
- ✅ All interface abstractions are implemented and type assertions pass
- ✅ CRDs are generated successfully for all new types
- ✅ Example YAML files demonstrate namespace-scoped usage
- ✅ Pattern consistency with existing `ResourcePlacement` vs `ClusterResourcePlacement` implementation
- ✅ All new types are registered in the scheme
- ✅ Generated code compiles without errors
- ✅ Interface naming follows the `UpdateRunObj` convention to avoid confusion

## Next Steps

The implementation is complete and ready for use. Controllers can now be updated to work with both cluster-scoped and namespace-scoped staged update types using the interface abstractions, similar to how the placement scheduler works with both `ClusterResourcePlacement` and `ResourcePlacement`.