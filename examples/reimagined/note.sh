kind export kubeconfig -n cluster-1
kind export kubeconfig -n cluster-2
kind export kubeconfig -n cluster-3
kind export kubeconfig -n hub

kubectl create serviceaccount virtual-cluster-helper -n fleet-system

kubectl label membercluster kind-cluster-1 topology.kubernetes.io/region=eastus --overwrite
kubectl label membercluster kind-cluster-1 region=eastus --overwrite

kubectl label membercluster kind-cluster-2 topology.kubernetes.io/region=westus2 --overwrite
kubectl label membercluster kind-cluster-2 region=westus2 --overwrite

kubectl label membercluster kind-cluster-3 topology.kubernetes.io/region=centralus --overwrite
kubectl label membercluster kind-cluster-3 region=centralus --overwrite

kubectl create ns work
kubectl create ns work --context kind-cluster-1
kubectl create ns work --context kind-cluster-2
kubectl create ns work --context kind-cluster-3

#####

kubectl apply -f deploy-1.yaml
kubectl annotate deploy nginx -n work experimental.kubefleet.dev/place-to-regions=westus2,eastus --overwrite

kubectl label workloadplacement nginx foo=bar -n work --overwrite

# New placement API, Pods/logs retrieval

#####
kubectl annotate deploy nginx -n work experimental.kubefleet.dev/place-to-regions=westus2,eastus,germanycentral --overwrite
# Expected to be unavailable - no rollout performed.

#####
kubectl apply -f migration-req-1.yaml

#####
kubectl delete -f migration-req-1.yaml

#####
kubectl apply -f deploy-2.yaml
kubectl apply -f deploy-3.yaml
kubectl apply -f deploy-4.yaml
kubectl apply -f deploy-5.yaml
kubectl apply -f deploy-6.yaml
kubectl annotate deploy pause-1 -n work experimental.kubefleet.dev/place-to-regions=centralus --overwrite
kubectl annotate deploy pause-2 -n work experimental.kubefleet.dev/place-to-regions=centralus --overwrite
kubectl annotate deploy pause-3 -n work experimental.kubefleet.dev/place-to-regions=centralus --overwrite
kubectl annotate deploy pause-4 -n work experimental.kubefleet.dev/place-to-regions=centralus --overwrite
kubectl annotate deploy pause-5 -n work experimental.kubefleet.dev/place-to-regions=centralus --overwrite

kubectl label workloadplacement pause-1 foo=baz -n work --overwrite
kubectl label workloadplacement pause-2 foo=baz -n work --overwrite
kubectl label workloadplacement pause-3 foo=baz -n work --overwrite
kubectl label workloadplacement pause-4 foo=baz -n work --overwrite
kubectl label workloadplacement pause-5 foo=baz -n work --overwrite

kubectl apply -f migration-req-2.yaml

#####
kubectl delete -f migration-req-2.yaml
kubectl delete -f deploy-2.yaml
kubectl delete -f deploy-3.yaml
kubectl delete -f deploy-4.yaml
kubectl delete -f deploy-5.yaml
kubectl delete -f deploy-6.yaml

#####
kubectl delete -f deploy-1.yaml