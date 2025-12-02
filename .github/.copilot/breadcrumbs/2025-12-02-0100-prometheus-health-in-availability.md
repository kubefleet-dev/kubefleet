# Prometheus Health Check Integration with WorkloadHealthChecker Interface

## Context
Integrate Prometheus health checking into `trackDeploymentAvailability` using the `WorkloadHealthChecker` interface. The simplified approach:
1. Add `prometheusClient` as field on `Reconciler` (shared across all health checks)
2. Discover Prometheus service in member cluster (lazy, cached on Reconciler)
3. Inside `trackDeploymentAvailability`, create `DeploymentHealthChecker` after Prometheus is discovered
4. `DeploymentHealthChecker` uses the Reconciler's `prometheusClient` to query health
5. Query `workload_health{namespace="<ns>"}` metric for all workloads  
6. Find the specific deployment's health in the results
7. Combine availability + health = overall availability

## Based on Existing Code
- **Interface**: `WorkloadHealthChecker` already defined in `health_checker.go`
- **From `collector.go`**: Reuse `PrometheusClient`, query logic, response parsing
- **From `examples/prometheus/`**: Service name `prometheus`, port `9090`, namespace `test-ns`
- **WorkloadMetrics**: Reuse struct with `Namespace`, `ClusterName`, `WorkloadName`, `Health` fields
- **Query**: `workload_health{namespace="<ns>"}` returns all workloads' health in namespace

---
### Phase 1: Copy Required Code to workapplier Package
**Objective**: Make Prometheus client and types available in workapplier

#### Task 1.1: Create prometheus_client.go in workapplier
- [ ] Copy from `metriccollector/collector.go`:
  - `PrometheusClient` interface
  - `prometheusClient` struct and implementation
  - `NewPrometheusClient` function  
  - `Query` method and `addAuth` method
  - `PrometheusResponse`, `PrometheusData`, `PrometheusResult` types
- [ ] File: `pkg/controllers/workapplier/prometheus_client.go`

#### Task 1.2: Create workload_metrics.go in workapplier  
- [ ] Copy `WorkloadMetrics` struct from `apis/placement/v1beta1/metriccollector_types.go`
- [ ] Keep fields: `Namespace`, `ClusterName`, `WorkloadName`, `Health`
- [ ] File: `pkg/controllers/workapplier/workload_metrics.go`

**Success Criteria**: Prometheus client code compiles in workapplier package

---

### Phase 2: Add Prometheus Discovery to Reconciler
**Objective**: Add prometheusClient field and discovery logic to Reconciler

#### Task 2.1: Add fields to Reconciler struct
- [ ] In `controller.go`, add to `Reconciler` struct:
  ```go
  // Prometheus health checking fields
  prometheusClient       PrometheusClient
  prometheusURL          string
  prometheusDiscovered   *atomic.Bool
  prometheusAvailable    *atomic.Bool
  ```

#### Task 2.2: Initialize in NewReconciler constructor
- [ ] Initialize: `prometheusDiscovered: atomic.NewBool(false)`
- [ ] Initialize: `prometheusAvailable: atomic.NewBool(false)`
- [ ] Set `prometheusClient` to nil initially

#### Task 2.3: Create discoverPrometheus method on Reconciler
- [ ] Add method: `func (r *Reconciler) discoverPrometheus(ctx context.Context, namespace string) error`
- [ ] Check if already discovered: `if r.prometheusDiscovered.Load() { return nil }`
- [ ] Search for Prometheus Service in namespace:
  - List services with label `app=prometheus`
  - Or look for service named `prometheus`
- [ ] If found:
  - Set `r.prometheusURL = "http://prometheus.<namespace>.svc.cluster.local:9090"`
  - Create `r.prometheusClient = NewPrometheusClient(r.prometheusURL, "", nil)`
  - Set `r.prometheusAvailable.Store(true)`
  - Log: "Discovered Prometheus"
- [ ] If not found:
  - Set `r.prometheusAvailable.Store(false)`
  - Log: "Prometheus not found, health checks disabled"
- [ ] Set `r.prometheusDiscovered.Store(true)`

**Success Criteria**: Reconciler can discover and cache Prometheus client

---

