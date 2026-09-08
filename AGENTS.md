# KubeFleet

Guidance for AI coding assistants working in this repository. Tools that read `AGENTS.md` load it on their own; `CLAUDE.md` is a one-line import for the assistant that reads that file instead, and `.github/copilot-instructions.md` points here. It is also the shortest "start here" for humans; the contributor process (DCO, labels, backports) is in [CONTRIBUTING.md](CONTRIBUTING.md).

## Rules for agents

- **Only a human certifies the DCO** (Developer Certificate of Origin, see [CONTRIBUTING.md](CONTRIBUTING.md)). Never write a `Signed-off-by` trailer, and never list an AI tool as `Co-authored-by`. The author signs with `git commit -s`.
- **Disclose AI assistance in the PR description**, not through commit trailers. The human author is responsible for every line and must be able to explain it; remind them of this when preparing a PR.
- **Do not answer reviewers with generated text.** Review threads are conversations between humans.
- **Do not open a PR without an open issue that asks for the change.** Search for duplicates first: `gh issue list --search "<keywords>"` and `gh pr list --search "<keywords>"`.
- **PR titles** start with one of `feat:`, `fix:`, `docs:`, `test:`, `style:`, `interface:`, `util:`, `chore:`, `ci:`, `perf:`, `refactor:`, `revert:` (enforced by `pr-title-lint.yml`, which also accepts `[WIP] `). Each PR carries one base `release-note/*` label from the table in [CONTRIBUTING.md](CONTRIBUTING.md), plus `release-note/breaking`, `release-note/security`, or `release-note/none` when they apply; if you cannot apply labels, name them in the PR description and a maintainer adds them.
- **No `@mentions` or `Fixes #N` in commit messages.** They belong in the PR description.
- **Never run `make e2e-tests` unless asked.** It creates four kind clusters (Kubernetes in Docker; one hub, three members by default) and has a 70-minute timeout.
- **Keep changes focused.** No drive-by refactors or reformatting outside the change.

## Workflow

The steps that touch GitHub or the DCO (assigning, signing off, opening the PR, answering reviews) are the human author's. An agent prepares; the human acts.

1. Start from an open issue. To take one, comment `/assign <your GitHub ID>` on it.
2. Branch from `main`.
3. For a bug, write the failing test first.
4. Implement, keeping the change focused on the issue.
5. Run `make reviewable`, then the tests that cover the change (`make local-unit-test`; `make integration-test` when `apis/` or the scheduler changed).
6. The author commits with `git commit -s`. No `Fixes #N` or `@mentions` in the message.
7. Open the PR from the template: prefixed title, the `release-note/*` label (or its name in the description), `Fixes #N` in the body, AI assistance disclosed if any.
8. Answer review comments yourself. Maintainers squash-merge.

## Commands

`make help` lists every target. The ones that matter:

```sh
make reviewable        # fmt, vet, lint, staticcheck, crd-verify, go mod tidy. Required before every PR.
make local-unit-test   # unit tests under pkg/ and cmd/, plus the *_integration_test.go there that run against envtest (a local API server); installs it on first run
make integration-test  # Ginkgo (Go BDD test framework) suites in test/scheduler and test/apis against envtest
go test ./pkg/controllers/rollout/... -run TestPickBindingsToRoll -race   # one package or one test
make generate          # DeepCopy after editing apis/
make manifests         # CRD YAML under config/crd/bases after editing apis/
```

The make targets download the tools they need (envtest, golangci-lint, staticcheck, controller-gen, goimports) into `hack/tools/bin` at the versions pinned in the `Makefile`. `make integration-test` also needs the Ginkgo CLI on your `PATH`: `go install github.com/onsi/ginkgo/v2/ginkgo@<version in go.mod>`. The Go version is pinned in `go.mod`. `make kubebuilder-assets-path` prints `KUBEBUILDER_ASSETS` if you run Ginkgo directly.

## Generated files and boilerplate

