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

package sessioncookie

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/gprossliner/xhdl"
)

const testUser = "alice"

func TestEncodeDecodeRoundTrip(t *testing.T) {
	var got Claims
	want := Claims{User: testUser, IssuedAt: time.Now()}

	err := xhdl.Run(func(ctx xhdl.Context) {
		codec := NewCodec(ctx, "test-secret")
		value := codec.Encode(ctx, want)
		got = codec.Decode(ctx, value, 0)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.User != want.User {
		t.Errorf("User = %q, want %q", got.User, want.User)
	}
	if !got.IssuedAt.Equal(want.IssuedAt) {
		t.Errorf("IssuedAt = %v, want %v", got.IssuedAt, want.IssuedAt)
	}
}

func TestDecodeRejectsTamperedValue(t *testing.T) {
	var value string
	err := xhdl.Run(func(ctx xhdl.Context) {
		codec := NewCodec(ctx, "test-secret")
		value = codec.Encode(ctx, Claims{User: testUser, IssuedAt: time.Now()})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Tamper the decoded bytes, not the base64 string - flipping an encoded
	// *character* isn't reliable, since a trailing base64 group can have
	// unused padding bits that don't map to any actual byte. XOR-ing a
	// whole byte with 0xFF is guaranteed to change it, whatever its value.
	sealed, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		t.Fatalf("decoding test fixture: %v", err)
	}
	sealed[len(sealed)-1] ^= 0xFF
	tampered := base64.RawURLEncoding.EncodeToString(sealed)

	err = xhdl.Run(func(ctx xhdl.Context) {
		codec := NewCodec(ctx, "test-secret")
		codec.Decode(ctx, tampered, 0)
	})
	if err == nil {
		t.Error("Decode succeeded on a tampered value, want error")
	}
}

func TestDecodeRejectsDifferentSecret(t *testing.T) {
	var value string
	err := xhdl.Run(func(ctx xhdl.Context) {
		encoder := NewCodec(ctx, "secret-a")
		value = encoder.Encode(ctx, Claims{User: testUser, IssuedAt: time.Now()})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = xhdl.Run(func(ctx xhdl.Context) {
		decoder := NewCodec(ctx, "secret-b")
		decoder.Decode(ctx, value, 0)
	})
	if err == nil {
		t.Error("Decode succeeded with a different secret, want error")
	}
}

func TestDecodeRejectsExpiredSession(t *testing.T) {
	var value string
	err := xhdl.Run(func(ctx xhdl.Context) {
		codec := NewCodec(ctx, "test-secret")
		value = codec.Encode(ctx, Claims{User: testUser, IssuedAt: time.Now().Add(-2 * time.Hour)})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = xhdl.Run(func(ctx xhdl.Context) {
		codec := NewCodec(ctx, "test-secret")
		codec.Decode(ctx, value, time.Hour)
	})
	if err == nil {
		t.Error("Decode succeeded on an expired session, want error")
	}

	// A session within maxAge should still decode fine.
	err = xhdl.Run(func(ctx xhdl.Context) {
		codec := NewCodec(ctx, "test-secret")
		codec.Decode(ctx, value, 3*time.Hour)
	})
	if err != nil {
		t.Errorf("Decode failed on a non-expired session: %v", err)
	}
}
