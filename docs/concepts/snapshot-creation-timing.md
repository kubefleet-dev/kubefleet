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
t=0.15s : Eventually() first check → matches expected status → SUCCESS, stops checking
t=0.35s : CRP status changes to ObservedResourceIndex="1" (but Eventually() already stopped)
```
**Result**: Test passes with index "0". Eventually() never sees index "1" because it stopped after first successful match.

**Scenario B - Test only sees index 1 (fails if expecting 0, passes if expecting 1)**:
```
t=0.05s : Snapshot 0 created but status not yet updated
t=0.08s : Envelope processing completes quickly
t=0.1s  : Snapshot 1 created  
t=0.12s : Eventually() first check → CRP status still updating
t=0.15s : Eventually() second check → CRP status = ObservedResourceIndex="1" with complete conditions
t=0.15s : Matches expected? If expecting "0" → NO → keeps checking
t=0.4s-10s : Eventually() keeps checking every 250ms, always sees "1", expects "0" → TIMEOUT/FAIL
```
**Result**: Test fails. Eventually() NEVER sees index "0" because envelope processing completed before CRP status reached the "snapshot 0" state with complete conditions.

**Important**: In Scenario A, even though snapshot 1 WILL be created at t=0.35s, the test never observes it because Eventually() already stopped at t=0.15s after matching index "0". The test doesn't "eventually catch index 1" - it stops checking as soon as it succeeds.

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

3. **Eventually() Behavior - Critical Point**: 
   - Checks every 250ms (eventuallyInterval) for up to 10s (eventuallyDuration)
   - **First successful match stops the checking immediately**
   - If it catches index "0" state early and matches expected status → **SUCCESS, stops checking, never sees index "1"**
   - If index "1" is created before the first successful match → **only sees index "1", never sees index "0"**
   
   **Key**: Eventually() does NOT keep checking after success. Once the actual status matches expected status, it stops. So:
   - ✅ If first match happens during "snapshot 0" state → test succeeds with index "0", never observes transition to "1"
   - ❌ If "snapshot 1" created before first match → test only sees index "1", fails if expecting "0"

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

## Why Not Just Match Index 1?

**Q: If envelope processing usually creates snapshot 1, why not just expect `ObservedResourceIndex="1"` instead of ignoring it?**

**A**: This would still be brittle and incorrect for several reasons:

### 1. It's Not Always Index 1

The final snapshot index is **non-deterministic** and depends on:

**Possible Outcomes**:
- **Index 0**: Envelope processing completes, but no resource hash change occurs (rare but possible if envelope is empty or unchanged)
- **Index 1**: Most common - envelope processing triggers one hash change
- **Index 2+**: Multiple changes occur:
  - Envelope metadata updated by controller
  - Status updates from member clusters
  - Additional resource modifications
  - Concurrent changes to other selected resources

**Example Timeline Leading to Index 2**:
```
t=0s    : CRP created → snapshot 0 (namespace + envelope)
t=0.3s  : Envelope processed → hash change → snapshot 1
t=0.5s  : Member cluster updates Work status
t=0.6s  : Status propagates back, triggers CRP reconcile
t=0.7s  : Resource hash changes again → snapshot 2
t=10s   : Test checks → sees index 2, expects index 1 → FAIL
```

### 2. Test Semantics Are Wrong

The test validates **duplicate detection behavior**, not **snapshot versioning**:

```go
// What the test SHOULD validate:
✅ Duplicate ConfigMap is detected
✅ Failure condition is set correctly
✅ Failed placement is reported with correct reason
✅ Other resources are placed successfully

