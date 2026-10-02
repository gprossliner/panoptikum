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

package portalshell

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gprossliner/panoptikum/internal/oidcauth"
	"github.com/gprossliner/panoptikum/internal/portalconfig"
)

func TestServeHTTPRendersUserAppsAndCustomization(t *testing.T) {
	cfg := &portalconfig.Config{
		Portal: portalconfig.PortalConfig{
			DisplayName: "Acme Portal",
			Customization: &portalconfig.CustomizationConfig{
				LogoURL:    "https://example.com/logo.png",
				FaviconURL: "https://example.com/favicon.ico",
			},
		},
		Apps: []portalconfig.AppConfig{
			{DisplayName: "Grafana", PathPrefix: "/grafana/"},
			{PathPrefix: "/headlamp/"},
		},
	}

	h, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(oidcauth.WithUser(req.Context(), "alice"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html prefix", ct)
	}

	body := rec.Body.String()
	for _, want := range []string{
		"<title>Acme Portal</title>",
		`<summary id="user">alice</summary>`,
		`src="https://example.com/logo.png"`,
		`href="https://example.com/favicon.ico"`,
		`href="#/grafana" data-app="grafana">Grafana</a>`,
		// PathPrefix-derived id used as the fallback label when DisplayName is unset.
		`href="#/headlamp" data-app="headlamp">headlamp</a>`,
		`"grafana":{"base":"/grafana/"}`,
		`"headlamp":{"base":"/headlamp/"}`,
		"Built with panoptikum",
		`href="https://github.com/gprossliner/panoptikum"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered body missing %q\nfull body:\n%s", want, body)
		}
	}
}

func TestServeHTTPDefaultsDisplayNameAndOmitsCustomization(t *testing.T) {
	cfg := &portalconfig.Config{}

	h, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "<title>Management Portal</title>") {
		t.Errorf("rendered body missing default DisplayName title\nfull body:\n%s", body)
	}
	if strings.Contains(body, `<img class="logo"`) {
		t.Error("rendered body has a logo <img> despite no Customization set")
	}
	if !strings.Contains(body, `<summary id="user"></summary>`) {
		t.Errorf("rendered body should show an empty user when none is in context\nfull body:\n%s", body)
	}
	if !strings.Contains(body, "No apps registered yet.") {
		t.Errorf("rendered body should show the zero-apps empty state\nfull body:\n%s", body)
	}
}
