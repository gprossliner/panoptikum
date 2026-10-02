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

// Package portalconfig defines the merged configuration document the
// operator generates for a Portal (see docs/ARCHITECTURE.md Decision 4)
// and writes as JSON into a generated Secret. The portal-server reads and
// unmarshals that same document from its mounted Secret.
//
// Both cmd/operator and cmd/server import this package, so it must never
// depend on client-go/controller-runtime (Decision 4) - it's plain Go
// structs with JSON tags only.
//
// Every cross-reference (UserAuthentication, AppAuthentication, backend
// Service) is already resolved to a concrete value by the time a Config is
// built - the portal-server never talks to the Kubernetes API itself.
package portalconfig

// Config is the full, resolved configuration for one Portal instance.
type Config struct {
	// Portal is this Portal's own display/branding settings.
	Portal PortalConfig `json:"portal"`

	// UserAuthentication is the resolved OIDC configuration gating this Portal.
	UserAuthentication UserAuthenticationConfig `json:"userAuthentication"`

	// Apps are the AppRegistrations currently bound and Accepted for this
	// Portal, ordered by sortOrder.
	Apps []AppConfig `json:"apps,omitempty"`
}

// PortalConfig is the resolved subset of Portal.spec the portal-server
// needs to render its shell.
type PortalConfig struct {
	// host is the externally-visible hostname, used for the OIDC redirect URI.
	Host string `json:"host"`

	// displayName is shown in the portal shell header.
	DisplayName string `json:"displayName,omitempty"`

	// customization configures optional cosmetic branding of the portal shell.
	Customization *CustomizationConfig `json:"customization,omitempty"`
}

// CustomizationConfig mirrors Portal.spec.customization.
type CustomizationConfig struct {
	LogoURL         string `json:"logoURL,omitempty"`
	FaviconURL      string `json:"faviconURL,omitempty"`
	BackgroundColor string `json:"backgroundColor,omitempty"`
	AccentColor     string `json:"accentColor,omitempty"`
}

// UserAuthenticationConfig is the resolved OIDC configuration, carrying the
// actual secret values rather than Secret references - the portal-server
// cannot read Secrets itself.
type UserAuthenticationConfig struct {
	IssuerURL    string `json:"issuerURL"`
	ClientID     string `json:"clientID"`
	ClientSecret string `json:"clientSecret"`

	// CookieSecret is the shared symmetric key used to encrypt/sign session
	// and OIDC handshake cookies (see docs/ARCHITECTURE.md Decision 7).
	CookieSecret string `json:"cookieSecret"`

	Scopes               []string `json:"scopes,omitempty"`
	AllowUnverifiedEmail bool     `json:"allowUnverifiedEmail,omitempty"`
}

// AppConfig is one resolved AppRegistration entry, with its AppAuthentication inlined.
type AppConfig struct {
	DisplayName string `json:"displayName,omitempty"`
	PathPrefix  string `json:"pathPrefix"`
	SortOrder   int32  `json:"sortOrder,omitempty"`

	// Routes overrides the default Authenticated access requirement for
	// matching sub-paths (see RouteConfig).
	Routes []RouteConfig `json:"routes,omitempty"`

	// BackendURL is the fully resolved backend address (e.g.
	// "http://grafana.management-portal.svc.cluster.local:80"), computed
	// by the operator so the portal-server never needs in-cluster DNS
	// knowledge of its own.
	BackendURL string `json:"backendURL"`

	// Authorization selects how the portal-server vouches for the logged-in
	// user to this app's backend.
	Authorization AppAuthorization `json:"authorization"`
}

// RouteAccess mirrors AppRegistrationRouteAccess (see
// docs/ARCHITECTURE.md "AppRegistration") but is kept as its own type here,
// free of the CRD API's dependencies (Decision 4).
type RouteAccess string

const (
	// RouteAccessAuthenticated requires a valid session, redirecting to
	// login otherwise. Default when no route matches or Routes is empty.
	RouteAccessAuthenticated RouteAccess = "Authenticated"

	// RouteAccessAnonymous allows the request through without a session -
	// no trusted header is injected either, since there's no logged-in
	// user to vouch for.
	RouteAccessAnonymous RouteAccess = "Anonymous"
)

// RouteConfig mirrors one AppRegistration.spec.routes[] entry. Match is
// already known to compile - validated at reconcile time (see
// docs/ARCHITECTURE.md "Reconciliation design"), not here.
type RouteConfig struct {
	Match  string      `json:"match"`
	Access RouteAccess `json:"access"`
}

// AppAuthorizationType selects the mechanism used to vouch for the
// logged-in user to a backend app. Mirrors AppAuthentication.spec.type (see
// docs/ARCHITECTURE.md "AppAuthentication") but is kept as its own type
// here rather than imported from api/v1alpha1, since this package must
// stay free of the CRD API's dependencies (Decision 4).
type AppAuthorizationType string

const (
	// AppAuthorizationTypeProxyAuthentication injects trusted headers into
	// requests proxied to the backend app.
	AppAuthorizationTypeProxyAuthentication AppAuthorizationType = "ProxyAuthentication"
)

// AppAuthorization is a typed union selecting how the portal-server vouches
// for the logged-in user to a backend app. Only one variant exists for now
// (matching AppAuthentication); more (e.g. Basic, Anonymous) can be added
// later as a pure addition without breaking already-written Secrets.
type AppAuthorization struct {
	Type AppAuthorizationType `json:"type"`

	// ProxyAuthentication configures trusted-header injection. Set when
	// Type is ProxyAuthentication.
	ProxyAuthentication *ProxyAuthenticationConfig `json:"proxyAuthentication,omitempty"`
}

// ProxyAuthenticationConfig configures trusted-header injection.
type ProxyAuthenticationConfig struct {
	// Headers maps a header name to a template string (only "$user" is
	// supported for now, see docs/ARCHITECTURE.md non-goals).
	Headers map[string]string `json:"headers,omitempty"`
}
