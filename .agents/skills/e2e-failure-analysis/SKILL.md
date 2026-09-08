---
name: e2e-failure-analysis
description: Find the root cause of a failing or flaky KubeFleet e2e job from a GitHub Actions run, a PR number, or downloaded logs. Use when a CI e2e job fails, a test is suspected flaky, or someone asks why a placement, rollout, or Work never reached the expected state. Also covers inspecting a live hub and member cluster.
license: Apache-2.0
metadata:
  author: The KubeFleet Authors
---

# E2E failure analysis

Work backwards from the assertion that failed to the log line that explains it. Never stop at the first error message; for each finding ask "what caused this?" until the answer is backed by a timestamped log line. Read [`AGENTS.md`](../../../AGENTS.md) first for the pipeline the objects flow through.

## 1. Find the failing job

`ci.yml` runs four e2e jobs in a matrix named by `customized-settings`: `default`, `resourceplacement`, `joinleave`, `custom`. From a PR:

```sh
gh pr checks <pr> --repo kubefleet-dev/kubefleet          # failing jobs with their URLs
gh run list --repo kubefleet-dev/kubefleet --commit <sha> --json databaseId,name,conclusion,url
```

## 2. Collect the evidence

```sh
gh api --allow-escape-sequences repos/kubefleet-dev/kubefleet/actions/jobs/<job_id>/logs \
  | sed 's/\x1b\[[0-9;]*m//g' > job.log     # the Ginkgo output; gh refuses the ANSI colours without the flag
gh run download <run_id> --repo kubefleet-dev/kubefleet -n e2e-logs-<setting> -D e2e-logs-<setting>   # agent logs from every cluster
```

The artifact is what `make collect-e2e-logs` (`test/e2e/collect-logs.sh`) produced. Without `-D` a single artifact extracts into the current directory; with it the layout is:

```text
e2e-logs-<setting>/<timestamp>/
  hub/nodes/<node>/hub-agent-*.log          hub agent; the failure window is often in a rotated file named hub-agent-*.log.<timestamp>.log, so grep all of them
  cluster-1/nodes/<node>/member-agent-*.log  one directory per member (cluster-1, cluster-2, cluster-3)
  hub-debug-info.txt, cluster-N-debug-info.txt   pod status and events in fleet-system
  summary.txt
```

Locally, the same layout appears under `test/e2e/logs/<timestamp>/` after `make collect-e2e-logs`.

## 3. Find the failure in the Ginkgo log

```sh
grep -n '\[FAILED\]\|Summarizing' job.log
```

For the failing spec, extract the assertion (file:line and the timeout), the `STEP:` timeline with timestamps, and the timeline of the previous spec that shared resources (same placement, namespace, or member cluster). Flakes are usually the previous spec's cleanup racing the current spec's setup. A readiness wait that passes in milliseconds instead of seconds means it read stale state. A `Consistently` that fails well under a second right after a member-cluster change usually means an unrelated scheduling cycle ran; grep the hub log for `Enqueueing placement for scheduler processing` and `Scheduling cycle starts` in that window to find the trigger.

## 4. Trace backwards through the agents

The object that never reached the expected state has one owning controller. Grep that controller's log for the object name and count the hits: one hit and no retry means the controller gave up, so check its requeue logic in `pkg/controllers/<name>` before claiming it "should self-heal".

| Symptom | Owning controller | Log |
| --- | --- | --- |
| Placement never scheduled, `Scheduled` false | scheduler (`pkg/scheduler`) | `hub-agent-*.log`, search the placement name and `scheduling cycle` |
| Binding stuck, rollout never advanced | rollout (`pkg/controllers/rollout`) | hub agent |
| No `Work` in the member's namespace on the hub | work generator (`pkg/controllers/workgenerator`) | hub agent |
| `Work` exists but `AppliedWork` or `Applied`/`Available` condition missing | work applier (`pkg/controllers/workapplier`) | `member-agent-*.log` on that member |
| Member shows unhealthy or heartbeats stop | `internalmembercluster` controller | member agent, then hub `membercluster` controller |

Build a timeline of writes to the state the controller read, with exact timestamps, and compare it with the test's `STEP:` timeline to find the window where the state was stale or wrong. Member agents refresh properties, resource usage, and the namespace list every heartbeat period, and any change re-enqueues every placement; the `Calling property provider` line in the member-agent log gives the exact times.

## 5. Inspect a live cluster

With a kubeconfig for the hub (`kind export kubeconfig --name hub` locally):

```sh
kubectl get clusterresourceplacement <name> -o yaml | yq .status         # conditions and per-cluster status
kubectl get clusterresourcesnapshot,clusterschedulingpolicysnapshot \
  -l kubernetes-fleet.io/parent-CRP=<name>,kubernetes-fleet.io/is-latest-snapshot=true
kubectl get clusterresourcebinding -l kubernetes-fleet.io/parent-CRP=<name> -o wide
kubectl get work -A -l kubernetes-fleet.io/parent-CRP=<name>              # one per target cluster, in that member's hub namespace
kubectl get membercluster -o wide                                          # join state, heartbeat, capacity
```

On a member (`kind export kubeconfig --name cluster-1`): `kubectl get appliedwork` and `kubectl -n fleet-system logs deploy/member-agent`. Namespace-scoped placements use `resourceplacement`, `resourcebinding`, and the same labels.

## 6. Report

An evidence chain, from the error backwards: the assertion (file:line), what it means, the owning controller's decision with log lines and timestamps, the stale or wrong state it read, the root cause, and why it did not self-heal (checked against the code). Quote log lines verbatim. Then say whether it is a product bug, a test race, or an infrastructure failure, and what the fix is.

## 7. Improve this skill

If you found a failure pattern, log location, or command this file lacks, propose an addition. Keep it generic (the pattern, not this test's names and timestamps), show it to the user, and let them decide whether to commit it.
