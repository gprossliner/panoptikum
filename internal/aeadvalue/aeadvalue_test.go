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

package aeadvalue

import (
	"encoding/base64"
	"testing"

	"github.com/gprossliner/xhdl"
)

func TestSealOpenRoundTrip(t *testing.T) {
	want := []byte("hello, world")
	var got []byte

	err := xhdl.Run(func(ctx xhdl.Context) {
		codec := New(ctx, "test-secret")
		value := codec.Seal(ctx, want)
		got = codec.Open(ctx, value)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(got) != string(want) {
		t.Errorf("Open() = %q, want %q", got, want)
	}
}

func TestOpenRejectsTamperedValue(t *testing.T) {
	var value string
	err := xhdl.Run(func(ctx xhdl.Context) {
		codec := New(ctx, "test-secret")
		value = codec.Seal(ctx, []byte("hello, world"))
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
		codec := New(ctx, "test-secret")
		codec.Open(ctx, tampered)
	})
	if err == nil {
		t.Error("Open succeeded on a tampered value, want error")
	}
}

func TestOpenRejectsDifferentSecret(t *testing.T) {
	var value string
	err := xhdl.Run(func(ctx xhdl.Context) {
		encoder := New(ctx, "secret-a")
		value = encoder.Seal(ctx, []byte("hello, world"))
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = xhdl.Run(func(ctx xhdl.Context) {
		decoder := New(ctx, "secret-b")
		decoder.Open(ctx, value)
	})
	if err == nil {
		t.Error("Open succeeded with a different secret, want error")
	}
}
