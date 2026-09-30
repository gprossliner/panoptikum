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

package oidcauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gprossliner/xhdl"

	"github.com/gprossliner/panoptikum/internal/sessioncookie"
)

const testUser = "alice"

type fakeClaimsSource struct {
	raw []byte
}

func (f fakeClaimsSource) Claims(v any) error {
	return json.Unmarshal(f.raw, v)
}

func TestExtractUserPrefersPreferredUsername(t *testing.T) {
	src := fakeClaimsSource{raw: []byte(`{"preferred_username":"alice","email":"alice@example.com"}`)}

	var got string
	err := xhdl.Run(func(ctx xhdl.Context) {
		got = extractUser(ctx, "subject-id", src)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "alice" {
		t.Errorf("extractUser() = %q, want %q", got, "alice")
	}
}

func TestExtractUserFallsBackToEmail(t *testing.T) {
	src := fakeClaimsSource{raw: []byte(`{"email":"alice@example.com"}`)}

	var got string
	err := xhdl.Run(func(ctx xhdl.Context) {
		got = extractUser(ctx, "subject-id", src)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "alice@example.com" {
		t.Errorf("extractUser() = %q, want %q", got, "alice@example.com")
	}
}

func TestExtractUserFallsBackToSubject(t *testing.T) {
	src := fakeClaimsSource{raw: []byte(`{}`)}

	var got string
	err := xhdl.Run(func(ctx xhdl.Context) {
		got = extractUser(ctx, "subject-id", src)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "subject-id" {
		t.Errorf("extractUser() = %q, want %q", got, "subject-id")
	}
}

func TestRandomTokenIsUnique(t *testing.T) {
	var a, b string
	err := xhdl.Run(func(ctx xhdl.Context) {
		a = randomToken(ctx)
		b = randomToken(ctx)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if a == "" || b == "" {
		t.Fatal("randomToken produced an empty string")
	}
	if a == b {
		t.Error("randomToken produced the same value twice")
	}
}

func newTestSessionCodec(t *testing.T) *sessioncookie.Codec {
	t.Helper()

	var codec *sessioncookie.Codec
	err := xhdl.Run(func(ctx xhdl.Context) {
		codec = sessioncookie.NewCodec(ctx, "test-secret")
	})
	if err != nil {
		t.Fatalf("NewCodec: %v", err)
	}
	return codec
}

func TestHandleLogoutClearsSessionCookieAndRedirects(t *testing.T) {
	h := &Handler{sessionCodec: newTestSessionCodec(t)}

	req := httptest.NewRequest(http.MethodGet, LogoutPath, nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "some-session-value"})
	rec := httptest.NewRecorder()
	h.HandleLogout(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want %q", loc, "/")
	}

	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("HandleLogout did not clear the session cookie")
	}
}

func TestMiddlewareRedirectsWithoutSessionCookie(t *testing.T) {
	h := &Handler{sessionCodec: newTestSessionCodec(t)}

	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })

	req := httptest.NewRequest(http.MethodGet, "/grafana/dashboards?x=1", nil)
	rec := httptest.NewRecorder()
	h.Middleware(next).ServeHTTP(rec, req)

	if called {
		t.Error("next handler was called without a session cookie")
	}
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}

	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, LoginPath+"?") {
		t.Fatalf("Location = %q, want prefix %q", loc, LoginPath+"?")
	}
	if got := mustParseRD(t, loc); got != "/grafana/dashboards?x=1" {
		t.Errorf("rd = %q, want %q", got, "/grafana/dashboards?x=1")
	}
}

func TestMiddlewareRedirectsOnTamperedCookie(t *testing.T) {
	h := &Handler{sessionCodec: newTestSessionCodec(t)}

	req := httptest.NewRequest(http.MethodGet, "/grafana/", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "not-a-valid-cookie"})
	rec := httptest.NewRecorder()
	h.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("next handler was called with a tampered session cookie")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}

	// The bad cookie must be cleared, not left for the next request to trip over again.
	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("Middleware did not clear the tampered session cookie")
	}
}

func TestMiddlewareRedirectsOnExpiredSession(t *testing.T) {
	codec := newTestSessionCodec(t)

	var value string
	err := xhdl.Run(func(ctx xhdl.Context) {
		value = codec.Encode(ctx, sessioncookie.Claims{User: testUser, IssuedAt: time.Now().Add(-2 * SessionMaxAge)})
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	h := &Handler{sessionCodec: codec}
	req := httptest.NewRequest(http.MethodGet, "/grafana/", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: value})
	rec := httptest.NewRecorder()
	h.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("next handler was called with an expired session")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusFound)
	}
}

func TestMiddlewareAllowsValidSession(t *testing.T) {
	codec := newTestSessionCodec(t)

	var value string
	err := xhdl.Run(func(ctx xhdl.Context) {
		value = codec.Encode(ctx, sessioncookie.Claims{User: testUser, IssuedAt: time.Now()})
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	h := &Handler{sessionCodec: codec}

	var gotUser string
	var gotOK bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotOK = UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/grafana/", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: value})
	rec := httptest.NewRecorder()
	h.Middleware(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !gotOK {
		t.Fatal("UserFromContext() ok = false, want true")
	}
	if gotUser != testUser {
		t.Errorf("UserFromContext() user = %q, want %q", gotUser, testUser)
	}
}

func mustParseRD(t *testing.T, redirectURL string) string {
	t.Helper()

	u, err := url.Parse(redirectURL)
	if err != nil {
		t.Fatalf("parsing redirect URL %q: %v", redirectURL, err)
	}
	return u.Query().Get("rd")
}
