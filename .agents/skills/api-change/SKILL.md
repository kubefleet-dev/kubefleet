---
name: api-change
description: Add, change, or deprecate a field on a KubeFleet CRD under apis/ (ClusterResourcePlacement, ResourcePlacement, MemberCluster, overrides, staged update run, and the rest) and regenerate everything that depends on it. Use for any edit to a *_types.go file, a kubebuilder marker, or a CRD's validation.
license: Apache-2.0
metadata:
  author: The KubeFleet Authors
---

# API change

An API edit touches the type, its DeepCopy, the CRD YAML, the chart symlink, and the tests here, plus the website docs and downstream consumers outside. Missing any one fails CI or, worse, ships a hub that older member agents cannot talk to. Read [`AGENTS.md`](../../../AGENTS.md) and the version-skew section of [`VERSIONING.md`](../../../VERSIONING.md) first.

## 1. Decide the version and the bump

- API groups: `placement.kubernetes-fleet.io` (`apis/placement/v1`, `v1beta1`, `v1alpha1`), `cluster.kubernetes-fleet.io` (`apis/cluster/v1`, `v1beta1`), and the newer `placement.kubefleet.dev` (`apis/kubefleet.dev/placement/v1alpha1`; part of FEP-0001, the in-progress enhancement proposal tracked from issue #834). There is no conversion webhook: a field that exists in one served version but not the storage version is silently dropped on write. Check which versions the type has and which is `storage: true` in the generated CRD before editing one.
- Per `VERSIONING.md`: a new CRD, a new field, or a new enum value is a **minor** bump; a removed or renamed field, tightened validation, or changed default is a **breaking** minor change and needs `release-note/breaking`. Say which in the PR.
- If the type is exchanged between hub and member (`Work`, `AppliedWork`, `InternalMemberCluster`), the change must be readable by an agent one minor older or newer. New fields must be optional with a safe zero value; never repurpose an existing field.

## 2. Edit the type

- `ClusterResourcePlacement` and `ResourcePlacement` share `PlacementSpec` and `PlacementStatus` in `apis/placement/<version>/clusterresourceplacement_types.go`, so one field added there lands in both CRDs; the same holds for the other cluster/namespace pairs. Check which types embed the struct you edit.
- Doc comment on every field, complete sentences. Add the kubebuilder markers the neighbours use: `+optional`, `+kubebuilder:default`, `+kubebuilder:validation:Enum`, `+kubebuilder:validation:XValidation` for CEL rules.
- Immutable fields carry an `XValidation` rule with `self == oldSelf`; look at an existing one in the same file and copy its message style.
- Every new `.go` file starts with the header in `hack/boilerplate.go.txt`.

## 3. Regenerate

```sh
make generate      # zz_generated.deepcopy.go
make manifests     # config/crd/bases/*.yaml
make crd-verify    # the chart CRD directories must cover every base CRD
```

A new CRD needs a symlink in `charts/hub-agent/templates/crds/` or `charts/member-agent/templates/crds/` pointing at `../../../../config/crd/bases/<file>`; existing entries are symlinks, so follow the pattern. Exception: CRDs in the `placement.kubefleet.dev` group are deliberately excluded from `crd-verify` and not yet shipped in the charts while FEP-0001 is in progress; do not add symlinks for them unless the issue you are working says to. Commit the regenerated files with the type change. Never hand-edit them.

## 4. Test

- Validation: `test/apis/placement/v1beta1` and `test/apis/cluster/{v1,v1beta1}` run against envtest and exercise CEL rules and defaults (placement has no v1 suite; add cases against the storage version). Add a case for every new rule, including the rejection path.
- Behaviour: the controller that reads the field gets a table-driven unit test; scheduler-visible fields get a case in `test/scheduler`. Follow the conventions in `AGENTS.md` (`cmp.Diff`, `want`, new Ginkgo `Context`).
- Run `make reviewable && make local-unit-test && make integration-test`.
- If the field changes what members see, check the `upgrade.yml` jobs on the PR (`hub-agent-backward-compatibility`, `member-agent-backward-compatibility`, and `full-backward-compatibility`); they run automatically for any PR that touches non-documentation files, and there is no local shortcut.

## 5. Prepare to ship

Stop here; the author opens the PR (`AGENTS.md`, Workflow). What they need:

- PR title `feat:` (new field, label `release-note/feature`) or `interface:` (contract change, label `release-note/chore` per `CONTRIBUTING.md`); add `release-note/breaking` when step 1 said so.
- PR body: the field, its default, its validation, the version-skew reasoning from step 1, and example YAML.
- User docs live in the `kubefleet-dev/website` repository (API reference and concept pages); prepare the docs change for the author to open alongside and link.

## Traps

- `make manifests` only regenerates `config/crd/bases`; the charts pick the change up through symlinks, so a copied (not linked) CRD file silently goes stale.
- Adding a field to `v1beta1` without `v1` (or the reverse) compiles fine and the value is silently dropped whenever the object is stored in the other version.
- `+kubebuilder:default` on a field that older agents send empty changes behaviour on upgrade; call it out.