### Phase 3: Implement DeploymentHealthChecker
**Objective**: Create DeploymentHealthChecker that uses Reconciler's prometheusClient

#### Task 3.1: Add DeploymentHealthChecker struct in health_checker.go
- [ ] Keep existing `WorkloadHealthChecker` interface (already defined)
- [ ] Add `DeploymentHealthChecker` struct:
  ```go
  type DeploymentHealthChecker struct {
      prometheusClient PrometheusClient
  }
  ```
- [ ] Simple struct - just wraps the prometheusClient

#### Task 3.2: Create NewDeploymentHealthChecker constructor
- [ ] Add constructor:
  ```go
  func NewDeploymentHealthChecker(prometheusClient PrometheusClient) *DeploymentHealthChecker {
      return &DeploymentHealthChecker{
          prometheusClient: prometheusClient,
      }
  }
  ```

#### Task 3.3: Implement IsWorkloadHealthy method
- [ ] Signature (from interface): `IsWorkloadHealthy(ctx context.Context, gvr schema.GroupVersionResource, obj *unstructured.Unstructured) (healthy bool, reason string, err error)`
- [ ] Implementation:
  ```go
  func (d *DeploymentHealthChecker) IsWorkloadHealthy(ctx context.Context, gvr schema.GroupVersionResource, obj *unstructured.Unstructured) (bool, string, error) {
      // 1. Convert to Deployment
      var deploy appsv1.Deployment
      if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &deploy); err != nil {
          return false, "failed to convert to deployment", err
      }
      
      // 2. Collect workload health metrics
      workloadMetrics, err := d.collectWorkloadHealthMetrics(ctx, deploy.Namespace)
      if err != nil {
          klog.V(2).InfoS("Failed to collect health metrics", "error", err)
          return true, "failed to query prometheus, assuming healthy", nil
      }
      
      // 3. Find this deployment's health
      for _, wm := range workloadMetrics {
          if wm.WorkloadName == deploy.Name && wm.Namespace == deploy.Namespace {
              if wm.Health {
                  return true, "deployment is healthy", nil
              } else {
                  return false, "deployment is unhealthy", nil
              }
          }
      }
      
      // 4. No health metric found - assume healthy
      return true, "no health metric found, assuming healthy", nil
  }
  ```

#### Task 3.4: Implement collectWorkloadHealthMetrics helper
- [ ] Add private method: `func (d *DeploymentHealthChecker) collectWorkloadHealthMetrics(ctx context.Context, namespace string) ([]WorkloadMetrics, error)`
- [ ] Build PromQL query: `workload_health{namespace="<namespace>"}`
- [ ] Execute: `result, err := d.prometheusClient.Query(ctx, query)`
- [ ] Parse response (reuse logic from collector.go):
  ```go
  data, ok := result.(PrometheusData)
  workloadMetrics := make([]WorkloadMetrics, 0, len(data.Result))
  for _, res := range data.Result {
      namespace := res.Metric["namespace"]
      clusterName := res.Metric["cluster_name"]
      workloadName := res.Metric["workload_name"]
      
      if namespace == "" || workloadName == "" {
          continue
      }
      
      var health float64
      if len(res.Value) >= 2 {
          if valueStr, ok := res.Value[1].(string); ok {
              fmt.Sscanf(valueStr, "%f", &health)
          }
      }
      
      wm := WorkloadMetrics{
          Namespace:    namespace,
          ClusterName:  clusterName,
          WorkloadName: workloadName,
          Health:       health == 1.0,
      }
      workloadMetrics = append(workloadMetrics, wm)
  }
  return workloadMetrics, nil
  ```

**Success Criteria**: DeploymentHealthChecker implements WorkloadHealthChecker interface

---

### Phase 4: Modify trackDeploymentAvailability
**Objective**: Discover Prometheus and use DeploymentHealthChecker to check health

#### Task 4.1: Change function signature
- [ ] From: `func trackDeploymentAvailability(inMemberClusterObj *unstructured.Unstructured) (ManifestProcessingAvailabilityResultType, error)`
- [ ] To: `func (r *Reconciler) trackDeploymentAvailability(ctx context.Context, inMemberClusterObj *unstructured.Unstructured) (ManifestProcessingAvailabilityResultType, error)`

