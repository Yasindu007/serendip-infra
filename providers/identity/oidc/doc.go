// Package oidc verifies OIDC/OAuth2 JWT access tokens against any issuer
// that publishes a JWKS -- the provider-neutral half of the Identity
// capability (specs/015-identity-foundation plan.md decisions 7 and 9).
// Nothing here knows which identity provider issued a token: pointing a
// Verifier at a different issuer is configuration only, which is what the
// planned external-OIDC provider will rely on.
//
// It is standard library only (JWS compact parsing, crypto/rsa,
// crypto/ecdsa, crypto/ed25519), for the same reason the OpenBao client is
// hand-rolled (ADR 0009): an OIDC library would pull in a JOSE and an
// OAuth2 stack for a few hundred lines that can be fully tested here.
//
// Accepted: RS256/384/512, PS256/384/512, ES256/384/512, EdDSA (Ed25519).
// Rejected outright: "none", every HMAC algorithm, anything else, tokens
// with a "crit" header, and a signature from a key whose type or curve
// doesn't match the token's algorithm.
package oidc
