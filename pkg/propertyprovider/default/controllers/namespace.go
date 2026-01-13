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

// Package controllers feature a number of controllers that are in use
// by the default property provider.
package controllers

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kubefleet-dev/kubefleet/pkg/propertyprovider/default/trackers"
)

// NamespaceReconciler reconciles Namespace objects.
type NamespaceReconciler struct {
	NamespaceTracker *trackers.NamespaceTracker
	Client           client.Client
}

// Reconcile reconciles a namespace object.
func (r *NamespaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	namespaceRef := klog.KRef(req.Namespace, req.Name)
	startTime := time.Now()
	klog.V(2).InfoS("Reconciliation starts for namespace objects in the property provider", "namespace", namespaceRef)
	defer func() {
		latency := time.Since(startTime).Milliseconds()
		klog.V(2).InfoS("Reconciliation ends for namespace objects in the property provider", "namespace", namespaceRef, "latency", latency)
	}()

	namespace := &corev1.Namespace{}
	if err := r.Client.Get(ctx, req.NamespacedName, namespace); err != nil {
		if errors.IsNotFound(err) {
			klog.V(2).InfoS("Namespace is not found; untrack it from the property provider", "namespace", namespaceRef)
			r.NamespaceTracker.Remove(req.Name)
			return ctrl.Result{}, nil
		}
		klog.ErrorS(err, "Failed to get the namespace object", "namespace", namespaceRef)
		return ctrl.Result{}, err
	}

	// Note that the tracker will attempt to untrack the namespace if it has been marked
	// for deletion or is in a terminating state. So that the new workloads will not be scheduled
	// to this namespace.
	if namespace.DeletionTimestamp != nil {
		klog.V(2).InfoS("Namespace is marked for deletion; untrack it from the property provider", "namespace", namespaceRef)
		r.NamespaceTracker.Remove(req.Name)
		return ctrl.Result{}, nil
	}

	// Track the namespace. If it has been tracked, update its information with the tracker.
	klog.V(2).InfoS("Attempt to track the namespace", "namespace", namespaceRef)
	r.NamespaceTracker.AddOrUpdate(namespace)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *NamespaceReconciler) SetupWithManager(mgr ctrl.Manager, controllerName string) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		For(&corev1.Namespace{}).
		Complete(r)
}
