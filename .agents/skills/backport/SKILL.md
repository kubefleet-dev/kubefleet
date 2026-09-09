---
name: backport
description: Backport a fix that is already merged to main onto a supported release-0.Y branch, using the cherry-pick label automation or, when it refuses, a manual cherry-pick that keeps the DCO sign-off. Use when asked to backport, when a cherry-pick/0.Y label produced a failure comment, or when a fix must ship in a patch release.
license: Apache-2.0
metadata:
  author: The KubeFleet Authors
---

# Backport

Backports are cherry-picks of a commit that is already on `main`, never direct PRs against a release branch. `backport.yml` does the common case; this skill covers using it and the manual path when it declines. Read the rules in [`CONTRIBUTING.md`](../../../CONTRIBUTING.md#backporting-to-release-branches).

## 1. Use the automation

Agree the target minors with the human and ask them to add one `cherry-pick/0.Y` label per target minor to the original PR, before or after it merges; each label makes `backport.yml` push a branch and open a PR on their behalf. On merge, or on labeling an already-merged PR, the workflow cherry-picks the squash commit onto `release-0.Y`, pushes a `cherry-pick/0.Y/pr-<N>` branch, and opens the backport PR.

Which minors are supported is in `SECURITY.md` (`N` and `N-1`). Do not label older branches. If `git ls-remote --heads origin 'release-*'` returns nothing, no release branch has been cut yet and there is nothing to backport to; say so rather than creating a `cherry-pick/0.Y` label.

## 2. Read its failure comment

The workflow comments on the original PR when it cannot proceed; the first and third cases leave the run green, a conflict fails it. Three causes:

| Comment says | Why | What to do |
| --- | --- | --- |
| Not a squash merge | It only picks single squash commits | Manual path below, one commit at a time |
| Cherry-pick did not apply cleanly | Conflict with the release branch | Manual path below; resolve, then push your own branch |
| Branch `release-0.Y` not found | The minor has no release branch yet | Cut it first (see the `release` skill) or pick a supported minor |

Re-adding the label retries the automation after you fix the cause. The bot's `cherry-pick/0.Y/pr-<N>` branches are force-pushed on retry, so never do manual work on one.

## 3. Manual cherry-pick

```sh
git fetch origin
git checkout -b backport-<N>-0.Y origin/release-0.Y
git cherry-pick -x <squash sha>          # -x adds "(cherry picked from commit ...)"
# resolve conflicts if any, then:
GIT_EDITOR=true git cherry-pick --continue   # keeps the original message without opening an editor
```

Rules that hold on the manual path:

- The cherry-picked commit keeps the original author's `Signed-off-by`. If you had to rewrite the change substantially, the human doing the backport adds their own with `git commit -s --amend`; a tool never adds one.
- Same PR-title prefix as the original, same `release-note/*` label, and `Fixes #<issue>` so the patch notes link the bug.
- Only the fix. If the conflict resolution needs code that is not on `release-0.Y`, that is a sign the fix cannot be backported as-is; say so instead of pulling in extra commits.

## 4. Verify

`make reviewable` and `make local-unit-test` on the release branch; the tool versions pinned there may differ from `main`, so run them from the checkout, not from memory.

## 5. Hand the branch to the author

Stop here and show the human the resolved diff; pushing and opening the PR are theirs (`AGENTS.md`, Workflow). What they run:

```sh
git push -u origin backport-<N>-0.Y
gh pr create --base release-0.Y --title "<original title> [backport release-0.Y]" \
  --label "release-note/<same as original>" --body "Backport of #<N>. Fixes #<issue>."
```

Confirm the PR's base is `release-0.Y` and CI runs there before asking for review.
