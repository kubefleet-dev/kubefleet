# Implementation notes: FEP-0001 placement policy controller (#786)

Observations collected while implementing the controller against the
`placement.kubefleet.dev/v1alpha1` APIs. Items in "API gaps" are candidate
follow-ups for the API definition (#781/#803); they are recorded here rather
than silently worked around, and the ones marked *pinned* have a test in this
package demonstrating the current behavior.

## API gaps found while implementing

1. **`ClusterSelector.count` accepts non-positive integers** (*pinned*). The
   field is `XIntOrString` with `Pattern="^([1-9][0-9]{0,2}|All)$"`, but a
   pattern only constrains the string form; integer values bypass it entirely,
   so `count: 0` and `count: -1` pass CRD validation. The controller rejects
   them at evaluation time (`resolveCounts`), surfacing
   `Scheduled=False/InvalidClusterSelectors`. Suggested API fix: a CEL rule
   such as `type(self.count) == int ? self.count >= 1 : true`.

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

## Boy Scout fixes riding along

- `pkg/scheduler/framework/plugins/clusteraffinity/types.go`: resource
  property names containing dashes in the resource name (e.g.
  `allocatable-ephemeral-storage`) were rejected by the two-segment split;
  now parsed with `strings.Cut` and covered by a regression test.
