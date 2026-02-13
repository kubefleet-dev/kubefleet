# ClusterResourceSnapshot Creation Timing and Envelope Processing

This document explains why envelope processing might trigger additional ClusterResourceSnapshots and why snapshot index is not always "0" even with a single envelope.

## Overview

ClusterResourceSnapshots (and ResourceSnapshots) are versioned, immutable records of resources selected by a ClusterResourcePlacement (CRP). The snapshot index increments whenever the selected resources change. This document explains the timing mechanisms and conditions that lead to multiple snapshots.

## Snapshot Creation Triggers

The PlacementController creates a new snapshot when:

1. **No snapshot exists** (initial creation → index 0)
2. **Resource content hash changes** - selected resources differ from latest snapshot
3. **Sub-indexed snapshots are incomplete** - master snapshot exists but related sub-snapshots are missing
4. **Timing requirements are met** - both configured delays have elapsed

## Timing Mechanisms

Two environment variables control when new snapshots can be created:

### 1. `RESOURCE_SNAPSHOT_CREATION_MINIMUM_INTERVAL`
- **Purpose**: Minimum time between consecutive snapshot creations
- **Default**: 30 seconds (in E2E tests: 0m for faster testing)
- **Location**: Set in hub-agent deployment

### 2. `RESOURCE_CHANGES_COLLECTION_DURATION`
- **Purpose**: Time to wait and bundle multiple resource changes into one snapshot
- **Default**: 15 seconds (in E2E tests: 0m for faster testing)
- **Location**: Set in hub-agent deployment

### Combined Logic

From `pkg/controllers/placement/controller.go:621`:

```go
nextCreationTime := fleettime.MaxTime(
    nextResourceSnapshotCandidateDetectionTime.Add(r.ResourceChangesCollectionDuration),
    latestResourceSnapshot.GetCreationTimestamp().Add(r.ResourceSnapshotCreationMinimumInterval)
)
```

**Result**: A new snapshot can only be created after **both**:
- ≥ 30 seconds since the last snapshot was created, **AND**
- ≥ 15 seconds since resource changes were first detected

The controller takes whichever time is later.

## Why Envelopes Trigger Additional Snapshots

### The ResourceEnvelope Flow

1. **Initial CRP Creation (t=0s)**
   - User creates CRP with selector matching a namespace
   - ResourceEnvelope exists in that namespace
   - PlacementController detects: Namespace + ResourceEnvelope
   - **Snapshot 0 created** with envelope count annotation

2. **ChangeDetector Notices Changes (t=0-30s)**
   - ChangeDetector watches ALL resources via dynamic informers
   - Updates every 30 seconds to discover new resource types
   - When envelope is processed/modified, triggers ResourceChangeController
   - ResourceChangeController enqueues CRP for reconciliation

3. **Resource Hash Changes (variable timing)**
   Several scenarios can cause hash changes:
   
   a. **Envelope content extraction**:
      - WorkGenerator extracts wrapped manifests from envelope
      - Internal representation changes
      - Resource hash differs from initial snapshot
   
   b. **Envelope metadata updates**:
      - Annotations added by controllers
      - Labels modified during processing
      - Status updates
   
   c. **Concurrent resource changes**:
      - User modifies envelope content
      - Other resources in namespace change
      - Namespace annotations/labels updated

4. **Delayed Snapshot Creation (t=30-45s)**
   - PlacementController detects hash mismatch
   - Checks timing: must wait until both delays elapse
   - If t < 30s: requeues with delay
   - If t ≥ 30s: **Snapshot 1 created**

### Example Timeline (Production with 30s delays)

```
t=0s    : CRP created, selects Namespace + ResourceEnvelope
t=0.1s  : Snapshot 0 created (hash: abc123)
t=0.5s  : WorkGenerator processes envelope, extracts manifests
t=1s    : Envelope metadata updated by member cluster status
t=2s    : ChangeDetector detects envelope change, enqueues CRP
t=2.1s  : PlacementController reconciles, calculates new hash (hash: def456)
t=2.1s  : Hash mismatch detected (abc123 ≠ def456)
t=2.1s  : Check timing: last snapshot at t=0.1s, need to wait until t=30.1s
t=2.1s  : Requeue with delay of 28 seconds
t=30.1s : Reconcile again, timing requirement met
t=30.2s : Snapshot 1 created (hash: def456)
```

### Example Timeline (E2E Tests with 0s delays)

```
t=0s     : CRP created, selects Namespace + ResourceEnvelope
t=0.05s  : PlacementController reconciles, creates snapshot 0 (hash: abc123)
t=0.1s   : CRP status updated: ObservedResourceIndex="0"
t=0.15s  : Test's Eventually() starts checking CRP status
t=0.2s   : Envelope processing happens (varies by timing)
t=0.25s  : Hash changes to def456
t=0.3s   : PlacementController reconciles again, creates snapshot 1
t=0.35s  : CRP status updated: ObservedResourceIndex="1"
```

