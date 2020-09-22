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
	"fmt"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	netv1beta1 "k8s.io/api/networking/v1beta1"
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

// Service constants
const (
	addressPoolAnnotationField = "metallb.universe.tf/address-pool"
)

// Route constants
const (
	routeFinalizer       = "finalizer.router.v8s.cedio.dev"
	lastManagedTimeField = "router.v8s.cedio.dev/last-managed-time"
	managedByField       = "router.v8s.cedio.dev/managed-by"
)

// Nginx Ingress constants
const (
	nginxClassField           = "kubernetes.io/ingress.class"
	nginxSSLRedirectField     = "nginx.ingress.kubernetes.io/ssl-redirect"
	nginxSSLPassthroughField  = "nginx.ingress.kubernetes.io/ssl-passthrough"
	nginxBackendProtocolField = "nginx.ingress.kubernetes.io/backend-protocol"
)

// HAProxy Ingress constants
const (
	haproxyClassField          = "haproxy.org/ingress.class"
	haproxyServerSSLField      = "haproxy.org/server-ssl"
	haproxySSLRedirectField    = "haproxy.org/ssl-redirect"
	haproxySSLPassthroughField = "haproxy.org/ssl-passthrough"
)

// Pass from Helm Chart during installation
var (
	// Cluster domain for generating default host i.e. <service name>-<namespace name>.<subdomain>.<cluster-domain>
	clusterDomain = "v8s.lab"

	// HAProxy typed subdomain
	haproxySubDomain = "apps1"

	// Nginx typed subdomain
	nginxSubDomain = "apps2"
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
	if servicePtr.ObjectMeta.Annotations == nil {
		servicePtr.ObjectMeta.Annotations = map[string]string{}
	}
	servicePtr.ObjectMeta.Annotations[lastManagedTimeField] = time.Now().UTC().Format(time.RFC3339)
	servicePtr.ObjectMeta.Annotations[managedByField] = routePtr.Name

	return nil
}

func (r *RouteReconciler) revokeRouteService(routePtr *routerv1beta1.Route, servicePtr *corev1.Service) error {
	delete(servicePtr.ObjectMeta.Annotations, lastManagedTimeField)
	delete(servicePtr.ObjectMeta.Annotations, managedByField)

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
	if _, ok := servicePtr.Annotations[managedByField]; !ok {
		return false
	}
	err = r.Get(
		context.Background(),
		types.NamespacedName{
			Name:      servicePtr.Annotations[managedByField],
			Namespace: req.Namespace,
		},
		routePtr,
	)
	if err != nil {
		return false
	}
	return true
}

func (r *RouteReconciler) resetRouteStatus(routePtr *routerv1beta1.Route) {
	routePtr.Status = routerv1beta1.RouteStatus{Conditions: []routerv1beta1.RouteCondition{}}
}

func (r *RouteReconciler) routeFinalizeCondition(proflows map[string]*Proflow, reqLogger logr.Logger) Condition {
	return func(proflow *Proflow, apis ...interface{}) error {
		routePtr, _ := apis[0].(*routerv1beta1.Route)

		if routePtr.GetDeletionTimestamp() != nil {
			r.resetRouteStatus(routePtr)
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

				reqLogger.Info("Deleted")
			}
		} else {
			// Add finalizer for this CR
			if !contains(routePtr.GetFinalizers(), routeFinalizer) {
				routePtr.SetFinalizers(append(routePtr.GetFinalizers(), routeFinalizer))
			}

			// Deprovision current class
			currentClass := ""
			if routePtr.Status.Ingress != nil && len(routePtr.Status.Ingress) > 0 {
				currentClass = "ingress"
			} else if routePtr.Status.Loadbalancer != nil && len(routePtr.Status.Loadbalancer) > 0 {
				currentClass = "loadbalancer"
			}

			if currentClass != "" && currentClass != string(routePtr.Spec.Type) {
				reqLogger.Info(fmt.Sprintf("Changing class from %s to %s", currentClass, string(routePtr.Spec.Type)))
				if err := proflows[currentClass].ApplyClass("absent"); err != nil {
					return err
				}
			}
			r.resetRouteStatus(routePtr)

			if err := proflow.ApplyClass("present"); err != nil {
				return err
			}

			reqLogger.Info("Updated")
		}
		return nil
	}
}

func (r *RouteReconciler) getIngress(routePtr *routerv1beta1.Route, ingressPtr *netv1beta1.Ingress) error {
	return r.Get(
		context.Background(),
		types.NamespacedName{
			Name:      fmt.Sprintf("%s-ingress", routePtr.Name),
			Namespace: routePtr.Namespace,
		},
		ingressPtr,
	)
}

