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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

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
	serviceManagedByField       = "router.v8s.cedio.dev/managed-by"
	routeFinalizer              = "finalizer.router.v8s.cedio.dev"
)

// +kubebuilder:rbac:groups=router.v8s.cedio.dev,resources=routes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=router.v8s.cedio.dev,resources=routes/status,verbs=get;update;patch

// hasConditionsDenied returns true if status.conditions contains 'Denied'
func (r *RouteReconciler) hasConditionsDenied(ro *routerv1beta1.Route) bool {
	// Check status.conditions do not contain denied
	for _, co := range ro.Status.Conditions {
		if co.Type == routerv1beta1.RouteDenied {
			return true
		}
	}
	return false
}

// appendStatusDenied appends a status.conditions[].type==Denied with message
func (r *RouteReconciler) appendStatusDenied(ro *routerv1beta1.Route, message string) error {
	ro.Status.Conditions = append(ro.Status.Conditions, routerv1beta1.RouteCondition{
		Type:               routerv1beta1.RouteDenied,
		Status:             corev1.ConditionFalse,
		Message:            message,
		LastTransitionTime: &metav1.Time{Time: time.Now()},
	})
	return nil
}

// patchRouteService updates Route related Service with timestamp as touched
func (r *RouteReconciler) patchRouteService(ro *routerv1beta1.Route, service *corev1.Service) error {
	annotations := map[string]string{
		serviceLastManagedTimeField: time.Now().UTC().Format(time.RFC3339),
		serviceManagedByField:       ro.Name,
	}
	for k, v := range service.ObjectMeta.Annotations {
		annotations[k] = v
	}
	service.ObjectMeta.Annotations = annotations
	return nil
}

// patchServiceLoadBalancer patch Service to Loadbalancer type
func (r *RouteReconciler) patchServiceLoadBalancer(ro *routerv1beta1.Route, service *corev1.Service) error {
	service.ObjectMeta.Annotations[addressPoolAnnotationField] = string(ro.Spec.Loadbalancer.AddressPool)
	service.Spec.Type = corev1.ServiceTypeLoadBalancer
	if ro.Spec.Loadbalancer.TargetIP != "" {
		service.Spec.LoadBalancerIP = ro.Spec.Loadbalancer.TargetIP
	}
	return nil
}

// getRouteFromService tries to resolve Route from serviceManagedByField in service
func (r *RouteReconciler) getRouteFromService(req ctrl.Request, ro *routerv1beta1.Route) bool {
	// Fetch Service instance
	service := &corev1.Service{}
	err := r.Get(context.Background(), req.NamespacedName, service)
	if err != nil {
		return false
	}
	if _, ok := service.Annotations[serviceManagedByField]; !ok {
		return false
	}
	err = r.Get(
		context.Background(),
		types.NamespacedName{
			Name:      service.Annotations[serviceManagedByField],
			Namespace: req.Namespace,
		},
		ro,
	)
	if err != nil {
		return false
	}
	return true
}

func (r *RouteReconciler) loadbalancerClass(state State, _route interface{}) error {
	route, _ := _route.(*routerv1beta1.Route)

	service := corev1.Service{}
	err := r.Get(context.Background(), types.NamespacedName{Name: route.Spec.ServiceName, Namespace: route.Namespace}, &service)
	if err != nil {
		return NewProflowRuntimeError("Failed getting Service named in spec.serviceName")
	}
	if route.Spec.Loadbalancer == nil {
		return NewProflowRuntimeError("Missing spec.loadbalancer for spec.type='loadbalancer'")
	}
	err = state(_route, &service)
	if err != nil {
		return NewProflowRuntimeError("Error occurs in changing state of API objects")
	}
	err = r.Update(context.Background(), &service)
	if err != nil {
		return NewProflowRuntimeError("Failed updating Service")
	}
	return nil
}