**The Race Condition**: Where does the test's `Eventually()` check occur relative to the status updates?

**Scenario A - Test sees index 0 (passes if expecting 0, fails if expecting 1)**:
```
t=0.1s  : CRP status = ObservedResourceIndex="0" with complete conditions
t=0.15s : Eventually() checks → matches expected status → SUCCESS
t=0.35s : CRP status changes to ObservedResourceIndex="1" (but test already passed)
```

**Scenario B - Test only sees index 1 (fails if expecting 0, passes if expecting 1)**:
```
t=0.05s : Snapshot 0 created but status not yet updated
t=0.08s : Envelope processing completes quickly
t=0.1s  : Snapshot 1 created  
t=0.12s : Eventually() starts checking
t=0.15s : CRP status = ObservedResourceIndex="1" with complete conditions
t=0.15s-10s : Eventually() keeps checking, always sees "1", expects "0" → TIMEOUT/FAIL
```

## Why E2E Tests Set Delays to 0

In `test/e2e/setup.sh`:

```bash
export RESOURCE_SNAPSHOT_CREATION_MINIMUM_INTERVAL="${RESOURCE_SNAPSHOT_CREATION_MINIMUM_INTERVAL:-0m}"
export RESOURCE_CHANGES_COLLECTION_DURATION="${RESOURCE_CHANGES_COLLECTION_DURATION:-0m}"
```

**Reason**: Tests need fast feedback and don't want to wait 30-45 seconds for snapshots. However, this makes timing **non-deterministic**:

- With delays=0: Snapshots created immediately when hash changes
- Race condition: Test validation might catch status in "snapshot 0" state OR "snapshot 1" state
- Solution: Use `placementStatusCmpOptionsOnCreate` to ignore `ObservedResourceIndex`

### Why the Race Isn't Always Index 1

Even though envelope processing ALWAYS happens, the test doesn't always see index 1 because:

1. **Timing Variability**: Multiple asynchronous operations with variable latencies:
   - PlacementController reconciliation loop timing
   - CRP status update to API server
   - Test's `Eventually()` check timing  
   - Envelope processing speed (depends on content size, CPU load)
   - Controller work queue depth and processing rate
   - Kubernetes API server response times
   - Informer cache synchronization delays

2. **The Critical Window**: There's a brief period where:
   - Snapshot 0 exists and CRP status shows ObservedResourceIndex="0"
   - Envelope processing hasn't completed yet
   - If the test's `Eventually()` checks during this window, it sees index "0"
   - If the test checks after this window, it only sees index "1"

3. **Eventually() Behavior**: 
   - Checks every 250ms (eventuallyInterval) for up to 10s (eventuallyDuration)
   - First successful match stops the checking
   - If it catches index "0" state early, it succeeds and stops checking
   - If index "1" is created before the first successful match, it never sees index "0"

### Timing Variability Factors

What makes the critical window variable:

| Factor | Impact on Window | Example |
|--------|------------------|---------|
| CPU load | High load → slower processing → longer window | CI with parallel jobs |
| Envelope size | Larger content → slower extraction → longer window | Simple ConfigMap vs large Secret |
| Controller queue depth | More items → delayed reconciliation → longer window | Multiple CRPs created simultaneously |
| API server latency | Higher latency → slower status updates → longer window | Network issues, etcd performance |
| Cache warmth | Cold cache → slower reads → longer window | First test run vs subsequent runs |

**Example**: On a busy CI runner with high CPU load, envelope processing might take 200ms instead of 50ms, giving the test's Eventually() more chances to catch the "snapshot 0" state.

## Resource Hash Calculation

From `pkg/controllers/placement/controller.go:481`:

```go
resourceHash, err := resource.HashOf(resourceSnapshotSpec)
```

The hash includes:
- All selected resources' content
- Resource metadata (except runtime fields)
- Resource ordering

**Critical**: Even minor changes (annotations, labels, status) trigger new hash → new snapshot.

## ChangeDetector Behavior

From `pkg/resourcewatcher/change_dector.go`:

1. **Discovery Cycle**: Every 30 seconds, rediscovers API resources
2. **Dynamic Informers**: Creates informers for all discovered resource types
3. **Event Handling**: Watches Create/Update/Delete events
4. **Resource Filtering**: Skips Fleet's own CRDs and system namespaces
5. **Enqueue Logic**: Enqueues affected CRPs/RPs to ResourceChangeController

