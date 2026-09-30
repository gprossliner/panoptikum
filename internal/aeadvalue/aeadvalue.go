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

// Package aeadvalue encrypts/authenticates an opaque byte value into a
// URL-safe string using a shared symmetric key, and back. It's the shared
// low-level primitive behind both internal/sessioncookie's session cookie
// and the portal-server's OIDC login handshake cookie - centralized here
// rather than duplicated, since it's security-sensitive code.
package aeadvalue

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"

	"github.com/gprossliner/xhdl"
)

// Codec encrypts/decrypts opaque byte values.
type Codec struct {
	gcm cipher.AEAD
}

// New derives an AES-256-GCM key from secret (an arbitrary-length string)
// via SHA-256, so callers never need to worry about key length.
func New(ctx xhdl.Context, secret string) *Codec {
	key := sha256.Sum256([]byte(secret))

	block, err := aes.NewCipher(key[:])
	ctx.Throw(err)

	gcm, err := cipher.NewGCM(block)
	ctx.Throw(err)

	return &Codec{gcm: gcm}
}

// Seal encrypts plaintext into an opaque, URL-safe string.
func (c *Codec) Seal(ctx xhdl.Context, plaintext []byte) string {
	nonce := make([]byte, c.gcm.NonceSize())
	_, err := io.ReadFull(rand.Reader, nonce)
	ctx.Throw(err)

	sealed := c.gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.RawURLEncoding.EncodeToString(sealed)
}

// Open validates and decrypts a string produced by Seal.
func (c *Codec) Open(ctx xhdl.Context, value string) []byte {
	sealed, err := base64.RawURLEncoding.DecodeString(value)
	ctx.Throw(err)

	nonceSize := c.gcm.NonceSize()
	if len(sealed) < nonceSize {
		ctx.Throw(errors.New("ciphertext too short"))
	}
	nonce, ciphertext := sealed[:nonceSize], sealed[nonceSize:]

	plaintext, err := c.gcm.Open(nil, nonce, ciphertext, nil)
	ctx.Throw(err)

	return plaintext
}
