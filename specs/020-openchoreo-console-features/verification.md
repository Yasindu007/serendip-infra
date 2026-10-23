# Spec 020 verification (T015, real cluster)

Run 2026-10-23 on `serendip-t018`: kind, `kindest/node:v1.34.0`, with
the spec 017/018 stack (OpenBao, kgateway, OpenChoreo 1.3.1, ThunderID
1.0.1). Tokens, codes, passwords and keys were never printed.

## Setup

- **`serendipd`:** built from spec 020 at `b126882` (through T014), on
  `:8088`, with a **copy** of spec 021 T011's store (itself spec 019
  T011's). API keys: `verifier` (Owner at `/`) and, from the second
  restart, `t015-reader`.
- **OpenBao** unsealed with the spec 018 credentials file, as in spec 019
  and 021.
- **The person:** `admin` in ThunderID.
  - **The CLI** signed in through `serendip login --no-browser`, driven
    by spec 021's stand-in for the browser. It now needs `--yes`, since
    the CLI asks before the first sign-in to a server.
  - **The console** was signed in through ThunderID's own login page, in
    headless Chrome driven over the DevTools protocol (spec 019's scratch
    driver). Screenshots were taken and looked at; they aren't committed.
- **The AI client:** Claude Code 2.1.309, `claude -p` with
  `--strict-mcp-config`, only `mcp__serendip__*` tools allowed, running
  `serendip.exe --server http://localhost:8088 mcp` as the signed-in
  person.

## The migration (T003) on a real store

- **Before:** schema 7. Applications `t019-app` and `t019-stale` were
  owned by Environment `t019-dev`, in Project `t019-agents`.
- **After the first start:** schema 8, with no migration notes.
  - **Both Applications** are owned by `t019-agents`.
  - **Each of their 3 Components** has a Deployment in `t019-dev`.
  - **Each Deployment** is Ready (`Deployed`) on a new release.
  - **The pre-020 bindings** (`cmp-…-development`) are gone. Each
    Deployment has its own `cmp-…-env-…` ReleaseBinding.
- **Readiness:** `application-platform`, `gateway`, `identity`,
  `secrets` and `storage` are Ready. `build` and `observability` are
  `Unavailable`, "not available in this build yet".

## Evidence, by acceptance criterion

