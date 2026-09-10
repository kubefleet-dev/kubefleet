# KubeFleet Copilot Instructions

## Build, Test, and Lint Commands

```bash
make build                # Build all binaries
make run-hubagent         # Run hub agent binary directly
make run-memberagent      # Run member agent binary directly
make reviewable           # Run all quality checks (fmt, vet, lint, staticcheck, tidy) — required before PRs
make lint                 # Fast linting
make lint-full            # Thorough linting (--fast=false)
make staticcheck          # Static analysis
make fmt                  # Format code
make vet                  # Run go vet
make test                 # Unit + integration tests
make local-unit-test      # Unit tests only
make integration-test     # Integration tests only (Ginkgo, uses envtest)
make manifests            # Regenerate CRDs from API types
make generate             # Regenerate deep copy methods
```

### Running a single test

```bash
# Single package
go test -v -race -timeout=30m ./pkg/controllers/rollout/...

# Single test by name
go test -v -race -run TestReconcile ./pkg/controllers/rollout/...

# Single Ginkgo integration test by description
cd test/scheduler && ginkgo -v --focus="should schedule"
```

### E2E tests

```bash
make setup-clusters                         # Create 3 Kind clusters
make setup-clusters MEMBER_CLUSTER_COUNT=5  # Custom cluster count
make e2e-tests                              # Run E2E suite (ginkgo, ~70min timeout)
make e2e-tests-custom                       # Run E2E tests with custom labels
make collect-e2e-logs                       # Collect logs after E2E tests
make clean-e2e-tests                        # Tear down clusters
```

### Docker and Images

```bash
make push                       # Build and push all images
make docker-build-hub-agent     # Build hub agent image
make docker-build-member-agent  # Build member agent image
make docker-build-refresh-token # Build refresh token image
```

## Architecture

KubeFleet is a CNCF sandbox project providing multi-cluster application management for Kubernetes. It uses a hub-and-spoke model. The **hub agent** runs on a central cluster; **member agents** run on each managed cluster.

### Core API Types

- **ClusterResourcePlacement (CRP)**: Main API for placing cluster-scoped resources across clusters with scheduling policies. If one namespace is selected, everything in that namespace is placed across clusters.
- **ResourcePlacement (RP)**: Main API for placing namespaced resources across clusters with scheduling policies.
- **MemberCluster**: Represents a member cluster with identity and heartbeat settings
- **ClusterResourceBinding**: Represents scheduling decisions binding cluster-scoped resources to clusters
- **ResourceBinding**: Represents scheduling decisions binding resources to clusters
- **ClusterResourceSnapshot**: Immutable snapshot of cluster resource state for rollback and history
- **ResourceSnapshot**: Immutable snapshot of namespaced resource state for rollback and history
- **Work**: Contains manifests to be applied on member clusters
- **ClusterSchedulingPolicySnapshot**: Immutable snapshots of scheduling policies

### Reconciliation Pipeline

User-created placement flows through a chain of controllers:

```
ClusterResourcePlacement / ResourcePlacement  (user intent)
        ↓
  Placement Controller → creates ResourceSnapshot + SchedulingPolicySnapshot (immutable)
        ↓
  Scheduler → creates ClusterResourceBinding / ResourceBinding (placement decisions)
        ↓
  Rollout Controller → manages staged rollout of bindings
        ↓
  Work Generator → creates Work objects (per-cluster manifests)
        ↓
  Work Applier (member agent) → applies manifests, creates AppliedWork
        ↓
  Status flows back: AppliedWork → Work status → Binding status → Placement status
```

### Key Controllers

- **ClusterResourcePlacement Controller** (`pkg/controllers/clusterresourceplacement/`): Manages CRP lifecycle
- **Scheduler** (`pkg/scheduler/`): Makes placement decisions using pluggable framework
- **Rollout Controller** (`pkg/controllers/rollout/`): Manages rollout of changes to all clusters with a placement decision
- **WorkGenerator** (`pkg/controllers/workgenerator/`): Generates Work objects from bindings
- **WorkApplier** (`pkg/controllers/workapplier/`): Applies Work manifests on member clusters
- **ClusterResourceBinding Watcher** (`pkg/controllers/clusterresourcebindingwatcher/`): Watches binding changes
- **ClusterResourcePlacement Watcher** (`pkg/controllers/clusterresourceplacementwatcher/`): Watches placement changes

### API Naming Convention

CRDs starting with `Cluster` are cluster-scoped; the name without the `Cluster` prefix is the namespace-scoped counterpart. For example: `ClusterResourcePlacement` (cluster-scoped) vs `ResourcePlacement` (namespace-scoped). This affects CRUD operations — namespace-scoped resources require a `Namespace` field in `types.NamespacedName`.

### Scheduler Framework