func (r *RouteReconciler) routeFinalizeCondition(proflow *Proflow, _route interface{}) error {
	route, _ := _route.(*routerv1beta1.Route)

	if route.GetDeletionTimestamp() != nil {
		if contains(route.GetFinalizers(), routeFinalizer) {
			// Run finalization logic. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			if err := proflow.ApplyClass("absent", _route); err != nil {
				return err
			}
			// Remove finalizer. Once all finalizers have been
			// removed, the object will be deleted.
			route.SetFinalizers(remove(route.GetFinalizers(), routeFinalizer))
		}
	} else {
		// Add finalizer for this CR
		if !contains(route.GetFinalizers(), routeFinalizer) {
			route.SetFinalizers(append(route.GetFinalizers(), routeFinalizer))
		}
		if err := proflow.ApplyClass("present", _route); err != nil {
			return err
		}
		service := &corev1.Service{}
		_ = r.Get(context.Background(), types.NamespacedName{Name: route.Spec.ServiceName, Namespace: route.Namespace}, service)
		if len(service.Status.LoadBalancer.Ingress) > 0 {
			// Set status.loadbalancer
			route.Status.Loadbalancer = []routerv1beta1.RouteLoadbalancer{{
				IP: service.Status.LoadBalancer.Ingress[0].IP,
			}}
		}
	}
	return nil
}

func (r *RouteReconciler) loadbalancerPresent(apis ...interface{}) error {
	route, _ := apis[0].(*routerv1beta1.Route)
	service, _ := apis[1].(*corev1.Service)

	err := r.patchRouteService(route, service)
	if err != nil {
		return NewProflowRuntimeError("Failed updating metadata of Service")
	}
	err = r.patchServiceLoadBalancer(route, service)
	if err != nil {
		return NewProflowRuntimeError("Failed patching Service for type loadbalancer")
	}

	return nil
}

// Reconcile K8s API events
func (r *RouteReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	reqLogger := r.Log.WithValues("route", req.NamespacedName)

	// Fetch Route instance
	route := routerv1beta1.Route{}
	err := r.Get(context.Background(), req.NamespacedName, &route)
	if err != nil {
		if errors.IsNotFound(err) {
			if !r.getRouteFromService(req, &route) {
				return ctrl.Result{}, nil
			}
		} else {
			return ctrl.Result{}, err
		}
	}
	reqLogger.Info("Accepted")

	// Reset Route Status
	route.Status = routerv1beta1.RouteStatus{
		Conditions: []routerv1beta1.RouteCondition{},
	}
	err = nil

	// Determine Spec.Type
	switch route.Spec.Type {
	case routerv1beta1.RouteTypeIngress:
		{

		}
	case routerv1beta1.RouteTypeLoadbalancer:
		{
			err = new(Proflow).
				InitClass(r.loadbalancerClass).
				InitCondition(r.routeFinalizeCondition).
				SetState("present", r.loadbalancerPresent).
				// TODO
				// SetState("absent", nil).
				Apply(&route)
		}
	default:
		{
			err = NewProflowRuntimeError("Supported spec.type: ['ingress', 'loadbalancer']")
		}
	}

	// If Proflow returns Error
	if err != nil {
		r.appendStatusDenied(&route, err.Error())
	}

	if !r.hasConditionsDenied(&route) {
		// Baseline status.conditions
		route.Status.Conditions = append(route.Status.Conditions, routerv1beta1.RouteCondition{
			Type:               routerv1beta1.RouteAdmitted,
			Status:             corev1.ConditionTrue,
			Message:            "",
			LastTransitionTime: &metav1.Time{Time: time.Now()},
		})
	}
	_ = r.Status().Update(context.Background(), &route)

	return ctrl.Result{}, nil
}

// SetupWithManager setups controller
func (r *RouteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&routerv1beta1.Route{}).
		// Add extra watch to all Services for CRUD callback
		Watches(&source.Kind{Type: &corev1.Service{}}, &handler.EnqueueRequestForObject{}).
		Complete(r)
}
