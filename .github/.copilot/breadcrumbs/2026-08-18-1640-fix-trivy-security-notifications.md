# Fix Scheduled Trivy Security Notifications

## Overview

Update the scheduled Trivy issue workflow to notify KubeFleet security owners without assigning the invalid `copilot` REST assignee.

## Plan

1. Rename the issue step to describe its create-or-update behavior.
2. Build the issue body with a `@kubefleet-dev/kubefleet-secops` security-owner mention.
3. Update an existing same-day Trivy issue with the latest scan results, or create it when absent.
4. Remove the invalid `copilot` assignee.
5. Validate the workflow syntax and review the final diff.

## Success Criteria

- [x] Scheduled findings create or refresh one daily Trivy issue.
- [x] The issue mentions `@kubefleet-dev/kubefleet-secops`.
- [x] Issue creation does not specify the invalid `copilot` assignee.
- [x] Non-scheduled vulnerability failures remain unchanged.

## Implementation Notes

- Reused the existing daily issue title and `security,trivy` labels.
- Existing same-day issues are refreshed with the latest vulnerability summary.
- Validated `.github/workflows/trivy.yml` with actionlint v1.7.12, matching CI.
