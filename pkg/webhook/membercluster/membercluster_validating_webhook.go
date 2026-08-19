package membercluster

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/validator"

	fleetnetworkingv1alpha1 "go.goms.io/fleet-networking/api/v1alpha1"
)

var (
	// ValidationPath is the webhook service path which admission requests are routed to for validating ReplicaSet resources.
	ValidationPath = fmt.Sprintf(utils.ValidationPathFmt, clusterv1beta1.GroupVersion.Group, clusterv1beta1.GroupVersion.Version, "membercluster")
)

type memberClusterValidator struct {
	client                  client.Client
	decoder                 webhook.AdmissionDecoder
	networkingAgentsEnabled bool
}

// Add registers the webhook for K8s built-in object types.
func Add(mgr manager.Manager, networkingAgentsEnabled bool) {
	hookServer := mgr.GetWebhookServer()
	hookServer.Register(ValidationPath, &webhook.Admission{Handler: &memberClusterValidator{
		client:                  mgr.GetClient(),
		decoder:                 admission.NewDecoder(mgr.GetScheme()),
		networkingAgentsEnabled: networkingAgentsEnabled,
	}})
}

// Handle memberClusterValidator checks to see if member cluster has valid fields.
func (v *memberClusterValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	mcObjectName := types.NamespacedName{Name: req.Name, Namespace: req.Namespace}
	klog.V(2).InfoS("Validating webhook handling member cluster", "operation", req.Operation, "memberCluster", mcObjectName)

	var mc clusterv1beta1.MemberCluster
	if req.Operation == admissionv1.Delete { // Will reject the requests whenever the serviceExport is not deleted
		if err := v.decoder.DecodeRaw(req.OldObject, &mc); err != nil {
			klog.ErrorS(err, "Failed to decode member cluster object for validating fields", "userName", req.UserInfo.Username, "groups", req.UserInfo.Groups)
			return admission.Errored(http.StatusBadRequest, err)
		}

		if mc.Spec.DeleteOptions != nil && mc.Spec.DeleteOptions.ValidationMode == clusterv1beta1.DeleteValidationModeSkip {
			klog.V(2).InfoS("Skipping validation for member cluster DELETE when the validation mode is set to skip", "memberCluster", mcObjectName)
			return admission.Allowed("Skipping validation for member cluster DELETE when the validation mode is set to skip")
		}
		if !v.networkingAgentsEnabled {
			klog.V(2).InfoS("Networking agents disabled; skipping ServiceExport validation", "memberCluster", mcObjectName)
			return admission.Allowed("Networking agents disabled; skipping ServiceExport validation")
		}

		klog.V(2).InfoS("Validating webhook member cluster DELETE", "memberCluster", mcObjectName)
		namespaceName := fmt.Sprintf(utils.NamespaceNameFormat, mcObjectName.Name)
		internalServiceExportList := &fleetnetworkingv1alpha1.InternalServiceExportList{}
		if err := v.client.List(ctx, internalServiceExportList, client.InNamespace(namespaceName)); err != nil {
			klog.ErrorS(err, "Failed to list internalServiceExportList when validating")
			return admission.Errored(http.StatusInternalServerError, fmt.Errorf("failed to list internalServiceExportList, please retry the request: %w", err))
		}
		for _, internalServiceExport := range internalServiceExportList.Items {
			if internalServiceExport.DeletionTimestamp.IsZero() {
				klog.Warning("ServiceExport exists in the member cluster, request is denied", "operation", req.Operation, "memberCluster", mcObjectName)
				return admission.Denied(fmt.Sprintf("Please delete serviceExport %s in the member cluster before leaving, request is denied", internalServiceExport.Spec.ServiceReference.NamespacedName))
			}
		}
		return admission.Allowed("Member cluster is ready to leave")
	}

	if err := v.decoder.Decode(req, &mc); err != nil {
		klog.ErrorS(err, "Failed to decode member cluster object for validating fields", "userName", req.UserInfo.Username, "groups", req.UserInfo.Groups)
		return admission.Errored(http.StatusBadRequest, err)
	}

	if err := validator.ValidateMemberCluster(mc); err != nil {
		klog.V(2).ErrorS(err, "Member cluster has invalid fields, request is denied", "operation", req.Operation, "memberCluster", mcObjectName)
		return admission.Denied(err.Error())
	}

	response := admission.Allowed("Member cluster has valid fields")
	if warning := v.clusterAliasCollisionWarning(ctx, &mc); warning != "" {
		response = response.WithWarnings(warning)
	}
	return response
}

// clusterAliasCollisionWarning returns a warning message if another member cluster already carries
// the alias this one is being labelled with, or the empty string otherwise.
//
// The alias selects a cluster by a name of the admin's choosing, so it is meant to identify one
// cluster; two clusters sharing an alias makes an alias-based selector match both. It is only a
// warning, never a denial: labelling a replacement cluster with the outgoing one's alias before
// removing it from the outgoing one is exactly the handoff the alias exists to allow, and that
// handoff passes through a state where two clusters share the alias. For the same reason a failure
// to list the member clusters does not block the request -- an advisory check must not stand
// between an admin and the cluster they are registering.
func (v *memberClusterValidator) clusterAliasCollisionWarning(ctx context.Context, mc *clusterv1beta1.MemberCluster) string {
	alias, ok := mc.Labels[kfplacementv1alpha1.ClusterAliasLabel]
	if !ok || alias == "" {
		return ""
	}

	memberClusterList := &clusterv1beta1.MemberClusterList{}
	if err := v.client.List(ctx, memberClusterList); err != nil {
		klog.V(2).ErrorS(err, "Failed to list member clusters for the alias uniqueness check; admitting without a warning", "memberCluster", klog.KObj(mc))
		return ""
	}

	holders := make([]string, 0, len(memberClusterList.Items))
	for i := range memberClusterList.Items {
		other := &memberClusterList.Items[i]
		if other.Name == mc.Name {
			continue
		}
		if other.Labels[kfplacementv1alpha1.ClusterAliasLabel] == alias {
			holders = append(holders, other.Name)
		}
	}
	if len(holders) == 0 {
		return ""
	}
	// The message leads with the alias value and lists at most a few holders: an admission warning
	// is truncated by the API server past 256 bytes, and the admin already knows which label they
	// set, so the actionable half -- the value and who else holds it -- must fit inside that budget.
	const maxListedHolders = 3
	listed := holders
	if len(listed) > maxListedHolders {
		listed = append(listed[:maxListedHolders:maxListedHolders], fmt.Sprintf("and %d more", len(holders)-maxListedHolders))
	}
	return fmt.Sprintf("cluster alias %q is already used by %s; an alias-based cluster selector will match more than one cluster while this is the case", alias, strings.Join(listed, ", "))
}
