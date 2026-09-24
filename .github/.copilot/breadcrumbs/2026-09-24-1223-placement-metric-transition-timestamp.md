# Placement Status Metric Transition Timestamp

## Requirements

Fix placement status metric emission so hub-agent reconciles and restarts do not replace persisted RP/CRP status transition times with reconcile time.

## Plan

- [x] Confirm the metric selection order is shared by `ClusterResourcePlacement` and `ResourcePlacement`.
- [x] Emit the selected current-generation condition's `LastTransitionTime`.
- [x] Emit the final expected true condition's transition time for synthetic `Completed`.
- [x] Use the placement creation timestamp when the selected condition is missing.
- [x] Add focused table-driven RP/CRP unit tests for stable repeated emission and condition priority.
- [x] Run focused placement controller unit tests.

## Decisions

- Missing conditions have no transition timestamp, so their stable fallback is the placement object's `CreationTimestamp`.
- Synthetic `Completed` is established when the final expected condition becomes true, so that condition's `LastTransitionTime` is the completion timestamp.
- Metric label selection and expected-condition priority remain unchanged.

## Validation

- `go test -run '^TestEmitPlacementStatusMetric$' ./pkg/controllers/placement`
- The full package test command cannot start the integration suite in this Windows worktree because `/usr/local/kubebuilder/bin/etcd` is not installed.
