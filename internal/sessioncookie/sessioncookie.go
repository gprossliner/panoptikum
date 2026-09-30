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

// Package sessioncookie encrypts and authenticates the portal-server's
// session cookie value using a shared symmetric key (see
// docs/ARCHITECTURE.md Decision 7). Any replica can validate a cookie
// issued by any other, so no server-side session store is needed.
package sessioncookie

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/gprossliner/xhdl"

	"github.com/gprossliner/panoptikum/internal/aeadvalue"
)

// Claims is the identity carried in the session cookie. Only $user is
// supported for now (see docs/ARCHITECTURE.md non-goals).
type Claims struct {
	User     string    `json:"user"`
	IssuedAt time.Time `json:"issuedAt"`
}

// Codec encrypts/decrypts session cookie values.
type Codec struct {
	aead *aeadvalue.Codec
}

// NewCodec derives an AES-256-GCM key from secret (an arbitrary-length
// string, e.g. UserAuthentication's resolved cookieSecretRef value) via
// SHA-256, so callers never need to worry about key length.
func NewCodec(ctx xhdl.Context, secret string) *Codec {
	return &Codec{aead: aeadvalue.New(ctx, secret)}
}

// Encode encrypts claims into an opaque, URL-safe cookie value.
func (c *Codec) Encode(ctx xhdl.Context, claims Claims) string {
	plaintext, err := json.Marshal(claims)
	ctx.Throw(err)
	return c.aead.Seal(ctx, plaintext)
}

// Decode validates and decrypts a cookie value produced by Encode. maxAge
// bounds the session's own freshness independent of the cookie's Expires
// attribute, which is only a client-side hint, not a security boundary. A
// maxAge of zero disables the freshness check.
func (c *Codec) Decode(ctx xhdl.Context, value string, maxAge time.Duration) Claims {
	plaintext := c.aead.Open(ctx, value)

	var claims Claims
	err := json.Unmarshal(plaintext, &claims)
	ctx.Throw(err)

	if maxAge > 0 && time.Since(claims.IssuedAt) > maxAge {
		ctx.Throw(errors.New("session expired"))
	}

	return claims
}
