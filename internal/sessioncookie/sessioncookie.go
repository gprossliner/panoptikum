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
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/gprossliner/xhdl"
)

// Claims is the identity carried in the session cookie. Only $user is
// supported for now (see docs/ARCHITECTURE.md non-goals).
type Claims struct {
	User     string    `json:"user"`
	IssuedAt time.Time `json:"issuedAt"`
}

// Codec encrypts/decrypts session cookie values.
type Codec struct {
	gcm cipher.AEAD
}

// NewCodec derives an AES-256-GCM key from secret (an arbitrary-length
// string, e.g. UserAuthentication's resolved cookieSecretRef value) via
// SHA-256, so callers never need to worry about key length.
func NewCodec(ctx xhdl.Context, secret string) *Codec {
	key := sha256.Sum256([]byte(secret))

	block, err := aes.NewCipher(key[:])
	ctx.Throw(err)

	gcm, err := cipher.NewGCM(block)
	ctx.Throw(err)

	return &Codec{gcm: gcm}
}

// Encode encrypts claims into an opaque, URL-safe cookie value.
func (c *Codec) Encode(ctx xhdl.Context, claims Claims) string {
	plaintext, err := json.Marshal(claims)
	ctx.Throw(err)

	nonce := make([]byte, c.gcm.NonceSize())
	_, err = io.ReadFull(rand.Reader, nonce)
	ctx.Throw(err)

	sealed := c.gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.RawURLEncoding.EncodeToString(sealed)
}

// Decode validates and decrypts a cookie value produced by Encode. maxAge
// bounds the session's own freshness independent of the cookie's Expires
// attribute, which is only a client-side hint, not a security boundary. A
// maxAge of zero disables the freshness check.
func (c *Codec) Decode(ctx xhdl.Context, value string, maxAge time.Duration) Claims {
	sealed, err := base64.RawURLEncoding.DecodeString(value)
	ctx.Throw(err)

	nonceSize := c.gcm.NonceSize()
	if len(sealed) < nonceSize {
		ctx.Throw(errors.New("ciphertext too short"))
	}
	nonce, ciphertext := sealed[:nonceSize], sealed[nonceSize:]

	plaintext, err := c.gcm.Open(nil, nonce, ciphertext, nil)
	ctx.Throw(err)

	var claims Claims
	err = json.Unmarshal(plaintext, &claims)
	ctx.Throw(err)

	if maxAge > 0 && time.Since(claims.IssuedAt) > maxAge {
		ctx.Throw(errors.New("session expired"))
	}

	return claims
}
