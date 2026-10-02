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

package appproxy

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gprossliner/panoptikum/internal/oidcauth"
	"github.com/gprossliner/panoptikum/internal/portalconfig"
	"github.com/gprossliner/panoptikum/internal/routeaccess"
)

const grafanaPrefix = "/grafana/"

func TestNewForwardsRequestURIUnmodified(t *testing.T) {
	var gotPath, gotQuery string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	handler, err := New(portalconfig.AppConfig{PathPrefix: grafanaPrefix, BackendURL: backend.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/grafana/dashboards/db/1?refresh=5s", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotPath != "/grafana/dashboards/db/1" {
		t.Errorf("backend saw path %q, want %q", gotPath, "/grafana/dashboards/db/1")
	}
	if gotQuery != "refresh=5s" {
		t.Errorf("backend saw query %q, want %q", gotQuery, "refresh=5s")
	}
}

func TestNewInjectsTrustedHeaderAndStripsClientSupplied(t *testing.T) {
	var gotHeader string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Forwarded-User")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	app := portalconfig.AppConfig{
		PathPrefix: grafanaPrefix,
		BackendURL: backend.URL,
		Authorization: portalconfig.AppAuthorization{
			Type: portalconfig.AppAuthorizationTypeProxyAuthentication,
			ProxyAuthentication: &portalconfig.ProxyAuthenticationConfig{
				Headers: map[string]string{"X-Forwarded-User": "$user"},
			},
		},
	}
	handler, err := New(app)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, grafanaPrefix, nil)
	req.Header.Set("X-Forwarded-User", "attacker")
	req = req.WithContext(oidcauth.WithUser(req.Context(), "alice"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if gotHeader != "alice" {
		t.Errorf("backend saw X-Forwarded-User = %q, want %q (client-supplied value must be stripped)", gotHeader, "alice")
	}
}

func TestNewRefusesToProxyWithoutAuthenticatedUser(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("backend was called despite missing authenticated user")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	app := portalconfig.AppConfig{
		PathPrefix: grafanaPrefix,
		BackendURL: backend.URL,
		Authorization: portalconfig.AppAuthorization{
			Type: portalconfig.AppAuthorizationTypeProxyAuthentication,
			ProxyAuthentication: &portalconfig.ProxyAuthenticationConfig{
				Headers: map[string]string{"X-Forwarded-User": "$user"},
			},
		},
	}
	handler, err := New(app)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, grafanaPrefix, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// TestNewAllowsAnonymousRouteWithoutHeaderOrError confirms issue #11: a
// request explicitly routed Anonymous (no authenticated user, by design)
// is proxied normally rather than refused, and gets no trusted header -
// there's no user to vouch for.
func TestNewAllowsAnonymousRouteWithoutHeaderOrError(t *testing.T) {
	var sawHeader bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawHeader = r.Header.Get("X-Forwarded-User") != ""
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	app := portalconfig.AppConfig{
		PathPrefix: grafanaPrefix,
		BackendURL: backend.URL,
		Authorization: portalconfig.AppAuthorization{
			Type: portalconfig.AppAuthorizationTypeProxyAuthentication,
			ProxyAuthentication: &portalconfig.ProxyAuthenticationConfig{
				Headers: map[string]string{"X-Forwarded-User": "$user"},
			},
		},
	}
	handler, err := New(app)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, grafanaPrefix+"public-dashboards/abc", nil)
	req = req.WithContext(routeaccess.WithAccess(req.Context(), portalconfig.RouteAccessAnonymous))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if sawHeader {
		t.Error("backend saw a trusted header on an Anonymous-routed request, want none")
	}
}

// TestBarePrefixRedirectsToTrailingSlash confirms (rather than assumes, per
// docs/ARCHITECTURE.md "Reverse proxy") that mounting an app's handler on
// its pathPrefix (always ending in "/") in a real net/http.ServeMux is
// enough to get the "/grafana" -> "/grafana/" redirect, and that the
// Location header is path-only - the browser supplies whatever
// scheme/host it already used, so no X-Forwarded-Proto/-Host trust is
// needed to build it.
func TestBarePrefixRedirectsToTrailingSlash(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	handler, err := New(portalconfig.AppConfig{PathPrefix: grafanaPrefix, BackendURL: backend.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle(grafanaPrefix, handler)
	server := httptest.NewServer(mux)
	defer server.Close()

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(server.URL + "/grafana?x=1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusTemporaryRedirect)
	}
	loc := resp.Header.Get("Location")
	if loc != "/grafana/?x=1" {
		t.Errorf("Location = %q, want %q", loc, "/grafana/?x=1")
	}
}

// TestWebSocketPassthrough confirms httputil.ReverseProxy proxies an
// Upgrade: websocket handshake and the raw bytes exchanged afterwards,
// per docs/ARCHITECTURE.md "Reverse proxy" (Headlamp live logs/exec,
// Grafana Live) - verified explicitly rather than assumed.
func TestWebSocketPassthrough(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("backend ResponseWriter does not support hijacking")
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack: %v", err)
		}
		defer func() { _ = conn.Close() }()

		_, err = conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"))
		if err != nil {
			t.Fatalf("writing 101 response: %v", err)
		}
		// Echo whatever the client sends after the handshake.
		_, _ = io.Copy(conn, buf)
	}))
	defer backend.Close()

	handler, err := New(portalconfig.AppConfig{PathPrefix: "/app/", BackendURL: backend.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	conn, err := net.Dial("tcp", proxy.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer func() { _ = conn.Close() }()

	req, err := http.NewRequest(http.MethodGet, "/app/ws", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	if err := req.Write(conn); err != nil {
		t.Fatalf("writing handshake request: %v", err)
	}

	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("reading status line: %v", err)
	}
	if !strings.Contains(statusLine, "101") {
		t.Fatalf("status line = %q, want 101 Switching Protocols", statusLine)
	}
	// Drain the rest of the response headers.
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("reading response headers: %v", err)
		}
		if line == "\r\n" {
			break
		}
	}

	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatalf("writing payload: %v", err)
	}
	echoed := make([]byte, len("hello"))
	if _, err := io.ReadFull(reader, echoed); err != nil {
		t.Fatalf("reading echo: %v", err)
	}
	if string(echoed) != "hello" {
		t.Errorf("echoed = %q, want %q", echoed, "hello")
	}
}

