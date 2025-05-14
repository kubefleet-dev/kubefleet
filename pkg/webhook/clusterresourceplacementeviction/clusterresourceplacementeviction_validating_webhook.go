/*
Copyright 2025 The KubeFleet Authors.

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

// Package clusterresourceplacementeviction provides a validating webhook for the clusterresourceplacementeviction custom resource in the KubeFleet API group.
package clusterresourceplacementeviction

import (
	"context"
	"fmt"
	"net/http"

	fleetv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/condition"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/validator"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var (
	// ValidationPath is the webhook service path which admission requests are routed to for validating clusterresourceplacementeviction resources.
	ValidationPath = fmt.Sprintf(utils.ValidationPathFmt, fleetv1beta1.GroupVersion.Group, fleetv1beta1.GroupVersion.Version, "clusterresourceplacementeviction")
)

type clusterResourcePlacementEvictionValidator struct {
	client client.Client
	// UncachedReader is only used to read disruption budget objects directly from the API server to ensure we can enforce the disruption budget for eviction.
	UncachedReader client.Reader
	decoder        webhook.AdmissionDecoder
}

// Add registers the webhook for K8s bulit-in object types.
func Add(mgr manager.Manager) error {
	uncachedReader, err := client.New(mgr.GetConfig(), client.Options{Scheme: mgr.GetScheme()})
	if err != nil {
		return err
	}
	hookServer := mgr.GetWebhookServer()
	hookServer.Register(ValidationPath, &webhook.Admission{Handler: &clusterResourcePlacementEvictionValidator{mgr.GetClient(), uncachedReader, admission.NewDecoder(mgr.GetScheme())}})
	return nil
}

// Handle clusterResourcePlacementEvictionValidator checks to see if resource override is valid.
func (v *clusterResourcePlacementEvictionValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	var crpe fleetv1beta1.ClusterResourcePlacementEviction
	klog.V(2).InfoS("Validating webhook handling cluster resource placement eviction", "operation", req.Operation)
	if err := v.decoder.Decode(req, &crpe); err != nil {
		klog.ErrorS(err, "Failed to decode cluster resource placement eviction object for validating fields", "userName", req.UserInfo.Username, "groups", req.UserInfo.Groups)
		return admission.Errored(http.StatusBadRequest, err)
	}

	// Get the ClusterResourcePlacement object
	var crp fleetv1beta1.ClusterResourcePlacement
	if err := v.client.Get(ctx, types.NamespacedName{Name: crpe.Spec.PlacementName}, &crp); err != nil {
		if k8serrors.IsNotFound(err) {
			klog.V(2).InfoS(condition.EvictionInvalidMissingCRPMessage, "clusterResourcePlacementEviction", crpe.Name, "clusterResourcePlacement", crpe.Spec.PlacementName)
			return admission.Denied(err.Error())
		}
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("failed to get clusterResourcePlacement %s: %w", crpe.Spec.PlacementName, err))
	}

	// Check ClusterResourcePlacementDisruptionBudget object
	var db fleetv1beta1.ClusterResourcePlacementDisruptionBudget
	if err := v.UncachedReader.Get(ctx, types.NamespacedName{Name: crp.Name}, &db); err != nil {
		if k8serrors.IsNotFound(err) {
			klog.V(2).InfoS(condition.EvictionAllowedNoPDBMessage, "clusterResourcePlacementEviction", crpe.Name, "clusterResourcePlacementDisruptionBudget", crp.Name)
			return admission.Allowed("clusterResourcePlacementDisruptionBudget not found, eviction is allowed")
		}
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("failed to get clusterResourcePlacementDisruptionBudget %s: %w", crp.Name, err))
	}

	if err := validator.ValidateClusterResourcePlacementForEviction(crp, db); err != nil {
		klog.V(2).ErrorS(err, "ClusterResourcePlacement has invalid fields, request is denied", "operation", req.Operation)
		return admission.Denied(err.Error())
	}

	return admission.Allowed("clusterResourcePlacementEviction has valid fields")
}
