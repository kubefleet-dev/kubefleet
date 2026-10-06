# Implementation notes: FEP-0001 placement policy controller (#786)

Observations collected while implementing the controller against the
`placement.kubefleet.dev/v1alpha1` APIs. Items in "API gaps" are candidate
follow-ups for the API definition (#781/#803); they are recorded here rather
than silently worked around, and the ones marked *pinned* have a test in this
package demonstrating the current behavior.

## API gaps found while implementing

1. **`ClusterSelector.count` accepts non-positive integers** (*closed*). The
   field is `XIntOrString` with `Pattern="^([1-9][0-9]{0,2}|All)$"`, and a
   pattern only constrains the string form, so `count: 0` and `count: -1` used
   to bypass CRD validation and be rejected by the controller at evaluation
   time instead. #829 added the CEL rule bounding the integer form, so they are
   now rejected at admission; the controller's own check remains as a backstop
   for objects admitted before the rule shipped.

2. **Numeric operators are accepted in `matchLabelExpressions` at admission**
   (*pinned*). `MatchLabelExpressions` reuses the
   `LabelClusterPropertyExpression` type, whose enum includes `Gt`/`Lt`/etc.;
   the API doc comment defers misuse to "an error at the scheduling phase".
   Admission could reject it instead:
   `self.all(e, e.operator in ['In', 'NotIn', 'Exists', 'DoesNotExist'])` on
   the `matchLabelExpressions` field. Until then, the controller surfaces
   `Scheduled=False/InvalidClusterSelectors`.

3. **Aggregation across multiple selectors follows the FEP's overlapping-selectors
   note.** A cluster satisfying two selectors of the same policy counts toward
   both in `desiredClusters`/`scheduledClusters` (per-selector sums) — the
   FEP's "About overlapping cluster selectors" section explicitly allows
   totals to deviate from the sum of counts when selectors overlap. For a
   `count: All` selector the desired count floors at `minCount`, so an
   unfulfilled All selector shows a gap instead of trivially reporting
   `desired == scheduled`.

4. **`Scheduled` condition semantics with `minCount`.** The API's reason
   constants encode a binary contract (`FoundAllRequiredClusters` /
   `FailedToFindSomeRequiredClusters`), so reaching the floor (minCount) but
   not the desired count reports `False` here, with the floor state surfaced
   in the condition message. Whether the floor deserves a dedicated reason
   (or a `True` polarity) is an FEP-level question worth settling before
   beta; the claim lifecycle also needs a defined stance on whether a
   floor-satisfied selector still warrants a claim.

5. **Status count fields lack `+kubebuilder:validation:Optional` markers**
   (`DesiredClusters` through `ActiveClusterClaims`) — cosmetic, but the
   package convention annotates every optional field.

## Deliberate implementation decisions (for reviewers)

- **Fulfillment is judged against the scheduler's eligibility gate**
  (`clustereligibilitychecker`) plus taint/toleration filtering, not raw label
  matching — a provisioner-created or newly registered cluster does not count
  until its member agent is online, heartbeating, and joined. See the
  discussion on #791.
- **Selected-cluster choice is first-N over sorted names** — a deterministic
  placeholder until the scheduling framework support lands; the counts are
  what matter to the status surface today.
- **Heartbeat-noise suppression**: member cluster watch events pass through a
  projection predicate that ignores heartbeat/observation timestamps;
  time-driven eligibility transitions (e.g., heartbeats going stale) are
  covered by periodic requeues instead (fulfilled policies re-check at half
  the eligibility timeout, bounding worst-case staleness detection at about
  1.5x the timeout).
- **Malformed per-cluster data never fails a policy**: selector specs are
  validated structurally up front, so any evaluation-time error is by
  construction caused by data one cluster self-reported (e.g., a property
  value that is not a quantity); that cluster is skipped and logged rather
  than aborting evaluation for the whole policy.
- **Claims are issued while a selector is below its desired count** (not just
  below the minCount floor), consistent with the binary Scheduled contract;
  whether floor-satisfied selectors should stop claiming is part of the same
  FEP follow-up as item 4 above.
- **Claim names are deterministic and namespace-qualified**: the name embeds
  the selector index and a hash of the policy's namespaced name, so
  same-named policies in different namespaces cannot collide on the
  cluster-scoped claim namespace, and a restarted reconciler converges on
  get-or-create instead of duplicating claims.
- **A Terminating claim occupies its budget slot**: a claim held by a
  provisioner finalizer is not replaced, so a slow teardown can starve the
  policy of its single claim slot but can never cause double-provisioning —
  the safer failure mode for a provisioner acting on the claim.
- **Cleanup is finalizer-driven**: cross-scope owner references (namespaced
  policy owning a cluster-scoped claim) are invalid in garbage collection —
  and, trap-like, accepted at admission — so claims carry ownership labels,
  the policy carries a cleanup finalizer (added before the first claim is
  created, so a crash between the two writes cannot orphan a claim), and
  deletion withdraws claims before the policy goes away. Claims are deleted
  one by one rather than via DeleteAllOf, which keeps the RBAC surface free
  of deletecollection.
- **Withdrawal is eligibility-gated**: a claim is withdrawn only when its
  selector is fulfilled by schedulable clusters, so a provisioned cluster
  that has registered but not yet joined does not withdraw the claim (the
  join-window race raised on the verification issue).
- **Selector-term changes replace the claim under the same name**: the stale
  claim is withdrawn and the same deterministic name is re-created with the
  new terms. While the old claim is held Terminating by a provisioner
  finalizer, `activeClusterClaims` keeps counting it: the withdraw round
  treats the just-deleted claim as still occupying its slot, and the
  replacement create waits for the slot to free rather than standing beside
  it, so the budget holds through the swap.
- **A policy with no cluster selectors claims like any other**: the
  synthesized "all clusters" selector inherits the API's `AddClusterClaim`
  default, so a bare policy facing an empty fleet still signals that a
  cluster is needed rather than silently waiting forever.
- **The FEP's eligible-keys allowlist is the class's selector vocabulary.** A
  claim goes to the `ClusterProviderClass` the policy names, else the fleet's
  single default class; a selector whose terms fall outside that class's
  vocabulary gets no claim, and a policy with no resolvable class gets none at
  all. Either way the `Scheduled` condition keeps its binary reason and says
  why in its message, and a `ClusterClaimNotIssued` warning event is raised,
  so the condition contract the FEP defines is unchanged. Both fire only when
  some selector actually wants a claim: a satisfied policy, or one whose
  selectors all `KeepSearching`, reports nothing in a fleet with no classes.
  Only terms that say what to provision are claimable: label matchers, `In`
  label expressions, and `Eq`/`Gt`/`Ge`/`Lt`/`Le` property comparisons on
  admitted keys. A comparison is judged by whether some value within the
  class's bounds satisfies it, since its value is a threshold rather than the
  cluster delivered -- `Le 200` under a max of 100 is claimable, `Gt 100` is
  not. A class with no vocabulary admits only a selector with no terms. The
  per-fleet concurrency limit remains a config surface for the terminal-state
  unit.
- **The class and the vocabulary gate issuing, never keeping.** A claim that
  is outstanding stays wanted while its selector is unfulfilled and its terms
  unchanged, even if the policy resolves to no class for the moment (the class
  was deleted, or two classes are marked default while an admin moves the
  annotation) or the vocabulary narrowed under it. Withdrawing there would
  tear down provisioning in flight for a transient admin state; the FEP
  validates the vocabulary "when a claim would be issued". The one exception
  is a class that resolves to a *different* class, below.
- **Automatic approval is re-asserted, not only stamped.** `Approved` is a
  second write after the create, so a controller stopped between the two
  would leave a claim that no provider may act on and that, being
  unapproved, never expires; every reconcile of a kept claim of an
  `Automatic` class with no `Approved` entry stamps it, in the same status
  write as the freshness marker so a conflict on one cannot strand the
  other. A claim already carrying an entry -- an approver's, or a denial --
  is left alone. Flipping a class from `Manual` to `Automatic` therefore
  approves every kept claim of it that no approver has ruled on yet.
- **A class change replaces the claim** the way a term change does: the
  class is part of what makes an outstanding claim still wanted, since the
  claim is stamped with it and providers filter on the stamp. The same holds
  for a policy that is deleted and recreated by `annotationplacement`: the
  regenerated policy starts without a class or a claim limit, which the sync
  otherwise preserves on an existing policy.
- **The controller watches `ClusterProviderClass`**, so the hub's cache must
  be able to list it; the CRD and the RBAC rule ship with the integration
  unit, and the controller fails to start without them.

## How a claim records the policy that owns it

`spec.placementPolicyRef` is the authoritative identity: it is required,
immutable, and has no length limit, so the claim watch maps an event back to
its policy through that field. The ownership **labels** exist only so that a
policy's claims can be selected server-side with a `List`, which no spec field
allows.

That distinction matters because label values stop at 63 bytes while object
names run to 253. When a policy's name does not fit, the label carries a
prefix plus a hash of the full name instead — so selecting on the label with
the policy's own name matches nothing, and consumers that must handle such
names should list claims and match on `spec.placementPolicyRef`. Generated
names are trimmed of trailing separators at every truncation point: a `.` or
`-` landing exactly on the boundary would otherwise produce a name the API
server rejects. Both flaws were found by manual testing with a 250-character
policy name, and each made claim creation fail validation and the reconciler
retry forever, with the policy silently never getting a claim.

## Operational notes

- **Stopping the controller with claims outstanding leaves policies
  undeletable.** The cleanup finalizer is only removed by this controller, so
  running the hub agent without it while a policy holds cluster claims parks
  that policy in `Terminating` and leaves its claims in place until the
  controller runs again, at which point reconciliation resumes and completes
  the cleanup. This mirrors the existing behavior of the staged update run
  APIs and their finalizer; the integration PR that wires the controller in
  should document it on the enabling flag and in the chart README rather than
  work around it, since removing a finalizer without a controller to withdraw
  the claims would orphan them instead.
- **Not wired into the hub agent yet.** The controller is dormant code until
  the integration PR adds the flag, scheme registration, RBAC, and the chart
  CRDs; its kind e2e is parked at `parked/fep0001-placement-policy-e2e`.
- **Restarting the hub agent mid-flow is safe**: claim creation is
  get-or-create on a deterministic name, and the finalizer is added before the
  first claim exists, so no window can orphan a claim.

## Cleanup lists every claim

Releasing the claim-cleanup finalizer is gated on an uncached list of ALL
cluster claims, filtered by each claim's immutable `spec.placementPolicyRef`
-- the ownership labels are mutable, and a label-stripped claim vanishing
from a label-selected list would be orphaned permanently. This trades a
full-collection read per deleting policy for that guarantee; cheap while the
per-policy claim budget is 1, worth revisiting together with the
configurable concurrency limits.