func (r *RouteReconciler) ingressClass(state State, apis ...interface{}) error {
	routePtr, _ := apis[0].(*routerv1beta1.Route)

	ingress := netv1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{},
			Labels:      map[string]string{},
		},
		Spec: netv1beta1.IngressSpec{
			Rules: []netv1beta1.IngressRule{{
				IngressRuleValue: netv1beta1.IngressRuleValue{
					HTTP: &netv1beta1.HTTPIngressRuleValue{
						Paths: []netv1beta1.HTTPIngressPath{{}},
					},
				},
			}},
		},
	}

	ingressErr := r.getIngress(routePtr, &ingress)
	if ingressErr != nil && !errors.IsNotFound(ingressErr) {
		return NewProflowRuntimeError("Error in getting related Ingress")
	}

	voidFlag := false
	err := state(routePtr, &ingress, &voidFlag)
	if err != nil {
		return err
	}

	if voidFlag {
		if errors.IsNotFound(ingressErr) {
			return nil
		}
		err = r.Delete(context.Background(), &ingress)
		if err != nil {
			return NewProflowRuntimeError("Failed deleting Ingress")
		}
	}

	if errors.IsNotFound(ingressErr) {
		err = r.Create(context.Background(), &ingress)
		if err != nil {
			return NewProflowRuntimeError("Failed creating Ingress")
		}
	} else {
		err = r.Update(context.Background(), &ingress)
		if err != nil {
			return NewProflowRuntimeError("Failed updating Ingress")
		}
	}

	port := 80
	if routePtr.Spec.Ingress.TLS != nil {
		port = 443
	}
	r.getIngress(routePtr, &ingress)
	routePtr.Status.Ingress = []routerv1beta1.RouteIngress{{
		Host: ingress.Spec.Rules[0].Host,
		Port: &port,
	}}

	return nil
}

func (r *RouteReconciler) generateHostName(routePtr *routerv1beta1.Route, subdomain string) string {
	if routePtr.Spec.Ingress.Host != "" {
		return routePtr.Spec.Ingress.Host
	}
	return fmt.Sprintf("%s-%s.%s.%s", routePtr.Spec.ServiceName, routePtr.Namespace, subdomain, clusterDomain)
}

func (r *RouteReconciler) patchIngressClassNginx(routePtr *routerv1beta1.Route, ingressPtr *netv1beta1.Ingress) error {
	ingressPtr.ObjectMeta.Annotations[nginxClassField] = "nginx"

	// Set hostname
	ingressPtr.Spec.Rules[0].Host = r.generateHostName(routePtr, nginxSubDomain)

	// No TLS
	if routePtr.Spec.Ingress.TLS == nil {
		ingressPtr.ObjectMeta.Annotations[nginxSSLRedirectField] = "false"
		return nil
	}

	// With TLS
	switch routePtr.Spec.Ingress.TLS.Termination {
	case routerv1beta1.TLSTerminationPassthrough:
		{
			ingressPtr.ObjectMeta.Annotations[nginxSSLPassthroughField] = "true"
			ingressPtr.ObjectMeta.Annotations[nginxBackendProtocolField] = "HTTPS"
		}
	case routerv1beta1.TLSTerminationEdge, routerv1beta1.TLSTerminationReencrypt:
		{
			ingressPtr.Spec.TLS = []netv1beta1.IngressTLS{{
				Hosts:      []string{ingressPtr.Spec.Rules[0].Host},
				SecretName: routePtr.Spec.Ingress.TLS.TLSSecretName,
			}}
			ingressPtr.ObjectMeta.Annotations[nginxSSLRedirectField] = strconv.FormatBool(routePtr.Spec.Ingress.TLS.SSLRedirect)

			if routePtr.Spec.Ingress.TLS.Termination == routerv1beta1.TLSTerminationReencrypt {
				ingressPtr.ObjectMeta.Annotations[nginxBackendProtocolField] = "HTTPS"
			}
		}
	}

	return nil
}

