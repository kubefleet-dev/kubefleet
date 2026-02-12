# KubeFleet Helm Charts

This directory contains Helm charts for deploying KubeFleet components.

## Available Charts

- **hub-agent**: The central controller that runs on the hub cluster, managing placement decisions, scheduling, and cluster inventory
- **member-agent**: The agent that runs on each member cluster, applying workloads and reporting cluster status

## Using Published Charts

KubeFleet Helm charts are automatically published to GitHub Pages and can be consumed directly from the Helm repository.

### Adding the Repository

```bash
# Add the KubeFleet Helm repository
helm repo add kubefleet https://kubefleet-dev.github.io/kubefleet/charts

# Update your local Helm chart repository cache
helm repo update
```

### Installing Charts

#### Hub Agent

```bash
# Install hub-agent on the hub cluster
helm install hub-agent kubefleet/hub-agent \
  --namespace fleet-system \
  --create-namespace
```

#### Member Agent

```bash
# Install member-agent on each member cluster
helm install member-agent kubefleet/member-agent \
  --namespace fleet-system \
  --create-namespace
```

### Installing Specific Versions

```bash
# List available versions
helm search repo kubefleet --versions

# Install a specific version
helm install hub-agent kubefleet/hub-agent \
  --version 0.1.0 \
  --namespace fleet-system \
  --create-namespace
```

### Upgrading Charts

```bash
# Upgrade hub-agent
helm upgrade hub-agent kubefleet/hub-agent --namespace fleet-system

# Upgrade member-agent
helm upgrade member-agent kubefleet/member-agent --namespace fleet-system
```

## Chart Publishing

Charts are automatically published to the `gh-pages` branch when:
- Changes are pushed to the `main` branch affecting chart files
- A version tag (e.g., `v1.0.0`) is created

The publishing workflow is defined in `.github/workflows/chart.yml`.

## Development

### Local Installation

For development and testing, you can install charts directly from the local repository:

```bash
# Install from local path
helm install hub-agent ./charts/hub-agent --namespace fleet-system --create-namespace
helm install member-agent ./charts/member-agent --namespace fleet-system --create-namespace
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

## OCI Registry Support

In the future, KubeFleet charts may also be published to OCI-compliant registries (like GitHub Container Registry) for enhanced security and artifact management. Stay tuned for updates.

## Contributing

When making changes to charts:
1. Update the chart version in `Chart.yaml` following [Semantic Versioning](https://semver.org/)
2. Update the `appVersion` if the application version changes
3. Run `helm lint` to validate your changes
4. Update the chart's README.md with any new parameters or changes
5. Test the chart installation locally before submitting a PR

## Support

For issues or questions about KubeFleet Helm charts, please:
- Check the [main documentation](https://kubefleet.dev/docs/)
- Review chart-specific READMEs
- Open an issue in the [GitHub repository](https://github.com/kubefleet-dev/kubefleet/issues)
