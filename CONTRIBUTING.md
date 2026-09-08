# Contributing

KubeFleet welcomes contributions and suggestions!

## Terms

All contributions to the repository must be submitted under the terms of the [Apache Public License 2.0](https://www.apache.org/licenses/LICENSE-2.0).

## Certificate of Origin

By contributing to this project, you agree to the Developer Certificate of Origin (DCO). This document was created by the Linux Kernel community and is a simple statement that you, as a contributor, have the legal right to make the contribution. See the [DCO](DCO) file for details.

## DCO Sign Off

You must sign off your commit to state that you certify the [DCO](DCO). To certify your commit for DCO, add a line like the following at the end of your commit message:

```text
Signed-off-by: John Smith <john@example.com>
```

This can be done with the `--signoff` option to `git commit`. See the [Git documentation](https://git-scm.com/docs/git-commit#Documentation/git-commit.txt--s) for details.

## Code of Conduct

The KubeFleet project has adopted the CNCF Code of Conduct. Refer to our [Community Code of Conduct](CODE_OF_CONDUCT.md) for details.

## Contributing a patch

1. Submit an issue describing your proposed change. The maintainers will respond to your issue promptly.
2. Fork the repository, then develop and test your change. The commands, generated files, and test conventions are in [AGENTS.md](AGENTS.md); it is written for assistants but is the shortest start-here for people too.
3. Submit a pull request.

## Working with AI coding tools

AI-assisted contributions are welcome under the same rules as any other. You, the human author, are responsible for every line you submit.

- **Disclose it.** If an AI tool helped write the change, say so in the pull request description (the template has a field for it). Disclosure goes in the PR, not in commit trailers; never list an AI tool as `Co-authored-by`.
- **Understand it.** You must be able to explain every change in your PR. Reviewers may ask, and a PR whose author cannot explain it may be closed.
- **Reply as yourself.** Do not use AI tools to answer review comments. Review threads are conversations between humans.
- **Sign it yourself.** The DCO can only be certified by a human. Never let a tool add a `Signed-off-by` trailer; use `git commit -s`.

Point your tool at [`AGENTS.md`](AGENTS.md); it carries the commands, conventions, and rules an assistant needs. Most tools read it automatically. The exceptions:

| Tool | What to do |
| --- | --- |
| Copilot, Codex, and every other tool that reads `AGENTS.md`, and Claude Code through the one-line `CLAUDE.md` import | Nothing. Open the repository. |
| A tool that does not read `AGENTS.md` | Point its own context setting at `AGENTS.md` once, in your user-level config (for example Gemini CLI's `context.fileName`, or Aider's `--read`). The repository ships no per-tool shims beyond `CLAUDE.md` and `.github/copilot-instructions.md`. |
| Visual Studio (not VS Code) | It reads `.github/copilot-instructions.md`, which points at `AGENTS.md`, rather than `AGENTS.md` itself. Enable custom instructions in Copilot's options; nothing else is needed. |
| Tools that read `.mcp.json` | You may be asked to approve the `squad_state` server that the Squad framework installs. Declining is fine; nothing in `AGENTS.md` needs it. |
| Squad (an optional Copilot multi-agent framework installed under `.squad/`) | Nothing unless you want it. Invoke the `Squad` custom agent in Copilot; its members take project conventions from `AGENTS.md`, which override the trailer and PR-opening steps in Squad's bundled templates. |

Tool behaviour above was checked against each vendor's documentation in 2026-09; open an issue if a row is out of date.

Example prompts that work with any tool once `AGENTS.md` is loaded:

- "Add a field to `ClusterResourcePlacement` and regenerate everything that depends on it."
- "Why did the e2e job on my PR fail? Here is the run URL."
- "Review my diff against this repository's conventions before I push."

## Issue and pull request management

Anyone can comment on issues and submit reviews for pull requests. In order to be assigned an issue or pull request, you can leave a `/assign <your Github ID>` comment on the issue or pull request.

## Pull request titles

PR titles must begin with one of the following prefixes (enforced by [`pr-title-lint.yml`](.github/workflows/pr-title-lint.yml)):

`feat:`, `fix:`, `docs:`, `test:`, `style:`, `interface:`, `util:`, `chore:`, `ci:`, `perf:`, `refactor:`, `revert:` (a `[WIP] ` prefix is also accepted)

Add `make reviewable` to your workflow before opening a PR — the PR template will remind you, but running it locally first saves a round trip.

## Release note labels

Each PR should carry one base `release-note/*` label matching its title prefix; additive labels (below) may be stacked on top. If you cannot apply labels, name the intended one in the PR description and a maintainer adds it.

| PR title prefix | Label |
| --- | --- |
| `feat:` / `perf:` | `release-note/feature` |
| `fix:` / `revert:` | `release-note/fix` |
| `docs:` | `release-note/docs` |
| `test:` | `release-note/test` |
| `chore:` / `ci:` / `style:` / `interface:` / `util:` | `release-note/chore` |
| `refactor:` | `release-note/refactor` |

Additive labels (stack on top of the above when applicable):

- `release-note/breaking` — a change that requires user action to upgrade (manifest edit, CRD reapply, RBAC reapply, webhook config update, member-cluster re-join, etc.) **or** that alters scheduling, override, or apply semantics in a way that re-ranks or re-applies existing placements without a manifest change. Pre-1.0, internal refactors of any alpha or beta API shape that don't require migration steps or change observable semantics do not qualify.
- `release-note/security` — security fixes or vulnerability disclosures
- `release-note/none` — suppresses the entry in release notes but keeps the PR visible in GitHub's auto-notes drafter UI
- `ignore-for-release` — hides the PR entirely from auto-generated notes. Default to this for CI-only or internal-cleanup PRs with no user impact.

PRs with no `release-note/*` label fall into "Other Changes" in the generated notes. Dependabot PRs are labeled `dependencies` automatically and land under "Maintenance and Dependencies" without a `release-note/*` label.

## Backporting to release branches

Fixes that must land in a supported release (see the support window in
[SECURITY.md](SECURITY.md)) are backported by cherry-picking the squash commit
from `main` onto the matching `release-0.Y` branch. Backports are automated by
[`backport.yml`](.github/workflows/backport.yml):

1. Merge the fix to `main` first. Backports are always cherry-picks of a commit
   already on `main`, never direct PRs against a release branch.
2. Add a `cherry-pick/0.Y` label to the PR — before or after the merge, one
   label per target minor. On merge (or on labeling an already-merged PR), the
   automation opens a backport PR against `release-0.Y`.

The automation follows three explicit rules:

- **Squash merges only.** It cherry-picks the PR's single squash commit. PRs
  merged any other way (merge commit, multi-commit rebase) are skipped with a
  comment and must be backported manually.
- **Conflicts are never pushed.** If the pick does not apply cleanly, it aborts
  and comments manual instructions on the original PR; it never opens a PR with
  conflict markers. Re-adding the label retries after you resolve the cause.
- **Bot branches are force-pushed.** `cherry-pick/0.Y/pr-<N>` branches belong to
  the automation and are overwritten on retries — do manual backports on your
  own branch, not on a bot branch.

Backport PRs keep the original commit's `Signed-off-by` (DCO) and gain a
`(cherry picked from commit ...)` trailer. Merging the backport PR into
`release-0.Y` is still subject to the usual review and CI gates.
