/*
Copyright 2020 cedio.

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

package controllers

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	routerv1beta1 "v8s-router/api/v1beta1"
)

// RouteReconciler reconciles a Route object
type RouteReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

const (
	addressPoolAnnotationField  = "metallb.universe.tf/address-pool"
	serviceLastManagedTimeField = "router.v8s.cedio.dev/last-managed-time"
	serviceRouteTypeField       = "router.v8s.cedio.dev/type"
)

// +kubebuilder:rbac:groups=router.v8s.cedio.dev,resources=routes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=router.v8s.cedio.dev,resources=routes/status,verbs=get;update;patch

func (r *RouteReconciler) patchRouteService(ro *routerv1beta1.Route, service *corev1.Service) error {
	annotations := map[string]string{
		serviceLastManagedTimeField: time.Now().Format(time.RFC3339),
		serviceRouteTypeField:       string(ro.Spec.Type),
	}
	service.ObjectMeta.Annotations = annotations
	return nil
}

func (r *RouteReconciler) patchServiceLoadBalancer(ro *routerv1beta1.Route, service *corev1.Service) error {
	service.ObjectMeta.Annotations[addressPoolAnnotationField] = string(ro.Spec.Loadbalancer.AddressPool)
	service.Spec.Type = corev1.ServiceTypeLoadBalancer
	if ro.Spec.Loadbalancer.TargetIP != "" {
		service.Spec.LoadBalancerIP = ro.Spec.Loadbalancer.TargetIP
	}
	return nil
}

func (r *RouteReconciler) routeLoadBalancer(ro *routerv1beta1.Route, reqLogger logr.Logger) (*ctrl.Result, error) {
	service := &corev1.Service{}
	err := r.Get(context.Background(), types.NamespacedName{Name: ro.Spec.ServiceName, Namespace: ro.Namespace}, service)
	if err != nil {
		reqLogger.Error(err, "Failed to get Route Service")
		return &reconcile.Result{}, err
	}
	if ro.Spec.Loadbalancer == nil {
		reqLogger.Error(err, "Error getting spec.loadbalancer")
		return &reconcile.Result{}, errors.NewBadRequest("Missing spec.loadbalancer for spec.type='loadbalancer'")
	}
	err = r.patchRouteService(ro, service)
	if err != nil {
		reqLogger.Error(err, "Error patching Route Service")
		return &reconcile.Result{}, err
	}
	err = r.patchServiceLoadBalancer(ro, service)
	if err != nil {
		reqLogger.Error(err, "Error patching Service to LoadBalancer")
		return &reconcile.Result{}, err
	}
	err = r.Update(context.Background(), service)
	if err != nil {
		reqLogger.Error(err, "Failed to update Route Service", "Service.Namespace", service.Namespace, "Service.Name", service.Name)
		return &reconcile.Result{}, err
	}
	return nil, nil
}

// Reconcile K8s API events
func (r *RouteReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	_ = context.Background()
	reqLogger := r.Log.WithValues("route", req.NamespacedName)

	// Fetch instance
	ro := &routerv1beta1.Route{}
	err := r.Get(context.Background(), req.NamespacedName, ro)
	if err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Determine Spec.Type
	switch ro.Spec.Type {
	case routerv1beta1.RouteTypeIngress:

	case routerv1beta1.RouteTypeLoadbalancer:
		// Patch related Service to regarding type
		reconcileResult, err := r.routeLoadBalancer(ro, reqLogger)
		if err != nil {
			ro.Status.Loadbalancer = nil
			if err := r.Status().Update(context.Background(), ro); err != nil {
				reqLogger.Error(err, "Failed to update Route Status")
			}
			return *reconcileResult, err
		} else if err == nil && reconcileResult != nil {
			// In case requeue required
			return *reconcileResult, nil
		}
	default:
		return ctrl.Result{}, errors.NewBadRequest("Supported spec.type: ['ingress', 'loadbalancer']")
	}

	return ctrl.Result{}, nil
}

// SetupWithManager setups controller
func (r *RouteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&routerv1beta1.Route{}).
		Complete(r)
}
