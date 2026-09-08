# Redact values in namespace-accessible placement status

## Requirements

- Keep this PR independent from the namespace-to-cluster boundary fix in PR #867.
- Redact non-empty `ValueInMember` and `ValueInHub` fields in namespaced `ClusterResourcePlacementStatus` objects.
- Preserve drift and diff paths and empty-side semantics.
- Preserve the full cluster-scoped `ClusterResourcePlacement` status.
- Address Michael A. Yu's review comment by using an explanatory redaction marker instead of an empty string.

## Plan

- [x] Add focused tests for redacted member/hub values and empty-side preservation.
- [x] Redact values in the existing CRP-to-CRPS copy path without relying on PR #867.
- [x] Update shared integration assertions and served API documentation.
- [x] Regenerate CRDs and run focused validation.
- [x] Rewrite and push PR #886 as a standalone change based on `cncf/main`.

## Decisions

- Use the repository's established marker, `(redacted for security reasons)`, for consistency with Secret drift redaction.
- Replace only non-empty values because an empty string indicates that the JSON path does not exist on that side.
- Do not redact condition messages in this PR; its scope is limited to `ValueInMember` and `ValueInHub`.
- Keep namespace-boundary filtering in PR #867 so either security change can be reviewed and merged independently.

## Verification

- The direct redaction unit test passes with Go 1.26.6.
- Focused namespace status tests pass, the shared CRPS helper compiles, and `git diff --check` passes.

