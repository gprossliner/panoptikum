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

// AppAuthenticationType selects the mechanism used to vouch for the
// logged-in user to a backend app. Only one variant exists for now; more
// can be added later without a new CRD (see docs/ARCHITECTURE.md
// "AppAuthentication").
// +kubebuilder:validation:Enum=ProxyAuthentication
type AppAuthenticationType string

const (
	// AppAuthenticationTypeProxyAuthentication injects trusted headers into
	// requests proxied to the backend app.
	AppAuthenticationTypeProxyAuthentication AppAuthenticationType = "ProxyAuthentication"
)

// ProxyAuthenticationConfig configures trusted-header injection.
type ProxyAuthenticationConfig struct {
	// headers maps a header name to a template string identifying the claim
	// to inject. Only the "$user" template variable is supported for now
	// (see docs/ARCHITECTURE.md non-goals: no email/groups/other claims
	// passthrough yet).
	// +required
	// +kubebuilder:validation:XValidation:rule="self.all(k, self[k] == '$user')",message="only the $user template variable is supported"
	Headers map[string]string `json:"headers"`
}

// AppAuthenticationSpec defines the desired state of AppAuthentication
// +kubebuilder:validation:XValidation:rule="self.type != 'ProxyAuthentication' || has(self.proxyAuthentication)",message="proxyAuthentication is required when type is ProxyAuthentication"
type AppAuthenticationSpec struct {
	// type selects which authentication mechanism vouches for the user to the backend app.
	// +required
	Type AppAuthenticationType `json:"type"`

	// proxyAuthentication configures header injection. Required when type is ProxyAuthentication.
	// +optional
	ProxyAuthentication *ProxyAuthenticationConfig `json:"proxyAuthentication,omitempty"`
}

// AppAuthenticationStatus defines the observed state of AppAuthentication.
type AppAuthenticationStatus struct {
	// conditions represent the current state of the AppAuthentication resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// appRegistrations lists the AppRegistrations currently bound to this AppAuthentication.
	// +listType=map
	// +listMapKey=name
	// +listMapKey=namespace
	// +optional
	AppRegistrations []NamespacedObjectReference `json:"appRegistrations,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// AppAuthentication is the Schema for the appauthentications API
type AppAuthentication struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of AppAuthentication
	// +required
	Spec AppAuthenticationSpec `json:"spec"`

	// status defines the observed state of AppAuthentication
	// +optional
	Status AppAuthenticationStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// AppAuthenticationList contains a list of AppAuthentication
type AppAuthenticationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AppAuthentication `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &AppAuthentication{}, &AppAuthenticationList{})
		return nil
	})
}
