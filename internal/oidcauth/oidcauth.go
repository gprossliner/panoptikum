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

// Package oidcauth implements the portal-server's OIDC login flow
// (/_panoptikum/login, /_panoptikum/oidc-callback) and owns the session
// cookie it produces (see docs/ARCHITECTURE.md "Server (portal-server)" >
// "Session & OIDC").
package oidcauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/gprossliner/xhdl"

	"github.com/gprossliner/panoptikum/internal/aeadvalue"
	"github.com/gprossliner/panoptikum/internal/portalconfig"
	"github.com/gprossliner/panoptikum/internal/sessioncookie"
)

const (
	// ReservedPrefix is the root for the portal-server's own routes,
	// distinct from any AppRegistration's own pathPrefix (validated, via
	// CEL, to never start with this - see AppRegistrationRouting.PathPrefix).
	ReservedPrefix = "/_panoptikum/"

	// LoginPath, CallbackPath and LogoutPath are where
	// HandleLogin/HandleCallback/HandleLogout are meant to be mounted.
	LoginPath    = ReservedPrefix + "login"
	CallbackPath = ReservedPrefix + "oidc-callback"
	LogoutPath   = ReservedPrefix + "logout"

	// HandshakeCookieName holds the short-lived, single-use OIDC handshake
	// state between /_panoptikum/login and /_panoptikum/oidc-callback.
	HandshakeCookieName = "panoptikum_handshake"

	// SessionCookieName holds the logged-in user's session, set by
	// /_panoptikum/oidc-callback and read by the auth middleware.
	SessionCookieName = "panoptikum_session"

	// HandshakeMaxAge bounds how long a user has to complete login at the
	// IdP before the handshake cookie is considered stale.
	HandshakeMaxAge = 10 * time.Minute

	// SessionMaxAge bounds how long a session cookie is valid for,
	// independent of its own Expires attribute (see sessioncookie.Codec.Decode).
	SessionMaxAge = 8 * time.Hour
)

// handshakeClaims is the short-lived state carried between /_panoptikum/login
// and /_panoptikum/oidc-callback - never exposed outside this package.
type handshakeClaims struct {
	State        string    `json:"state"`
	Nonce        string    `json:"nonce"`
	CodeVerifier string    `json:"codeVerifier"`
	ReturnTo     string    `json:"returnTo"`
	IssuedAt     time.Time `json:"issuedAt"`
}

// Handler implements the OIDC login flow for one Portal.
type Handler struct {
	oauth2Config   oauth2.Config
	verifier       *oidc.IDTokenVerifier
	handshakeCodec *aeadvalue.Codec
	sessionCodec   *sessioncookie.Codec
}

// NewHandler discovers the OIDC provider at cfg.IssuerURL and builds a
// Handler for it. redirectURL must be the externally-reachable
// /_panoptikum/oidc-callback URL (Portal.spec.host + "/_panoptikum/oidc-callback").
func NewHandler(ctx xhdl.Context, cfg portalconfig.UserAuthenticationConfig, redirectURL string) *Handler {
	provider, err := oidc.NewProvider(ctx, cfg.IssuerURL)
	ctx.Throw(err)

	return &Handler{
		oauth2Config: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  redirectURL,
			Scopes:       cfg.Scopes,
		},
		verifier:       provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		handshakeCodec: aeadvalue.New(ctx, cfg.CookieSecret),
		sessionCodec:   sessioncookie.NewCodec(ctx, cfg.CookieSecret),
	}
}

// HandleLogin starts the OIDC authorization code + PKCE flow, remembering
// the originally-requested URL (the "rd" query parameter, matching the
// predecessor oauth2-proxy's sign_in?rd=<url> convention) so
// /_panoptikum/oidc-callback can redirect back to it.
func (h *Handler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	err := xhdl.RunContext(r.Context(), func(ctx xhdl.Context) {
		returnTo := r.URL.Query().Get("rd")
		if returnTo == "" {
			returnTo = "/"
		}

		claims := handshakeClaims{
			State:        randomToken(ctx),
			Nonce:        randomToken(ctx),
			CodeVerifier: oauth2.GenerateVerifier(),
			ReturnTo:     returnTo,
			IssuedAt:     time.Now(),
		}

		data, err := json.Marshal(claims)
		ctx.Throw(err)

		setCookie(w, HandshakeCookieName, h.handshakeCodec.Seal(ctx, data), HandshakeMaxAge)

		authURL := h.oauth2Config.AuthCodeURL(claims.State,
			oidc.Nonce(claims.Nonce),
			oauth2.S256ChallengeOption(claims.CodeVerifier),
		)
		http.Redirect(w, r, authURL, http.StatusFound)
	})
	if err != nil {
		http.Error(w, "login failed", http.StatusInternalServerError)
	}
}

