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

// ServiceBackend identifies an in-cluster Service backing an AppRegistration.
type ServiceBackend struct {
	// name of the Service.
	// +required
	Name string `json:"name"`

	// namespace of the Service. Defaults to the AppRegistration's own namespace when omitted.
	// +optional
	// +kubebuilder:default=""
	Namespace string `json:"namespace,omitempty"`

	// port is the Service port to route to.
	// +required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`
}

// AppRegistrationBackend selects the app's backend. Nested under a single
// key (service) rather than flat fields on AppRegistrationSpec so a future
// variant (e.g. an external url:) can be added as a pure addition (see
// docs/ARCHITECTURE.md Decision 1).
type AppRegistrationBackend struct {
	// service is the in-cluster Service backend. Only supported backend kind for now.
	// +required
	Service ServiceBackend `json:"service"`
}

// AppRegistrationRouting selects how an AppRegistration is exposed in the
// Portal. Nested under a single key (pathPrefix) rather than a flat field
// on AppRegistrationSpec so a future variant (e.g. subdomain-based host:
// routing) can be added as a pure addition (see docs/ARCHITECTURE.md
// Decision 1).
type AppRegistrationRouting struct {
	// pathPrefix is the path this app is mounted under, e.g. "/grafana/".
	// Only supported routing kind for now. Requires a leading and trailing
	// slash, and at least one path segment (no root-only apps, see
	// docs/ARCHITECTURE.md non-goals). Must not start with "/_panoptikum/",
	// reserved for the portal-server's own routes (login, OIDC callback,
	// health check).
	// +required
	// +kubebuilder:validation:Pattern=`^/.+/$`
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('/_panoptikum/')",message="pathPrefix must not start with the reserved /_panoptikum/ prefix"
	PathPrefix string `json:"pathPrefix"`
}

// AppRegistrationSpec defines the desired state of AppRegistration
type AppRegistrationSpec struct {
	// portalRef references the Portal this app is registered with.
	// +required
	PortalRef NamespacedObjectReference `json:"portalRef"`

	// appAuthenticationRef references how the portal vouches for the
	// logged-in user to this app.
	// +required
	AppAuthenticationRef NamespacedObjectReference `json:"appAuthenticationRef"`

	// displayName is shown in the Portal's nav. Defaults to metadata.name when omitted.
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// routing selects how this app is exposed in the Portal.
	// +required
	Routing AppRegistrationRouting `json:"routing"`

	// sortOrder controls this app's position in the Portal's nav (ascending).
	// +optional
	SortOrder int32 `json:"sortOrder,omitempty"`

	// backend selects the app's backend.
	// +required
	Backend AppRegistrationBackend `json:"backend"`
}

// AppRegistrationStatus defines the observed state of AppRegistration.
type AppRegistrationStatus struct {
	// conditions represent the current state of the AppRegistration
	// resource: ResolvedRefs (portalRef/appAuthenticationRef/backend all
	// found) and Accepted (bound into the Portal's routing table; False
	// with reason NamespaceNotAllowed if this namespace doesn't match the
	// Portal's allowedAppNamespaces, see docs/ARCHITECTURE.md Decision 8).
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// AppRegistration is the Schema for the appregistrations API
type AppRegistration struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of AppRegistration
	// +required
	Spec AppRegistrationSpec `json:"spec"`

	// status defines the observed state of AppRegistration
	// +optional
	Status AppRegistrationStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// AppRegistrationList contains a list of AppRegistration
type AppRegistrationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AppRegistration `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &AppRegistration{}, &AppRegistrationList{})
		return nil
	})
}