**Important**: ResourceEnvelopes are regular Kubernetes objects, so they're watched like any other resource. Updates to envelopes trigger the same change detection flow as ConfigMaps, Deployments, etc.

## Best Practices for Tests

### ❌ Don't Do This
```go
wantStatus := placementv1beta1.PlacementStatus{
    ObservedResourceIndex: "0",  // Hardcoded - will break with timing variance
    PerClusterPlacementStatuses: []placementv1beta1.PerClusterPlacementStatus{
        {ObservedResourceIndex: "0", ...},  // Also hardcoded
    },
}
if diff := cmp.Diff(crp.Status, wantStatus, placementStatusCmpOptions...); diff != "" {
    return fmt.Errorf("diff: %s", diff)
}
```

### ✅ Do This Instead
```go
wantStatus := placementv1beta1.PlacementStatus{
    // No ObservedResourceIndex field - let it vary
    PerClusterPlacementStatuses: []placementv1beta1.PerClusterPlacementStatus{
        {ClusterName: "cluster-1", ...},  // No ObservedResourceIndex
    },
}
// Use placementStatusCmpOptionsOnCreate which ignores ObservedResourceIndex
if diff := cmp.Diff(crp.Status, wantStatus, placementStatusCmpOptionsOnCreate...); diff != "" {
    return fmt.Errorf("diff: %s", diff)
}
```

**Rationale**: Tests should validate behavior (conditions, failed placements, applied resources), not timing artifacts (snapshot index).

## Production Behavior

In production (delays enabled):

✅ **Benefits**:
- Reduced snapshot churn (batches changes over 30-45s window)
- Lower etcd write load
- More stable rollout behavior

⚠️ **Trade-offs**:
- 30-45 second delay before new changes propagate
- More complex debugging (need to wait for delays)
- Requires understanding of timing behavior

## Debugging Tips

### Check Current Delays

```bash
# On hub cluster
kubectl get deployment -n fleet-system fleet-hub-agent -o yaml | grep -A 2 "RESOURCE_"
```

### List Snapshots for a CRP

```bash
kubectl get clusterresourcesnapshots -l placement.kubernetes-fleet.io/placement-tracking-label=my-crp
```

### Check Snapshot Hash

```bash
kubectl get clusterresourcesnapshot my-crp-0 -o jsonpath='{.metadata.annotations.resource\.kubernetes-fleet\.io/resource-group-hash}'
```

### Monitor Snapshot Creation

```bash
# Watch for new snapshots
kubectl get clusterresourcesnapshots -w

# Check hub-agent logs
kubectl logs -n fleet-system deployment/fleet-hub-agent | grep "resourceSnapshot"
```

## Related Code Locations

- **Snapshot creation logic**: `pkg/controllers/placement/controller.go:474-631`
- **Timing check**: `pkg/controllers/placement/controller.go:594-631`
- **ChangeDetector**: `pkg/resourcewatcher/change_dector.go`
- **ResourceChangeController**: `pkg/controllers/resourcechange/resourcechange_controller.go`
- **Test timing setup**: `test/e2e/setup_test.go:326-344`
- **E2E comparison options**: `test/e2e/setup_test.go:233-259`

## Summary

**Q: Why envelope processing might trigger additional snapshots?**

**A**: Envelopes are regular Kubernetes objects watched by ChangeDetector. When envelopes are processed (manifests extracted, metadata updated, status changed), the resource content hash changes, triggering a new snapshot after timing delays elapse.

**Q: Why is it not always snapshot 0 if there is only 1 envelope?**

**A**: Even with one envelope, multiple snapshots can be created because:
1. Initial selection creates snapshot 0
2. Envelope processing/updates change the resource hash
3. After 30-45 seconds (or immediately in tests with delays=0), snapshot 1 is created
4. This can repeat for each envelope modification

**Q: If envelope processing always happens, why isn't the test always failing with index 1?**

**A**: The race condition exists because of timing variability. The test's `Eventually()` checks every 250ms:

- **Sometimes** it catches the brief "snapshot 0" state before envelope processing completes → sees index "0"
- **Sometimes** envelope processing completes before the first successful check → only sees index "1"

The critical window (when CRP status shows index "0") duration varies based on:
- CPU load and scheduling
- Controller work queue processing rate
- Envelope content size and processing complexity
- Kubernetes API server latency
- Informer cache synchronization timing

On a fast system with light load, the window might be only 20-50ms. On a busy CI runner, it might be 100-300ms, giving the test more chances to catch the "snapshot 0" state.

**Key Insight**: The number of envelopes doesn't determine snapshot count. Instead, it's the **number of times the resource content hash changes** over time that drives snapshot creation. The flakiness comes from **when** the test observes the CRP status relative to **when** the state transitions occur.