- `zz_generated.deepcopy.go` anywhere: run `make generate`.
- `config/crd/bases/*.yaml`: run `make manifests`. A new CRD must also be symlinked into `charts/hub-agent/templates/crds/` or `charts/member-agent/templates/crds/`, or `make crd-verify` fails (except the `placement.kubefleet.dev` group, excluded while FEP-0001, the in-progress enhancement proposal tracked from issue #834, is being built).
- Every new `.go` file starts with the header in `hack/boilerplate.go.txt`.

## Architecture in brief

Hub-and-spoke. The **hub agent** (`cmd/hubagent`, controllers in `pkg/controllers/`, scheduler in `pkg/scheduler/`) runs on one cluster. A **member agent** (`cmd/memberagent`) on each managed cluster pulls work from the hub over an outbound-only connection and reports status back.

Placement flows through a chain of controllers, each producing the next object:

```text
ClusterResourcePlacement / ResourcePlacement           user intent
  → ResourceSnapshot + SchedulingPolicySnapshot        immutable, versioned
  → scheduler → ClusterResourceBinding / ResourceBinding   placement decisions
  → rollout controller                                 staged progression
  → work generator → Work                              one per target cluster
  → work applier (member agent) → AppliedWork          applied on the member
status flows back: AppliedWork → Work → Binding → Placement
```

- **The `Cluster` prefix means cluster-scoped.** `ClusterResourcePlacement` is cluster-scoped; `ResourcePlacement` is its namespace-scoped counterpart. Namespace-scoped objects need `Namespace` in `types.NamespacedName` on every get, update, and delete, and `client.InNamespace` on list. The same pairing applies to bindings and snapshots.
- **Scheduler** (`pkg/scheduler/framework`): plugins implement `PreFilterPlugin`, `FilterPlugin`, `PreScorePlugin`, `ScorePlugin`, `PostBatchPlugin` from `interface.go`; built-ins live in `framework/plugins/`. Placement types are `PickAll`, `PickN`, `PickFixed`.
- **Controllers** embed `client.Client`, follow fetch → check deletion → defaults → reconcile → requeue, update status through the status subresource, and record events. Wrap failures with a constructor from `pkg/utils/controller` (`NewAPIServerError`, `NewUserError`, `NewExpectedBehaviorError`, `NewUnexpectedBehaviorError`) so retry behaviour is right.
- **Interfaces**: resources implement `Conditioned` and `ConditionedObj` from `apis/interface.go`.
- **Property providers** (`pkg/propertyprovider/`) feed cluster properties to the scheduler; a new provider implements `PropertyProvider` from `interface.go` there, next to the `azure` and `default` ones.

## Test conventions the linter cannot check

- No assertion libraries. Compare with `cmp.Equal` / `cmp.Diff` from `google/go-cmp`, whole structs in one shot, never field by field.
- Name the expected value `want` or `wanted`, never `expect`. Print the actual value first: `t.Errorf("Reconcile() = %v, want %v", got, want)`, or `t.Errorf("Reconcile() mismatch (-want +got):\n%s", diff)`.
- Table-driven tests in `<file>_test.go`. Mock external dependencies with `gomock`.
- Integration tests are Ginkgo/Gomega. `test/scheduler` and `test/apis` run under `make integration-test`; `<file>_integration_test.go` next to the code runs under `make local-unit-test`. Add a case as a new `Context` and reuse the suite's setup. E2E lives in `test/e2e` and runs against kind.
- New behaviour ships with tests in the same PR. A bug fix ships with a test that fails before the fix.

## Style

- Follow the [Uber Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md). Prefer the standard library over third-party packages.
- Comments that are complete sentences are capitalized and punctuated; documentation comments always are.
- Files end with a newline. `gofmt` and `goimports` run under `make reviewable`, so never hand-format.

## Gotchas

- **Version skew is a contract.** The hub agent and member agent may differ by one minor version in either direction. Everything the two agents exchange (`Work`, `AppliedWork`, `InternalMemberCluster` status and heartbeats) must stay compatible across that window. Read [VERSIONING.md](VERSIONING.md) before touching those types; `upgrade.yml` tests both directions in CI.
- **Backports are label-driven.** Merge to `main` first, then, once a `release-0.Y` branch exists, add `cherry-pick/0.Y`; `backport.yml` cherry-picks the squash commit. Never open a PR directly against `release-0.Y`.
- **CVE remediation is manual.** Dependabot bumps Go modules, Actions, and the `docker/` base-image digests on its weekly schedule; `trivy.yml` scans daily and keeps one issue per ISO week. Fix a Go CVE with `go get <module>@<fixed>` and `go mod tidy`; fix an OS CVE by bumping the base image in every affected `docker/*.Dockerfile`, which is usually all three.
- **Squad** (`.squad/`, `.github/skills/`, `squad-*` workflows, `.mcp.json`) is a separate Copilot-based team framework installed here; `squad upgrade` rewrites its files. Do not edit them to change project conventions. Change this file. Squad's bundled templates add a `Co-authored-by: Copilot` trailer and open PRs with `gh pr create`; the rules above override them: strip the trailer and let the human open the PR.

## Where to look

- [CONTRIBUTING.md](CONTRIBUTING.md): DCO, PR titles, release-note labels, backports.
- `.agents/skills/`: step-by-step procedures (API change, backport, CVE remediation, e2e failure analysis, release, review) that assistants load on demand and people can read.
- [VERSIONING.md](VERSIONING.md), [SECURITY.md](SECURITY.md), [ROADMAP.md](ROADMAP.md).
- User documentation: <https://kubefleet.dev/docs/> (source in `kubefleet-dev/website`). Runnable manifests in `examples/`.
- `.github/.copilot/`: the maintainers' domain knowledge and breadcrumbs (per-task design notes). The team tracks its non-trivial work there under the protocol in [`.github/copilot-instructions.md`](.github/copilot-instructions.md), whatever tool it uses; external contributors are not expected to write one, and "plan approval" for them is agreement on the issue.
