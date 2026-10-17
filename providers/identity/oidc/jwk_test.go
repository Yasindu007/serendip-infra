package oidc

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"testing"
	"time"
)

func b64u(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// parseJWKS keeps good keys and silently skips bad ones; each bad case here
// must be skipped, not turned into a usable key.
func TestParseJWKS_SkipsInvalidKeys(t *testing.T) {
	rk, _ := rsa.GenerateKey(rand.Reader, 2048)
	small, _ := rsa.GenerateKey(rand.Reader, 1024)
	ek, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	x, y := ek.PublicKey.X.FillBytes(make([]byte, 32)), ek.PublicKey.Y.FillBytes(make([]byte, 32))
	offCurveY := append([]byte(nil), y...)
	offCurveY[31] ^= 1
	e := b64u(big.NewInt(int64(rk.E)).Bytes())

	cases := map[string]map[string]string{
		"good rsa":         {"kty": "RSA", "kid": "good-rsa", "n": b64u(rk.N.Bytes()), "e": e},
		"good ec":          {"kty": "EC", "kid": "good-ec", "crv": "P-256", "x": b64u(x), "y": b64u(y)},
		"rsa under 2048":   {"kty": "RSA", "kid": "small", "n": b64u(small.N.Bytes()), "e": e},
		"rsa even e":       {"kty": "RSA", "kid": "even-e", "n": b64u(rk.N.Bytes()), "e": b64u([]byte{4})},
		"rsa e of 1":       {"kty": "RSA", "kid": "e1", "n": b64u(rk.N.Bytes()), "e": b64u([]byte{1})},
		"rsa e too long":   {"kty": "RSA", "kid": "long-e", "n": b64u(rk.N.Bytes()), "e": b64u([]byte{1, 0, 0, 0, 1})},
		"ec off curve":     {"kty": "EC", "kid": "off", "crv": "P-256", "x": b64u(x), "y": b64u(offCurveY)},
		"ec short x":       {"kty": "EC", "kid": "short", "crv": "P-256", "x": b64u(x[:31]), "y": b64u(y)},
		"ec unknown curve": {"kty": "EC", "kid": "k256", "crv": "secp256k1", "x": b64u(x), "y": b64u(y)},
		"okp not ed25519":  {"kty": "OKP", "kid": "x25519", "crv": "X25519", "x": b64u(make([]byte, 32))},
		"okp short x":      {"kty": "OKP", "kid": "short-ed", "crv": "Ed25519", "x": b64u(make([]byte, 31))},
		"symmetric":        {"kty": "oct", "kid": "hmac", "k": "c2VjcmV0"},
		"encryption use":   {"kty": "RSA", "kid": "enc", "use": "enc", "n": b64u(rk.N.Bytes()), "e": e},
	}
	var keys []map[string]string
	for _, k := range cases {
		keys = append(keys, k)
	}
	doc, _ := json.Marshal(map[string]any{"keys": keys})
	got, err := parseJWKS(doc)
	if err != nil {
		t.Fatal(err)
	}
	kids := map[string]bool{}
	for _, k := range got {
		kids[k.kid] = true
	}
	if len(got) != 2 || !kids["good-rsa"] || !kids["good-ec"] {
		t.Errorf("usable keys = %v, want exactly good-rsa and good-ec", kids)
	}

	if _, err := parseJWKS([]byte("not json")); err == nil {
		t.Error("unreadable document: want an error")
	}
}

func TestParseJWKS_CapsKeyCount(t *testing.T) {
	ek, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	x, y := ek.PublicKey.X.FillBytes(make([]byte, 32)), ek.PublicKey.Y.FillBytes(make([]byte, 32))
	var keys []map[string]string
	for i := range maxKeys + 10 {
		keys = append(keys, map[string]string{"kty": "EC", "kid": fmt.Sprint(i), "crv": "P-256", "x": b64u(x), "y": b64u(y)})
	}
	doc, _ := json.Marshal(map[string]any{"keys": keys})
	got, _ := parseJWKS(doc)
	if len(got) != maxKeys {
		t.Errorf("keys = %d, want capped at %d", len(got), maxKeys)
	}
}

func TestUnixTime(t *testing.T) {
	tests := []struct {
		raw    string
		want   time.Time
		wantOK bool
	}{
		{"1792250480", time.Unix(1792250480, 0), true},
		{"1792250480.5", time.Unix(1792250480, 5e8), true},
		{"0", time.Unix(0, 0), true},
		{"1e30", time.Time{}, false},
		{"-1e30", time.Time{}, false},
		{"-1", time.Time{}, false},
		{"1e999", time.Time{}, false},
	}
	for _, tt := range tests {
		n := json.Number(tt.raw)
		got, ok := unixTime(&n)
		if ok != tt.wantOK || (ok && !got.Equal(tt.want)) {
			t.Errorf("unixTime(%s) = (%v, %v), want (%v, %v)", tt.raw, got, ok, tt.want, tt.wantOK)
		}
	}
	if _, ok := unixTime(nil); ok {
		t.Error("unixTime(nil): want not ok")
	}
}