#### Task 4.2: Add Prometheus discovery and health checking
- [ ] Keep existing availability check logic
- [ ] After availability passes, discover Prometheus and check health:
  ```go
  // Existing availability check
  if deploy.Status.ObservedGeneration == deploy.Generation &&
      requiredReplicas == deploy.Status.AvailableReplicas &&
      requiredReplicas == deploy.Status.UpdatedReplicas &&
      deploy.Status.UnavailableReplicas == 0 {
      
      // Deployment is available, now check health from Prometheus
      
      // Discover Prometheus if not already done
      if !r.prometheusDiscovered.Load() {
          if err := r.discoverPrometheus(ctx, deploy.Namespace); err != nil {
              klog.V(2).InfoS("Failed to discover Prometheus", "error", err)
          }
      }
      
      // If Prometheus available, check health
      if r.prometheusAvailable.Load() && r.prometheusClient != nil {
          // Create health checker with the Reconciler's prometheusClient
          healthChecker := NewDeploymentHealthChecker(r.prometheusClient)
          
          healthy, reason, err := healthChecker.IsWorkloadHealthy(ctx, utils.DeploymentGVR, inMemberClusterObj)
          if err != nil {
              klog.V(2).InfoS("Health check failed, assuming healthy", 
                  "deployment", klog.KObj(inMemberClusterObj), "error", err)
          } else if !healthy {
              klog.V(2).InfoS("Deployment is available but unhealthy",
                  "deployment", klog.KObj(inMemberClusterObj), "reason", reason)
              return AvailabilityResultTypeNotYetAvailable, nil
          } else {
              klog.V(2).InfoS("Deployment is available and healthy",
                  "deployment", klog.KObj(inMemberClusterObj), "reason", reason)
          }
      }
      
      klog.V(2).InfoS("Deployment is available", "deployment", klog.KObj(inMemberClusterObj))
      return AvailabilityResultTypeAvailable, nil
  }
  
  klog.V(2).InfoS("Deployment is not ready yet", "deployment", klog.KObj(inMemberClusterObj))
  return AvailabilityResultTypeNotYetAvailable, nil
  ```

**Success Criteria**: trackDeploymentAvailability discovers Prometheus and creates DeploymentHealthChecker
          healthy, reason, err := r.deploymentHealthChecker.IsWorkloadHealthy(ctx, utils.DeploymentGVR, inMemberClusterObj)
          if err != nil {
              klog.V(2).InfoS("Health check failed, assuming healthy", 
                  "deployment", klog.KObj(inMemberClusterObj), "error", err)
          } else if !healthy {
              klog.V(2).InfoS("Deployment is available but unhealthy",
                  "deployment", klog.KObj(inMemberClusterObj), "reason", reason)
              return AvailabilityResultTypeNotYetAvailable, nil
          } else {
              klog.V(2).InfoS("Deployment is available and healthy",
                  "deployment", klog.KObj(inMemberClusterObj), "reason", reason)
          }
      }
      
      klog.V(2).InfoS("Deployment is available", "deployment", klog.KObj(inMemberClusterObj))
      return AvailabilityResultTypeAvailable, nil
  }
  
  klog.V(2).InfoS("Deployment is not ready yet", "deployment", klog.KObj(inMemberClusterObj))
  return AvailabilityResultTypeNotYetAvailable, nil
  ```

**Success Criteria**: trackDeploymentAvailability uses WorkloadHealthChecker interface

---

### Phase 6: Update trackInMemberClusterObjAvailabilityByGVRewDeploymentHealthChecker(spokeClient)`

**Success Criteria**: Reconciler has access to health checker

---

### Phase 5: Modify trackDeploymentAvailability
---

### Phase 4: Modify trackDeploymentAvailability
**Objective**: Integrate health checking into availability tracking

#### Task 4.1: Change function signature
- [ ] From: `func trackDeploymentAvailability(inMemberClusterObj *unstructured.Unstructured) (ManifestProcessingAvailabilityResultType, error)`
- [ ] To: `func (r *Reconciler) trackDeploymentAvailability(ctx context.Context, inMemberClusterObj *unstructured.Unstructured) (ManifestProcessingAvailabilityResultType, error)`

