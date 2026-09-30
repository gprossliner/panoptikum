/*
Copyright 2026.

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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// PortalCustomization configures optional cosmetic branding of the portal shell.
type PortalCustomization struct {
	// logoURL is rendered as an <img src> in the portal header.
	// +optional
	LogoURL string `json:"logoURL,omitempty"`

	// faviconURL is rendered as a <link href>.
	// +optional
	FaviconURL string `json:"faviconURL,omitempty"`

	// backgroundColor sets the header background, e.g. "#303030".
	// +optional
	BackgroundColor string `json:"backgroundColor,omitempty"`

	// accentColor sets the active nav link / highlight color.
	// +optional
	AccentColor string `json:"accentColor,omitempty"`
}

// PortalIngressTLS configures certificate issuance for a Portal's Ingress.
type PortalIngressTLS struct {
	// clusterIssuer is the cert-manager ClusterIssuer name.
	// +optional
	ClusterIssuer string `json:"clusterIssuer,omitempty"`
}

// PortalIngress configures whether/how a Portal creates its own Ingress.
type PortalIngress struct {
	// enabled selects whether an Ingress is created for this Portal. When
	// false, no Ingress is created and the user provides their own routing.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// ingressClassName selects the IngressClass to use.
	// +optional
	IngressClassName string `json:"ingressClassName,omitempty"`

	// tls configures certificate issuance for this Portal's Ingress.
	// +optional
	TLS *PortalIngressTLS `json:"tls,omitempty"`
}

// PortalSpec defines the desired state of Portal
type PortalSpec struct {
	// host is the externally-visible hostname, used for the OIDC redirect
	// URI even if ingress.enabled is false and the user fronts it themselves.
	// +required
	Host string `json:"host"`

	// displayName is shown in the portal shell header. Defaults to metadata.name when omitted.
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// customization configures optional cosmetic branding of the portal shell.
	// +optional
	Customization *PortalCustomization `json:"customization,omitempty"`

	// userAuthenticationRef references the OIDC configuration gating this Portal.
	// +required
	UserAuthenticationRef NamespacedObjectReference `json:"userAuthenticationRef"`

	// allowedAppNamespaces is a regex (RE2), anchored and matched against an
	// AppRegistration's namespace to decide whether it may bind to this
	// Portal (see docs/ARCHITECTURE.md Decision 8). Defaults to ".+" (any namespace).
	// +optional
	// +kubebuilder:default=".+"
	AllowedAppNamespaces string `json:"allowedAppNamespaces,omitempty"`

	// ingress configures whether/how this Portal creates its own Ingress.
	// +optional
	Ingress *PortalIngress `json:"ingress,omitempty"`
}

// PortalStatus defines the observed state of Portal.
type PortalStatus struct {
	// conditions represent the current state of the Portal resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// appRegistrations lists the AppRegistrations currently bound to this Portal.
	// +listType=map
	// +listMapKey=name
	// +listMapKey=namespace
	// +optional
	AppRegistrations []NamespacedObjectReference `json:"appRegistrations,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Portal is the Schema for the portals API
type Portal struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Portal
	// +required
	Spec PortalSpec `json:"spec"`

	// status defines the observed state of Portal
	// +optional
	Status PortalStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PortalList contains a list of Portal
type PortalList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Portal `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Portal{}, &PortalList{})
		return nil
	})
}
