---
name: release
description: Cut a KubeFleet release or release candidate, or check that a release that already ran is complete. Only when a human explicitly asks for a release. Covers the tag grammar, what release.yml and chart.yml produce, the release-notes labels, the release branch, and the notice to downstream consumers.
license: Apache-2.0
metadata:
  author: The KubeFleet Authors
---

# Release

**Do not start this procedure on your own initiative.** Tags and releases are public and cannot be undone. Only follow it when a human has explicitly asked for a release or a release check, and stop before any `git push` or `gh release` command unless they confirmed the exact tag or branch name.

Two human actions produce a release: push a tag, then review the draft. Everything else is automation whose outputs this skill tells you to verify. Read [`VERSIONING.md`](../../../VERSIONING.md) before starting; it decides whether this is a patch or a minor.

## 1. Before tagging

- Pick the version with the table in `VERSIONING.md`. Any new CRD field or changed placement semantics since the last tag means a minor.
- Confirm every merged PR since the previous tag carries a `release-note/*` label; unlabeled PRs land under "Other Changes" and breaking changes without `release-note/breaking` are invisible. Check with `gh pr list --repo kubefleet-dev/kubefleet --state merged --limit 200 --search "merged:>YYYY-MM-DD" --json number,title,labels`.
- CI on `main` is green, including `upgrade.yml` (the version-skew jobs).
- For the first release candidate of a minor, cut the release branch from the same commit: `git branch release-0.Y <sha> && git push origin release-0.Y`. `backport.yml` and `CONTRIBUTING.md` assume this branch exists; `git ls-remote --heads origin 'release-*'` shows whether any has been cut, and if none has, confirm the convention with the maintainers first.

## 2. Tag

Only two formats are accepted; `setup-release.yml` rejects anything else:

```text
vMAJOR.MINOR.PATCH          v0.5.0
vMAJOR.MINOR.PATCH-rc.N     v0.5.0-rc.1
```

```sh
git tag -a <tag> <sha> -m <tag> && git push origin <tag>
```

Signed tags are not required today; if the release-signing work lands, this line changes to `git tag -s`.

`release.yml` also accepts `workflow_dispatch` with the tag as input, for re-running a release whose tag already exists.

## 3. What the tag triggers

| Workflow | Produces | Verify |
| --- | --- | --- |
| `release.yml` → `build-and-publish` | `ghcr.io/kubefleet-dev/kubefleet/{hub-agent,member-agent,refresh-token}` for `linux/amd64` and `linux/arm64`, tagged `v0.5.0` and, for GA only, the short alias `0.5.0` | `docker buildx imagetools inspect ghcr.io/kubefleet-dev/kubefleet/hub-agent:v0.5.0` shows both platforms; inspect the short alias too on a GA release |
| `release.yml` → `publish-crds` | A GitHub release created as a **draft**, with the CRD tarball and its `.sha256` from `make crd-package`, then flipped to published | Release page shows the two assets and is no longer a draft |
| `chart.yml` (GA tags only, `-rc` tags are filtered out) | The Helm charts in the GitHub Pages index (`publish-github-pages` job) and as OCI artifacts under `ghcr.io/kubefleet-dev/kubefleet/charts` (`publish-oci` job, `make helm-push`) | `helm repo add kubefleet https://kubefleet-dev.github.io/kubefleet/charts && helm repo update && helm search repo kubefleet` lists the new version; `helm show chart oci://ghcr.io/kubefleet-dev/kubefleet/charts/hub-agent --version <ver>` resolves |

Release notes are generated from PR labels by `.github/release.yml`. Read the draft once for wrong categories and for anything that needs a manual upgrade note, then edit the release text; do not edit labels retroactively to fix notes.

## 4. After publishing

- A release candidate stays marked pre-release; a GA release must not be.
- Update the support window in `SECURITY.md` if a minor dropped out of `N` and `N-1`.
- Draft the downstream notice and hand it to the human to send; do not post to other repositories yourself. `Azure/fleet` mirrors this repository and publishes it as `go.goms.io/fleet`; `Azure/fleet-networking` and other consumers bump that module.
- Close the release tracking issue if one exists.

## If something failed

`release.yml` is safe to re-run with `workflow_dispatch` and the same tag: image pushes are idempotent, and a release the run created is only flipped out of draft after every asset uploads. If the draft already existed before the re-run, the run uploads the assets but leaves it a draft and prints a warning; publish it by hand. Never delete and re-push a tag that anyone may have pulled; cut the next patch instead.
