package clusterresourceplacement

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/defaulter"
)

var (
	// MutatingPath is the webhook service path for mutating v1beta1 CRP resources.
	MutatingPath = fmt.Sprintf(utils.MutatingPathFmt, v1beta1.GroupVersion.Group, v1beta1.GroupVersion.Version, "clusterresourceplacement")
)

type clusterResourcePlacementMutator struct {
	decoder webhook.AdmissionDecoder
}

// AddMutating registers the mutating webhook for v1beta1 CRP.
func AddMutating(mgr manager.Manager) error {
	hookServer := mgr.GetWebhookServer()
	hookServer.Register(MutatingPath, &webhook.Admission{Handler: &clusterResourcePlacementMutator{admission.NewDecoder(mgr.GetScheme())}})
	return nil
}

// Handle mutates CRP objects on create and update.
func (m *clusterResourcePlacementMutator) Handle(_ context.Context, req admission.Request) admission.Response {
	var crp v1beta1.ClusterResourcePlacement
	if req.Operation == admissionv1.Create || req.Operation == admissionv1.Update {
		klog.V(2).InfoS("handling CRP", "operation", req.Operation, "namespacedName", types.NamespacedName{Name: req.Name})
		// Decode the request object into a ClusterResourcePlacement.
		if err := m.decoder.Decode(req, &crp); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}

		// Apply default values to the CRP object.
		defaulter.SetDefaultsClusterResourcePlacement(&crp)
		marshaled, err := json.Marshal(crp)
		if err != nil {
			return admission.Errored(http.StatusInternalServerError, err)
		}
		klog.V(2).InfoS("mutating CRP", "operation", req.Operation, "namespacedName", types.NamespacedName{Name: req.Name})
		return admission.PatchResponseFromRaw(req.Object.Raw, marshaled)
	}
	klog.V(2).InfoS("skipping CRP mutation", "operation", req.Operation, "namespacedName", types.NamespacedName{Name: req.Name})
	return admission.Allowed("no mutation required")
}
