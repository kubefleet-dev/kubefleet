# KubeFleet Helm Charts

This directory contains Helm charts for deploying KubeFleet components.

## Available Charts

- **hub-agent**: The central controller that runs on the hub cluster, managing placement decisions, scheduling, and cluster inventory
- **member-agent**: The agent that runs on each member cluster, applying workloads and reporting cluster status

## Chart Versioning

**Important:** Chart versions match the KubeFleet release versions. When a KubeFleet release is tagged (e.g., `v0.3.0`), the Helm charts are published with the same version (`0.3.0`).

**Example:** To install KubeFleet v0.3.0, use:
```bash
helm install hub-agent oci://ghcr.io/kubefleet-dev/kubefleet/charts/hub-agent --version 0.3.0 --namespace fleet-system --create-namespace
```

This ensures consistency between the application version and the chart version, making it easy to know which chart version to use with each KubeFleet release.

## Using Published Charts

KubeFleet Helm charts are automatically published to both GitHub Container Registry (GHCR) as OCI artifacts and GitHub Pages as a traditional Helm repository.

### Option 1: OCI Registry (Recommended)

Install directly from GitHub Container Registry without adding a repository:

#### Hub Agent

```bash
# Install hub-agent on the hub cluster (replace VERSION with your desired release)
helm install hub-agent oci://ghcr.io/kubefleet-dev/kubefleet/charts/hub-agent \
  --version VERSION \
  --namespace fleet-system \
  --create-namespace
```

#### Member Agent

```bash
# Install member-agent on each member cluster (replace VERSION with your desired release)
helm install member-agent oci://ghcr.io/kubefleet-dev/kubefleet/charts/member-agent \
  --version VERSION \
  --namespace fleet-system \
  --create-namespace \
  --set config.hubURL=https://<hub-api-server> \
  --set config.hubCA=<base64-encoded-hub-ca> \
  --set config.memberClusterName=<member-cluster-name>
```

### Option 2: Traditional Helm Repository

