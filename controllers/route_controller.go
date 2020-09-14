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
func (r *RouteReconciler) hasConditionsDenied(routePtr *routerv1beta1.Route) bool {
	// Check status.conditions do not contain denied
	for _, co := range routePtr.Status.Conditions {
		if co.Type == routerv1beta1.RouteDenied {
			return true
		}
	}
	return false
}

// appendStatusDenied appends a status.conditions[].type==Denied with message
func (r *RouteReconciler) appendStatusDenied(routePtr *routerv1beta1.Route, message string) error {
	routePtr.Status.Conditions = append(routePtr.Status.Conditions, routerv1beta1.RouteCondition{
		Type:               routerv1beta1.RouteDenied,
		Status:             corev1.ConditionFalse,
		Message:            message,
		LastTransitionTime: &metav1.Time{Time: time.Now()},
	})
	return nil
}

// patchRouteService updates Route related Service with timestamp as touched
func (r *RouteReconciler) patchRouteService(routePtr *routerv1beta1.Route, servicePtr *corev1.Service) error {
	annotations := map[string]string{
		serviceLastManagedTimeField: time.Now().UTC().Format(time.RFC3339),
		serviceManagedByField:       routePtr.Name,
	}
	for k, v := range servicePtr.ObjectMeta.Annotations {
		annotations[k] = v
	}
	servicePtr.ObjectMeta.Annotations = annotations
	return nil
}

func (r *RouteReconciler) revokeRouteService(routePtr *routerv1beta1.Route, servicePtr *corev1.Service) error {
	delete(servicePtr.ObjectMeta.Annotations, serviceLastManagedTimeField)
	delete(servicePtr.ObjectMeta.Annotations, serviceManagedByField)

	return nil
}

// patchServiceLoadBalancer patch Service to Loadbalancer type
func (r *RouteReconciler) patchServiceLoadBalancer(routePtr *routerv1beta1.Route, servicePtr *corev1.Service) error {
	servicePtr.ObjectMeta.Annotations[addressPoolAnnotationField] = string(routePtr.Spec.Loadbalancer.AddressPool)
	servicePtr.Spec.Type = corev1.ServiceTypeLoadBalancer
	if routePtr.Spec.Loadbalancer.TargetIP != "" {
		servicePtr.Spec.LoadBalancerIP = routePtr.Spec.Loadbalancer.TargetIP
	}
	return nil
}

func (r *RouteReconciler) revokeServiceLoadbalancer(routePtr *routerv1beta1.Route, servicePtr *corev1.Service) error {
	delete(servicePtr.ObjectMeta.Annotations, addressPoolAnnotationField)
	servicePtr.Spec.Type = corev1.ServiceTypeClusterIP
	servicePtr.Spec.Ports[0].NodePort = 0
	servicePtr.Spec.LoadBalancerIP = ""

	return nil
}

// getRouteFromService tries to resolve Route from serviceManagedByField in service
func (r *RouteReconciler) getRouteFromService(req ctrl.Request, routePtr *routerv1beta1.Route) bool {
	// Fetch Service instance
	servicePtr := &corev1.Service{}
	err := r.Get(context.Background(), req.NamespacedName, servicePtr)
	if err != nil {
		return false
	}
	if _, ok := servicePtr.Annotations[serviceManagedByField]; !ok {
		return false
	}
	err = r.Get(
		context.Background(),
		types.NamespacedName{
			Name:      servicePtr.Annotations[serviceManagedByField],
			Namespace: req.Namespace,
		},
		routePtr,
	)
	if err != nil {
		return false
	}
	return true
}

