# Spike: verifying the cluster claim workflow (FEP-0001, issue #791)

This directory is a time-boxed spike for
[#791 — Verify the cluster claim workflow](https://github.com/kubefleet-dev/kubefleet/issues/791).
It is spike code held to test-quality standards: the suite runs in
`make integration-test` (and therefore CI) so the contract gaps it pins
cannot regress silently. It also runs
against the API types merged in #781 (`ClusterClaim`, as renamed
by #803).

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
- `eligibility_test.go` (round 2) — pins the join-window gap: raw-match
  withdrawal vs. withdrawal gated on the scheduler's real eligibility check.
- `lifecycle_test.go` (round 2) — pins the deletion/ownership gaps:
  provisioner finalizers, policy-deletion orphans, cross-scope ownerRefs,
  claim-name collisions.

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
   a hard delete "regardless of the status of the cluster claim." Nothing
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

## Round 2: contract gaps verified empirically

A second pass turned the open contract questions from finding 2 into pinned,
runnable demonstrations wherever envtest allows:

### 4. The withdrawal predicate fires before the cluster is usable (join window)

The FEP withdraws a claim once a matching cluster "is joined to the fleet",
without defining "joined". A provisioner-created `MemberCluster` matches its
selector at object-creation time; the scheduler's real gate
(`pkg/scheduler/clustereligibilitychecker`: member agent online, recent
heartbeat, `Joined=True`, healthy) passes minutes later, and taints are a
further, separate filter (the claim spec carries no tolerations at all).
`eligibility_test.go` demonstrates both readings side by side with the actual
checker:

- as written, the claim is withdrawn while `IsEligible` still returns false —
  the placement stays unschedulable with no outstanding claim (and no
  concurrency-budget entry) to explain why;
- gated on `IsEligible`, the claim survives the join window and withdraws only
  when the member agent reports in — the semantics #786 should implement.

Withdrawal and the "still unfulfilled" check that gates *new* claims should
both reuse the scheduler's predicate. (Round 1's suite missed this because
envtest never populates agent status — the naive matcher and the real gate
agree vacuously there.)

### 5. Deletion and ownership are unspecified, with sharp edges (`lifecycle_test.go`)

- **Provisioner finalizers "work" but mean nothing.** The API machinery lets a
  provisioner protect in-flight work with a finalizer; withdrawal then only
  marks the claim Terminating, indefinitely. The contract must pick: does a
  Terminating claim still occupy the concurrency slot (starvation) or is it
  replaced (double-provisioning)? Neither answer exists today.
- **Policy deletion orphans claims forever.** Cross-scope ownerReferences
  (namespaced `PlacementPolicy` owning a cluster-scoped claim) are invalid in
  Kubernetes GC — yet the API server *accepts* them at admission, failing only
  at GC time (`OwnerRefInvalidNamespace`, object skipped), which makes them an
  attractive trap for #786. Cleanup has to be explicit reconciliation, and the
  FEP doesn't specify it. `placementPolicyRef` also records no UID, so a
  deleted-and-recreated same-named policy silently inherits the orphan.
- **Claim names collide across namespaces.** Claims are cluster-scoped;
  policies are namespaced; the FEP's own examples reuse the policy name `app`.
  Any deterministic name derived from the policy name alone collides — and a
  deterministic name is exactly what restart-safe provisioners need (see the
  get-or-create lesson in section 3). The naming scheme must include the
  namespace (or a hash of it).

### 6. The status contract has no teeth (additions to `validation_test.go`)

- The immutability bypass is symmetric: terms can be *added* after a
  no-terms create, silently narrowing the claim (and `[]` serializes as
  absent via `omitempty`, so empty-list creates stay mutable too). The
  spec-level guard fixes both directions.
- `Completed=True` with no `provisionedClusterName` is accepted; so is
  downgrading a completed claim back to in-progress. Status atomicity and
  terminal-state pinning (section 3) need to be CEL rules, not conventions.

## Round 3: review hardening

The review pass on this harness surfaced one more contract gap and tightened
two specs:

- **No periodic re-evaluation of time-based eligibility.** The withdrawer
  re-evaluates claims only on `ClusterClaim`/`MemberCluster` write events,
  but the eligibility checker's heartbeat-staleness test is time-based: a
  cluster can silently cross the staleness threshold with no corresponding
  write, and nothing re-enqueues the claim to notice. The gated-withdrawal
  reading needs a resync interval (or a periodic requeue) that the FEP does
  not currently call for.
- The happy-path spec now proves the provisioner's half of the contract
  (`Completed=True`, `provisionedClusterName`) while withdrawal is held by
  the eligibility gate, and the release is the member agent genuinely
  reporting in -- the same transition the eligibility specs drive -- rather
  than a harness bypass.
- A name-squatting spec pins that a pre-existing cluster holding the
  deterministic provisioned name, but not satisfying the claim, is never
  reported as fulfillment.

## Suggested next steps

1. File finding 1 against the API (fix in #803 or a follow-up), flip the
   `KNOWN GAP` specs when merged (removal *and* add-after-create).
2. Raise findings 2.1–2.6 and 4–6 on the FEP/issue for maintainer decisions —
   the round-2 findings (withdrawal predicate, deletion protocol, policy
   cleanup, naming, status CEL) are the ones #786 would otherwise hard-code
   answers to implicitly.
3. Once #786/#788 land, replace `withdrawer.go` with the real controller and
   grow `workflow_test.go` into the full #791 verification matrix (concurrency
   limits, eligible-keys gating, `whenUnfulfilled: KeepSearching`,
   delete-races, policy-deletion cleanup) — keeping the eligibility-gated
   specs as the acceptance bar for the withdrawal predicate.