> **Important:** every release before this fix reached this index as chart
> `0.1.0`, which depending on when you installed it either failed to start or
> silently tracked the unreleased `main` branch. Releases now publish their real
> version. If you already installed from here, see
> [Migrating from the 0.1.0 chart index](#migrating-from-the-010-chart-index).

Add the repository and install from it:

```bash
# Add the KubeFleet Helm repository
helm repo add kubefleet https://kubefleet-dev.github.io/kubefleet/charts

# Update your local Helm chart repository cache
helm repo update

# Install hub-agent
helm install hub-agent kubefleet/hub-agent \
  --namespace fleet-system \
  --create-namespace

# Install member-agent
helm install member-agent kubefleet/member-agent \
  --namespace fleet-system \
  --create-namespace \
  --set config.hubURL=https://<hub-api-server> \
  --set config.hubCA=<base64-encoded-hub-ca> \
  --set config.memberClusterName=<member-cluster-name>
```

### Installing Specific Versions

#### OCI Registry

```bash
# Install a specific version from OCI registry (e.g., v0.3.0 release)
helm install hub-agent oci://ghcr.io/kubefleet-dev/kubefleet/charts/hub-agent \
  --version 0.3.0 \
  --namespace fleet-system \
  --create-namespace
```

#### Traditional Repository

> **Note:** `helm search repo kubefleet --versions` still lists the stale
> `0.1.0` entry described in
> [Migrating from the 0.1.0 chart index](#migrating-from-the-010-chart-index) —
> do not install it — along with an unrelated `arc-member-cluster-agents-helm-chart`
> entry that `--merge` has preserved since 2025. Charts carrying their own
> release version exist for `v0.2.2`, `v0.3.0`, and `v0.3.1`, in the OCI registry
> only; no such chart exists for any earlier release on either channel.

```bash
# List available versions
helm search repo kubefleet --versions

# Install a specific version (replace VERSION with one listed above)
helm install hub-agent kubefleet/hub-agent \
  --version VERSION \
  --namespace fleet-system \
  --create-namespace
```

### Upgrading Charts

#### OCI Registry

```bash
# Upgrade to a specific version (e.g., v0.3.0)
helm upgrade hub-agent oci://ghcr.io/kubefleet-dev/kubefleet/charts/hub-agent \
  --version 0.3.0 \
  --namespace fleet-system

helm upgrade member-agent oci://ghcr.io/kubefleet-dev/kubefleet/charts/member-agent \
  --version 0.3.0 \
  --namespace fleet-system
```

#### Traditional Repository

```bash
# Upgrade to latest version
helm upgrade hub-agent kubefleet/hub-agent --namespace fleet-system
helm upgrade member-agent kubefleet/member-agent --namespace fleet-system
```

If you have been tracking this repository since before per-release versions were
published, this upgrade moves off `0.1.0` for the first time and can cross
several releases at once. Read the release notes for the whole span, not just
the newest entry, and see
[Migrating from the 0.1.0 chart index](#migrating-from-the-010-chart-index).

## Migrating from the `0.1.0` chart index

The action that maintains the GitHub Pages index was never told the release
version, so it packaged the in-tree `Chart.yaml` verbatim — always version
`0.1.0`. Because `helm repo index --merge` lets a freshly generated entry win a
version collision, every publish silently replaced the contents of that same
`0.1.0` tarball. Until this fix, the index held exactly one entry per chart, and
that one entry was rewritten 50 times: on releases, and — until February 2026 —
on any push to `main` that touched `charts/`. What an install got depended on
when you last ran `helm repo update`, not on any release.

Because the in-tree values changed over time, that single `0.1.0` slot has
deployed three different things:

| Published | Rendered image | Effect |
| --- | --- | --- |
| Oct 2025 – Apr 2026 | `ghcr.io/azure/fleet/hub-agent:main` | **Works, and tracks unreleased code.** `main` is a floating tag and `pullPolicy` is `Always`, so every pod restart pulls whatever was last built from `main`. |
| Feb – Mar 2026 | `ghcr.io/kubefleet-dev/kubefleet/hub-agent:0.1.0` | `ImagePullBackOff` — that tag does not exist. |
| Apr 2026 – this fix | `ghcr.io/kubefleet-dev/kubefleet/hub-agent:v0.1.0` | `ImagePullBackOff` — that tag does not exist either. |

These interleaved rather than replacing one another: publishes ran from
different refs, so three weeks after the broken chart first appeared the index
went back to serving the `:main` one, and did so for a month — from 2026-03-10
until 2026-04-08. Which of the three you got depends on when you last ran
`helm repo update`.

The first row is the one worth acting on. There is no `v0.1.0` release of
KubeFleet — the `0.1` series ran `v0.1`, `v0.1.1`, `v0.1.2` — and neither
`0.1.0` nor `v0.1.0` was ever pushed to `ghcr.io/kubefleet-dev/kubefleet`. So the
later two rows fail loudly and harmlessly. An install from the first row came up
fine and has been following `main` ever since.

That drift has not stopped. `ghcr.io/azure/fleet/hub-agent:main` is still being
rebuilt — the current image was built on 2026-08-04 — and nothing in this
repository publishes to `ghcr.io/azure/fleet`; it is outside the KubeFleet
release process entirely. A cluster in the first row is therefore still picking
up new, unreleased builds from a namespace this project does not control, and it
will keep doing so until you pin `image.tag`. There is no symptom to wait for:
those pods are healthy.

Check what you are running:

```bash
kubectl get deployment -n fleet-system \
  -o custom-columns='NAME:.metadata.name,IMAGE:.spec.template.spec.containers[*].image'
```

Anything that is not a release tag — `:main`, `:0.1.0`, `:v0.1.0`, or any
`ghcr.io/azure/fleet/*` image — came from this bug. Once a release has been
published with this fix in place, move to it:

```bash
helm repo update
helm upgrade hub-agent kubefleet/hub-agent \
  --version VERSION \
  --namespace fleet-system

# member-agent holds its hub connection in values, so re-pass them (or
# --reuse-values); a bare upgrade resets them to the chart's placeholders.
helm upgrade member-agent kubefleet/member-agent \
  --version VERSION \
  --namespace fleet-system \
  --set config.hubURL=https://<hub-api-server> \
  --set config.hubCA=<base64-encoded-hub-ca> \
  --set config.memberClusterName=<member-cluster-name>
```

Until such a release exists, the index offers only `0.1.0`; pull the chart from
the OCI registry instead.

<!-- Maintainers: once the first release with this fix is out, replace the
     relative phrasing above and in this section's opening ("before this fix")
     with that version number, and delete the "Until such a release exists"
     sentence. -->

`member-agent` behaves the same way, with the added wrinkle that it runs two
images — the agent and the `refresh-token` sidecar — affected identically in
every row above, and needing two overrides rather than one.

**The OCI registry.** Charts there have carried a real per-release version since
`0.2.2`, and from `0.2.2` on they render a published image. The two earlier ones
should not be used: `0.1.0` pins `ghcr.io/azure/fleet/hub-agent:main`, the same
floating tag as the first row above, and `0.2.1-test` renders a `0.2.1-test`
image that was never pushed. Pin `0.2.2` or later and the OCI channel is
unaffected.

**Installing from a git checkout.** The in-tree `Chart.yaml` carries the same
`v0.1.0` placeholder, so a local `helm install ./charts/hub-agent` hits this too.
Pass `--set image.tag=VERSION` (and `--set refreshtoken.tag=VERSION` for
member-agent) with a published release tag.

## Chart Publishing

Charts are published to both locations when a stable version tag (e.g.
`v1.0.0`) is pushed. The push trigger filters out release-candidate tags
(e.g. `v1.0.0-rc.1`), so an RC does not reach the chart index by cutting a tag;
a maintainer can still publish one deliberately via `workflow_dispatch`.

**Published Locations:**
- **OCI Registry**: `oci://ghcr.io/kubefleet-dev/kubefleet/charts/{chart-name}`
- **GitHub Pages**: `https://kubefleet-dev.github.io/kubefleet/charts`

The publishing workflow is defined in `.github/workflows/chart.yml`.

## Development

### Local Installation

For development and testing, you can install charts directly from the local
repository. The in-tree `Chart.yaml` carries a placeholder `appVersion` that does
not name a published image, so a local install has to say which images to run:

```bash
# Install from local path (replace VERSION with a published release tag, e.g. v0.3.1)
helm install hub-agent ./charts/hub-agent \
  --namespace fleet-system \
  --create-namespace \
  --set image.tag=VERSION
helm install member-agent ./charts/member-agent \
  --namespace fleet-system \
  --create-namespace \
  --set image.tag=VERSION \
  --set refreshtoken.tag=VERSION \
  --set config.hubURL=https://<hub-api-server> \
  --set config.hubCA=<base64-encoded-hub-ca> \
  --set config.memberClusterName=<member-cluster-name>
```

### Linting

```bash
# Lint a chart
helm lint charts/hub-agent
helm lint charts/member-agent
```

### Packaging

```bash
# Package charts locally
helm package charts/hub-agent
helm package charts/member-agent
```

## Chart Documentation

For detailed documentation on each chart including configuration parameters, see:
- [Hub Agent Chart](./hub-agent/README.md)
- [Member Agent Chart](./member-agent/README.md)

## Contributing

When making changes to charts:
1. Leave `version` and `appVersion` in `Chart.yaml` alone — the release
   workflow injects the release version at package time, so the in-tree values
   are placeholders and are not what gets published
2. Run `helm lint` to validate your changes
3. Update the chart's README.md with any new parameters or changes
4. Test the chart installation locally before submitting a PR

## Support

For issues or questions about KubeFleet Helm charts, please:
- Check the [main documentation](https://kubefleet.dev/docs/)
- Review chart-specific READMEs
- Open an issue in the [GitHub repository](https://github.com/kubefleet-dev/kubefleet/issues)