#### Task 4.2: Add health checking logic
- [ ] Keep existing availability check logic (replicas, generation, etc.)
- [ ] After availability check passes, add health check:
  ```go
  // Existing availability check
  if deploy.Status.ObservedGeneration == deploy.Generation &&
      requiredReplicas == deploy.Status.AvailableReplicas &&
      requiredReplicas == deploy.Status.UpdatedReplicas &&
      deploy.Status.UnavailableReplicas == 0 {
### Phase 6: Update trackInMemberClusterObjAvailabilityByGVR
**Objective**: Pass context and reconciler to deployment tracker

#### Task 6.1: Change to Reconciler method
- [ ] From: `func trackInMemberClusterObjAvailabilityByGVR(gvr *schema.GroupVersionResource, inMemberClusterObj *unstructured.Unstructured)`
- [ ] To: `func (r *Reconciler) trackInMemberClusterObjAvailabilityByGVR(ctx context.Context, gvr *schema.GroupVersionResource, inMemberClusterObj *unstructured.Unstructured)`

#### Task 6.2: Update Deployment case
- [ ] Change: `return trackDeploymentAvailability(inMemberClusterObj)`
- [ ] To: `return r.trackDeploymentAvailability(ctx, inMemberClusterObj)`

#### Task 6.3: Update call site
- [ ] In `trackInMemberClusterObjAvailability`, update:
  - From: `availabilityResTyp, err := trackInMemberClusterObjAvailabilityByGVR(bundle.gvr, bundle.inMemberClusterObj)`
  - To: `availabilityResTyp, err := r.trackInMemberClusterObjAvailabilityByGVR(childCtx, bundle.gvr, bundle.inMemberClusterObj)`

**Success Criteria**: Context flows through to trackDeploymentAvailability

---

### Phase 7: Testingis deployment's health metric
          for _, wm := range workloadMetrics {
### Phase 7: Testing

#### Task 7.1: Unit tests for DeploymentHealthChecker
- [ ] Test `IsWorkloadHealthy` with mock PrometheusClient
- [ ] Test cases:
  - Prometheus discovered and workload healthy
  - Prometheus discovered and workload unhealthy
  - Prometheus not found (graceful skip)
  - Prometheus query error (graceful fallback)
  - No health metric for deployment (assume healthy)
  - Invalid deployment object

#### Task 7.2: Unit tests for trackDeploymentAvailability
- [ ] Test with mock WorkloadHealthChecker
- [ ] Test cases:
  - Available + healthy → Available
  - Available + unhealthy → NotYetAvailable
  - Not available (skip health check) → NotYetAvailable
  - Health checker nil (backward compat) → Available
  - Health check error (fallback) → Available

#### Task 7.3: Integration tests
- [ ] Deploy Prometheus using `examples/prometheus/` manifests
- [ ] Deploy test deployment with `prometheus.io/scrape: "true"` annotation
- [ ] Verify health checking works end-to-end
- [ ] Test without Prometheus (graceful degradation)

**Success Criteria**: All tests pass

---

## Checklist

- [ ] **Phase 1**: Copy Prometheus client and WorkloadMetrics to workapplier
- [ ] **Phase 2**: Implement DeploymentHealthChecker with prometheusClient field
- [ ] **Phase 3**: Implement IsWorkloadHealthy method
## Key Design Decisions

1. **prometheusClient on Reconciler** - Shared across all availability tracking, discovered once
2. **Lazy discovery** - Discover Prometheus on first deployment availability check, cache on Reconciler
3. **DeploymentHealthChecker created on-demand** - Only instantiated inside trackDeploymentAvailability after Prometheus is discovered
4. **Simple health checker** - Just wraps prometheusClient, no discovery logic
5. **Graceful degradation** - If Prometheus unavailable, rely on availability only
6. **Query once per namespace** - Get all workload_health metrics in one query
7. **Health is advisory** - Unhealthy but available = NotYetAvailable (will retry)
8. **Interface-based design** - Easy to mock for testing, can extend to other workload types

---

## Architecture

```
Reconciler
    ↓ has field
prometheusClient (PrometheusClient interface)
    ↓ discovered by
discoverPrometheus() method
    ↓
