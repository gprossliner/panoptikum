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

// Package portalshell renders the portal's single static HTML/JS shell
// page (see docs/ARCHITECTURE.md "Server (portal-server)" > "Portal shell
// UI"): a nav bar generated from the Portal's bound apps, each embedded in
// a same-origin <iframe>, with the logged-in user filled in server-side.
package portalshell

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/gprossliner/panoptikum/internal/oidcauth"
	"github.com/gprossliner/panoptikum/internal/portalconfig"
)

//go:embed shell.html
var shellFS embed.FS

var shellTemplate = template.Must(template.ParseFS(shellFS, "shell.html"))

// appView is one nav entry, server-rendered into both the <nav> links and
// the client-side apps lookup used for iframe/hash routing (see shell.html).
type appView struct {
	ID    string
	Label string
	Base  string
}

// jsApp is appView's shape as embedded JSON (see Handler.AppsJSON in shell.html).
type jsApp struct {
	Base string `json:"base"`
}

// shellData is static for the lifetime of a Handler - cfg.Apps/cfg.Portal
// never change without a process restart (a new config Secret triggers a
// new Deployment rollout, not a live reload) - only User varies per request.
type shellData struct {
	DisplayName   string
	Customization *portalconfig.CustomizationConfig
	Apps          []appView
	AppsJSON      template.JS
}

// Handler serves the rendered portal shell page. Re-executes the parsed
// template per request (cheap - one small page, no I/O) rather than
// caching rendered HTML, since the logged-in user varies per request.
type Handler struct {
	data shellData
}

// New builds a Handler from a Portal's resolved config.
func New(cfg *portalconfig.Config) (*Handler, error) {
	apps := make([]appView, 0, len(cfg.Apps))
	jsApps := make(map[string]jsApp, len(cfg.Apps))
	for _, app := range cfg.Apps {
		id := strings.Trim(app.PathPrefix, "/")
		label := app.DisplayName
		if label == "" {
			label = id
		}
		apps = append(apps, appView{ID: id, Label: label, Base: app.PathPrefix})
		jsApps[id] = jsApp{Base: app.PathPrefix}
	}

	appsJSON, err := json.Marshal(jsApps)
	if err != nil {
		return nil, fmt.Errorf("marshaling apps: %w", err)
	}

	displayName := cfg.Portal.DisplayName
	if displayName == "" {
		displayName = "Management Portal"
	}

	return &Handler{
		data: shellData{
			DisplayName:   displayName,
			Customization: cfg.Portal.Customization,
			Apps:          apps,
			AppsJSON:      template.JS(appsJSON),
		},
	}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user, _ := oidcauth.UserFromContext(r.Context())

	data := struct {
		shellData
		User string
	}{shellData: h.data, User: user}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := shellTemplate.Execute(w, data); err != nil {
		http.Error(w, "failed to render portal shell", http.StatusInternalServerError)
	}
}
