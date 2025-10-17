# Breadcrumb: ResourceOverride Namespace Isolation E2E Test

**Date**: 2025-10-17
**Task**: Add e2e test to validate resourceoverride namespace isolation
**Issue**: test: add an e2e test to validate resourceoverride

## Context

We need to verify that a resourceoverride in one namespace won't override resources in another namespace. This is an important test to ensure namespace isolation is properly maintained.

The user requested:
- Add test case with CPR (ClusterResourcePlacement)
- Add test case with RP (ResourcePlacement)
- Follow existing test styles

## Analysis

After reviewing the existing test files:
- `test/e2e/placement_ro_test.go` - Contains RO tests with CRP (ClusterResourcePlacement)
- `test/e2e/resource_placement_ro_test.go` - Contains RO tests with RP (ResourcePlacement)
- Test naming pattern: `workNamespaceNameTemplate = "application-%d"` where %d is GinkgoParallelProcess()
- ResourceOverride resources are namespaced and use PlacementRef to reference placements
- CRP tests use `ClusterScoped` placement references
- RP tests use `NamespaceScoped` placement references

## Key Understanding

ResourceOverride has:
1. A namespace (where the RO lives)
2. A PlacementRef that can reference either CRP (cluster-scoped) or RP (namespace-scoped)
3. ResourceSelectors that target resources to override

The test needs to verify:
- RO in namespace-A with reference to CRP/RP should NOT override resources placed by a different CRP/RP
- Resources in different namespaces should not be affected by RO in another namespace

## Implementation Plan

### Phase 1: Add CPR test case for namespace isolation
- **Task 1.1**: Add new test context in `placement_ro_test.go`
  - Create two separate namespaces (namespace-A and namespace-B)
  - Create two CRPs (crp-A targets namespace-A, crp-B targets namespace-B)
  - Create RO in namespace-A that references crp-A
  - Verify RO only affects resources in namespace-A, not namespace-B
  
### Phase 2: Add RP test case for namespace isolation
- **Task 2.1**: Add new test context in `resource_placement_ro_test.go`
  - Create two separate namespaces (namespace-A and namespace-B)
  - Create RP in each namespace (rp-A in namespace-A, rp-B in namespace-B)
  - Create RO in namespace-A that references rp-A
  - Verify RO only affects resources in namespace-A, not namespace-B

### Phase 3: Run tests and verify
- **Task 3.1**: Run the e2e tests
- **Task 3.2**: Ensure tests pass and validate behavior

## Detailed Test Design

### Test Case 1: CRP with ResourceOverride Namespace Isolation
```
Context: "resourceOverride in one namespace should not affect resources in another namespace for CRP"
- Setup:
  - Create namespace-A with configmap-A
  - Create namespace-B with configmap-B
  - Create CRP-A selecting namespace-A resources
  - Create CRP-B selecting namespace-B resources
  - Create RO in namespace-A referencing CRP-A
- Validation:
  - ConfigMap in namespace-A should have annotations from RO
  - ConfigMap in namespace-B should NOT have annotations from RO
- Cleanup:
  - Delete both ROs, CRPs, and namespaces
```

### Test Case 2: RP with ResourceOverride Namespace Isolation
```
Context: "resourceOverride in one namespace should not affect resources in another namespace for RP"
- Setup:
  - Create namespace-A with configmap-A  
  - Create namespace-B with configmap-B
  - Create CRP for namespace placement
  - Create RP-A in namespace-A selecting configmap-A
  - Create RP-B in namespace-B selecting configmap-B
  - Create RO in namespace-A referencing RP-A
- Validation:
  - ConfigMap in namespace-A should have annotations from RO
  - ConfigMap in namespace-B should NOT have annotations from RO
- Cleanup:
  - Delete ROs, RPs, CRP, and namespaces
```

## Implementation Checklist

- [x] Task 1.1: Add CRP namespace isolation test in placement_ro_test.go
- [x] Task 2.1: Add RP namespace isolation test in resource_placement_ro_test.go
- [ ] Task 3.1: Run e2e tests to validate
- [ ] Task 3.2: Verify all tests pass

## Success Criteria

1. New test case added for CRP with namespace isolation
2. New test case added for RP with namespace isolation  
3. Tests follow existing code style and patterns
4. Tests pass and validate that RO in one namespace doesn't affect resources in another namespace
5. Code is properly formatted with `make reviewable`