func (r *RouteReconciler) routeFinalizeCondition(proflow *Proflow, apis ...interface{}) error {
	routePtr, _ := apis[0].(*routerv1beta1.Route)

	if routePtr.GetDeletionTimestamp() != nil {
		if contains(routePtr.GetFinalizers(), routeFinalizer) {
			// Run finalization logic. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			if err := proflow.ApplyClass("absent"); err != nil {
				return err
			}
			// Remove finalizer. Once all finalizers have been
			// removed, the object will be deleted.
			routePtr.SetFinalizers(remove(routePtr.GetFinalizers(), routeFinalizer))
		}
	} else {
		// Add finalizer for this CR
		if !contains(routePtr.GetFinalizers(), routeFinalizer) {
			routePtr.SetFinalizers(append(routePtr.GetFinalizers(), routeFinalizer))
		}
		if err := proflow.ApplyClass("present"); err != nil {
			return err
		}
		service := &corev1.Service{}
		_ = r.Get(context.Background(), types.NamespacedName{Name: routePtr.Spec.ServiceName, Namespace: routePtr.Namespace}, service)
		if len(service.Status.LoadBalancer.Ingress) > 0 {
			// Set status.loadbalancer
			routePtr.Status.Loadbalancer = []routerv1beta1.RouteLoadbalancer{{
				IP: service.Status.LoadBalancer.Ingress[0].IP,
			}}
		}
	}
	return nil
}

func (r *RouteReconciler) ingressClass(state State, apis ...interface{}) error {
	//routePtr, _ := apis[0].(*routerv1beta1.Route)
	return nil
}

func (r *RouteReconciler) loadbalancerClass(state State, apis ...interface{}) error {
	routePtr, _ := apis[0].(*routerv1beta1.Route)

	service := corev1.Service{}
	err := r.Get(context.Background(), types.NamespacedName{Name: routePtr.Spec.ServiceName, Namespace: routePtr.Namespace}, &service)
	if err != nil {
		return NewProflowRuntimeError("Failed getting Service named in spec.serviceName")
	}
	if routePtr.Spec.Loadbalancer == nil {
		return NewProflowRuntimeError("Missing spec.loadbalancer for spec.type='loadbalancer'")
	}
	err = state(routePtr, &service)
	if err != nil {
		return NewProflowRuntimeError("Error occurs in changing state of API objects")
	}
	err = r.Update(context.Background(), &service)
	if err != nil {
		return NewProflowRuntimeError("Failed updating Service")
	}
	return nil
}

func (r *RouteReconciler) loadbalancerPresent(apis ...interface{}) error {
	routePtr, _ := apis[0].(*routerv1beta1.Route)
	servicePtr, _ := apis[1].(*corev1.Service)

	err := r.patchRouteService(routePtr, servicePtr)
	if err != nil {
		return NewProflowRuntimeError("Failed applying metadata of Service")
	}
	err = r.patchServiceLoadBalancer(routePtr, servicePtr)
	if err != nil {
		return NewProflowRuntimeError("Failed applying Service for type loadbalancer")
	}

	return nil
}

func (r *RouteReconciler) loadbalancerAbsent(apis ...interface{}) error {
	routePtr, _ := apis[0].(*routerv1beta1.Route)
	servicePtr, _ := apis[1].(*corev1.Service)

	err := r.revokeRouteService(routePtr, servicePtr)
	if err != nil {
		return NewProflowRuntimeError("Failed revoking metadata of Service")
	}
	err = r.revokeServiceLoadbalancer(routePtr, servicePtr)
	if err != nil {
		return NewProflowRuntimeError("Failed revoking Service for type loadbalancer")
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

	// Setup Proflow
	proflows := map[string]*Proflow{
		"ingress":      {Name: "Ingress"},
		"loadbalancer": {Name: "Loadbalancer"},
	}
	proflows["loadbalancer"].
		Init(r.loadbalancerClass, r.routeFinalizeCondition).
		SetAPI(&route).
		SetState("present", r.loadbalancerPresent).
		SetState("absent", r.loadbalancerAbsent)

	// Determine Spec.Type
	switch route.Spec.Type {
	case routerv1beta1.RouteTypeIngress:
		{

		}
	case routerv1beta1.RouteTypeLoadbalancer:
		{
			err = proflows["loadbalancer"].Apply()
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

	routeDup := route
	r.Status().Update(context.Background(), &route)
	r.Update(context.Background(), &routeDup)

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