func (r *RouteReconciler) patchIngressClassHAProxy(routePtr *routerv1beta1.Route, ingressPtr *netv1beta1.Ingress) error {
	ingressPtr.ObjectMeta.Annotations[haproxyClassField] = "haproxy"

	// Set hostname
	ingressPtr.Spec.Rules[0].Host = r.generateHostName(routePtr, haproxySubDomain)

	// No TLS
	if routePtr.Spec.Ingress.TLS == nil {
		ingressPtr.ObjectMeta.Annotations[haproxyServerSSLField] = "false"
		ingressPtr.ObjectMeta.Annotations[haproxySSLRedirectField] = "false"
		return nil
	}

	// With TLS
	switch routePtr.Spec.Ingress.TLS.Termination {
	case routerv1beta1.TLSTerminationPassthrough:
		{
			ingressPtr.ObjectMeta.Annotations[haproxySSLPassthroughField] = "true"
		}
	case routerv1beta1.TLSTerminationEdge, routerv1beta1.TLSTerminationReencrypt:
		{
			ingressPtr.Spec.TLS = []netv1beta1.IngressTLS{{
				Hosts:      []string{ingressPtr.Spec.Rules[0].Host},
				SecretName: routePtr.Spec.Ingress.TLS.TLSSecretName,
			}}
			ingressPtr.ObjectMeta.Annotations[haproxySSLRedirectField] = strconv.FormatBool(routePtr.Spec.Ingress.TLS.SSLRedirect)

			if routePtr.Spec.Ingress.TLS.Termination == routerv1beta1.TLSTerminationReencrypt {
				ingressPtr.ObjectMeta.Annotations[haproxyServerSSLField] = "true"
			}
		}
	}

	return nil
}

func (r *RouteReconciler) ingressPresent(apis ...interface{}) error {
	routePtr, _ := apis[0].(*routerv1beta1.Route)
	ingressPtr, _ := apis[1].(*netv1beta1.Ingress)

	ingressPtr.ObjectMeta.Name = fmt.Sprintf("%s-ingress", routePtr.Name)
	ingressPtr.ObjectMeta.Namespace = routePtr.Namespace
	ingressPtr.ObjectMeta.Annotations[lastManagedTimeField] = time.Now().UTC().Format(time.RFC3339)
	ingressPtr.ObjectMeta.Annotations[managedByField] = routePtr.Name

	ingressPtr.Spec.Rules[0].IngressRuleValue.HTTP.Paths[0].Path = "/"
	ingressPtr.Spec.Rules[0].IngressRuleValue.HTTP.Paths[0].Backend.ServiceName = routePtr.Spec.ServiceName
	ingressPtr.Spec.Rules[0].IngressRuleValue.HTTP.Paths[0].Backend.ServicePort = routePtr.Spec.Ingress.ServicePort

	patcher := r.patchIngressClassHAProxy

	switch routePtr.Spec.Ingress.Class {
	case routerv1beta1.IngressClassHAProxy:
		patcher = r.patchIngressClassHAProxy
	case routerv1beta1.IngressClassNginx:
		patcher = r.patchIngressClassNginx
	default:
		return NewProflowRuntimeError("Not supported spec.ingress.class from [nginx, haproxy]")
	}

	err := patcher(routePtr, ingressPtr)
	if err != nil {
		return NewProflowRuntimeError("Error in patching typed Ingress")
	}

	return nil
}

func (r *RouteReconciler) ingressAbsent(apis ...interface{}) error {
	voidFlagPtr, _ := apis[2].(*bool)
	*voidFlagPtr = true
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
		return err
	}

	err = r.Update(context.Background(), &service)
	if err != nil {
		return NewProflowRuntimeError("Failed updating Service")
	}

	r.Get(context.Background(), types.NamespacedName{Name: routePtr.Spec.ServiceName, Namespace: routePtr.Namespace}, &service)
	if len(service.Status.LoadBalancer.Ingress) > 0 {
		// Set status.loadbalancer
		routePtr.Status.Loadbalancer = []routerv1beta1.RouteLoadbalancer{{
			IP: service.Status.LoadBalancer.Ingress[0].IP,
		}}
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
	err = nil

	// Setup Proflow
	proflows := map[string]*Proflow{
		"ingress":      {Name: "Ingress"},
		"loadbalancer": {Name: "Loadbalancer"},
	}

	proflows["loadbalancer"].
		Init(r.loadbalancerClass, r.routeFinalizeCondition(proflows, reqLogger)).
		SetAPI(&route).
		SetState("present", r.loadbalancerPresent).
		SetState("absent", r.loadbalancerAbsent)

	proflows["ingress"].
		Init(r.ingressClass, r.routeFinalizeCondition(proflows, reqLogger)).
		SetAPI(&route).
		SetState("present", r.ingressPresent).
		SetState("absent", r.ingressAbsent)

	// Determine Spec.Type
	switch route.Spec.Type {
	case routerv1beta1.RouteTypeIngress:
		{
			err = proflows["ingress"].Apply()
		}
	case routerv1beta1.RouteTypeLoadbalancer:
		{
			err = proflows["loadbalancer"].Apply()
		}
	default:
		{
			err = NewProflowRuntimeError("Not supported spec.type from [ingress, loadbalancer]")
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
