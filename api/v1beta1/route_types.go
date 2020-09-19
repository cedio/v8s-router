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

package v1beta1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// RouteSpec defines the desired state of Route
type RouteSpec struct {
	// Name of the Service which this route is backed
	ServiceName string `json:"serviceName" protobuf:"bytes,1,opt,name=serviceName"`

	// Route type for the route to operate
	// Supported: ingress/loadbalancer
	Type RouteType `json:"type" protobuf:"bytes,2,opt,name=type,casttype=RouteType"`

	// If routeType="loadbalancer", configuration of loadbalancer will be applied
	Loadbalancer *LoadbalancerConfig `json:"loadbalancer,omitempty" protobuf:"bytes,3,opt,name=loadbalancer"`

	// If routeType="loadbalancer", configuration of loadbalancer will be applied
	Ingress *IngressConfig `json:"ingress,omitempty" protobuf:"bytes,4,opt,name=ingress"`
}

// RouteType indicates the type of route it supports
type RouteType string

const (
	// RouteTypeIngress indicates voyager ingress is used for routing
	RouteTypeIngress RouteType = "ingress"

	// RouteTypeLoadbalancer indicates metallb is used for routing
	RouteTypeLoadbalancer RouteType = "loadbalancer"
)

// LoadbalancerConfig defines config for route backed using metallb
type LoadbalancerConfig struct {
	// Loadbalancer IP Pool for Service
	// Supported: internal/external
	AddressPool LBAddressPoolType `json:"addressPool" protobuf:"bytes,1,opt,name=addressPool,casttype=LBAddressPoolType"`

	// Specific IP for metallb to assign to Service
	TargetIP string `json:"targetIP,omitempty" protobuf:"bytes,2,opt,name=targetIP"`
}

// LBAddressPoolType indicates the IP Pool for metallb to route
type LBAddressPoolType string

const (
	// LBAddressPoolInternal indicates internal as ips for metallb
	LBAddressPoolInternal LBAddressPoolType = "internal"

	// LBAddressPoolExternal indicates external as ips for metallb
	LBAddressPoolExternal LBAddressPoolType = "external"
)

// IngressConfig defines config for route backed using Voyager ingress
type IngressConfig struct {
	// Ingress Controller class for provisioning ingress
	// Supported: haproxy/nginx
	Class IngressClassType `json:"class" protobuf:"bytes,1,opt,name=class,casttype=IngressClassType"`

	// Hostname of Ingress typed Route
	// Automatically generated as <service-name>-<namespace-name>.<cluster-domain> if not supplied
	// +optional
	Host string `json:"host,omitempty" protobuf:"bytes,2,opt,name=host"`

	// The target port on pods selected by the service this route points to.
	// If this is a string, it will be looked up as a named port in the target
	// endpoints port list.
	ServicePort intstr.IntOrString `json:"servicePort" protobuf:"bytes,3,opt,name=servicePort"`

	// TLS configuration for Ingress
	// +optional
	TLS *IngressTLSConfig `json:"tls,omitempty" protobuf:"bytes,4,opt,name=tls"`
}

// IngressClassType defines which type of ingress controller used
type IngressClassType string

const (
	// IngressClassHAProxy means HAProxy
	IngressClassHAProxy IngressClassType = "haproxy"

	// IngressClassNginx means Nginx
	IngressClassNginx IngressClassType = "nginx"
)

// IngressTLSConfig defines the TLS encryption method for ingress typed Route
type IngressTLSConfig struct {
	// Type for TLS Termination to be applied
	// Supported: passthrough/edge/reencrypt
	Termination TLSTerminationType `json:"termination" protobuf:"bytes,1,opt,name=termination,casttype=TLSTerminationType"`

	// Secret Name containing tls certificates
	TLSSecretName string `json:"tlsSecretName,omitempty" protobuf:"bytes,2,opt,name=tlsSecretName"`

	// TODO: TLS Termination Reencryption backend validating CA Certificate
	BackendCACertificate string `json:"backendCACertificate,omitempty" protobuf:"bytes,3,opt,name=backendCACertificate"`

	// ::80 -> ::443
	SSLRedirect bool `json:"sslRedirect,omitempty" protobuf:"varint,4,opt,name=sslRedirect"`
}

// TLSTerminationType defines type for TLS Termination to be applied
type TLSTerminationType string

const (
	// TLSTerminationEdge indicates TLS termination, same certificate for both frontend and backend
	TLSTerminationEdge TLSTerminationType = "edge"

	// TLSTerminationPassthrough indicates passthrough using L4 traffic
	TLSTerminationPassthrough TLSTerminationType = "passthrough"

	// TLSTerminationReencrypt indicates TLS termination, different certificate for frontend and backend
	TLSTerminationReencrypt TLSTerminationType = "reencrypt"
)

// RouteStatus defines the observed state of Route
type RouteStatus struct {
	// Ingress status of Route as list
	Ingress []RouteIngress `json:"ingress,omitempty" protobuf:"bytes,1,rep,name=ingress"`

	// Loadbalancer status of Route as list
	Loadbalancer []RouteLoadbalancer `json:"loadbalancer,omitempty" protobuf:"bytes,2,rep,name=loadbalancer"`

	// Conditions is the state of the route, may be empty.
	Conditions []RouteCondition `json:"conditions,omitempty" protobuf:"bytes,3,rep,name=conditions"`
}

// RouteIngress holds information about the places where a route is exposed as ingress
type RouteIngress struct {
	// Host is the host string under which the route is exposed; this value is required
	Host string `json:"host,omitempty" protobuf:"bytes,1,opt,name=host"`

	// Port for http or tcp, optional for http
	// +optional
	Port *int `json:"port,omitempty" protobuf:"varint,2,opt,name=port"`
}

// RouteLoadbalancer holds information about the places where a route is exposed as lb
type RouteLoadbalancer struct {
	// IP is the location which the route is exposed as loadbalancer
	IP string `json:"ip,omitempty" protobuf:"bytes,1,opt,name=ip"`
}

// RouteCondition contains details for the current condition of this route on a particular router
// ref: OpenShift API Route
type RouteCondition struct {
	// Type is the type of the condition.
	Type RouteConditionType `json:"type" protobuf:"bytes,1,opt,name=type,casttype=RouteConditionType"`
	// Status is the status of the condition.
	// Can be True, False, Unknown.
	Status corev1.ConditionStatus `json:"status" protobuf:"bytes,2,opt,name=status,casttype=k8s.io/api/core/v1.ConditionStatus"`
	// Human readable message indicating details about last transition.
	Message string `json:"message,omitempty" protobuf:"bytes,3,opt,name=message"`
	// RFC 3339 date and time when this condition last transitioned
	LastTransitionTime *metav1.Time `json:"lastTransitionTime,omitempty" protobuf:"bytes,4,opt,name=lastTransitionTime"`
}

// RouteConditionType is a valid value for RouteCondition
type RouteConditionType string

// These are valid conditions of pod.
const (
	// RouteAdmitted means the route is able to service requests for the provided Host
	RouteAdmitted RouteConditionType = "Admitted"

	// RouteDenied means the route is not completed
	RouteDenied RouteConditionType = "Denied"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Route is the Schema for the routes API
type Route struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RouteSpec   `json:"spec,omitempty"`
	Status RouteStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RouteList contains a list of Route
type RouteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Route `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Route{}, &RouteList{})
}