// What the test should NOT validate:
❌ Which snapshot index happens to be active
❌ How many times the snapshot was recreated
❌ Timing of snapshot creation
```

Hardcoding any index value (0, 1, 2) makes the test validate an **implementation detail** rather than the **functional behavior**.

### 3. Consistently() Check Would Still Fail

The test uses `Consistently()` after `Eventually()`:

```go
Eventually(crpStatusUpdatedActual, 10s, 250ms).Should(Succeed())
Consistently(crpStatusUpdatedActual, 15s, 500ms).Should(Succeed()) // ← checks for 15 more seconds!
```

Even if you catch index 1 with Eventually():
- Consistently() checks every 500ms for 15 seconds (30 more checks!)
- Any additional resource changes during those 15s → index 2, 3, etc.
- Test would fail because index changed from 1 to 2

**Possible during Consistently()**:
- Controller status updates
- Informer cache resync
- Background resource reconciliation
- Namespace finalizer processing
- Garbage collection annotations

### 4. Future Brittleness

Controller implementation could change:
- Optimization might reduce unnecessary snapshots
- New features might trigger additional snapshots
- Hash calculation changes
- Timing adjustments in reconciliation loops

Hardcoding index 1 couples the test to **current implementation details**, making it break when the implementation improves.

### 5. Multiple Independent Change Sources

Many components can trigger snapshot updates:

| Component | Trigger | Example |
|-----------|---------|---------|
| WorkGenerator | Extracts envelope manifests | Snapshot 0 → 1 |
| Member agent | Updates Work status | Snapshot 1 → 2 |
| PlacementController | Detects resource changes | Any index + 1 |
| ChangeDetector | Watches all resource types | Any index + 1 |
| User operations | Modifies envelope content | Any index + 1 |
| GC controller | Adds/removes finalizers | Any index + 1 |

Any combination of these can occur during the test, creating unpredictable snapshot counts.

### The Correct Solution

**Ignore `ObservedResourceIndex` entirely** using `placementStatusCmpOptionsOnCreate`:

```go
// Validates behavior, not implementation details
wantStatus := placementv1beta1.PlacementStatus{
    // No ObservedResourceIndex field
    PerClusterPlacementStatuses: []placementv1beta1.PerClusterPlacementStatus{
        {
            ClusterName: "cluster-1",
            FailedPlacements: [...],  // ← This is what matters
            Conditions: [...],         // ← This is what matters
        },
    },
}
```

This approach:
- ✅ Tests functional behavior (duplicate detection)
- ✅ Ignores timing artifacts (snapshot index)
- ✅ Works regardless of how many snapshots are created
- ✅ Remains stable through implementation changes
- ✅ Passes both Eventually() and Consistently() checks

### Summary

Matching index 1 would be **better than matching index 0** (catches more cases), but it's still **fundamentally wrong** because:
1. Test would still flake when index is 0 or 2+
2. Test validates wrong thing (timing artifact vs behavior)
3. Consistently() check would fail on subsequent changes
4. Brittle to implementation changes

**The right fix**: Ignore snapshot index, validate actual behavior.

## Alternative Approach: Wait for Stable State

### Valid Criticism

**"It is a test case we have control over. It should be eventually consistently 1"**

This is a **valid point**! Looking at the test setup:
- ✅ Static resources (namespace, envelope, CRP created once)
- ✅ NO subsequent modifications
- ✅ Controlled environment
- ✅ Eventually() already waits up to 10 seconds

So why not just **wait for the system to stabilize at index 1**?

### The Alternative Solution

Instead of ignoring `ObservedResourceIndex`, we could:

```go
It("should update CRP status as expected", func() {
    crpStatusUpdatedActual := func() error {
        crp := &placementv1beta1.ClusterResourcePlacement{}
        if err := hubClient.Get(ctx, types.NamespacedName{Name: crpName}, crp); err != nil {
            return err
        }

        wantStatus := placementv1beta1.PlacementStatus{
            ObservedResourceIndex: "1",  // Explicitly wait for index 1
            Conditions: crpAppliedFailedConditions(crp.Generation),
            PerClusterPlacementStatuses: []placementv1beta1.PerClusterPlacementStatus{
                {
                    ClusterName: memberCluster1EastProdName,
                    ObservedResourceIndex: "1",  // Explicitly wait for index 1
                    FailedPlacements: [...],
                    Conditions: [...],
                },
            },
            SelectedResources: [...],
        }
        if diff := cmp.Diff(crp.Status, wantStatus, placementStatusCmpOptions...); diff != "" {
            return fmt.Errorf("CRP status diff (-got, +want): %s", diff)
        }
        return nil
    }
    
    // Wait longer to ensure envelope processing completes
    Eventually(crpStatusUpdatedActual, eventuallyDuration*2, eventuallyInterval).Should(Succeed())
    Consistently(crpStatusUpdatedActual, consistentlyDuration, consistentlyInterval).Should(Succeed())
})
```

### Pros and Cons

| Approach | Pros | Cons |
|----------|------|------|
| **Ignore index** (current) | • Works with any number of snapshots<br>• Robust to timing variations<br>• Focuses on behavior, not implementation<br>• Simple and maintainable | • Doesn't validate system stabilization<br>• Could miss snapshot thrashing issues<br>• Less explicit about expected state |
| **Wait for index 1** (alternative) | • Validates system reaches stable state<br>• More explicit test expectations<br>• Could catch snapshot thrashing<br>• Proves envelope processing completes | • Assumes envelope processing creates exactly 1 new snapshot<br>• Could fail if optimization reduces snapshots<br>• Could fail if additional processing creates snapshot 2<br>• Harder to maintain if implementation changes |

### When Each Approach is Appropriate

**Use "Ignore index" when**:
- ✅ Test validates **functional behavior** (e.g., duplicate detection, error conditions)
- ✅ Snapshot versioning is an **implementation detail**
- ✅ Test should be **resilient to controller optimizations**
- ✅ Multiple controllers might trigger snapshots unpredictably

**Use "Wait for specific index" when**:
- ✅ Test specifically validates **snapshot creation behavior**
- ✅ Snapshot count is part of the **contract being tested**
- ✅ Need to prove **system stabilization** after known operations
- ✅ Can guarantee **no additional snapshot triggers** during test

### Why We Chose "Ignore Index" for This Test

For the duplicate detection test specifically:

1. **Test Purpose**: Validate that duplicate ConfigMaps are detected and reported correctly
   - The failure condition is what matters
   - The snapshot index is incidental

2. **Robustness**: Even with controlled setup, edge cases exist:
   - If E2E delays are set to 0, BOTH snapshots might be created before first Eventually() check
   - Controller optimizations might eliminate snapshot 0→1 transition
   - Additional controllers (GC, finalizers) might trigger snapshot 2

3. **Maintenance**: If implementation improves (e.g., skips unnecessary snapshots), test continues working

### The Pragmatic Middle Ground

For tests where you DO control the setup and want to validate stabilization:

```go
// First wait for the expected stable state
Eventually(func() error {
    crp := &placementv1beta1.ClusterResourcePlacement{}
    if err := hubClient.Get(ctx, types.NamespacedName{Name: crpName}, crp); err != nil {
        return err
    }
    
    // Check the functional status (behavior)
    if diff := cmp.Diff(crp.Status, wantStatus, placementStatusCmpOptionsOnCreate...); diff != "" {
        return fmt.Errorf("behavior diff: %s", diff)
    }
    
    // Additionally check we've reached a stable snapshot index (optional)
    if crp.Status.ObservedResourceIndex == "" {
        return fmt.Errorf("snapshot not yet created")
    }
    
    return nil
}, workloadEventuallyDuration, eventuallyInterval).Should(Succeed())

