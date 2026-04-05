# KubeFleet Release Process

This document describes how KubeFleet maintainers publish a release from the `kubefleet-dev/kubefleet` repository.

## What a Release Publishes

Publishing a GitHub release with a semver tag publishes all of the following artifacts from the same tagged commit:

- A GitHub release page for the tag, for example `v0.2.2`
- Container images in GitHub Container Registry (GHCR):
  - `ghcr.io/kubefleet-dev/kubefleet/hub-agent`
  - `ghcr.io/kubefleet-dev/kubefleet/member-agent`
  - `ghcr.io/kubefleet-dev/kubefleet/refresh-token`
- Helm charts in both GHCR and GitHub Pages:
  - `oci://ghcr.io/kubefleet-dev/kubefleet/charts/hub-agent`
  - `oci://ghcr.io/kubefleet-dev/kubefleet/charts/member-agent`
  - `https://kubefleet-dev.github.io/kubefleet/charts`

The release automation is defined in:

- `.github/workflows/release.yml`
- `.github/workflows/chart.yml`
- `.github/workflows/setup-release.yml`

## Prerequisites

- You have permission to create releases and tags in `kubefleet-dev/kubefleet`.
- You have permission to publish packages to the repository's GHCR namespace.
- The commit you want to release is already merged and validated on `main` or the relevant `release-*` maintenance branch.
- The required GitHub Actions workflows are green for the commit you intend to tag.
- If you are using the CLI flow, `gh` is installed and authenticated.

## Tag and Version Rules

- Release tags must begin with `v` and use semantic versioning, for example `v0.2.3`.
- The workflows derive the chart version by stripping the leading `v`, so `v0.2.3` becomes chart version `0.2.3`.
- The release image workflow publishes image tags both with and without the `v` prefix:
  - `ghcr.io/kubefleet-dev/kubefleet/hub-agent:v0.2.3`
  - `ghcr.io/kubefleet-dev/kubefleet/hub-agent:0.2.3`
- The chart publishing workflow packages the charts with:
  - `version: 0.2.3`
  - `appVersion: v0.2.3`

Do not edit `charts/*/Chart.yaml` only to cut a release. The chart workflow injects the release `version` and `appVersion` during packaging, so the tag is the source of truth for published chart versions.

## Create the GitHub Release

Create the release from the exact commit you want to ship.

### Option 1: GitHub CLI

```bash
TAG=v0.2.3
TARGET=origin/main

gh release create "${TAG}" \
  --target "${TARGET}" \
  --title "${TAG}" \
  --generate-notes
```

For a prerelease, add `--prerelease`.

### Option 2: GitHub UI

1. Open the repository's Releases page.
2. Choose `Draft a new release`.
3. Create a new tag in the form `vX.Y.Z`, or select an existing one.
4. Point the release at the exact commit or branch tip you want to publish.
5. Mark the release as a prerelease if needed.
6. Publish the release.

Publishing the release creates or publishes the tag, which triggers the release automation.

## What the Automation Does

### Release Images Workflow

`.github/workflows/release.yml`:

- validates the tag via `.github/workflows/setup-release.yml`
- checks out the tagged ref
- builds and pushes the `hub-agent`, `member-agent`, and `refresh-token` images
- publishes image tags with and without the `v` prefix

### Helm Chart Publisher Workflow

`.github/workflows/chart.yml`:

- validates the same tag via `.github/workflows/setup-release.yml`
- packages `charts/hub-agent` and `charts/member-agent`
- sets chart `version` to `${TAG#v}`
- sets chart `appVersion` to `${TAG}`
- publishes the charts to GHCR as OCI artifacts
- updates the GitHub Pages chart repository
- verifies that the packaged chart `appVersion` matches the release tag

The GitHub release page itself does not need binary assets attached to it; the release artifacts are the published images and charts above.

## Monitor the Release

After publishing the release, watch these workflows in GitHub Actions:

- `Release Images`
- `Helm Chart Publisher`

Both workflows also support `workflow_dispatch` with a `tag` input. Use that to rerun the release jobs for an existing tag when the tagged commit is already correct and you only need to retry the automation.

If the tagged commit itself is wrong, do not force-move the release tag after artifacts have already been published. Fix the issue on a new commit and cut a new release tag instead.

## Verify the Published Artifacts

Once the workflows succeed, verify the release end to end.

### 1. Verify the GitHub Release

- Confirm the release page exists for the tag.
- Confirm the release notes and prerelease flag are correct.

### 2. Verify the Container Images

Inspect the published image tags:

```bash
TAG=v0.2.3
VERSION=${TAG#v}

for image in hub-agent member-agent refresh-token; do
  docker buildx imagetools inspect "ghcr.io/kubefleet-dev/kubefleet/${image}:${TAG}" >/dev/null
  docker buildx imagetools inspect "ghcr.io/kubefleet-dev/kubefleet/${image}:${VERSION}" >/dev/null
done
```

### 3. Verify the OCI Helm Charts

Check that both charts are available at the expected version and appVersion:

```bash
TAG=v0.2.3
VERSION=${TAG#v}

helm show chart "oci://ghcr.io/kubefleet-dev/kubefleet/charts/hub-agent" --version "${VERSION}"
helm show chart "oci://ghcr.io/kubefleet-dev/kubefleet/charts/member-agent" --version "${VERSION}"
```

Verify that the output reports:

- `version: ${VERSION}`
- `appVersion: ${TAG}`

### 4. Verify the GitHub Pages Helm Repository

```bash
helm repo add kubefleet https://kubefleet-dev.github.io/kubefleet/charts
helm repo update
helm search repo kubefleet --versions
```

Confirm that the new chart version is visible for both `hub-agent` and `member-agent`.

## Release Checklist

- The intended release commit is merged and green.
- The GitHub release tag uses the `vX.Y.Z` format.
- `Release Images` succeeded.
- `Helm Chart Publisher` succeeded.
- GHCR image tags exist with and without the `v` prefix.
- OCI charts exist at version `${TAG#v}`.
- Chart `appVersion` matches the full tag `${TAG}`.
- The GitHub Pages chart repository shows the new chart version.

