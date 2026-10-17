package oidc

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// header is the subset of a JWS protected header the verifier reads.
type header struct {
	Alg  string          `json:"alg"`
	Kid  string          `json:"kid"`
	Crit json.RawMessage `json:"crit"`
}

// token is a parsed (not yet verified) JWS compact serialization.
type token struct {
	header       header
	payload      []byte // decoded claims JSON
	signingInput []byte // "<header>.<payload>" as sent, what the signature covers
	signature    []byte
}

// maxTokenSize bounds what is even parsed; real access tokens are a few KB.
const maxTokenSize = 16 << 10

func parseToken(raw string) (token, error) {
	if len(raw) > maxTokenSize {
		return token{}, errors.New("token too large")
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return token{}, errors.New("not a JWS compact token")
	}
	hb, err := b64(parts[0])
	if err != nil {
		return token{}, fmt.Errorf("header: %w", err)
	}
	var h header
	if err := json.Unmarshal(hb, &h); err != nil {
		return token{}, fmt.Errorf("header: %w", err)
	}
	if len(h.Crit) > 0 {
		return token{}, errors.New(`unsupported "crit" header`)
	}
	payload, err := b64(parts[1])
	if err != nil {
		return token{}, fmt.Errorf("payload: %w", err)
	}
	sig, err := b64(parts[2])
	if err != nil {
		return token{}, fmt.Errorf("signature: %w", err)
	}
	if len(sig) == 0 {
		return token{}, errors.New("empty signature")
	}
	return token{
		header:       h,
		payload:      payload,
		signingInput: []byte(parts[0] + "." + parts[1]),
		signature:    sig,
	}, nil
}

// b64 decodes unpadded base64url, as JOSE requires.
func b64(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

// algorithm describes one accepted JWS algorithm.
type algorithm struct {
	kty   string      // the JWK key type it needs
	hash  crypto.Hash // 0 for EdDSA (signs the message itself)
	pss   bool        // RSASSA-PSS rather than PKCS#1 v1.5
	curve string      // ECDSA/EdDSA curve the key must be on
}

// algorithms is the allow-list. Anything absent -- "none", HS*, unknown --
// is rejected before any key is looked at.
var algorithms = map[string]algorithm{
	"RS256": {kty: "RSA", hash: crypto.SHA256},
	"RS384": {kty: "RSA", hash: crypto.SHA384},
	"RS512": {kty: "RSA", hash: crypto.SHA512},
	"PS256": {kty: "RSA", hash: crypto.SHA256, pss: true},
	"PS384": {kty: "RSA", hash: crypto.SHA384, pss: true},
	"PS512": {kty: "RSA", hash: crypto.SHA512, pss: true},
	"ES256": {kty: "EC", hash: crypto.SHA256, curve: "P-256"},
	"ES384": {kty: "EC", hash: crypto.SHA384, curve: "P-384"},
	"ES512": {kty: "EC", hash: crypto.SHA512, curve: "P-521"},
	"EdDSA": {kty: "OKP", curve: "Ed25519"},
}

// verifySignature checks t's signature with k, which must suit alg.
func verifySignature(t token, alg algorithm, k jwk) error {
	if k.kty != alg.kty || (alg.curve != "" && k.crv != alg.curve) {
		return fmt.Errorf("key %q (%s %s) does not match algorithm %s", k.kid, k.kty, k.crv, t.header.Alg)
	}
	if k.alg != "" && k.alg != t.header.Alg {
		return fmt.Errorf("key %q is for %s, token uses %s", k.kid, k.alg, t.header.Alg)
	}
	switch pub := k.key.(type) {
	case *rsa.PublicKey:
		digest := hashOf(alg.hash, t.signingInput)
		if alg.pss {
			return rsa.VerifyPSS(pub, alg.hash, digest, t.signature, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		}
		return rsa.VerifyPKCS1v15(pub, alg.hash, digest, t.signature)
	case *ecdsa.PublicKey:
		// JWS ECDSA signatures are R||S, each the curve's byte size.
		size := (pub.Curve.Params().BitSize + 7) / 8
		if len(t.signature) != 2*size {
			return errors.New("ecdsa signature has the wrong length")
		}
		r := new(big.Int).SetBytes(t.signature[:size])
		s := new(big.Int).SetBytes(t.signature[size:])
		if !ecdsa.Verify(pub, hashOf(alg.hash, t.signingInput), r, s) {
			return errors.New("ecdsa signature invalid")
		}
		return nil
	case ed25519.PublicKey:
		if !ed25519.Verify(pub, t.signingInput, t.signature) {
			return errors.New("ed25519 signature invalid")
		}
		return nil
	default:
		return fmt.Errorf("key %q has an unsupported type", k.kid)
	}
}

func hashOf(h crypto.Hash, data []byte) []byte {
	hh := h.New()
	hh.Write(data)
	return hh.Sum(nil)
}
