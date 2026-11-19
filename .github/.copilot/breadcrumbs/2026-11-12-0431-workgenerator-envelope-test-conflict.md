# Work generator envelope deletion test conflict

## User-specified plan

Reproduce the unchanged focused integration test repeatedly before editing, then
retry only the conflicting binding update with a fresh fetch and mutation on
every attempt. Preserve the success assertion and all envelope deletion checks.
Validate with 100 repetitions, package lint, formatting, and vet.
The user subsequently requested publishing the validated fix as a PR.

## Baseline evidence

- Isolated branch: `fix/workgenerator-envelope-test-conflict`, based on
  `4e28515c454601122ff1147bb401f17f2774bba0`.
- Go 1.26.7; existing envtest Kubernetes 1.33.0 assets.
- Command: `go run github.com/onsi/ginkgo/v2/ginkgo --no-color --focus="Should delete the enveloped work object in the target namespace after it's removed from snapshot$" --repeat=99 ./pkg/controllers/workgenerator`.
- The unchanged suite stopped on attempt 32: 31 successful iterations, then
  1 passing spec and 1 failing spec (63 passing specs, 1 failing spec total).
- Failure at `controller_integration_test.go:957`, immediately after the second
  snapshot was created: `Operation cannot be fulfilled on clusterresourcebindings.placement.kubernetes-fleet.io "binding-crph6czewg3vk-1-snapshot": the object has been modified; please apply your changes to the latest version and try again`.
- Full evidence: session `d026990d-f954-48a8-9ae3-4170dca4593b`,
  `files/envelope-baseline.log`.

## Implementation and validation

Use the existing `retry.DefaultBackoff` convention; return fetch/update errors
unchanged so non-conflict failures still fail the test.

- Only the reproduced cluster-scoped envelope test update is changed, plus the
  retry import. Success, synchronization, and deletion assertions are intact.
- The same command completed all 100 post-fix repetitions: 200 passing specs,
  zero failures. Both selected envelope tests use `ClusterResourceBinding`.
- `gofmt -l` and `git diff --check` are clean; package `go vet` passes.
- Pinned `golangci-lint-v2.13.2 run --new-from-rev=HEAD
  ./pkg/controllers/workgenerator/...` passes with zero issues. Full package lint
  reports 62 pre-existing issues (50 goconst, 3 prealloc, 9 staticcheck); unrelated
  code was not changed.
- Session evidence: `files/envelope-post-fix.log`, `files/envelope-checks.log`,
  and `files/envelope-evidence-summary.log`. An interrupted draft run at a similar
  earlier test block is retained separately as
  `files/envelope-post-fix-aborted-wrong-site.log` and excluded from final counts.
- Validation is focused, not the full repository suite.
- Publish as a standalone test-only PR against main, without the unrelated
  Go-fix modernization commit from #1027.
- After rebasing onto main, both focused specs passed again and `make reviewable`
  passed. Unrelated generated import-alias normalization was excluded.
