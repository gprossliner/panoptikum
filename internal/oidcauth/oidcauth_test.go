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
	"testing"

	"github.com/gprossliner/xhdl"
)

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
