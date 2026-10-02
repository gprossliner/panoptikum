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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// UserAuthenticationType selects the mechanism used to authenticate users to
// a Portal. Only one variant exists for now; more can be added later
// without a new CRD (see issue #9).
// +kubebuilder:validation:Enum=OIDC
type UserAuthenticationType string

const (
	// UserAuthenticationTypeOIDC authenticates users via an OIDC provider.
	UserAuthenticationTypeOIDC UserAuthenticationType = "OIDC"
)

// OIDCConfig configures OIDC-based user authentication.
type OIDCConfig struct {
	// issuerURL is the OIDC issuer URL used to discover endpoints and validate tokens.
	// +required
	IssuerURL string `json:"issuerURL"`

	// clientID is the OAuth2/OIDC client ID registered with the issuer.
	// +required
	ClientID string `json:"clientID"`

	// clientSecretRef references the Secret key holding the OAuth2/OIDC client secret.
	// +required
	ClientSecretRef corev1.SecretKeySelector `json:"clientSecretRef"`

	// scopes are the OIDC scopes requested during authentication.
	// +optional
	// +kubebuilder:default={openid,profile,email}
	Scopes []string `json:"scopes,omitempty"`

	// allowUnverifiedEmail allows login for users whose email claim is not
	// marked verified by the issuer.
	// +optional
	AllowUnverifiedEmail bool `json:"allowUnverifiedEmail,omitempty"`
}

// UserAuthenticationSpec defines the desired state of UserAuthentication
// +kubebuilder:validation:XValidation:rule="self.type != 'OIDC' || has(self.oidc)",message="oidc is required when type is OIDC"
type UserAuthenticationSpec struct {
	// type selects which mechanism authenticates users to the Portal.
	// +required
	Type UserAuthenticationType `json:"type"`

	// cookieSecretRef references the Secret key holding the symmetric key used to
	// encrypt and sign session and OIDC handshake cookies, shared by every
	// portal-server replica (see docs/ARCHITECTURE.md Decision 7). Independent
	// of type - every mechanism issues the same kind of session cookie.
	// +required
	CookieSecretRef corev1.SecretKeySelector `json:"cookieSecretRef"`

	// oidc configures OIDC-based authentication. Required when type is OIDC.
	// +optional
	OIDC *OIDCConfig `json:"oidc,omitempty"`
}

// UserAuthenticationStatus defines the observed state of UserAuthentication.
type UserAuthenticationStatus struct {
	// conditions represent the current state of the UserAuthentication resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// portals lists the Portals currently bound to this UserAuthentication.
	// +listType=map
	// +listMapKey=name
	// +listMapKey=namespace
	// +optional
	Portals []NamespacedObjectReference `json:"portals,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// UserAuthentication is the Schema for the userauthentications API
type UserAuthentication struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of UserAuthentication
	// +required
	Spec UserAuthenticationSpec `json:"spec"`

	// status defines the observed state of UserAuthentication
	// +optional
	Status UserAuthenticationStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// UserAuthenticationList contains a list of UserAuthentication
type UserAuthenticationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []UserAuthentication `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &UserAuthentication{}, &UserAuthenticationList{})
		return nil
	})
}
