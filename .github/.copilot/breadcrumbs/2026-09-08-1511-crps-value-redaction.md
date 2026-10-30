# Redact values in namespace-accessible placement status

## Requirements

- Stack this change on the namespace-to-cluster boundary fix in `fix/msrc-crps-status-disclosure`.
- Redact non-empty `ValueInMember` and `ValueInHub` fields in namespaced `ClusterResourcePlacementStatus` objects.
- Preserve drift and diff paths and empty-side semantics.
- Preserve the full cluster-scoped `ClusterResourcePlacement` status.
- Address Michael A. Yu's review comment by using an explanatory redaction marker instead of an empty string.

## Plan

- [x] Add focused tests for redacted member/hub values and empty-side preservation.
- [x] Redact values only at the CRP-to-CRPS projection boundary.
- [x] Update shared integration assertions and served API documentation.
- [x] Regenerate CRDs and run focused validation.
- [x] Commit, push, and open PR #886 stacked on `fix/msrc-crps-status-disclosure`.

## Decisions

- Use the marker `(redacted)` for projected drift and diff values.
- Replace only non-empty values because an empty string indicates that the JSON path does not exist on that side.
- Do not redact condition messages in this PR; its scope is limited to `ValueInMember` and `ValueInHub`.

## Verification

- Focused namespace projection tests pass with Go 1.26.6.
- The shared CRPS integration helper compiles and `git diff --check` passes.
