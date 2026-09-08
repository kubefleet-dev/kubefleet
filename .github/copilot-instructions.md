# KubeFleet Copilot Instructions

Read [`AGENTS.md`](../AGENTS.md) first. It is the single source of truth for every AI assistant in this repository: rules, commands, generated files, architecture, test conventions, gotchas. Copilot reads it on its own; this file only adds what is specific to Copilot's environments and keeps the team's collaboration protocol.

## Copilot environments

- **Coding agent (cloud):** `make reviewable` and `make local-unit-test` need only the Go version in `go.mod`; the Makefile downloads the rest (see `AGENTS.md`, Commands). Do not run `make setup-clusters` or `make e2e-tests`; kind is not available there.
- **Code review:** apply the test conventions and the generated-files rule in `AGENTS.md`.
- **Squad:** `.squad/`, `.github/skills/`, and the `squad-*` workflows belong to the Squad framework and are rewritten by `squad upgrade`, which may also rewrite this file. Project conventions live in `AGENTS.md`, not there.

## Collaboration Protocol

### Domain Knowledge

Refer to `.github/.copilot/domain_knowledge/` for entity relationships, workflows, and ubiquitous language. Update these files as understanding grows.

### Specifications

Use `.github/.copilot/specifications/` for feature specs. Ask which specifications apply if unclear.

### Breadcrumb Protocol

For non-trivial tasks, create a breadcrumb file at `.github/.copilot/breadcrumbs/yyyy-mm-dd-HHMM-{title}.md` to track decisions and progress. Update it before and after code changes, and get plan approval before implementation. See existing breadcrumbs for format examples.
