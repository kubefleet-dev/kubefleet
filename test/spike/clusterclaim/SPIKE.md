# Spike: verifying the cluster claim workflow (FEP-0001, issue #791)

This directory is a time-boxed spike for
[#791 — Verify the cluster claim workflow](https://github.com/kubefleet-dev/kubefleet/issues/791).
It is not production code and is not wired into any build target. It runs
against the API types merged in #781 (`ClusterRequest`; renamed `ClusterClaim`
by the open #803 — helper names here say "claim" to survive the rename).

## What the spike does

The FEP-0001 claim workflow is a contract between two independent controllers:
KubeFleet creates a claim when a cluster selector can't be fulfilled, an
external platform provisions a matching cluster and reports back, and
KubeFleet withdraws the claim the moment the selector is satisfied by *any*
cluster. Since neither controller exists yet (#786/#788), the spike prototypes
the minimum of both sides and runs the whole loop against envtest:

- `fakeprovisioner.go` — the platform side (the role a CAPI- or ARM-based
  provisioner plays in production). Policies: `Fulfill` (create a matching
  `MemberCluster`, set `Completed=True` + `provisionedClusterName`), `Fail`
  (`Completed=False`, reason `Failed`), `Ignore`. It only writes the status
  fields that side of the contract owns.
- `withdrawer.go` — a prototype of the claim-management slice of #786:
  re-evaluates every claim on any `MemberCluster` event, withdraws fulfilled
  claims regardless of their status, refreshes
  `lastObservedMostRecentClusterCreationTimestamp` on unfulfilled ones.
- `matcher.go` — selector-term matching (labels only; property expressions
  need the property provider and are out of spike scope).
- `validation_test.go` — pins the CEL/schema contract of the claim API.
- `workflow_test.go` — pins the behavioral contract: happy path,
  withdraw-by-other-cluster, provisioning failure, staleness refresh.

## Run it

```sh
KUBEBUILDER_ASSETS="$(setup-envtest use 1.33.0 -p path)" \
  go test ./test/spike/clusterclaim/... -count=1
```

## Findings

### 1. CEL immutability on `clusterSelectorTerms` can be bypassed (API bug)

`spec.clusterSelectorTerms` carries a field-level
`+kubebuilder:validation:XValidation:rule="self == oldSelf"`. Kubernetes skips
field-scoped transition rules when the field is absent from the new object, so
an update that *removes* the field entirely is accepted — and since "no terms"
means "any cluster satisfies this claim", the mutation silently widens the
claim. Appending, dropping one term, and reordering are all correctly
rejected; full removal is not. Fix: duplicate the guard at the spec level,
e.g. `has(self.clusterSelectorTerms) == has(oldSelf.clusterSelectorTerms)`.
#803 (the rename PR) is a natural place. The
`KNOWN GAP` spec in `validation_test.go` pins the current behavior and should
be flipped when the fix lands. (`placementPolicyRef` is not affected — it is
required, so removal fails on that axis.)

### 2. Contract points the FEP leaves open (verification blockers)

These surfaced while writing the tests; each needs a design answer before
#786's reconciler hard-codes one:

1. **Delete-while-provisioning.** Claims carry no finalizer, and withdrawal is
   a hard delete "regardless of the status of the cluster request." Nothing
   tells a provisioner whether deletion means cancel-and-clean-up or
   finish-and-orphan. Prior art (CAPI `Machine`, Karpenter `NodeClaim`) uses
   finalizers so that "delete means stop and clean up" is explicit.
2. **Status ownership.** The provisioner owns `Completed` +
   `provisionedClusterName`; KubeFleet owns
   `lastObservedMostRecentClusterCreationTimestamp`. Two writers on one status
   subresource works (the spike does it) but the field boundary is convention
   only — it should be written down, and server-side apply field managers
   considered.
3. **`Failed` has no follow-up policy.** A failed claim sits forever,
   consuming the default 1-in-flight budget. Retry via fresh claim? Backoff?
   Give up and surface on the placement? Related: no TTL for claims whose
   provisioner crashed or was never installed.
4. **`Completed=True` with a non-matching cluster.** The status field's doc
   comment implies re-evaluating an already-completed claim; the FEP never
   defines what that means.
5. **Config surfaces don't exist.** The admin "eligible keys" allowlist and
   the per-placement/per-fleet concurrency limits are prose-only — no field or
   flag anywhere yet (the repo's pattern would be
   `cmd/hubagent/options/featureflags.go`).
6. **Timestamp freshness is coarse.** `metav1.Time` is second-resolution, so
   same-second joins tie; the marker is fleet-global, so unrelated joins mark
   claims stale; and the two controllers read it through independent caches.
   It works as an advisory skip-hint (the spike treats it that way) but should
   be documented as advisory-only, or rebased on `resourceVersion`.

### 3. Lessons from a production provisioner-side implementation

An internal controller that provisions managed clusters from a claim-like CRD
(same two-controller shape as this contract) was reviewed alongside the spike.
The patterns most worth carrying into the claim design:

- **Status atomicity via CEL.** Its CRD enforces "terminal state implies its
  evidence" at the API server: `Failed` requires a failure reason and last
  error; `Ready` requires the cluster's identity fields. The claim equivalent
  — `Completed=True` requires `provisionedClusterName` — would turn a
  controller-discipline convention into an apiserver guarantee.
- **Terminal states are CEL-pinned.** Once parked in a permanent failure the
  phase cannot be downgraded, and identity fields (`clusterName`) are
  immutable once set. Remediation is delete-and-recreate — which for a claim
  API means the claimant must be prepared to re-issue.
- **Restart-safe idempotency needs no in-memory state.** The long-running
  provisioning operation's poller is deliberately discarded; the only durable
  state is a deterministic resource name plus the name recorded in status
  (always preferred over re-derivation), and progress is re-derived from the
  provider on every pass. Get-then-create, never blind create.
- **Transient vs. permanent error classification is a first-class table**, and
  "transient" is only safe because every caller is idempotent. A claim
  `Completed`/`Failed` reason set should make the terminal/retryable split
  explicit rather than leaving it to string conventions.
- **The need disappearing does not cancel in-flight provisioning.** That
  controller garbage-collects only after the cluster reaches Ready, with a
  generous grace window and a three-stage uncached re-confirmation before any
  destructive action — because tearing down a cluster someone still references
  is far worse than leaking one for minutes. FEP-0001's
  withdraw-at-any-moment-with-no-finalizer stance is notably more aggressive
  than what that team found safe in production; it also had to add a
  shared-resource guard after an incident where deleting one claim destroyed a
  sibling's live cluster. Finding 2.1 above is not hypothetical.

## Suggested next steps

1. File finding 1 against the API (fix in #803 or a follow-up), flip the
   `KNOWN GAP` spec when merged.
2. Raise findings 2.1–2.6 on the FEP/issue for maintainer decisions.
3. Once #786/#788 land, replace `withdrawer.go` with the real controller and
   grow `workflow_test.go` into the full #791 verification matrix (concurrency
   limits, eligible-keys gating, `whenUnfulfilled: KeepSearching`,
   delete-races, policy-deletion cleanup).
