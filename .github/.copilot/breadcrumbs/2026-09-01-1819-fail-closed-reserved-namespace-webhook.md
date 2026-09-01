# Fail closed reserved namespace webhook

## Requirements

- Change only `fleet.namespace.guardrail.validating` from `failurePolicy: Ignore` to `failurePolicy: Fail`.
- Preserve the failure policy and behavior of every other Fleet guardrail webhook.
- Add focused regression coverage for the namespace webhook policy.

## Additional comments from user

- Apply the smallest immediate production mitigation for the reported reserved-namespace fail-open issue.
- A reserved namespace operation failing with 500/503 while the webhook is unavailable is preferable to admitting the operation.

## Plan

### Phase 1: Regression test

- [x] **Task 1.1:** Strengthen the guardrail webhook builder test to identify the namespace webhook and require `Fail`.
  - Success: the test fails while the namespace webhook still uses `Ignore`.
- [x] **Task 1.2:** Assert the other five guardrail webhooks remain `Ignore`.
  - Success: the change cannot accidentally broaden fail-closed behavior.

### Phase 2: Minimal mitigation

- [x] **Task 2.1:** Set only `fleet.namespace.guardrail.validating` to `failFailurePolicy`.
  - Success: reserved Namespace CREATE, UPDATE, and DELETE fail closed when the webhook backend is unavailable.

### Phase 3: Verification

- [x] **Task 3.1:** Format changed Go files.
  - Success: formatting produces no remaining diff.
- [x] **Task 3.2:** Run focused webhook tests.
  - Success: the focused package test passes.
- [x] **Task 3.3:** Run `git diff --check` and inspect the final diff.
  - Success: only the intended mitigation, regression test, and breadcrumb are changed.

## Decisions

- The user's selected fix option is treated as approval of this narrowly scoped implementation plan.
- Do not add Lease coverage, a new ValidatingAdmissionPolicy, identity-aware pod policy, or webhook lifecycle changes in this patch.
- Keep the existing one-second timeout unchanged; `Fail` ensures timeout and connection failures deny the Namespace operation.

## Implementation Details

- `buildFleetGuardRailValidatingWebhooks()` now points only the Namespace webhook at the existing `failFailurePolicy` value.
- `TestBuildFleetGuardRailValidatingWebhooks` compares the complete webhook-name-to-failure-policy map, requiring `Fail` for the Namespace guardrail and `Ignore` for all five remaining guardrails.

## Changes Made

- Changed `fleet.namespace.guardrail.validating` from `Ignore` to `Fail`.
- Added exact-scope regression coverage for all six guardrail webhook failure policies.
- Ran formatting and `git diff --check` successfully.
- The pre-change red-test attempt was blocked before execution because local Microsoft Go is 1.26.5 while the repository requires 1.26.6. Validation then used the exact Go 1.26.6 toolchain with the required explicit non-local-toolchain opt-in.
- Focused test and the complete `pkg/webhook` package test suite passed.

## Before/After Comparison

- **Before:** Reserved Namespace operations are admitted when the guardrail backend times out or is unavailable.
- **After:** Reserved Namespace operations fail closed when the guardrail backend times out or is unavailable.

## References

- MSRC report supplied by the user.
- `pkg/webhook/webhook.go`: Fleet guardrail webhook construction.
- `pkg/webhook/webhook_test.go`: webhook construction tests.

## Completion criteria

- [x] Only the reserved Namespace guardrail uses `failurePolicy: Fail`.
- [x] Other guardrail webhooks remain fail-open.
- [x] Focused tests pass.
- [x] Final diff is minimal and clean.
