# Grafana access

```sh
kubectl --namespace monitoring get secrets kube-prometheus-stack-grafana -o jsonpath="{.data.admin-password}" | base64 -d ; echo
```
Admin pass: `WRyRwziHdrgI3x7dAoSk8Kuo0PrXEc4HTtdL88aL`

```sh
export POD_NAME=$(kubectl --namespace monitoring get pod -l "app.kubernetes.io/name=grafana,app.kubernetes.io/instance=kube-prometheus-stack" -oname)
kubectl --namespace monitoring port-forward $POD_NAME 3000
```

# Prometheus queries

go_gc_cycles_total_gc_cycles_total{instance="fleet-metrics.fleet-system.svc.cluster.local:8080"}

