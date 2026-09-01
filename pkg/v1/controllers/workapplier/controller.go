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

package workapplier

import (
	"context"
	"sync/atomic"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	placementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/errors"
)

const (
	controllerName = "work-applier"
)

type Reconciler struct {
	hubClient          client.Client
	spokeDynamicClient dynamic.Interface
	spokeClient        client.Client

	restMapper meta.RESTMapper

	ready atomic.Bool
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if !r.ready.Load() {
		klog.V(2).InfoS("Work applier is not yet ready to start; the member agent might still be connecting to the hub cluster",
			"work", req.NamespacedName)
		return ctrl.Result{RequeueAfter: time.Second * 5}, nil
	}

	startTime := time.Now()
	klog.V(2).InfoS("Reconciliation starts", "work", req.NamespacedName, "controller", controllerName)
	defer func() {
		latency := time.Since(startTime).Milliseconds()
		klog.V(2).InfoS("Reconciliation ends", "work", req.NamespacedName, "latency", latency, "controller", controllerName)
	}()

	// Retrieve the work object.
	work := &placementv1alpha1.Work{}
	err := r.hubClient.Get(ctx, req.NamespacedName, work)
	switch {
	case apierrors.IsNotFound(err):
		klog.V(2).InfoS("The work object cannot be found", "work", req.NamespacedName, "controller", controllerName)
		return ctrl.Result{}, nil
	case err != nil:
		wrappedErr := errors.NewAPIServerError(err, "", true, "work", req.NamespacedName, "controller", controllerName)
		klog.ErrorS(err, "Failed to retrieve the work", errors.Args(wrappedErr)...)
		return ctrl.Result{}, wrappedErr
	}

	// Check if the work object is a primary one, i.e., it has the count annotation set.
	_, found := work.GetAnnotations()[placementv1alpha1.LinkedWorkCountAnnotationKey]
	if !found {
		klog.V(2).InfoS("The work object is not a primary one; skipping reconciliation", "work", req.NamespacedName, "controller", controllerName)
		return ctrl.Result{}, nil
	}

	// Retrieve all linked work objects.

	return ctrl.Result{}, nil
}