// TestModifyResponseRewritesXFrameOptions confirms a backend's own
// clickjacking protection (e.g. Grafana's default "X-Frame-Options: deny")
// is rewritten rather than passed through unmodified, which would
// otherwise block the portal shell's <iframe> embedding (see issue #1).
func TestModifyResponseRewritesXFrameOptions(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Frame-Options", "deny")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	handler, err := New(portalconfig.AppConfig{PathPrefix: grafanaPrefix, BackendURL: backend.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, grafanaPrefix, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Errorf("X-Frame-Options = %q, want %q", got, "SAMEORIGIN")
	}
}

// TestModifyResponseRewritesFrameAncestors confirms a backend's CSP
// frame-ancestors directive is narrowed to 'self' rather than left as
// whatever the backend sent (e.g. 'none', which would equally block
// embedding) - other directives in the same header must survive
// untouched.
func TestModifyResponseRewritesFrameAncestors(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; script-src 'self'")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	handler, err := New(portalconfig.AppConfig{PathPrefix: grafanaPrefix, BackendURL: backend.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, grafanaPrefix, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	got := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(got, "frame-ancestors 'self'") {
		t.Errorf("Content-Security-Policy = %q, want it to contain %q", got, "frame-ancestors 'self'")
	}
	if strings.Contains(got, "'none'") {
		t.Errorf("Content-Security-Policy = %q, still contains the backend's original 'none'", got)
	}
	if !strings.Contains(got, "default-src 'self'") || !strings.Contains(got, "script-src 'self'") {
		t.Errorf("Content-Security-Policy = %q, other directives must survive untouched", got)
	}
}

// TestModifyResponseLeavesUnrelatedHeadersAlone confirms a backend with no
// framing headers at all (the common case) isn't touched - nothing to
// rewrite, and no new restrictive header should be invented.
func TestModifyResponseLeavesUnrelatedHeadersAlone(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	handler, err := New(portalconfig.AppConfig{PathPrefix: grafanaPrefix, BackendURL: backend.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, grafanaPrefix, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Frame-Options"); got != "" {
		t.Errorf("X-Frame-Options = %q, want unset", got)
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != "" {
		t.Errorf("Content-Security-Policy = %q, want unset", got)
	}
}
