# Spec 017 — verification record (T010)

Everything here was observed directly on 2026-10-18 unless marked
otherwise.

## Setup

- **Cluster:** a fresh kind cluster `serendip-t017` with the simulated
  T470 limits from `kind-t470-setup.sh` (2 CPUs of quota on 4 threads).
  T009's simulated-T470 benchmark ran on it first, from a cold install.
- **serendipd:** built from the working tree with the console embedded.
  It used a fresh store, `SERENDIP_API_KEYS=…:ops` and the default public
  URL `http://localhost:8080`.
- **People in ThunderID** (created through its management API over a
  port-forward):
  - the bootstrap `admin`;
  - `t017-reader`;
  - `t017-member`, in the group `t017-readers`.
- **Drivers (scratch, session-local):**
  - a Python driver that signs in through `serendipd`'s own `/auth/login`
    and `/auth/callback` against the real ThunderID, then calls `/v1` with
    the session cookie, as the console does;
  - spec 016's dependency-free headless-Chrome script, for screenshots
    and in-page checks.
  - Passwords were read into the environment and never printed; nor were
    any tokens or session IDs.

## Found and fixed during T010

1. **An existing install's console URIs.** The benchmark's `serendipd`
   had installed the stack with a different console address, so this
   `serendipd` reported "console login not available yet".
   - That's the documented existing-install case, so it was a real test
     of T004's generalised operator tool. `serendip-identity-redirects
     --public-url http://localhost:8080` registered the URIs and confirmed
     the token attributes (`username, email, name, groups`).
   - After a restart: `console login enabled`.
2. **Console polish, seen only in a real browser:**
   - the Access control intro named the scope by raw ID, and now uses its
     name;
   - "Built-in" wrapped at its hyphen;
   - beside a `Guarded` action with its reason, the header's other button
     stretched to the same height (now `align-items: flex-start`).
   - All three were re-captured after the fix.

## Acceptance criteria

