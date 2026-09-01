# MSRC RBAC override containment

## Requirements

- Reject CRO JSON patches targeting `/subjects`, `/roleRef`, `/rules`, or `/aggregationRule` on `Role`, `RoleBinding`, `ClusterRole`, or `ClusterRoleBinding` resources.
- Add validation regression coverage for the reported Cluster Writer to member `cluster-admin` escalation path.

## Additional comments from user

- This is immediate containment for Incident 31000000704764.
- The requested scope is containment only; delegated source/member authorization is a later permanent fix.
- Scope was narrowed after plan approval: only CRO JSON-patch modification of RBAC binding privilege fields on member clusters is in scope. CRP/RP propagation, RO behavior, delete overrides, and other CRO patches must remain unchanged.

## Plan

### Phase 1: Tests first

- [x] **Task 1.1:** Add CRO validator cases reproducing `/subjects`, `/roleRef`, `/rules`, and `/aggregationRule` patches on Kubernetes RBAC kinds.
  - Success: both whole-field and descendant JSON-pointer paths are denied with an actionable member-cluster privilege-escalation message.
- [x] **Task 1.2:** Preserve positive cases.
  - Success: non-RBAC resources may use the same field names, and RBAC resources may still receive unrelated patches.

### Phase 2: CRO containment

- [x] **Task 2.1:** Add a focused helper identifying Kubernetes RBAC kinds and protected JSON-pointer path prefixes.
  - Success: the check requires the canonical `rbac.authorization.k8s.io` group and one of the four RBAC kinds.
- [x] **Task 2.2:** Make CRO policy validation target-kind aware and reject protected paths.
  - Success: each protected root and its descendants cannot be patched on selected RBAC resources.

### Phase 3: Verification

- [x] **Task 3.1:** Run formatting and required generation only if source/API changes require it.
  - Success: generated artifacts are synchronized and unrelated generated changes are absent.
- [x] **Task 3.2:** Run focused validator tests.
  - Success: all focused tests pass.
- [x] **Task 3.3:** Run the repository-required review checks practical for this change.
  - Success: failures are fixed or clearly documented with their cause.

## Decisions

- Apply the tactical path denylist requested by the user instead of rejecting RBAC CRO targets wholesale.
- Recognize all four Kubernetes RBAC kinds for completeness, although CRO normally selects cluster-scoped `ClusterRole` and `ClusterRoleBinding` objects.
- Match both the whole field and descendants so array-element or nested-field patches cannot bypass the mitigation.
- Leave CRP/RP propagation and namespaced RO behavior unchanged per the narrowed request.
- Do not add delegated SAR or Guard behavior in this containment branch.

## Implementation Details

- CRO policy validation will combine the selected GroupKinds with each JSON patch path and deny protected RBAC binding fields.
- Existing public API types remain unchanged; only admission behavior is tightened.

## Changes Made

- Branch renamed to `fix/msrc-rbac-cro-path-containment` to reflect the final scope.
- Plan approved, then narrowed to CRO-only RBAC override containment.
- Added target-aware CRO validation for all four escalation path roots and descendants.
- Added table-driven regression tests covering the exploit paths and allowed non-RBAC/unrelated patches.
- Validation passed: full `pkg/utils/validator` tests, focused `go vet`, fast repository lint, and `git diff --check`.
- No API or generated-file changes were required.

## Before/After Comparison

- **Before:** A caller able to write CRO objects can cause privileged Fleet controllers to rewrite RBAC grants on selected member clusters.
- **After:** CROs cannot rewrite RBAC subjects, role references, rules, or aggregation rules; propagation and unrelated override behavior remain unchanged.

## References

- Incident 31000000704764, supplied by the user.
- `.github/.copilot/domain_knowledge/cluster vs namespace scoped resources`: scope semantics for CRP/RP handling.
- `pkg/utils/validator/clusterresourceoverride.go`: CRO selector and policy validation used by the validating webhook.
- No applicable feature specification exists under `.github/.copilot/specifications/`.

## Completion criteria

- [x] CRO JSON patches to all four escalation paths on Kubernetes RBAC kinds are denied.
- [x] CRP/RP propagation, RO behavior, delete overrides, and unrelated CRO patches are unchanged.
- [x] The reported subject-rewrite exploit has validation regression coverage.
- [x] Focused tests and applicable repository checks pass.
- [x] This breadcrumb records completed tasks, test results, and any course corrections.
