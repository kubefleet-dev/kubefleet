# Prevent namespace-accessible placement status disclosure

## Requirements

- Build the fix from `cncf/main` on an isolated branch.
- Restrict the namespaced `ClusterResourcePlacementStatus` projection to the selected Namespace and resources contained in that namespace.
- Exclude unrelated cluster-scoped resources, such as `ClusterRole` objects.
- Preserve diff and drift details, including `ValueInMember` and `ValueInHub`, for retained in-namespace resources.
- Preserve the full cluster-scoped `ClusterResourcePlacement` status for authorized cluster-scoped readers.

## Additional comments from user

- The requested mitigation targets the namespace authorization boundary rather than changing member-side diff calculation globally.
- Redacting `ValueInMember` and `ValueInHub` is deferred to a separate PR stacked on this namespace-boundary change.

## Plan

### Phase 1: Tests first

- [x] **Task 1.1:** Add unit tests for the namespace-safe status projection.
  - Verify in-namespace resources remain visible.
  - Verify the selected Namespace resource remains visible.
  - Verify unrelated cluster-scoped and cross-namespace resources are removed.
  - Verify diff and drift details remain unchanged for retained resources.
  - **Success criterion:** Tests fail against the current verbatim-copy behavior.
- [x] **Task 1.2:** Add synchronization coverage proving the namespaced object is sanitized without mutating the source CRP status.
  - **Success criterion:** The test demonstrates separate privileged and namespace-safe views.

### Phase 2: Implement the safe projection

- [x] **Task 2.1:** Replace the current condition-only filter with a namespace-aware projection helper.
  - Deep-copy status before modification.
  - Filter resource-bearing status fields to the selected namespace boundary.
  - **Success criterion:** No out-of-scope resource or reference is copied into CRPS.
- [x] **Task 2.2:** Preserve non-resource status data for retained namespace-scoped entries.
  - Keep condition messages and drift/diff values unchanged in this PR.
  - **Success criterion:** This PR changes only the namespace-to-cluster authorization boundary.

### Phase 3: Verification

- [x] **Task 3.1:** Run focused placement controller unit tests.
  - **Success criterion:** Namespace status synchronization tests pass.
- [x] **Task 3.2:** Run broader relevant controller tests and formatting/lint checks where practical.
  - **Success criterion:** No regressions or new diagnostics are introduced.
- [x] **Task 3.3:** Review the final diff for minimal scope and document results.
  - **Success criterion:** The change affects only namespace status projection, its tests, and this breadcrumb.

## Decisions

- Sanitize at the CRP-to-CRPS trust boundary so privileged cluster-scoped status remains unchanged.
- Use an allowlist-style namespace projection for resource-bearing fields.
- Handle patch-value redaction separately so its API semantics and placeholder are reviewed independently.

## Implementation Details

- Replaced the verbatim CRP status copy with `buildNamespaceAccessiblePlacementStatus`, which deep-copies before sanitizing.
- Retained only the selected Namespace and resources whose `namespace` equals the CRPS namespace.
- Retained only same-namespace `ResourceOverride` references and omitted all cluster-scoped override references.
- Removed cluster-scoped envelope references while preserving same-namespace envelope metadata.
- Preserved drift/diff details and free-form condition messages for retained namespace-scoped entries.
- Updated the shared integration assertion to compare an independently constructed namespace projection, including all retained data.
- Kept resource indices and cluster names because they are status coordination metadata rather than Kubernetes object contents or out-of-scope object references.

## Changes Made

- Created branch `fix/msrc-crps-status-disclosure` from refreshed `cncf/main` in the isolated worktree `/home/weiweng/kubefleet-cncf`.
- Added this implementation breadcrumb.
- Updated `pkg/controllers/placement/namespace_status_sync.go` with the safe projection.
- Added projection and sync-boundary unit coverage in `pkg/controllers/placement/namespace_status_sync_test.go`.
- Updated `test/utils/crpstatussync/crp_status_sync.go` for the new CRPS contract.
- Installed the repository-pinned Kubernetes 1.33.0 envtest assets after the first full package run found no local `etcd` binary.

## Before/After Comparison

- **Before:** Namespaced CRPS receives nearly the complete CRP status, including selected cluster-scoped and cross-namespace resources.
- **After:** Namespaced CRPS contains only namespace-contained resource status and same-namespace override/envelope references. Retained status details remain unchanged, and cluster-scoped CRP status remains complete.

## References

- `pkg/controllers/placement/namespace_status_sync.go` — current CRP-to-CRPS copy boundary.
- `pkg/controllers/workapplier/drift_detection_takeover.go` — source of member/hub patch values and Secret-only redaction.
- `apis/placement/v1beta1/clusterresourceplacement_types.go` — placement status and patch detail API contracts.
- No applicable placement-specific domain knowledge or specification file exists under `.github/.copilot/domain_knowledge/` or `.github/.copilot/specifications/`.
- Verification: focused namespace projection tests passed with Go 1.26.6.
- Verification: the broader package compiled, but its envtest suite requires an `etcd` binary that is not installed at `/usr/local/kubebuilder/bin/etcd` in the current environment.

## Completion criteria

- [x] Namespaced CRPS cannot report unrelated cluster-scoped or cross-namespace resources.
- [x] Retained namespace-scoped values and condition messages remain unchanged.
- [x] Cluster-scoped CRP status behavior remains unchanged.
- [x] Focused tests pass and the final diff is minimal.
