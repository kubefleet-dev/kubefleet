---
name: review
description: Review a diff or pull request the way KubeFleet's maintainers do, read-only. Checks test conventions, controller and scheduler patterns, generated-file drift, cluster- versus namespace-scoped CRUD, hub/member version-skew impact, and PR hygiene. Use when asked to review a KubeFleet change, to self-review a branch before pushing (review first, then fix as the author), or when asked what a maintainer would flag.
license: Apache-2.0
metadata:
  author: The KubeFleet Authors
---

# Review

Review a change the way KubeFleet's maintainers do. This is a read-only procedure: do not edit files, and use the shell only for `git diff`, `git log`, `gh pr diff`, `gh pr view` and `gh issue view` (title, labels, body, linked issue), and the read-only make targets named below. Read [`AGENTS.md`](../../../AGENTS.md) first; every rule there is a review criterion.

For a diff that touches no Go (Markdown, YAML, JSON, workflows), skip the Go-specific checks and say so; hygiene still applies. For a diff under `.squad/`, `.github/skills/`, or the `squad-*` workflows, also check whether `squad upgrade` would revert it (`AGENTS.md`, Gotchas).

Report findings ordered by severity, each with file:line, what is wrong, why it matters here, and the concrete fix. Say "no findings" for a category you checked and found clean; do not pad. Quote the maintainers' convention you are applying so the author can look it up.

## What to check

**Tests**

- New behaviour has a test in the same PR; a bug fix has a test that fails without the fix.
- Comparisons use `cmp.Equal` / `cmp.Diff`, whole structs in one shot. Flag assertion libraries and field-by-field checks.
- Expected values are named `want`/`wanted`, the actual value prints first, and diffs are labelled `(-want +got)`.
- Table-driven unit tests; Ginkgo cases added as a new `Context` that reuses the suite's setup.

**Controllers and scheduler**

- Reconcile shape: fetch, check deletion, defaults, reconcile, requeue. Status written through the status subresource; events recorded for user-visible outcomes.
- Errors wrapped with `pkg/utils/controller` constructors so retry semantics are right; flag bare `return err` on API-server or user errors.
- Namespace-scoped objects (`ResourcePlacement`, `ResourceBinding`, `ResourceSnapshot`) carry `Namespace` in every `NamespacedName`; the `Cluster`-prefixed twins never do.
- Scheduler plugins implement the framework interfaces in `pkg/scheduler/framework/interface.go` and read shared state through `CycleStatePluginReadWriter` (`cyclestate.go` in the same package).

**APIs and generated files**

- Any edit under `apis/` comes with regenerated `zz_generated.deepcopy.go` and `config/crd/bases`, and `make crd-verify` passes (new CRDs are symlinked into the charts).
- No hand edits to generated files.
- New fields are optional with safe zero values, documented, and validated with markers or CEL; immutable fields have an `XValidation` rule.
- Anything exchanged between hub and member (`Work`, `AppliedWork`, `InternalMemberCluster`) stays readable one minor older and newer, per `VERSIONING.md`. Ask for the skew reasoning if the PR does not give it.

**Hygiene**

- Style and boilerplate per `AGENTS.md` (header, trailing newline, comment punctuation).
- PR title uses an allowed prefix; a `release-note/*` label is set; the PR links an issue; the change is focused, with no unrelated reformatting.
- No `Signed-off-by` written by a tool and no AI tool listed as `Co-authored-by`; AI assistance, if any, is disclosed in the PR description.

## How to work

1. `git diff --stat` to see the shape, then read the full diff.
2. For each touched controller or type, open the file and the nearest test file; check the conventions above against both.
3. If the author has not stated the result, run `make vet lint staticcheck` (these do not modify files; `make reviewable` does, through `fmt` and `go mod tidy`). Never run e2e.
4. Write the report. Finish with one line: ready to merge, ready after the listed fixes, or needs a design conversation.
