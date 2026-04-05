# Release Process Documentation

**Date**: April 6, 2026 00:33 IST  
**Task**: Document the maintainer release process for KubeFleet and link it from the contribution guide.

## Requirements

1. Add maintainer-facing release documentation for issue `#536`.
2. Base the instructions on the existing tag-driven GitHub Actions workflows.
3. Explain the tag format, published artifacts, rerun path, and post-release verification.
4. Make the documentation discoverable from `CONTRIBUTING.md`.

## Decisions

1. Add a new top-level `RELEASE.md` instead of hiding the runbook inside chart consumer docs.
2. Keep `CONTRIBUTING.md` lightweight and add a short pointer to `RELEASE.md`.
3. Document the workflow-derived chart versioning explicitly so maintainers do not edit `Chart.yaml` only to cut a release.
4. Treat the GitHub release page as the trigger point, while clarifying that the actual shipped artifacts live in GHCR and the chart repositories.

## Implementation Notes

- `RELEASE.md` is grounded in `.github/workflows/release.yml`, `.github/workflows/chart.yml`, `.github/workflows/setup-release.yml`, and the `push` / `helm-push` Make targets.
- The guide documents verification of:
  - GitHub release visibility
  - GHCR image tags with and without the `v` prefix
  - OCI Helm chart version and appVersion
  - GitHub Pages chart publication

