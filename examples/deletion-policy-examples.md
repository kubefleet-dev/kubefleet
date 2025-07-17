# ClusterResourcePlacement with Deletion Policy Examples

This example demonstrates the use of the `deletionPolicy` field in ClusterResourcePlacement to control whether placed resources are deleted or orphaned when the CRP is deleted.

## Example 1: Default Deletion Policy (Delete)

This example shows the default behavior where placed resources are deleted when the CRP is deleted.

```yaml
apiVersion: placement.kubernetes-fleet.io/v1beta1
kind: ClusterResourcePlacement
metadata:
  name: nginx-deployment-delete
spec:
  # Default deletion policy - will delete placed resources when CRP is deleted
  deletionPolicy: Delete
  resourceSelectors:
    - group: apps
      version: v1
      kind: Deployment
      name: nginx-deployment
  policy:
    placementType: PickAll
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-deployment
  labels:
    app: nginx
spec:
  replicas: 3
  selector:
    matchLabels:
      app: nginx
  template:
    metadata:
      labels:
        app: nginx
    spec:
      containers:
      - name: nginx
        image: nginx:1.14.2
        ports:
        - containerPort: 80
```

When you delete this CRP with `kubectl delete crp nginx-deployment-delete`, the nginx-deployment will be removed from all member clusters.

## Example 2: Orphan Deletion Policy

This example shows how to leave placed resources intact when the CRP is deleted.

```yaml
apiVersion: placement.kubernetes-fleet.io/v1beta1
kind: ClusterResourcePlacement
metadata:
  name: nginx-deployment-orphan
spec:
  # Orphan deletion policy - will leave placed resources when CRP is deleted
  deletionPolicy: Orphan
  resourceSelectors:
    - group: apps
      version: v1
      kind: Deployment
      name: nginx-deployment-persistent
  policy:
    placementType: PickAll
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-deployment-persistent
  labels:
    app: nginx-persistent
spec:
  replicas: 3
  selector:
    matchLabels:
      app: nginx-persistent
  template:
    metadata:
      labels:
        app: nginx-persistent
    spec:
      containers:
      - name: nginx
        image: nginx:1.14.2
        ports:
        - containerPort: 80
```

When you delete this CRP with `kubectl delete crp nginx-deployment-orphan`, the nginx-deployment-persistent will remain on all member clusters but will no longer be managed by Fleet.

## Example 3: Namespace Placement with Orphan Policy

This example shows orphaning an entire namespace and its contents.

```yaml
apiVersion: placement.kubernetes-fleet.io/v1beta1
kind: ClusterResourcePlacement
metadata:
  name: my-app-namespace-orphan
spec:
  # Orphan the entire namespace when CRP is deleted
  deletionPolicy: Orphan
  resourceSelectors:
    - group: ""
      version: v1
      kind: Namespace
      name: my-application
  policy:
    placementType: PickN
    numberOfClusters: 2
---
apiVersion: v1
kind: Namespace
metadata:
  name: my-application
  labels:
    managed-by: fleet
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
  namespace: my-application
spec:
  replicas: 2
  selector:
    matchLabels:
      app: my-app
  template:
    metadata:
      labels:
        app: my-app
    spec:
      containers:
      - name: app
        image: nginx:latest
        ports:
        - containerPort: 80
```

When you delete this CRP, the entire `my-application` namespace and all its contents will remain on the selected member clusters.

## Use Cases

### Use Delete Policy When:
- You want complete cleanup when removing the CRP
- Resources are only used by this specific CRP
- You want to ensure no resource leakage across member clusters

### Use Orphan Policy When:
- You want to hand over resource management to another system
- Resources might be needed after CRP removal
- You're migrating from Fleet to another management solution
- You want to keep resources for debugging or analysis purposes
- Resources have dependencies that shouldn't be broken