// HandleCallback completes the flow started by HandleLogin: validates the
// handshake, exchanges the authorization code, verifies the ID token, and
// sets the session cookie before redirecting back to the original URL.
func (h *Handler) HandleCallback(w http.ResponseWriter, r *http.Request) {
	err := xhdl.RunContext(r.Context(), func(ctx xhdl.Context) {
		claims := h.readHandshake(ctx, r)

		// Single-use: clear it regardless of what happens next.
		setCookie(w, HandshakeCookieName, "", -1)

		if r.URL.Query().Get("state") != claims.State {
			ctx.Throw(errors.New("state mismatch"))
		}

		token, err := h.oauth2Config.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(claims.CodeVerifier))
		ctx.Throw(err)

		rawIDToken, ok := token.Extra("id_token").(string)
		if !ok {
			ctx.Throw(errors.New("token response has no id_token"))
		}

		idToken, err := h.verifier.Verify(ctx, rawIDToken)
		ctx.Throw(err)

		if idToken.Nonce != claims.Nonce {
			ctx.Throw(errors.New("nonce mismatch"))
		}

		user := extractUser(ctx, idToken.Subject, idToken)
		sessionValue := h.sessionCodec.Encode(ctx, sessioncookie.Claims{User: user, IssuedAt: time.Now()})
		setCookie(w, SessionCookieName, sessionValue, SessionMaxAge)

		http.Redirect(w, r, claims.ReturnTo, http.StatusFound)
	})
	if err != nil {
		http.Error(w, "login callback failed", http.StatusBadRequest)
	}
}

// HandleLogout clears the session cookie and redirects to the portal root.
// No RP-initiated logout at the IdP (non-goal, see docs/ARCHITECTURE.md) -
// this only ends the portal's own session.
func (h *Handler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	setCookie(w, SessionCookieName, "", -time.Second)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (h *Handler) readHandshake(ctx xhdl.Context, r *http.Request) handshakeClaims {
	cookie, err := r.Cookie(HandshakeCookieName)
	if err != nil {
		ctx.Throw(fmt.Errorf("missing handshake cookie: %w", err))
	}

	data := h.handshakeCodec.Open(ctx, cookie.Value)

	var claims handshakeClaims
	err = json.Unmarshal(data, &claims)
	ctx.Throw(err)

	if time.Since(claims.IssuedAt) > HandshakeMaxAge {
		ctx.Throw(errors.New("handshake expired"))
	}

	return claims
}

// userContextKey is unexported so only this package can set/read it - no
// caller-supplied context value can spoof an authenticated user.
type userContextKey struct{}

// WithUser returns a copy of ctx carrying user, as UserFromContext expects.
// Middleware is the only production caller; exported so packages consuming
// UserFromContext (e.g. appproxy) can attach a user in tests without a real
// session cookie/Handler round trip.
func WithUser(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, userContextKey{}, user)
}

// UserFromContext returns the user attached by Middleware, or ok=false if
// the request context has none (i.e. the request didn't go through Middleware).
func UserFromContext(ctx context.Context) (user string, ok bool) {
	user, ok = ctx.Value(userContextKey{}).(string)
	return user, ok
}

// Middleware gates next behind a valid session cookie: missing, tampered,
// or expired -> redirect to /_panoptikum/login?rd=<original-url> (clearing
// the bad cookie first, so a stale-but-undecryptable cookie can't cause a
// redirect loop); valid -> the authenticated user is attached to the
// request context (see UserFromContext) and next is called.
func (h *Handler) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(SessionCookieName)
		if err != nil {
			h.redirectToLogin(w, r)
			return
		}

		var claims sessioncookie.Claims
		ok := xhdl.Run(func(ctx xhdl.Context) {
			claims = h.sessionCodec.Decode(ctx, cookie.Value, SessionMaxAge)
		}) == nil
		if !ok {
			setCookie(w, SessionCookieName, "", -time.Second)
			h.redirectToLogin(w, r)
			return
		}

		next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), claims.User)))
	})
}

func (h *Handler) redirectToLogin(w http.ResponseWriter, r *http.Request) {
	q := url.Values{"rd": {r.URL.RequestURI()}}
	http.Redirect(w, r, LoginPath+"?"+q.Encode(), http.StatusFound)
}

// claimsSource is satisfied by *oidc.IDToken - narrowed to just what
// extractUser needs, so it can be unit-tested without a real ID token.
type claimsSource interface {
	Claims(v any) error
}

// extractUser picks the $user claim per the non-goals (see
// docs/ARCHITECTURE.md): preferred_username, falling back to email, then
// subject, whichever is populated first.
func extractUser(ctx xhdl.Context, subject string, src claimsSource) string {
	var claims struct {
		PreferredUsername string `json:"preferred_username"`
		Email             string `json:"email"`
	}
	err := src.Claims(&claims)
	ctx.Throw(err)

	switch {
	case claims.PreferredUsername != "":
		return claims.PreferredUsername
	case claims.Email != "":
		return claims.Email
	default:
		return subject
	}
}

func randomToken(ctx xhdl.Context) string {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	ctx.Throw(err)
	return base64.RawURLEncoding.EncodeToString(b)
}

// setCookie sets a cookie with the security attributes appropriate for
// both the handshake and session cookies. Secure is always true: the
// browser only ever talks to the external HTTPS endpoint (ARCHITECTURE.md
// notes the portal-server itself only sees plain HTTP from the
// TLS-terminating ingress), so this reflects the browser-facing scheme,
// not the portal-server's own listener.
func setCookie(w http.ResponseWriter, name, value string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(maxAge.Seconds()),
	})
}