trackDeploymentAvailability()
    ↓ creates (if Prometheus available)
DeploymentHealthChecker
    ↓ implements
WorkloadHealthChecker interface
    ↓ uses
prometheusClient from Reconciler
    ↓ queries
Prometheus Service in member cluster
```
  ```

**Success Criteria**: trackDeploymentAvailability includes health checking

---

### Phase 5: Update trackInMemberClusterObjAvailabilityByGVR
**Objective**: Pass context and reconciler to deployment tracker

#### Task 5.1: Change to Reconciler method
- [ ] From: `func trackInMemberClusterObjAvailabilityByGVR(gvr *schema.GroupVersionResource, inMemberClusterObj *unstructured.Unstructured)`
- [ ] To: `func (r *Reconciler) trackInMemberClusterObjAvailabilityByGVR(ctx context.Context, gvr *schema.GroupVersionResource, inMemberClusterObj *unstructured.Unstructured)`

#### Task 5.2: Update Deployment case
- [ ] Change: `return trackDeploymentAvailability(inMemberClusterObj)`
- [ ] To: `return r.trackDeploymentAvailability(ctx, inMemberClusterObj)`

#### Task 5.3: Update call site
- [ ] In `trackInMemberClusterObjAvailability`, update the call:
  - From: `availabilityResTyp, err := trackInMemberClusterObjAvailabilityByGVR(bundle.gvr, bundle.inMemberClusterObj)`
  - To: `availabilityResTyp, err := r.trackInMemberClusterObjAvailabilityByGVR(childCtx, bundle.gvr, bundle.inMemberClusterObj)`

**Success Criteria**: Context flows through to trackDeploymentAvailability

---

### Phase 6: Testing

#### Task 6.1: Unit tests for collectWorkloadHealthMetrics
- [ ] Test with mock Prometheus responses
- [ ] Test cases:
  - Successful query with multiple workloads
  - Empty response (no workloads)
  - Prometheus query error
  - Invalid response format

#### Task 6.2: Unit tests for trackDeploymentAvailability
- [ ] Test availability + healthy → Available
- [ ] Test available + unhealthy → NotYetAvailable
- [ ] Test not available (skip health check) → NotYetAvailable
- [ ] Test Prometheus not discovered → Available (fallback)
- [ ] Test no health metric found → Available (fallback)

#### Task 6.3: Integration tests
- [ ] Test with real Prometheus in test environment
- [ ] Deploy prometheus using `examples/prometheus/` manifests
- [ ] Deploy test deployment with `prometheus.io/scrape: "true"` annotation
- [ ] Verify health checking works end-to-end

**Success Criteria**: All tests pass

---

## Checklist

- [ ] **Phase 1**: Copy Prometheus client and WorkloadMetrics to workapplier
- [ ] **Phase 2**: Add Prometheus discovery to Reconciler
- [ ] **Phase 3**: Implement collectWorkloadHealthMetrics
- [ ] **Phase 4**: Modify trackDeploymentAvailability with health checking
- [ ] **Phase 5**: Update trackInMemberClusterObjAvailabilityByGVR
- [ ] **Phase 6**: Write and run tests

---

## Key Design Decisions

1. **No new CRDs or APIs** - Everything in workapplier package
2. **Reuse existing code** - Copy from metriccollector, don't depend on it
3. **Lazy discovery** - Only discover Prometheus when first deployment is checked
4. **Graceful degradation** - If Prometheus unavailable, rely on availability only
5. **Query once per namespace** - Get all workload_health metrics in one query
6. **Health is advisory** - Unhealthy but available = NotYetAvailable (will retry)

---

## Expected Behavior

### With Prometheus Installed:
1. First deployment check triggers Prometheus discovery
2. Query `workload_health{namespace="test-ns"}` 
3. Find deployment's health metric
4. If healthy → Available
5. If unhealthy → NotYetAvailable (recheck later)

### Without Prometheus:
1. Discovery finds no Prometheus service
2. Log warning, cache result
3. All deployments use availability-only checks
4. No performance impact

### Deployment Without Health Metric:
1. Query returns results but deployment not in list
2. Assume healthy (pods may not have prometheus annotation)
3. Return Available based on availability check
