# Prevent namespace-accessible placement status disclosure

## Requirements

- Build the fix from `cncf/main` on an isolated branch.
- Restrict the namespaced `ClusterResourcePlacementStatus` projection to the selected Namespace and resources contained in that namespace.
- Exclude unrelated cluster-scoped resources, such as `ClusterRole` objects.
- Preserve diff and drift paths while omitting both `ValueInMember` and `ValueInHub` from the namespaced projection.
- Check condition and failure messages for copied live object values.
- Preserve the full cluster-scoped `ClusterResourcePlacement` status for authorized cluster-scoped readers.

## Additional comments from user

- The requested mitigation targets the namespace authorization boundary rather than changing member-side diff calculation globally.

## Plan

### Phase 1: Tests first

- [x] **Task 1.1:** Add unit tests for the namespace-safe status projection.
  - Verify in-namespace resources remain visible.
  - Verify the selected Namespace resource remains visible.
  - Verify unrelated cluster-scoped and cross-namespace resources are removed.
  - Verify diff and drift paths remain while both values are empty.
  - **Success criterion:** Tests fail against the current verbatim-copy behavior.
- [x] **Task 1.2:** Add synchronization coverage proving the namespaced object is sanitized without mutating the source CRP status.
  - **Success criterion:** The test demonstrates separate privileged and namespace-safe views.

### Phase 2: Implement the safe projection

- [x] **Task 2.1:** Replace the current condition-only filter with a namespace-aware projection helper.
  - Deep-copy status before modification.
  - Filter resource-bearing status fields to the selected namespace boundary.
  - Clear `ValueInMember` and `ValueInHub` from retained drift and diff details.
  - **Success criterion:** No live field value or out-of-scope resource is copied into CRPS.
- [x] **Task 2.2:** Audit copied conditions and failed placement details for value-bearing messages.
  - Apply the smallest additional sanitization required by observed data contracts.
  - **Success criterion:** No known free-form status field exposes member object contents.

### Phase 3: Verification

- [x] **Task 3.1:** Run focused placement controller unit tests.
  - **Success criterion:** Namespace status synchronization tests pass.
- [x] **Task 3.2:** Run broader relevant controller tests and formatting/lint checks where practical.
  - **Success criterion:** No regressions or new diagnostics are introduced.
- [x] **Task 3.3:** Review the final diff for minimal scope and document results.
  - **Success criterion:** The change affects only namespace status projection, its tests, and this breadcrumb.

## Decisions

- Sanitize at the CRP-to-CRPS trust boundary so privileged cluster-scoped status remains unchanged.
- Use an allowlist-style namespace projection plus complete value omission; object-kind-specific redaction is insufficient for arbitrary Kubernetes resources and CRDs.
- Keep patch paths because they communicate drift without disclosing live values.

## Implementation Details

- Replaced the verbatim CRP status copy with `buildNamespaceAccessiblePlacementStatus`, which deep-copies before sanitizing.
- Retained only the selected Namespace and resources whose `namespace` equals the CRPS namespace.
- Retained only same-namespace `ResourceOverride` references and omitted all cluster-scoped override references.
- Removed cluster-scoped envelope references while preserving same-namespace envelope metadata.
- Preserved drift/diff JSON paths but cleared both member and hub values.
- Cleared all free-form condition messages in the namespace projection, including failed placement, aggregate, and per-cluster conditions.
- Updated the shared integration assertion to validate namespace-safety invariants while continuing to compare unaffected CRP/CRPS fields.
- Kept resource indices and cluster names because they are status coordination metadata rather than Kubernetes object contents or out-of-scope object references.

## Changes Made

- Created branch `fix/msrc-crps-status-disclosure` from refreshed `cncf/main` in the isolated worktree `/home/weiweng/kubefleet-cncf`.
- Added this implementation breadcrumb.
- Updated `pkg/controllers/placement/namespace_status_sync.go` with the safe projection.
- Added projection and sync-boundary unit coverage in `pkg/controllers/placement/namespace_status_sync_test.go`.
- Updated `test/utils/crpstatussync/crp_status_sync.go` for the new CRPS contract.
- Installed the repository-pinned Kubernetes 1.33.0 envtest assets after the first full package run found no local `etcd` binary.

## Before/After Comparison

- **Before:** Namespaced CRPS receives nearly the complete CRP status, including raw member/hub diff values and selected cluster-scoped resources.
- **After:** Namespaced CRPS contains only namespace-contained resource status, same-namespace override/envelope references, and path-only drift/diff details. Free-form messages and cluster-scoped references are omitted. Cluster-scoped CRP status remains complete.

## References

- `pkg/controllers/placement/namespace_status_sync.go` — current CRP-to-CRPS copy boundary.
- `pkg/controllers/workapplier/drift_detection_takeover.go` — source of member/hub patch values and Secret-only redaction.
- `apis/placement/v1beta1/clusterresourceplacement_types.go` — placement status and patch detail API contracts.
- No applicable placement-specific domain knowledge or specification file exists under `.github/.copilot/domain_knowledge/` or `.github/.copilot/specifications/`.
- Verification: `go test ./pkg/controllers/placement ./test/utils/crpstatussync -count=1` passed with repository-pinned envtest assets and Go 1.26.6.
- Verification: focused `go vet` and `git diff --check` passed; modified files have no editor diagnostics.

## Completion criteria

- [x] Namespaced CRPS cannot reveal `ValueInMember` or `ValueInHub`.
- [x] Namespaced CRPS cannot report unrelated cluster-scoped or cross-namespace resources.
- [x] Cluster-scoped CRP status behavior remains unchanged.
- [x] Focused tests pass and the final diff is minimal.
