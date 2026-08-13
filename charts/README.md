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

> **Heads up if you already use this repository.** Until recently every release
> was published here as chart version `0.1.0` with `appVersion v0.1.0`, so an
> install from this channel that did not set `image.tag` has been running the
> `v0.1.0` images. Releases now publish their real version, which means the
> commands below resolve to the current release rather than to `0.1.0`. The OCI
> registry above was never affected.

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

> **Note:** `helm search repo kubefleet --versions` still lists a stale `0.1.0`
> entry from before per-release versions were published. Releases cut before
> that change are available from the OCI registry only.

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

If you have been tracking this repository since before per-release versions
were published, this upgrade moves off `0.1.0` for the first time and can cross
several releases at once. Read the release notes for the whole span, not just
the newest entry, and note that unless you set `image.tag` the running image
version moves with the chart's `appVersion`.

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

For development and testing, you can install charts directly from the local repository:

```bash
# Install from local path
helm install hub-agent ./charts/hub-agent --namespace fleet-system --create-namespace
helm install member-agent ./charts/member-agent \
  --namespace fleet-system \
  --create-namespace \
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
