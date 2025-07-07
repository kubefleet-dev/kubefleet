# Breadcrumb for CEL Conversion: ClusterResourceOverride

## Requirements
- Convert webhook validation logic for ClusterResourceOverride to CEL XValidation rules in the CRD.
- Remove/replace webhook logic that is now enforced by CEL.
- Ensure all constraints that can be enforced by CEL are migrated.
- Document what remains in webhook (cross-object checks).

## Additional comments from user
- User approved the plan to proceed with CEL conversion for ClusterResourceOverride.

## Plan
### Phase 1: Analyze and Write CEL Expressions
- [ ] Task 1.1: Identify all webhook validation logic that can be enforced by CEL.
- [ ] Task 1.2: Write CEL XValidation rules for:
  - No labelSelector in clusterResourceSelectors
  - Name is required in clusterResourceSelectors
  - No duplicate clusterResourceSelectors
- [ ] Task 1.3: Add CEL rules to the CRD YAML.

### Phase 2: Remove/Refactor Webhook Logic
- [ ] Task 2.1: Remove/replace Go validation logic now enforced by CEL.
- [ ] Task 2.2: Update webhook to only perform cross-object checks (e.g., uniqueness across objects).

### Phase 3: Test and Document
- [ ] Task 3.1: Test CRD validation with CEL rules.
- [ ] Task 3.2: Update documentation and this breadcrumb with before/after comparison.

## Decisions
- CEL will be used for all per-object validation that does not require access to other objects.
- Webhook will remain for cross-object uniqueness checks.

## Implementation Details
- CEL rules will be added under `x-kubernetes-validations` in the CRD.
- Go code will be refactored to avoid duplicate validation.

## Changes Made
- (To be updated after implementation)

## Before/After Comparison
- (To be updated after implementation)

## References
- `/pkg/utils/validator/clusterresourceoverride.go` (Go validation logic)
- `/config/crd/bases/placement.kubernetes-fleet.io_clusterresourceoverrides.yaml` (CRD definition)
- [Kubernetes CEL docs](https://kubernetes.io/docs/reference/using-api/cel/)
