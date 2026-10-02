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

package routeaccess

import (
	"context"
	"testing"

	"github.com/gprossliner/panoptikum/internal/portalconfig"
)

func TestForDefaultsToAuthenticatedWhenEmpty(t *testing.T) {
	rules, err := Compile(nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if got := rules.For("/anything"); got != portalconfig.RouteAccessAuthenticated {
		t.Errorf("For = %q, want %q", got, portalconfig.RouteAccessAuthenticated)
	}
}

func TestForReturnsAnonymousOnMatch(t *testing.T) {
	rules, err := Compile([]portalconfig.RouteConfig{
		{Match: "^/public-dashboards/", Access: portalconfig.RouteAccessAnonymous},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if got := rules.For("/public-dashboards/abc"); got != portalconfig.RouteAccessAnonymous {
		t.Errorf("For = %q, want %q", got, portalconfig.RouteAccessAnonymous)
	}
}

func TestForDefaultsToAuthenticatedWhenNoRuleMatches(t *testing.T) {
	rules, err := Compile([]portalconfig.RouteConfig{
		{Match: "^/public-dashboards/", Access: portalconfig.RouteAccessAnonymous},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if got := rules.For("/admin"); got != portalconfig.RouteAccessAuthenticated {
		t.Errorf("For = %q, want %q", got, portalconfig.RouteAccessAuthenticated)
	}
}

func TestForFirstMatchWins(t *testing.T) {
	rules, err := Compile([]portalconfig.RouteConfig{
		{Match: "^/public/", Access: portalconfig.RouteAccessAnonymous},
		{Match: "^/public/admin/", Access: portalconfig.RouteAccessAuthenticated},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	// The first (broader) rule matches "/public/admin/x" too, so it wins
	// even though the second, more specific rule would also match.
	if got := rules.For("/public/admin/x"); got != portalconfig.RouteAccessAnonymous {
		t.Errorf("For = %q, want %q (first match should win)", got, portalconfig.RouteAccessAnonymous)
	}
}

func TestCompileRejectsInvalidRegex(t *testing.T) {
	_, err := Compile([]portalconfig.RouteConfig{{Match: "(", Access: portalconfig.RouteAccessAnonymous}})
	if err == nil {
		t.Fatal("Compile succeeded on an invalid regex, want an error")
	}
}

func TestFromContextDefaultsToAuthenticated(t *testing.T) {
	if got := FromContext(context.Background()); got != portalconfig.RouteAccessAuthenticated {
		t.Errorf("FromContext = %q, want %q", got, portalconfig.RouteAccessAuthenticated)
	}
	if IsAnonymous(context.Background()) {
		t.Error("IsAnonymous = true on a bare context, want false")
	}
}

func TestWithAccessRoundTrips(t *testing.T) {
	ctx := WithAccess(context.Background(), portalconfig.RouteAccessAnonymous)
	if got := FromContext(ctx); got != portalconfig.RouteAccessAnonymous {
		t.Errorf("FromContext = %q, want %q", got, portalconfig.RouteAccessAnonymous)
	}
	if !IsAnonymous(ctx) {
		t.Error("IsAnonymous = false after WithAccess(Anonymous), want true")
	}
}
