# ReportDiff values after takeover

## Requirements

- Report raw member and hub values only when the live resource is owned by the expected AppliedWork.
- Report only changed paths before takeover, including when takeover is disabled or blocked by differences.
- Keep this change independent of the namespace status projection fix in PR #867.

## Additional comments from user

- Implement this policy in a separate pull request.

## Plan

### Phase 1: Specify the disclosure policy

- [x] Task 1.1: Add focused unit tests for owned, unowned, differently owned, and missing resources.
  Success: The tests distinguish exact AppliedWork ownership and fail before the implementation.

### Phase 2: Implement the policy

- [x] Task 2.1: Apply one ReportDiff value-redaction policy after manifest processing.
  Success: Every ReportDiff result contains values only when exact ownership is established.

### Phase 3: Validate and publish

- [x] Task 3.1: Run focused and package-level tests, formatting, vet, and diff checks.
  Success: All applicable checks pass.
- [x] Task 3.2: Commit with DCO sign-off, push the branch, and open an upstream PR.
  Success: A separate PR against `kubefleet-dev/kubefleet:main` is available for review.

## Decisions

- Evaluate ownership after takeover so a successful takeover may expose values while a refused takeover cannot.
- Require an owner reference equal to the expected AppliedWork; another Fleet owner is not sufficient.
- Treat a missing live resource as unowned, so its root diff contains only `/` and no whole-object placeholder.
- Keep the namespace projection sanitization from PR #867 as a separate authorization-boundary defense.

## Implementation Details

- `processOneManifest` defers a ReportDiff-specific disclosure check until lookup, takeover, and diff calculation finish.
- `hideDiffValuesUnlessOwned` preserves values only when the live object has the exact expected AppliedWork owner reference.
- Focused tests cover the ownership matrix and exercise unmanaged ReportDiff and takeover-refusal paths through complete manifest processing.
- Integration expectations retain all changed paths while omitting values for missing, takeover-disabled, and takeover-refused resources.
- E2E expectations apply the same ownership rule without weakening positive coverage: owned-resource diffs and all drift details still retain values.

## Changes Made

- Added the ReportDiff post-processing policy in `pkg/controllers/workapplier/process.go`.
- Added focused policy and behavioral tests in `pkg/controllers/workapplier/process_test.go`.
- Updated work-applier integration expectations for resources Fleet does not own.
- Updated CRP, ResourcePlacement, API progression, apply-strategy transition, staged rollout, and backoff expectations for unowned resources.

## Validation

- Focused ownership-policy tests pass.
- All ReportDiff integration contexts pass.
- Apply-mode partial/full comparison takeover-refusal and strategy-switch integration contexts pass.
- `go vet ./pkg/controllers/workapplier`, editor diagnostics, and `git diff --check` pass.
- The unfiltered package run reached the existing 10-minute Go test timeout while starting a later integration case; no assertion failure was reported. The changed contexts were rerun directly and passed.
- The E2E package compiles after updating all 14 ownership-related CI failures.
- The focused ownership-policy unit tests pass.
- The full ordered `slow backoff and fast backoff` envtest context passes after correcting its stale takeover-refusal fixture.
- Published for review as https://github.com/kubefleet-dev/kubefleet/pull/872.

## Before/After Comparison

- Before: ReportDiff can include raw values for resources Fleet has not taken over.
- After: Unowned ReportDiff results identify changed paths without disclosing values.

## References

- `pkg/controllers/workapplier/process.go`: manifest processing, takeover, ReportDiff, and ownership checks.
- `pkg/controllers/workapplier/process_test.go`: focused work-applier policy tests.
- Related defense in depth: kubefleet-dev/kubefleet#867.

## Success Criteria

- Exact AppliedWork ownership is the only condition that preserves ReportDiff values.
- All unowned ReportDiff paths omit both member and hub values.
- Tests and repository checks pass, and the change is published as an independent PR.