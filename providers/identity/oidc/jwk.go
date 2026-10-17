package oidc

import (
	"crypto"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
)

// jwk is one usable signing key from a JWKS.
type jwk struct {
	kid string
	kty string // RSA, EC, OKP
	crv string // EC/OKP curve
	alg string // optional; when set, the only algorithm it may verify
	key crypto.PublicKey
}

type rawJWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	Crv string `json:"crv"`
	N   string `json:"n"`
	E   string `json:"e"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// minRSABits rejects keys too weak to trust.
const minRSABits = 2048

// parseJWKS returns the usable signing keys in a JWKS document, and an
// error only when the document itself is unreadable. Individual keys that
// are encryption keys, of an unknown type, or malformed are skipped: one
// odd key mustn't stop the others from verifying.
func parseJWKS(data []byte) ([]jwk, error) {
	var doc struct {
		Keys []rawJWK `json:"keys"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("decode JWKS: %w", err)
	}
	var keys []jwk
	for _, rk := range doc.Keys {
		if rk.Use != "" && rk.Use != "sig" {
			continue
		}
		k, err := parseJWK(rk)
		if err != nil {
			continue
		}
		keys = append(keys, k)
	}
	return keys, nil
}

func parseJWK(rk rawJWK) (jwk, error) {
	k := jwk{kid: rk.Kid, kty: rk.Kty, crv: rk.Crv, alg: rk.Alg}
	switch rk.Kty {
	case "RSA":
		n, err := b64(rk.N)
		if err != nil || len(n) == 0 {
			return jwk{}, errors.New("rsa: bad n")
		}
		e, err := b64(rk.E)
		if err != nil || len(e) == 0 || len(e) > 4 {
			return jwk{}, errors.New("rsa: bad e")
		}
		pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		if pub.N.BitLen() < minRSABits || pub.E < 3 || pub.E%2 == 0 {
			return jwk{}, errors.New("rsa: weak or invalid key")
		}
		k.key = pub
	case "EC":
		pub, err := ecKey(rk)
		if err != nil {
			return jwk{}, err
		}
		k.key = pub
	case "OKP":
		if rk.Crv != "Ed25519" {
			return jwk{}, fmt.Errorf("okp: unsupported curve %q", rk.Crv)
		}
		x, err := b64(rk.X)
		if err != nil || len(x) != ed25519.PublicKeySize {
			return jwk{}, errors.New("okp: bad x")
		}
		k.key = ed25519.PublicKey(x)
	default:
		return jwk{}, fmt.Errorf("unsupported kty %q", rk.Kty)
	}
	return k, nil
}

// ecKey decodes an EC JWK, using crypto/ecdh to reject points that aren't
// on the curve.
func ecKey(rk rawJWK) (*ecdsa.PublicKey, error) {
	var curve elliptic.Curve
	var ec ecdh.Curve
	switch rk.Crv {
	case "P-256":
		curve, ec = elliptic.P256(), ecdh.P256()
	case "P-384":
		curve, ec = elliptic.P384(), ecdh.P384()
	case "P-521":
		curve, ec = elliptic.P521(), ecdh.P521()
	default:
		return nil, fmt.Errorf("ec: unsupported curve %q", rk.Crv)
	}
	size := (curve.Params().BitSize + 7) / 8
	x, err := b64(rk.X)
	if err != nil || len(x) != size {
		return nil, errors.New("ec: bad x")
	}
	y, err := b64(rk.Y)
	if err != nil || len(y) != size {
		return nil, errors.New("ec: bad y")
	}
	point := append(append([]byte{4}, x...), y...)
	if _, err := ec.NewPublicKey(point); err != nil {
		return nil, fmt.Errorf("ec: %w", err)
	}
	return &ecdsa.PublicKey{Curve: curve, X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}, nil
}
