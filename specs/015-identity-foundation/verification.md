# Spec 015 — verification record (T007, T008)

Everything here was observed directly on 2026-10-17, as T007's Verify line
requires, not inferred. Unit tests are listed only where they are the
evidence.

## Setup

- A fresh kind cluster (`kindest/node:v1.34.0`, `profiles/edge-1/bench/kind-t470.yaml`
  for the host port 30080 mapping; CPU limits lifted for speed).
- A fresh `serendipd` at `b0a55c6` with `SERENDIP_REENSURE_INTERVAL=30s` and
  API key `k-alice:alice`.
- Logins used a scratch script that drives the same flow as ThunderID's
  login page (plan.md Spike finding 4): `GET /oauth2/authorize` →
  `POST /flow/execute` (twice) → `POST /oauth2/auth/callback` →
  `POST /oauth2/token` with the PKCE verifier. The admin password was read
  from the Secret and never printed.
- The fresh install rode out two real chart-pull timeouts (kgateway,
  `TLS handshake timeout`) through spec 014's setup retries. It then logged
  `identity provider (thunderid) active, issuer http://identity.localhost:30080`
  and `all capabilities ready`.

## Acceptance criteria

| AC | Observed |
|---|---|
| 1 | ADR 0006 "Re-check 2026-10-17": a verdict per trigger, chart and image 1.0.1 pinned, repo `thunder-id/thunderid`. |
| 2 | `GET /v1/readiness`: all five capabilities, including `identity`, `true Ready`. Discovery fetched from the host at `http://identity.localhost:30080/.well-known/openid-configuration` reported `issuer` exactly that. `kubectl`: Secret `serendip-identity-admin`, Helm release `thunderid`, HTTPRoute `serendip-identity` (`Accepted=True ResolvedRefs=True Programmed=True`), PVCs `thunderid-{certs,database,secrets}-pvc` Bound, Deployment 1/1. |
| 3 | Scripted login as `admin` → access token. `GET /v1/applications` → **200**; `POST /v1/applications` → **201**. The audit row (SQLite `audit_log`) for the created Application has actor **`person:admin`**, interface `http`, with the request ID. |
| 4 | Each gave **401** with the same body, `{"title":"unauthorized","detail":"missing or invalid bearer token",…}`:<br>• no token;<br>• `garbage.token.here`;<br>• the real token re-signed with a foreign RSA key;<br>• the same with `iss` rewritten;<br>• the ID token (log: `no client_id (not an access token)`);<br>• the real token 45 s after its `exp` (log: `expired`).<br>The real token before expiry got 200. The rejection reasons appeared only in `serendipd`'s log. |
| 5 | With API key `k-alice`: Application → Deployed; an exposed nginx Component reached `Serving` in 31 s (the image was pulled); its endpoint `http://development-openchoreo-control-plane.localhost:30080/cmp-…-http` → **200**; delete → **204**; the audit actors were `alice` and `system:reconciler`. `go test -race ./...` green. |
| 6 | • **`serendipd` restart** (rebuilt binary, same store): Job event count 2 → 2 (no setup job), admin password hash unchanged, Helm revision 1 → 2 (upgrade only). A new login worked, and the pre-restart token → 200.<br>• **`kubectl delete pod`** on ThunderID: JWKS byte-identical before and after; the pre-restart token → 200; a fresh login signed with the same `kid`.<br>• **`kubectl delete httproute serendip-identity`**: discovery → 404 at once; the route was recreated by the foundations loop after **17 s**, discovery was back to 200 at 18 s, and `identity` stayed Ready, with no restart. |
| 7 | With the kind node container stopped, a second `serendipd` on a fresh store:<br>• API key → **200**;<br>• `/v1/readiness` `identity false SettingUp - waiting for the gateway capability` (gateway, secrets and storage `SetupFailed` with reasons);<br>• a valid access token → **503** `identity provider not ready` / `the bearer token can't be verified yet; retry later`.<br>After the node was started again, all capabilities were ready **76 s** later with no restart, and the same token → **200**. The first `serendipd`, with cached keys, kept accepting it throughout. |
| 8 | `check-imports.sh` clean. No product name added to `core/` or `api/` by this spec; the only hits are spec 008/009 comments in the Secrets code. `TestVerifier_SecondIssuerConfigurationOnly`: a second, unrelated issuer verifies with configuration only. |
| 9 | T008: two simulated-T470 runs in `profiles/edge-1/bench/results/2026-10-17-spec015-simulated-t470/`. Identity is about 10.7 MiB / 0.2 mcores; every existing group PASSes against spec 014's run 1, and the runs agree with each other; `serendipd` 26.2/26.3 MiB (rule 4 PASS); 16 GB at 11%/10%. Write-up: `002-edge1-footprint.md` "Spec 015 re-run". |
| 10 | `go vet ./...` and `gofmt -l .` clean. `contract-guardian` PASS (T002-T006, and the final holistic pass); `go-reviewer` PASS, with hardening in `235cbc1`. |