| AC | Observed |
|---|---|
| 1 Reader | **Setup:** `t017-reader` has Reader at Application `t017-a` only. **Lists:** `GET /v1/applications` returns `["t017-a"]`. **Reads:** `t017-a` and its Component are 200; `t017-b` is **403** with the detail `missing serendip/applications/read at Application/…`. **Changes:** creating a Component in `t017-a`, updating it, and assigning a role are each **403**. **Permissions at `t017-a`:** `[{"actions": ["*/read"]}]`. |
| 2 Contributor | **API tests** (`TestAuthz_Contributor`, `TestAuthzAPI_ContributorCantAssign`): changes in `prod`, 403 in `dev`, 403 on any role assignment. Not repeated on the cluster: the decision path is identical to AC 1's. |
| 3 Data plane | **Owner only:** the `ops` API key, Owner at `/` through the migration, reads the Secret (200) but its value is **403**. **Owner plus Secrets Officer:** the admin writes and reads the value (200). **The reader:** reading the value is 403; after Secrets User at that Secret it's 200; deleting the Secret is still **403**. |
| 4 Inheritance | **Observed:** Reader at the Application covers its Component; Owner at `/` covers everything. **API tests:** `TestAuthz_InheritanceAndUnion` (Contributor at a Project plus Reader lower down) and `TestInheritanceIsAdditive`. |
| 5 Groups | **Token:** `t017-member`'s access token carries `groups: ["t017-readers"]`, and the registry records it. **Through the group:** with Reader assigned to the group at `t017-b`, the member reads `t017-b` (200), and `t017-a` is 403. **Group removed:** after deleting the group in ThunderID (204) and signing in again, the member gets **403** on `t017-b`. |
| 6 Delegation | **API and policy tests:** `TestAuthzAPI_ConstrainedDelegation` and `TestConstrainedDelegation` (Reader and Contributor for people only; refused for Owner, groups, self and outside the scope), and `TestDelegateCantMintAWiderAdministrator`. Not repeated on the cluster. |
| 7 Custom roles | **API test:** `TestAuthzAPI_CustomRoles` covers creating, assigning and enforcing a custom role, refusing deletion while it's assigned, and refusing `/` as an assignable scope. Not repeated on the cluster. |
| 8 Bootstrap and lockout | **First start:** the log shows the bootstrap administrator (`01900000-…0030`) made Owner and Secrets Officer at `/`, and the API key `ops` migrated to Owner at `/`, with the stated description. **Second start:** nothing logged and nothing changed. The admin's `/v1/session` subject is the fixed ID, with 2 permission blocks at `/`. **Last Owner:** `TestAuthzAPI_LastOwner`, plus the console test for the 409 message. **Break-glass, on the live database while `serendipd` ran:** the reader was refused creating an application (403), then `serendipd break-glass grant-owner --subject …` exited 0 with its warning. The running server applied it **after 12 s** (within the 30 s reload), and the audit row has interface `break-glass` and actor `break-glass:<the Windows account>`. The admin then removed that assignment through the API (204). |
| 9 Fail closed | `TestEveryRouteHasARule` enumerates every registered route (all have rules; the table has no stale entries), and `TestUnmappedRouteFailsAtStartup`. |
| 10 Audit | **From the live database** (read-only): refused mutations are rows with no before or after, the person's subject, and `deny: missing <action> at <scope>`. Examples: a Component create, a Component update, a role assignment, and a Secret delete by `t017-reader`. **Allowed mutations** carry `allow: Owner (<assignment>) at /`. 7 role-assignment changes are audited; interfaces `bootstrap`, `http` and `reconcile`. Refused reads aren't audited, by design. |
| 11 Console | **Captures:** 36, at 1440 and 390 px, light and dark, for the reader and the admin. **The reader:** Applications lists only `t017-a`, with no "Create application"; on the Component, "Edit component" is disabled with "Your roles here don't allow changing this component."; on its Access control page, "Your roles here don't include assigning roles." and no Remove. **The admin:** Access control (IAM) for `t017-a` shows the inherited Owner, Secrets Officer and API-key assignments ("Inherited from the whole platform"), the reader's own assignment ("This scope") with the only Remove button, the Add form with its API request, and the Roles list. **Adding a Reader assignment** was done with the same request the console sends (the console's request equality is a UI test). The reader's access changed accordingly. **Not done by a person:** clicking through the Add form in a browser. |
| 12 Footprint | **Every test passes:** `go test -race ./...` and 69 UI tests. **T009, dev machine:** +74 µs p99 per request and +0.72 ms per 100-item list (Linux), +0.7 MiB idle. **Latency on the simulated T470:** +36 µs p99 per request and +0.24 ms p99 per 100-item list, measured in T011 with the same CPU limits (`profiles/edge-1/bench/results/2026-10-18-spec017-authz/summary.md`); both PASS. **Simulated T470 footprint** (`profiles/edge-1/bench/results/2026-10-18-spec017-simulated-t470/run1`): `serendipd` idle 36.3 MiB and 0.01% CPU, rule 4 **PASS** (spec 016: 34.5 MiB); 3 polling console tabs at 1.32 requests/s, 0 errors, 0.08% CPU, 38.2 MiB; fits 16 GB comfortably; cold start 257 s, warm restart 37 s. |

## Spec 016's checks, re-run

- **Headers on `/`:** `Content-Security-Policy` (`script-src 'self'`,
  `connect-src 'self'`, `frame-ancestors 'none'` …),
  `Cross-Origin-Opener-Policy: same-origin`, `Referrer-Policy: no-referrer`,
  `X-Content-Type-Options: nosniff` and `X-Frame-Options: DENY`.
- **In the browser:** zero CSP violations across all captures. On every
  page, JavaScript sees `document.cookie` = `""` and empty `localStorage`
  and `sessionStorage`.
  - The only console errors were on the reader's Access control page:
    the principals directory's **403**, by design (T006).
- **Logs:** none of the three `serendipd` logs contains a JWT, an
  `access_token` or `refresh_token`, `serendip_session=`, or a test
  user's password.

## After T011: upgrade notes

The `/security-review` fix changes where Secret values live (`plan.md`
Risks, "Secret values are stored per Secret"):

- **Moved values.** Values written before it, at the bare path in OpenBao,
  aren't read any more; write them again through
  `POST /v1/secrets/{id}/value`. There's deliberately no fallback to the
  old location, since that would reopen the alias.
- **IDs and paths.** Resource IDs are now plain tokens. A resource stored
  earlier with an unusual client-chosen ID (none exist in the repository's
  own flows) couldn't be updated. Paths that the new validator rejects
  can't be updated either.
- **Not re-verified on a cluster.** The real-cluster checks above were
  made before this fix. It's covered by provider, API and validation
  tests, including the aliasing attack end to end.
