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

// Package appproxy implements the per-app reverse proxy (see
// docs/ARCHITECTURE.md "Server (portal-server)" > "Reverse proxy"):
// unmodified passthrough of the request URI, trusted-header injection per
// AppAuthentication, and WebSocket passthrough (handled transparently by
// net/http/httputil.ReverseProxy).
//
// The bare-prefix -> trailing-slash redirect ("/grafana" -> "/grafana/")
// is deliberately NOT implemented here: net/http.ServeMux already performs
// this redirect for any subtree pattern (one ending in "/", which every
// AppRegistration.spec.routing.pathPrefix is normalized to by
// buildAppConfig in internal/controller/portal_controller.go before
// reaching here), and it does so with a path-only relative Location
// header - the browser keeps whatever scheme/host it already used to
// reach us, so there's no need to trust X-Forwarded-Proto/-Host to build
// an absolute URL.
package appproxy

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/gprossliner/panoptikum/internal/oidcauth"
	"github.com/gprossliner/panoptikum/internal/portalconfig"
)

const (
	xFrameOptionsHeader         = "X-Frame-Options"
	contentSecurityPolicyHeader = "Content-Security-Policy"
)

// New builds the http.Handler that reverse-proxies requests for one
// AppRegistration to app.BackendURL. Must be wrapped in (*oidcauth.Handler).
// Middleware by the caller, so oidcauth.UserFromContext resolves for
// header injection.
func New(app portalconfig.AppConfig) (http.Handler, error) {
	backend, err := url.Parse(app.BackendURL)
	if err != nil {
		return nil, fmt.Errorf("parsing backendURL %q: %w", app.BackendURL, err)
	}

	var headers map[string]string
	if app.Authorization.Type == portalconfig.AppAuthorizationTypeProxyAuthentication &&
		app.Authorization.ProxyAuthentication != nil {
		headers = app.Authorization.ProxyAuthentication.Headers
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(backend)
			injectHeaders(pr, headers)
		},
		ModifyResponse: func(resp *http.Response) error {
			rewriteFramingHeaders(resp)
			return nil
		},
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(headers) > 0 {
			if _, ok := oidcauth.UserFromContext(r.Context()); !ok {
				// Middleware always sets this; its absence means this
				// handler was mounted without auth - refuse rather than
				// silently proxy with an empty trusted header.
				http.Error(w, "no authenticated user in request context", http.StatusInternalServerError)
				return
			}
		}
		proxy.ServeHTTP(w, r)
	}), nil
}

// injectHeaders sets each configured trusted header on the outbound
// request, first deleting any client-supplied value with the same name -
// the exact bug class (a naive proxy that only adds a header without first
// removing the inbound one) that would reopen the auth-bypass hole this
// design exists to close (see docs/ARCHITECTURE.md Security considerations).
func injectHeaders(pr *httputil.ProxyRequest, headers map[string]string) {
	if len(headers) == 0 {
		return
	}

	user, _ := oidcauth.UserFromContext(pr.In.Context())
	for name, template := range headers {
		pr.Out.Header.Del(name)
		pr.Out.Header.Set(name, strings.ReplaceAll(template, "$user", user))
	}
}

// rewriteFramingHeaders neutralizes a backend app's own clickjacking
// protection headers (e.g. Grafana's default "X-Frame-Options: deny")
// that would otherwise block the portal shell's <iframe> embedding -
// without blindly allowing ANY page to frame the app. Because this proxy
// never rewrites the request URI (see package doc), the browser always
// sees the backend's response as served from the portal's own origin, so
// "SAMEORIGIN"/CSP 'self' correctly scope framing to the portal alone,
// not a wildcard allow-all.
func rewriteFramingHeaders(resp *http.Response) {
	if resp.Header.Get(xFrameOptionsHeader) != "" {
		resp.Header.Set(xFrameOptionsHeader, "SAMEORIGIN")
	}

	if csp := resp.Header.Get(contentSecurityPolicyHeader); csp != "" {
		if rewritten, changed := rewriteFrameAncestors(csp); changed {
			resp.Header.Set(contentSecurityPolicyHeader, rewritten)
		}
	}
}

// rewriteFrameAncestors replaces an existing frame-ancestors directive's
// value with 'self', leaving every other directive untouched. Returns
// changed=false (csp returned unmodified) if no frame-ancestors directive
// is present - a CSP without one isn't a clickjacking concern here.
func rewriteFrameAncestors(csp string) (rewritten string, changed bool) {
	directives := strings.Split(csp, ";")
	for i, d := range directives {
		trimmed := strings.TrimSpace(d)
		if trimmed == "frame-ancestors" || strings.HasPrefix(trimmed, "frame-ancestors ") {
			if i == 0 {
				directives[i] = "frame-ancestors 'self'"
			} else {
				directives[i] = " frame-ancestors 'self'"
			}
			changed = true
		}
	}
	if !changed {
		return csp, false
	}
	return strings.Join(directives, ";"), true
}
