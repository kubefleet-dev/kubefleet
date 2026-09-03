# Helm deploy: Azure Workload Identity Federation

Deploy KubeFleet's hub and member agents via Helm, with the member-agent authenticating to the
hub via Azure Workload Identity Federation — a federated credential exchanged for a short-lived
token, no static secret to manage.

For the managed identity alternative, see [../azure-helm-managedidentity/](../azure-helm-managedidentity/).

> Requires `charts/member-agent` support for `serviceAccount.annotations` and `pod.labels`.
> `hub-values.yaml` has its own dependency note at the top of the file.

## Files

```
hub-values.yaml      helm install ... -f hub-values.yaml   (pins the real image tag; auth-mode-agnostic)
member-values.yaml   helm install ... -f member-values.yaml (config.provider=azure, workload-identity annotation/label)
```

## Values to fill in

| File | Key | What it is | Where to get it |
|---|---|---|---|
| `member-values.yaml` | `config.hubURL` | The hub's API server address, reachable from the member cluster | `kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}'` (hub context) |
| `member-values.yaml` | `config.memberClusterName` | This cluster's name in the fleet | Your choice — must match the `MemberCluster` object's `metadata.name` |
| `member-values.yaml` | `config.hubCA` | The hub's CA cert, base64-encoded | `kubectl config view --raw -o jsonpath='{.clusters[0].cluster.certificate-authority-data}'` (hub context) |
| `member-values.yaml` | `serviceAccount.annotations` | The UAMI's client ID | `az identity show -g <member-rg> -n "<member-cluster-name>-hub-access" --query clientId -o tsv` |
| `member-values.yaml` | `refreshtoken.repository`/`tag` | Your custom-built `refresh-token` image | See Prerequisites |

The `MemberCluster` object's `spec.identity.name` (the UAMI's **object** ID, not client ID) isn't
a Helm value — it's applied as a separate Kubernetes object; see Step 2 below.

## Prerequisites (outside this repo, Azure/cluster-level)

1. **On the hub AKS cluster**: Azure AD integration enabled, so its apiserver can authenticate
   an AAD bearer token in the first place (this is what lets `MemberCluster.spec.identity.kind:
   User` mean anything at the apiserver level):
   ```bash
   az aks update -g <hub-rg> -n <hub-cluster> --enable-aad
   ```
2. **On the member AKS cluster**: OIDC issuer + workload identity:
   ```bash
   az aks update -g <member-rg> -n <member-cluster> --enable-oidc-issuer --enable-workload-identity
   export ISSUER=$(az aks show -g <member-rg> -n <member-cluster> --query "oidcIssuerProfile.issuerUrl" -o tsv)
   ```
3. **A User-Assigned Managed Identity**, representing this member cluster's identity on the hub:
   ```bash
   az identity create -g <member-rg> -n "<member-cluster-name>-hub-access"
   export CLIENT_ID=$(az identity show -g <member-rg> -n "<member-cluster-name>-hub-access" --query clientId -o tsv)
   export PRINCIPAL_ID=$(az identity show -g <member-rg> -n "<member-cluster-name>-hub-access" --query principalId -o tsv)
   ```
4. **A federated credential**, binding that UAMI to the member-agent's ServiceAccount
   (`member-agent-sa` in `fleet-system`):
   ```bash
   az identity federated-credential create \
     --name "<member-cluster-name>-fic" \
     --identity-name "<member-cluster-name>-hub-access" \
     --resource-group <member-rg> \
     --issuer $ISSUER \
     --subject system:serviceaccount:fleet-system:member-agent-sa \
     --audiences api://AzureADTokenExchange
   ```

## Order of operations

### 1. Hub cluster — one-time install

```bash
kubectl config use-context <hub-context>
helm install hub-agent ../../charts/hub-agent \
  -n fleet-system --create-namespace \
  -f hub-values.yaml
```

### 2. Hub cluster — per member cluster

```yaml
apiVersion: cluster.kubernetes-fleet.io/v1beta1
kind: MemberCluster
metadata:
  name: <member-cluster-name>
spec:
  identity:
    kind: User
    name: <uami-principal-id>   # the UAMI's Entra *object* ID -- az identity show --query principalId
    apiGroup: rbac.authorization.k8s.io
  heartbeatPeriodSeconds: 15
```
```bash
kubectl apply -f <your-filled-in-copy>.yaml
```
`kubectl get membercluster <name>` should show `ReadyToJoin=True` once the hub reconciles it.

### 3. Member cluster

Fill in `member-values.yaml`'s placeholders (see [Values to fill in](#values-to-fill-in)), then:

```bash
kubectl config use-context <member-context>
helm install member-agent ../../charts/member-agent \
  -n fleet-system --create-namespace \
  -f member-values.yaml
```

`kubectl get pods -n fleet-system` should show both containers ready, and back on the hub,
`kubectl get membercluster <name>` should progress to `Joined=True` within one heartbeat period.

## Upgrading

Same command with `helm upgrade` instead of `helm install`. If you change
`serviceAccount.annotations` on an existing install, restart the pod so the workload-identity
webhook re-injects the new identity (`kubectl rollout restart deployment/member-agent -n
fleet-system`) — the webhook only mutates pods at admission time.

## Uninstalling

Delete any `MemberCluster` objects *before* running `helm uninstall hub-agent`. hub-agent's CRDs
are Helm-templated (not in the CRD-exempt `crds/` directory), so uninstalling deletes them, and
a `MemberCluster` with pending finalizers can leave the CRD stuck mid-delete through a
reinstall. hub-agent also self-registers its webhook configs at runtime, so `helm uninstall`
never removes them — if you hit a webhook error deleting a `MemberCluster` after an uninstall:
```bash
kubectl delete validatingwebhookconfiguration fleet-guard-rail-webhook-configuration fleet-validating-webhook-configuration
kubectl delete mutatingwebhookconfiguration fleet-mutating-webhook-configuration
```
