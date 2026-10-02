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

// Package routeaccess evaluates AppRegistration.spec.accessRules[] (issue
// #11) against a request's app-relative path, and threads the resulting
// decision through the request context so internal/appproxy can tell a
// deliberately Anonymous route apart from a bug (Middleware not applied
// where it should have been) when no authenticated user is present.
package routeaccess

import (
	"context"
	"fmt"
	"regexp"

	"github.com/gprossliner/panoptikum/internal/portalconfig"
)

// rule is one compiled AppRegistration.spec.accessRules[] entry.
type rule struct {
	match  *regexp.Regexp
	access portalconfig.AccessRuleAccess
}

// Rules are evaluated in order by For; the first match wins.
type Rules []rule

// Compile compiles accessRules in order. Shouldn't ever fail in practice -
// each MatchRoute is already validated at reconcile time (see
// evaluateAccepted in internal/controller/appregistration_controller.go) -
// but returns an error rather than panicking, so a bad config can't crash
// the portal-server.
func Compile(accessRules []portalconfig.AccessRuleConfig) (Rules, error) {
	rules := make(Rules, 0, len(accessRules))
	for i, cfg := range accessRules {
		re, err := regexp.Compile(cfg.MatchRoute)
		if err != nil {
			return nil, fmt.Errorf("accessRules[%d].matchRoute %q: %w", i, cfg.MatchRoute, err)
		}
		rules = append(rules, rule{match: re, access: cfg.Access})
	}
	return rules, nil
}

// For returns the access level for relPath - the request path with the
// app's own pathPrefix already stripped by the caller. The first matching
// rule wins; Authenticated if nothing matches (or rs is empty).
func (rs Rules) For(relPath string) portalconfig.AccessRuleAccess {
	for _, ru := range rs {
		if ru.match.MatchString(relPath) {
			return ru.access
		}
	}
	return portalconfig.AccessRuleAccessAuthenticated
}

type contextKey struct{}

// WithAccess attaches the access level a request was routed with.
func WithAccess(ctx context.Context, access portalconfig.AccessRuleAccess) context.Context {
	return context.WithValue(ctx, contextKey{}, access)
}

// FromContext returns the access level attached by WithAccess, defaulting
// to Authenticated (the safe default) if none was attached.
func FromContext(ctx context.Context) portalconfig.AccessRuleAccess {
	if access, ok := ctx.Value(contextKey{}).(portalconfig.AccessRuleAccess); ok {
		return access
	}
	return portalconfig.AccessRuleAccessAuthenticated
}

// IsAnonymous reports whether ctx was attached with AccessRuleAccessAnonymous.
func IsAnonymous(ctx context.Context) bool {
	return FromContext(ctx) == portalconfig.AccessRuleAccessAnonymous
}