Pluggable architecture modeled after the Kubernetes scheduler:
- Plugin interfaces: `PreFilterPlugin`, `FilterPlugin`, `PreScorePlugin`, `ScorePlugin`, `PostBatchPlugin`
- Built-in plugins: `clusteraffinity`, `tainttoleration`, `clustereligibility`, `sameplacementaffinity`
- Placement strategies: **PickAll** (all matching), **PickN** (top N scored), **PickFixed** (named clusters)
- Plugins share state via `CycleStatePluginReadWriter`
- **Property-based scheduling**: Uses cluster properties (CPU, memory, cost) for decisions

### Snapshot-Based Versioning

All policy and resource changes create immutable snapshot CRDs (`ResourceSnapshot`, `SchedulingPolicySnapshot`, `OverrideSnapshot`). This enables rollback, change tracking, and consistent scheduling decisions.

## Directory Structure

```
apis/                     # API definitions and CRDs
├── cluster/v1beta1/      # MemberCluster APIs
├── placement/v1beta1/    # Placement and work APIs
pkg/controllers/          # All controllers organized by resource type
pkg/scheduler/            # Scheduler framework and plugins
pkg/propertyprovider/     # Cloud-specific property providers (Azure)
pkg/utils/                # Shared utilities and helpers
cmd/hubagent/             # Hub agent main and setup
cmd/memberagent/          # Member agent main and setup
test/e2e/                 # E2E tests (Ginkgo/Gomega against Kind clusters)
test/integration/         # Integration tests with envtest
test/scheduler/           # Scheduler integration tests
```

## Terminology

- **Fleet**: A collection of clusters managed together
- **Hub Cluster**: Central control plane cluster
- **Member Cluster**: A managed cluster in the fleet
- **Hub Agent**: Controllers on the hub for scheduling and placement
- **Member Agent**: Controllers on member clusters for applying workloads and reporting status

## Code Conventions

- Follow the [Uber Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md)
- Favor standard library over third-party libraries
- PR titles must use a prefix: `feat:`, `fix:`, `docs:`, `test:`, `chore:`, `ci:`, `perf:`, `refactor:`, `revert:`, `style:`, `interface:`, `util:`, or `[WIP] `
- Always add an empty line at the end of new files
- Run `make reviewable` before submitting PRs

### Controller Pattern

All controllers embed `client.Client`, use a standard `Reconcile` loop (fetch → check deletion → apply defaults → business logic → requeue), update status via the status subresource, and record events. Error handling uses categorized errors (API Server, User, Expected, Unexpected) for retry semantics. See existing controllers in `pkg/controllers/` for reference.

### API Interface Pattern

Resources implement `Conditioned` (for status conditions) and `ConditionedObj` (combining `client.Object` + `Conditioned`). See `apis/interface.go`.

### Watcher Pattern

Resource placement watchers monitor CRP and binding changes. The event-driven architecture uses separate watchers for different resource types to enable focused reconciliation.

### Multi-API Version Support

- v1beta1 APIs are the current stable version
- Feature flags control API version enablement

## Testing Conventions

- **Unit tests**: `<file>_test.go` in the same directory; table-driven style
- **Integration tests**: `<file>_integration_test.go`; use Ginkgo/Gomega with `envtest`
- **E2E tests**: `test/e2e/`; Ginkgo/Gomega against Kind clusters
- Do **not** use assert libraries; use `cmp.Diff` / `cmp.Equal` from `google/go-cmp` for comparisons
- Use `want` / `wanted` (not `expect` / `expected`) for desired state variables
- Test output format: `"FuncName(%v) = %v, want %v"`
- Compare structs in one shot with `cmp.Diff`, not field-by-field
- Mock external dependencies with `gomock`
- When adding Ginkgo tests, add to a new `Context`; reuse existing setup

### Test Coding Style

- Comments that are complete sentences should be capitalized and punctuated like standard English sentences.
- Comments that are sentence fragments have no such requirements for punctuation or capitalization.
- Documentation comments should always be complete sentences, and as such should always be capitalized and punctuated. Simple end-of-line comments (especially for struct fields) can be simple phrases that assume the field name is the subject.
- If a function returns a struct, construct the full expected struct and compare in one shot with `cmp.Diff`, not field-by-field. The same rule applies to arrays and maps.
- If multiple return values need comparison, compare them individually and print each; no need to wrap in a struct.

## Collaboration Protocol

### Domain Knowledge

Refer to `.github/.copilot/domain_knowledge/` for entity relationships, workflows, and ubiquitous language. Update these files as understanding grows.

### Specifications

Use `.github/.copilot/specifications/` for feature specs. Ask which specifications apply if unclear.

### Breadcrumb Protocol

For non-trivial tasks, create a breadcrumb file at `.github/.copilot/breadcrumbs/yyyy-mm-dd-HHMM-{title}.md` to track decisions and progress. Update it before and after code changes, and get plan approval before implementation. See existing breadcrumbs for format examples.