1. **Runtime view, in the console** (Component `t020-web`, Runtime tab,
   Environment `t020-staging`):
   - **The tree:** ConfigMap, Deployment, HTTPRoute, Service,
     NetworkPolicy and three Pods (after criterion 4's change), each
     Healthy.
   - **Events:** 31 recent ones, such as `ScalingReplicaSet` "Scaled up
     replica set … from 1 to 3".
   - **Live log following:** with Follow on, a request to staging's
     endpoint for `/t015-live-…` appeared in the log view at once. It
     was nginx's line for the missing file. The status read "Following:
     new lines appear as they're written." The panel showed `GET
     /v1/deployments/<id>/logs?follow=true&tailLines=100`. Stopping read
     "You stopped following."
2. **Promotion,** on a pipeline `t019-dev → t020-staging →
   t020-production` (the migrated Environment plus two new ones; the
   Project's pipeline reconciled to `pl-18f18a71b6`, Ready):
   - **Components:** `t020-api` (port http) and `t020-web` (exposed,
     `GREETING=hello`, calling `t020-api` as `API_URL`).
   - **Deploy and promote:** both were deployed to `t019-dev`, then
     promoted to staging (Ready with an endpoint in 18 s) and to
     production (24 s). Development's endpoint answered 200.
   - **A production-only override:** `MODE=production` and 2 replicas.
     Inside the containers:
     - development and staging: `GREETING=hello MODE=unset`, 1 replica;
     - production: `GREETING=hello MODE=production`, 2 replicas (2/2
       ready).
   - **A new release** (`GREETING=hi`) reached development about 30 s
     after the Component changed. Promoted on, production ran it with
     `GREETING=hi MODE=production`, still 2 replicas. Its history held
     the earlier release.
   - **Rollback:** a `PUT` naming the earlier release brought production
     back (`GREETING=hello MODE=production`, 2 replicas) in 3 s.
   - **Each Environment shows its release and state** on the Deploy tab:
     release, overrides ("1 variable, 1 setting" in production), the
     endpoint, Ready, and where it promotes to.
   - **Audited:** every Deployment change by a person or the assistant
     has a row with its decision (none without one):
     - `verifier` over `http`: 6 creates and 4 updates;
     - `person:admin` over `http`: the console's change;
     - the assistant's 2 rows, below.

     The reconciler's 43 status writes are `system:reconciler`.
3. **Policy on promotion:**
   - **A Reader** (`t015-reader`, Reader at Project `t019-agents`) reads
     the staging Deployment (200). Promoting it to production is refused:
     403, "missing serendip/deployments/write at Environment/…".
   - **The Owner** promotes at once (the steps above).
   - **The assistant** follows the same path (criterion 11).
   - **But the Reader's refusal left no audit row:** finding F3.
4. **Configuration through a form from the schema:**
   - **Configure t020-staging** showed `imagePullPolicy` (a choice),
     `replicas`, and `resources` (as JSON: finding U2), all generated
     from `GET /v1/components/{id}/schemas`.
   - **Setting replicas to 3** showed the request it would send
     (`PUT /v1/deployments/<id>`, `overrides: {"config": {"replicas":
     3}}`), saved, and returned to the Deploy tab.
   - **The staging workload** reached 3/3 replicas, and the runtime tree
     showed three Pods. The audit row is `person:admin` over `http`.
   - **No traits are installed** on this platform (T001), so trait forms
     were shown by the tests (T012, T013) only.
5. **Topology** (`/applications/<t019-app>/topology`):
   - **The diagram** has 4 Components (`t019-web-reject`, `rp018-check`,
     `t020-api`, `t020-web`) and "1 declared calls".
   - **The table:** `t020-web → t020-api`, port `http`, address in
     `API_URL`.
   - **The workload** received the address as `API_URL` in each
     Environment.
   - **Live traffic** is greyed out, naming the observability package.
6. **Platform view** (`/platform`):
   - **Where workloads run:** `default` Ready.
   - **Component types:** `serendip-service`, with per-environment
     settings `imagePullPolicy`, `replicas` and `resources`.
   - **Traits:** "No traits are installed."
   - **Environments:** `t019-agents`'s, in pipeline order, with where
     each promotes to. Failure protection reads "Not reported", which is
     honest: nothing sets it yet.
7. **Unavailable, honestly:**
   - **Builds:** "Not available yet. This needs the
     openchoreo-workflow-plane package, which isn't available in this
     version of Serendip yet."
   - **Observability:** the same, naming openchoreo-observability-plane.
   - **Packages:** each package with its description, "Cost: Not
     measured yet: it can't be installed in this version", and Install
     disabled with the reason.
   - **No errors and no placeholder data.**
   - **"Installing"** can't be shown here, because no package has an
     installer yet. It's proven in process by T013 criterion 7.
10. **Authorization** (on the real server):
    - **The Reader:** promotion 403 (criterion 3), and logs 403 "missing
      serendip/components/logs/read". Runtime and events read 200.
11. **Interface parity, and the AI:**
    - **Claude Code**, asked to promote `t020-web` from `t019-dev` to
      `t020-staging` and then undeploy it from staging:
      - It used `deployments_list`, `resources_get` (to map Environment
        names), `deployments_promote` (accepted at once) and
        `deployments_get`. Staging then ran the third release.
      - **`deployments_delete`** was refused: 403, an agent's delete
        needs a person's approval. Nothing changed.
      - **`approvals_request`** filed `01a16945-97ed-…`, Pending, with
        the reason given.
    - **The person rejected it** from the CLI (`serendip approvals
      reject … --comment`). Staging stayed deployed.
    - **The audit trail:**
      - the promotion as `agent:serendip-mcp/<admin>`, interface `mcp
        (declared)`, with the admin as sponsor and the decision naming
        `serendip/deployments/write` at the target;
      - the refused delete as `needsApproval`;
      - the request, by the agent;
      - the rejection as `person:admin`, `cli (declared)`.
    - **The console's requests** were each shown in their panel.

**Restarts:** `serendipd` restarted twice. Everything kept running, and
the store and the platform agreed afterwards (but see F2).

## Findings

- **F1 (to fix):** a Component deployed before a Component it calls is
  stuck in Progressing.
  - `t020-web` was deployed to `t019-dev` before `t020-api`. The pod
    ran, but the platform held the release back: "ConnectionsPending: 1
    connections pending".
  - For minutes Serendip showed only "Progressing, no Ready condition
    reported yet". Deploying `t020-api` made it Ready in 24 s.
  - The reason should be visible: the Component it calls isn't
    deployed in this Environment.
- **F2 (to look at):** right after a restart, production's `t020-web`
  briefly read Progressing, then WaitingForComponent. That lasted under
  a minute while its Component was re-checked, though nothing had
  changed.
- **F3 (to fix):** refusals made inside the Deployment and Component
  handlers aren't audited. Those are promote's write check at the target
  Environment, deploying without reading the Component, and depending
  on an unreadable Component. The authorizer's own refusals are
  audited, and constitution 8 and `api/CLAUDE.md` say a refused mutation
  gets a row.
- **UI, for T016's design review:**
  - **U1:** in an Environment card, the action buttons stretch to the
    height of the disabled Restart and its reason.
  - **U2:** `resources` is a JSON box, because the schema's `$ref` to
    `$defs` isn't resolved.
  - **U3:** the Configure page's breadcrumb reads only "Component".
  - **U4:** the events table lists every event, breaks object names
    mid-word, and shows raw JSON payloads. It pushes the logs far down.
  - **U5:** package descriptions shown to people say "(spec 022)" and
    "(spec 023)".
- **RP-035's question, answered in part:** first deploys into the new
  staging and production Environments served in 18 s and 24 s on this
  cluster. The 70 s in T014 was the simulated T470's.

**Left out:**
- **A rollout restart:** deferred in T006. Restart is shown disabled,
  "not available yet".
- **The "installing" state on a real cluster:** no package has an
  installer until spec 022.

## Leftovers

- **`serendipd`** (spec 020) is still running on `:8088` with the T015
  store copy.
- **Left on the cluster:** `t020-staging`, `t020-production`,
  `t020-web` and `t020-api`.
- **The scratch** (store, scripts, screenshots, CLI config) is under
  this session's scratchpad, `t015/`.
