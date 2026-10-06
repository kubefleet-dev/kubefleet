/*
Copyright 2026 The KubeFleet Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package capi bridges cluster claims to Cluster API: a claim becomes a CAPI Cluster rendered
// from a ClusterClass, and once Cluster API reports it provisioned, a MemberCluster on the hub.
// It speaks to Cluster API through unstructured objects, so KubeFleet carries no Cluster API
// dependency; the one served version it targets is cluster.x-k8s.io/v1beta1.
//
// A class's parameters name the ClusterClass, the Kubernetes version, the namespace the Cluster
// objects go in (which must exist on the management cluster already), and the hub ServiceAccount
// the registered cluster authenticates as; they are read on every call, so treat them as fixed
// once a claim of the class is outstanding -- a changed ClusterClass or version would be applied
// over a cluster that is already provisioning. A "variable.<name>: <label key>" parameter sets the
// ClusterClass variable <name> to the value the claim's selector asks for under that label; the
// region label maps to the "region" variable unless told otherwise. When several ORed selector
// terms ask for the same label with different values, the last term's value is the one rendered.
//
// On the hub the bridge needs the framework's and the provider's ClusterRoles from
// config/rbac/fulfiller, plus a Role on the leader election lease in its namespace; on the
// management cluster -- which may be another cluster -- it needs capi-clusterrole.yaml from the
// same directory.
//
// Deprovisioning requests the Cluster's deletion and leaves the teardown to Cluster API; a
// finalizer that never clears leaves a Cluster behind that nothing here reports, which is the
// management cluster's operator to notice.
//
// Registering is not joining. A provisioned CAPI cluster has a kubeconfig Secret on the
// management cluster, which the bridge names on the MemberCluster, but installing the member
// agent in it -- with the ServiceAccount and token the hub expects -- is the platform's step,
// the join hook this bridge leaves to the operator. Until it happens the cluster never joins,
// and the claim expires as JoinTimeout after the class's joinTimeout, which has the bridge
// deprovision the cluster.
package capi

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils"
	"github.com/kubefleet-dev/kubefleet/pkg/v1/fulfiller"
)

const (
	// ProvisionerName is the spec.provisionerName a ClusterProviderClass names to route its claims
	// here.
	ProvisionerName = "capi.fulfiller.kubefleet.dev"

	// FieldManager is the one server-side-apply manager the bridge writes Cluster objects with:
	// a re-issued claim applies the same object again rather than fighting an earlier manager.
	FieldManager = "kubefleet-capi-fulfiller"

	// The class parameters the bridge reads.
	ClusterClassParameter      = "clusterClassName"
	NamespaceParameter         = "namespace"
	KubernetesVersionParameter = "kubernetesVersion"
	IdentityParameter          = "identity"
	// VariableParameterPrefix prefixes parameters that map a ClusterClass variable to a selector
	// label key: "variable.region: topology.kubernetes.io/region" sets the variable "region" to
	// the value the claim's selector asks for under that label. The region mapping is on by
	// default; an empty value turns a mapping off.
	VariableParameterPrefix = "variable."

	// KubeconfigSecretAnnotation records, on the MemberCluster, the management-cluster Secret
	// that holds the provisioned cluster's kubeconfig, as namespace/name: the hand-off to the
	// join hook.
	KubeconfigSecretAnnotation = "capi.fulfiller.kubefleet.dev/kubeconfig-secret"

	defaultNamespace     = "default"
	defaultRegionLabel   = "topology.kubernetes.io/region"
	defaultRegionVarName = "region"
)

// ClusterGVK is the Cluster API object the bridge renders.
var ClusterGVK = schema.GroupVersionKind{Group: "cluster.x-k8s.io", Version: "v1beta1", Kind: "Cluster"}

// Options configure the bridge.
type Options struct {
	// Hub registers MemberCluster objects.
	Hub client.Client
	// HubReader reads MemberCluster objects past the cache, so that a deprovision never misses a
	// cluster registered a moment ago; pass the manager's GetAPIReader. Defaults to Hub.
	HubReader client.Reader
	// Management applies and reads Cluster API objects; the hub itself when the fleet hub is also
	// the management cluster.
	Management client.Client
}

// Provisioner is the Cluster API bridge. It implements no Remediator: a provisioned cluster that
// stops being eligible is Cluster API's to repair, and the claim fails.
type Provisioner struct {
	hub        client.Client
	hubReader  client.Reader
	management client.Client
}

var _ fulfiller.Provisioner = &Provisioner{}

// New returns a bridge.
func New(opts Options) *Provisioner {
	if opts.HubReader == nil {
		opts.HubReader = opts.Hub
	}
	return &Provisioner{hub: opts.Hub, hubReader: opts.HubReader, management: opts.Management}
}

// rendering is a Cluster rendered from a request, with what the MemberCluster needs.
type rendering struct {
	cluster  *unstructured.Unstructured
	identity string
	labels   map[string]string
}

// render turns a request into the Cluster API object for it. Every selector term is translated
// or refused: a matchLabels key mapped to a ClusterClass variable sets that variable, any other
// matchLabels key rides along as a label on the Cluster and on the MemberCluster, and a label
// expression or a property term has no inverse that would pick an instance shape, so it is
// refused as unsupported and the claim fails permanently.
func render(req fulfiller.Request) (*rendering, error) {
	required := func(key string) (string, error) {
		value := req.Parameters[key]
		if value == "" {
			return "", fulfiller.Permanent(fmt.Errorf("the class %s sets no %q parameter", req.ClusterProviderClassName, key))
		}
		return value, nil
	}
	className, err := required(ClusterClassParameter)
	if err != nil {
		return nil, err
	}
	version, err := required(KubernetesVersionParameter)
	if err != nil {
		return nil, err
	}
	identity, err := required(IdentityParameter)
	if err != nil {
		return nil, err
	}
	namespace := req.Parameters[NamespaceParameter]
	if namespace == "" {
		namespace = defaultNamespace
	}

	variableByLabel := map[string]string{defaultRegionLabel: defaultRegionVarName}
	for _, key := range sortedKeys(req.Parameters) {
		if !strings.HasPrefix(key, VariableParameterPrefix) {
			continue
		}
		name, labelKey := strings.TrimPrefix(key, VariableParameterPrefix), req.Parameters[key]
		for label, variable := range variableByLabel {
			if variable == name {
				delete(variableByLabel, label)
			}
		}
		if labelKey == "" {
			continue
		}
		if other, taken := variableByLabel[labelKey]; taken && other != defaultRegionVarName {
			return nil, fulfiller.Permanent(fmt.Errorf("the class %s maps the label %q to both the %q and the %q variables", req.ClusterProviderClassName, labelKey, other, name))
		}
		variableByLabel[labelKey] = name
	}

	labels := map[string]string{}
	for i := range req.ClusterSelectorTerms {
		term := &req.ClusterSelectorTerms[i]
		if len(term.MatchLabelExpressions) > 0 || len(term.MatchClusterPropertyExpressions) > 0 {
			return nil, fulfiller.Permanent(fmt.Errorf("unsupported term: selector term %d uses label expressions or property expressions, which the Cluster API bridge cannot turn into a cluster", i))
		}
		maps.Copy(labels, term.MatchLabels)
	}
	variables := make([]any, 0, len(variableByLabel))
	for _, label := range sortedKeys(variableByLabel) {
		if value, ok := labels[label]; ok {
			variables = append(variables, map[string]any{"name": variableByLabel[label], "value": value})
		}
	}
	maps.Copy(labels, req.OwnershipLabels())

	cluster := &unstructured.Unstructured{}
	cluster.SetGroupVersionKind(ClusterGVK)
	cluster.SetNamespace(namespace)
	cluster.SetName(req.ClusterName)
	cluster.SetLabels(labels)
	topology := map[string]any{"class": className, "version": version}
	if len(variables) > 0 {
		topology["variables"] = variables
	}
	if err := unstructured.SetNestedMap(cluster.Object, topology, "spec", "topology"); err != nil {
		return nil, err
	}
	return &rendering{cluster: cluster, identity: identity, labels: labels}, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// phaseOutcome judges a Cluster's status: done, in progress (a transient error), or beyond
// repair (a permanent one). Cluster API's failureReason is fatal whatever the phase says. The
// Failed phase and failureReason are deprecated in Cluster API v1.10's v1beta1 and gone from
// v1beta2, so this table is revisited with the served version.
func phaseOutcome(cluster *unstructured.Unstructured) error {
	reason, _, _ := unstructured.NestedString(cluster.Object, "status", "failureReason")
	message, _, _ := unstructured.NestedString(cluster.Object, "status", "failureMessage")
	if reason != "" || message != "" {
		return fulfiller.Permanent(fmt.Errorf("cluster API reports a fatal problem: %s: %s", reason, message))
	}
	phase, _, _ := unstructured.NestedString(cluster.Object, "status", "phase")
	switch phase {
	case "Provisioned":
		return nil
	case "Failed":
		return fulfiller.Permanent(errors.New("cluster API reports the cluster as Failed"))
	case "Deleting":
		return errors.New("the cluster from an earlier attempt is still being deleted")
	case "":
		return errors.New("cluster API has not reported a phase yet")
	default:
		return fmt.Errorf("the cluster is %s", phase)
	}
}

// Provision applies the rendered Cluster -- the same object on every call, under one field
// manager, so a restart resumes rather than duplicates -- and, once Cluster API reports it
// provisioned, registers the MemberCluster on the hub with the kubeconfig Secret named on it.
func (p *Provisioner) Provision(ctx context.Context, req fulfiller.Request) error {
	r, err := render(req)
	if err != nil {
		return err
	}
	// A Cluster under the name that is on its way out is waited for, whoever's it is: applying
	// over it would do nothing while it lasts and provision anew the moment it is gone, which is
	// not this call's decision to make. One that stays and is not this claim's is never applied
	// over.
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(ClusterGVK)
	switch err := p.management.Get(ctx, client.ObjectKeyFromObject(r.cluster), existing); {
	case apierrors.IsNotFound(err):
	case err != nil:
		return err
	case existing.GetDeletionTimestamp() != nil:
		return fmt.Errorf("cluster %s/%s is being deleted", r.cluster.GetNamespace(), r.cluster.GetName())
	case existing.GetLabels()[kfplacementv1alpha1.FulfilledClaimUIDLabel] != string(req.ClaimUID):
		return fulfiller.Permanent(fmt.Errorf("cluster %s/%s exists but is not this claim's", r.cluster.GetNamespace(), r.cluster.GetName()))
	}
	if err := p.management.Apply(ctx, client.ApplyConfigurationFromUnstructured(r.cluster), client.FieldOwner(FieldManager), client.ForceOwnership); err != nil {
		return fmt.Errorf("applying the cluster: %w", err)
	}
	// The status is read back rather than taken from the apply response, which need not carry it.
	applied := &unstructured.Unstructured{}
	applied.SetGroupVersionKind(ClusterGVK)
	if err := p.management.Get(ctx, client.ObjectKeyFromObject(r.cluster), applied); err != nil {
		return err
	}
	if err := phaseOutcome(applied); err != nil {
		return err
	}
	// Cluster API's convention for the Secret holding the cluster's kubeconfig; the bridge names
	// it without checking that it exists.
	secret := fmt.Sprintf("%s/%s-kubeconfig", r.cluster.GetNamespace(), r.cluster.GetName())
	member := &clusterv1beta1.MemberCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:        req.ClusterName,
			Labels:      r.labels,
			Annotations: map[string]string{KubeconfigSecretAnnotation: secret},
		},
		Spec: clusterv1beta1.MemberClusterSpec{
			Identity: rbacv1.Subject{Kind: rbacv1.ServiceAccountKind, Name: r.identity, Namespace: utils.FleetSystemNamespace},
		},
	}
	switch err := p.hub.Create(ctx, member); {
	case err == nil:
		klog.V(2).InfoS("Registered a cluster provisioned by Cluster API; its kubeconfig is ready for the join hook", "cluster", req.ClusterName, "clusterClaim", req.ClaimName, "kubeconfigSecret", secret)
	case apierrors.IsAlreadyExists(err):
		if err := p.hub.Get(ctx, client.ObjectKeyFromObject(member), member); err != nil {
			return err
		}
		if member.Labels[kfplacementv1alpha1.FulfilledClaimUIDLabel] != string(req.ClaimUID) {
			return fulfiller.Permanent(fmt.Errorf("member cluster %s exists but is not this claim's", req.ClusterName))
		}
	default:
		return err
	}
	return nil
}

// Deprovision removes the MemberCluster and the Cluster the request names, each only if it is
// this claim's. It is done once the MemberCluster is gone and the Cluster's deletion has been
// requested: Cluster API tears the infrastructure down behind its finalizer, in the background,
// which can take minutes and must not hold the claim -- a failed claim is written as Failed only
// once Deprovision is done, and a re-applied Cluster would otherwise be provisioned anew the
// moment the old one vanished.
func (p *Provisioner) Deprovision(ctx context.Context, req fulfiller.Request) error {
	member := &clusterv1beta1.MemberCluster{}
	switch err := p.hubReader.Get(ctx, client.ObjectKey{Name: req.ClusterName}, member); {
	case apierrors.IsNotFound(err):
	case err != nil:
		return err
	case member.Labels[kfplacementv1alpha1.FulfilledClaimUIDLabel] == string(req.ClaimUID):
		if err := p.hub.Delete(ctx, member, client.Preconditions{UID: &member.UID}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		if err := p.hubReader.Get(ctx, client.ObjectKeyFromObject(member), member); err == nil {
			return errors.New("the member cluster is still being removed")
		} else if !apierrors.IsNotFound(err) {
			return err
		}
	}

	// The Cluster is found by the claim's UID label across namespaces rather than under the
	// class's current namespace parameter, so an edited parameter cannot leak the cluster.
	clusters := &unstructured.UnstructuredList{}
	clusters.SetGroupVersionKind(ClusterGVK.GroupVersion().WithKind(ClusterGVK.Kind + "List"))
	if err := p.management.List(ctx, clusters, client.MatchingLabels{kfplacementv1alpha1.FulfilledClaimUIDLabel: string(req.ClaimUID)}); err != nil {
		return err
	}
	if len(clusters.Items) == 0 {
		return nil
	}
	cluster := &clusters.Items[0]
	if cluster.GetDeletionTimestamp() != nil {
		return nil
	}
	uid := cluster.GetUID()
	if err := p.management.Delete(ctx, cluster, client.Preconditions{UID: &uid}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	klog.V(2).InfoS("Requested the deletion of a Cluster API cluster", "cluster", cluster.GetName(), "namespace", cluster.GetNamespace(), "clusterClaim", req.ClaimName)
	return nil
}