// Then verify it stays stable
Consistently(crpStatusUpdatedActual, consistentlyDuration, consistentlyInterval).Should(Succeed())
```

This approach:
- ✅ Waits for system stabilization (snapshot index exists)
- ✅ Validates functional behavior (ignores which specific index)
- ✅ Uses Consistently() to prove no thrashing
- ✅ Robust to snapshot count variations

### Conclusion

**Your criticism is valid!** For a controlled test setup, we COULD wait for index 1 specifically. The trade-off is:

- **Ignore index** = More robust, less explicit
- **Wait for index 1** = More explicit, less robust

For this specific test (duplicate detection), we chose robustness over explicitness because:
1. The test validates behavior, not snapshot mechanics
2. Implementation changes shouldn't break the test
3. The approach matches the pattern used elsewhere in the codebase

But you're absolutely right that the alternative (waiting for index 1) is also valid, especially if you want to explicitly validate that envelope processing completes and the system stabilizes.

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

### ❌ Also Don't Do This

```go
wantStatus := placementv1beta1.PlacementStatus{
    ObservedResourceIndex: "1",  // Better than "0", but still wrong!
    PerClusterPlacementStatuses: []placementv1beta1.PerClusterPlacementStatus{
        {ObservedResourceIndex: "1", ...},  // Could be 0, 1, 2, or higher
    },
}
```

**Why "1" is also wrong**: See "Why Not Just Match Index 1?" section above for detailed explanation. TL;DR:
- Could be 0 (no changes), 1 (one change), or 2+ (multiple changes)
- Test semantics are wrong (validates timing, not behavior)
- Consistently() check will fail on subsequent updates
- Brittle to implementation changes

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

- **Sometimes** it catches the brief "snapshot 0" state before envelope processing completes → sees index "0", matches, **stops checking immediately**, never observes index "1"
- **Sometimes** envelope processing completes before the first successful check → only sees index "1", keeps checking for 10s expecting "0", times out and fails

The critical window (when CRP status shows index "0") duration varies based on:
- CPU load and scheduling
- Controller work queue processing rate
- Envelope content size and processing complexity
- Kubernetes API server latency
- Informer cache synchronization timing

On a fast system with light load, the window might be only 20-50ms. On a busy CI runner, it might be 100-300ms, giving the test more chances to catch the "snapshot 0" state.

**Q: So eventually it will catch index 1 then?**

**A**: **No, not necessarily!** This is a common misconception. `Eventually()` **stops checking as soon as it gets a successful match**. 

If the test's first successful status check happens at t=0.15s when index is "0", Eventually() returns success and stops. Even though snapshot 1 will be created at t=0.35s, the test never sees it because it already finished.

The test only "catches index 1" if envelope processing completes SO QUICKLY that snapshot 1 is created and CRP status is updated BEFORE Eventually() gets its first successful match with complete conditions.

Think of it like this:
- 🏁 **Race to first match**: Does Eventually() match index "0" first, OR does envelope processing create index "1" first?
- ⏱️ **Critical timing**: First successful match wins and stops the race
- 🎯 **Outcome**: Whichever state exists during first successful match determines what the test observes

**Key Insight**: The number of envelopes doesn't determine snapshot count. Instead, it's the **number of times the resource content hash changes** over time that drives snapshot creation. The flakiness comes from **when** the test observes the CRP status relative to **when** the state transitions occur.
