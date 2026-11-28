# Serendip's CLI and MCP server

The `serendip` CLI and Serendip's MCP server are clients of Serendip's API
(spec 021). They hold no authority of their own. The API's roles,
approvals and audit decide everything, exactly as for the console
(constitution 5 and 7).

- **The commands and tools come from one catalogue,** `api/catalog`.
- **A test fails** when an API route has neither a command nor a tool, or
  a reason it's left off.
- **This file's tables** are generated from the catalogue, and a test
  checks every operation appears here.

Build the CLI from the repository: `go build ./cmd/serendip`.

## Signing in

```
serendip --server http://localhost:8080 login
```

- **Finding the identity provider:** the CLI reads it from the server's
  `/.well-known/oauth-protected-resource` (RFC 9728).
- **The browser:** it opens your browser and receives the answer on
  `http://127.0.0.1:8250/callback`.
- **No browser,** or that port is taken: use `serendip login
  --no-browser`. Open the printed link on any device, sign in, then paste
  back the address the browser lands on. It won't load; that's expected.
- **Where the sign-in is kept:** `credentials.json` in your configuration
  directory (`%AppData%\serendip`, `~/.config/serendip`, or
  `SERENDIP_CONFIG_DIR`). The folder is made private to you on every save:
  an owner-only access list on Windows, 0700 elsewhere.
- **Renewal** is automatic, and serialised between commands, because a
  refresh token can be used once.
- **`serendip logout`** revokes the session at the identity provider and
  deletes the file. That also stops your AI assistant within about 5
  minutes.

**Plain `http`** is accepted only to this machine (`localhost`,
`*.localhost`, loopback). For anything else use https, or pass
`--insecure` to accept sending credentials in clear text.

A sign-in is only ever sent to the server it was made for.

**The first sign-in to a server asks you to confirm** the identity
provider that server names, since your sign-in for that account is then
sent to the server. The answer is remembered per server
(`trusted-servers.json`), and asked again if the provider changes. In a
script, `serendip login --yes` confirms without asking.

## Scripts

**Set a credential in the environment,** never on the command line, where
the process list shows it:
- `SERENDIP_API_KEY`, an API key from `SERENDIP_API_KEYS`;
- or `SERENDIP_TOKEN`, a bearer token, such as an agent's from its agent
  issuer.

**Output and exit codes:**
- `--json` prints the API's JSON.
- `--dry-run` prints the request (method, URL and body) without sending
  it.
- **Exit codes:** 0 ok, 1 error, 2 usage, 3 denied, 4 needs approval,
  5 not found, 6 conflict, 7 server error, 8 not signed in.

## Deletes and approvals

- **Every delete is recorded as an Approval** (spec 019). When your roles
  allow it, policy approves it at once and the CLI prints its ID.
- **When it waits for a person** (an agent's or an AI's delete), the CLI
  exits 4 and says how to ask. `--reason "why"` files the request
  directly:

  ```
  serendip components delete <id> --reason "no longer used"
  serendip approvals list --phase Pending
  serendip approvals approve <id> --comment "agreed"
  ```

## Secret values

- **Value commands** need the secrets value data permission
  (`serendip/secrets/value/*`).
- **Values are hidden** unless you add `--show-value`.
- **A value is read only from a file or standard input,** never from the
  command line:

  ```
  serendip secrets value set <id> --data @value.json
  serendip secrets value set <id> --data - < value.json
  ```

## Secret variables (spec 026)

- **A secret variable** sets one environment variable of a running
  Component from one key of a Secret's value. The Component and its
  Deployments name the Secret; the value never passes through the CLI,
  the request or the answer.
- **Naming a Secret** needs reading its value
  (`serendip/secrets/value/read`), and the Secret must sit in the
  Component's Project or above it.
- **The flags** work on `components create|update` (the Component's
  `spec.secretEnv`) and `deployments create|update` (the Deployment's
  `spec.overrides.secretEnv`, for that Environment only). Both repeat:

  ```
  serendip components update <id> --secret-env DB_PASSWORD=<secret-id>:password
  serendip components update <id> --secret-env API_TOKEN=<secret-id>:token --remove-secret-env OLD_TOKEN
  serendip deployments create --file dep.json --secret-env DB_PASSWORD=<secret-id>:password
  ```

  - `--secret-env NAME=SECRET_ID:KEY` adds NAME, or replaces it if it's
    set. The key is everything after the Secret ID's colon, so it may
    hold colons.
  - `--remove-secret-env NAME` removes NAME; a name that isn't set is a
    usage error (exit 2), so a typo doesn't pass silently.
  - **On update without `--file`,** the CLI reads the resource, edits the
    list and sends it back with the `resourceVersion` it read: a change
    made in between is refused as a conflict (exit 6), never
    overwritten. With `--file`, the flags edit the file's document.
    `--dry-run` still makes the read, then prints the write.
- **On MCP** there are no flags: `components_update` and
  `deployments_update` take the whole document, and the assistant writes
  the list in it. Each tool's description gives the list's shape.
- **Delivery status:** a Deployment's `status.secretEnvVersion` and its
  `SecretEnvDelivered` condition. `serendip secrets status` (MCP
  `secrets_status`) says whether the cluster encrypts Secrets at rest.

## Logs

- **Reading logs** needs the logs permission
  (`serendip/components/logs/read`), which Reader doesn't have; Log
  Reader does.
- **Recent lines:** `serendip deployments logs <id>`, with `--tailLines`,
  `--sinceSeconds` and `--previous`.
- **Following:** add `--follow` to keep printing new lines as
  `time instance line` (`--json` gives the raw NDJSON). Ctrl-C stops it.
  - **The server ends a stream** after 5 minutes without new lines, or
    10 minutes in all; run the command again to keep following.
  - **Limits:** at most 3 streams at once per person, and 20 per server.
- **Build logs** work the same way: `serendip builds logs <id>`, with
  `--tailLines`, `--sinceSeconds` and `--follow`, which ends when the build
  does. They share the same limits, and need the same permission. A
  build's logs and events are kept for an hour after it ends; after that
  they answer 410 ("logs expired"), and the build keeps its outcome.
- **MCP tools answer once,** so `deployments_logs` and `builds_logs` read
  recent lines and have no `follow`.

## Stored logs and metrics (the observability package)

- **Searching stored logs:** `serendip logs search <deployment-id>`, or
  across an Application's Components in one Environment,
  `serendip logs search-application <application-id> --environment <id>`.
  Both take `--q` (a plain phrase), `--from` and `--to` (RFC 3339; the
  last hour by default) and `--limit` (up to 500 a page). Lines come
  newest first, and include lines from instances that are gone.
  - **Paging:** pass a page's `next` as `--before` for the next, older
    page. A page can be short, even empty, with `next` set; only an empty
    `next` ends a search.
  - **An Application's search** reads only the Deployments whose logs
    you may read; `omitted` counts the rest, without naming them. With
    none readable it's 403.
  - Stored logs need the same permission as recent ones.
- **Metrics:** `serendip metrics <deployment-id> [--from] [--to]`: CPU in
  millicores and memory in MiB, at least 15 minutes and at most the
  retention period, with requests, limits, and min, average, max and
  latest per series. Metrics need `serendip/observability/read`, which
  Reader has. `--summary` keeps at most 60 points a series; the
  `deployments_metrics` tool always does.
- **Request metrics** (spec 025): `serendip metrics` also shows a
  Component's HTTP requests where the cluster's network layer records
  them; `unavailable` says why not otherwise.
- **The network layer:** `serendip networking show` (needs
  `serendip/platform/read`).
- **Connection records:** `serendip flows <deployment-id>
  [--verdict dropped]`, paged like stored logs. The other end is named
  only if it's in the same Environment and you may read it (up to 20
  Deployments), else `outside`; a refused record's count is the network
  layer's refusals, where retries can count more than once, not attempts. **Traffic:** `serendip traffic
  <application-id> --environment <id>`, per edge between Components you
  may read both ends of. Both need `serendip/observability/read`; on a
  network layer that doesn't provide them they answer 409 with the
  reason, never an empty list.
- **Traces** (the tracing package): `serendip traces list
  <deployment-id> [--errorsOnly] [--minDuration 250ms]` and `serendip
  traces show <deployment-id> <trace-id>`. They need
  `serendip/components/traces/read`, which Reader doesn't have (Log
  Reader does). A trace shows the spans of every Deployment in its
  Environment you may read traces of; `partial: true` says it has
  others. A trace with no span from any of those Deployments is 404,
  whoever else sent spans. On MCP they're tools like stored logs: an
  assistant reads traces only where the person it acts for may. A
  trace's answer can pass the MCP result cap (1,000 spans with their
  attributes); the tool says so when it's cut.
- **A finished build's logs** stay readable after its run expires, from
  the store (`"stored": true` in the answer).
- **Without the package ready,** these answer 503, saying whether it's
  not installed, installing or not ready. `serendip packages list` shows
  an installed package's current disk use (`usage`).
- **Limits:** at most 3 queries at once per person and 20 per server,
  each stopped after 20 seconds.

## Alerts and notifications

- **Alerts:** `serendip alerts list [--state firing|resolved]
  [--severity] [--componentId|--applicationId|--environmentId]
  [--limit]` (newest first, 100 by default, at most 500), `alerts get`,
  `alerts ack <id>` and `alerts summary`. **The `alerts_list` tool
  answers at most 50**: 50 by default, and a larger `limit` is refused
  (`limit must be from 1 to 50`). An agent narrows with the filters, or
  reads `alerts_summary`.
- **Rules:** `serendip alertrules create|list|get|update|delete`, and
  `silence <id> --until <RFC 3339> --reason <why>` / `unsilence <id>`.
  These are MCP tools too, under the agent's roles.
- **Channels, SMTP servers and the heartbeat:**
  `serendip channels create|list|get|update|delete|test`,
  `serendip smtp create|list|get|update|delete`,
  `serendip heartbeat set|get|test|delete` (`create`, `update` and `set`
  take `--file`). **CLI only:** people set these up, since they name
  Secrets and where alert content leaves the laptop (RP-070). An agent
  reads channels through `resources_list` and `resources_get`.
- **Tokens and passwords stay in their Secrets.** A channel, SMTP server
  or heartbeat shows only which of its Secrets are set (`secretSet`);
  a failed test send names the host, never the token or the URL's path.

## Packages and builds

- **`serendip packages list`** shows each optional package's measured
  cost, idle and in use (for builds, while a build runs), with where it
  was measured. A package without a measurement reads "not measured",
  never a number, and isn't installable yet. It also says what removing
  each package loses. `--json`, and the `packages_list` tool, give the
  raw record: `requirements` is `null` when not measured.
- **Builds** belong to a source Component:
  `serendip builds list <component-id>` and
  `serendip builds trigger <component-id> [--commit <sha>]`. The rest take
  the build's ID: `get`, `cancel`, `events` and `logs [--follow]`.
- **Builds need the builds package.** Without it ready, triggering a
  build or naming a `source` on a Component answers 422, naming the
  package; a build's logs and events answer 503. Listing and reading
  builds work either way.

## AI assistants: `serendip mcp` (local)

`serendip mcp` serves Serendip's MCP tools on standard input and output
for an AI assistant on your machine. **It acts for you, signed in through
the CLI, with an agent's guardrails:**
- **Your roles** decide what it may do, never more.
- **It can never manage access, or approve anything.**
- **Every delete it asks for waits for a person:** you approve it in the
  console or the CLI.
- **Audit records it** as `agent:serendip-mcp/<your id>`, with you as
  sponsor.

**How it works:** it exchanges your sign-in for a 5-minute token issued to
the `serendip-mcp` client (RFC 8693), and exchanges again as needed. It
refuses `SERENDIP_API_KEY` and `SERENDIP_TOKEN`, which would give the AI a
full credential.

`--no-secret-values` leaves the Secret value tools out entirely.

**Claude Code** (no credentials in the configuration; it uses your CLI
sign-in):

```
serendip login
claude mcp add --transport stdio serendip -- serendip --server http://localhost:8080 mcp
```

Any client that starts a local stdio server works the same way. Claude
Desktop, for example, takes `{"command": "serendip", "args": ["--server",
"http://localhost:8080", "mcp"]}`.

**Known limit (RP-019):** an AI assistant that can run commands as you can
also read your CLI sign-in. The guardrails protect against its mistakes
with Serendip's tools, not against hostile code running as you.

## Hosted agents: `/mcp` (remote)

`serendipd` serves MCP over streamable HTTP at `/mcp`, protocol versions
2026-07-28 and older.

- **Who may use it:** only an agent's token (from an agent issuer, spec
  018) or a delegated assistant token.
- **Refused:** API keys and a person's own token get 403. No token, or an
  invalid one, gets 401 with
  `WWW-Authenticate: Bearer resource_metadata="…/.well-known/oauth-protected-resource"`.
- **Each tool call** is the same API request any client makes, audited
  with the interface `mcp`.

Point the client at `https://<serendip>/mcp` with the agent's bearer token
in its `Authorization` header, from its own secret store. For Claude Code,
use `--header` with a `headersHelper` script that prints it. Never paste a
token into the configuration.

## Interfaces in audit

| Interface | Meaning |
|---|---|
| `mcp` | Through `/mcp`, set by serendipd. |
| `cli (declared)`, `mcp (declared)` | The client said so (`X-Serendip-Client`). Identity, not this label, is the boundary. |
| `http` | Any other API client, including the console. |

## Commands and tools

Generated from `api/catalog` (`TestReferenceListsEveryOperation`).
`resources_list` and `resources_get` read the kinds without their own
tools.

| CLI command | MCP tool | API | What it does |
|---|---|---|---|
| `serendip access assignments create` | — | `POST /v1/authorization/roleAssignments` | Assign a role to a principal at a scope |
| `serendip access assignments delete <id>` | — | `DELETE /v1/authorization/roleAssignments/{id}` | Remove a role assignment |
| `serendip access assignments get <id>` | `access_assignments_get` | `GET /v1/authorization/roleAssignments/{id}` | One role assignment |
| `serendip access assignments list` | `access_assignments_list` | `GET /v1/authorization/roleAssignments` | Role assignments the caller may read, optionally those that apply at a scope |
| `serendip access directory` | — | `GET /v1/authorization/directory` | Search the identity provider's directory |
| `serendip access permissions` | `access_permissions` | `GET /v1/authorization/permissions` | What the caller may do at a scope (default "/") |
| `serendip access principals groups <id>` | — | `GET /v1/authorization/principals/person/{id}/groups` | The groups a person belongs to |
| `serendip access principals list` | — | `GET /v1/authorization/principals` | Principals known to Serendip |
| `serendip access roles create` | — | `POST /v1/authorization/roleDefinitions` | Create a custom role definition |
| `serendip access roles delete <id>` | — | `DELETE /v1/authorization/roleDefinitions/{id}` | Delete a custom role definition |
| `serendip access roles get <id>` | `access_roles_get` | `GET /v1/authorization/roleDefinitions/{id}` | One role definition |
| `serendip access roles list` | `access_roles_list` | `GET /v1/authorization/roleDefinitions` | Role definitions, built-in and custom |
| `serendip alertrules create` | `alertrules_create` | `POST /v1/alertrules` | Create a AlertRule |
| `serendip alertrules delete <id>` | `alertrules_delete` | `DELETE /v1/alertrules/{id}` | Delete a AlertRule. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip alertrules get <id>` | `alertrules_get` | `GET /v1/alertrules/{id}` | Get one AlertRule |
| `serendip alertrules list` | `alertrules_list` | `GET /v1/alertrules` | List alertrules the caller may read |
| `serendip alertrules silence <id>` | `alertrules_silence` | `POST /v1/alertrules/{id}/silence` | Silence a rule's notifications until a time (at most 30 days away). Its Alerts still open and resolve; only sending stops |
| `serendip alertrules unsilence <id>` | `alertrules_unsilence` | `POST /v1/alertrules/{id}/unsilence` | End a rule's silence now |
| `serendip alertrules update <id>` | `alertrules_update` | `PUT /v1/alertrules/{id}` | Replace a AlertRule's spec (send the version read, for a conflict check) |
| `serendip alerts ack <id>` | `alerts_acknowledge` | `POST /v1/alerts/{id}/acknowledge` | Acknowledge an Alert: records who is looking at it. Notifications still go out |
| `serendip alerts get <id>` | `alerts_get` | `GET /v1/alerts/{id}` | One Alert |
| `serendip alerts list` | `alerts_list` | `GET /v1/alerts` | Alerts the caller may read, newest first: firing and resolved, with their value against the threshold, who acknowledged them and how each notification went |
| `serendip alerts summary` | `alerts_summary` | `GET /v1/alerts/summary` | How many of the caller's Alerts are firing, by severity, and how many of those nobody has acknowledged |
| `serendip applications create` | `applications_create` | `POST /v1/applications` | Create a Application |
| `serendip applications delete <id>` | `applications_delete` | `DELETE /v1/applications/{id}` | Delete a Application. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip applications get <id>` | `applications_get` | `GET /v1/applications/{id}` | Get one Application |
| `serendip applications list` | `applications_list` | `GET /v1/applications` | List applications the caller may read |
| `serendip applications topology <id>` | `applications_topology` | `GET /v1/applications/{id}/topology` | An Application's declared topology: its Components and the calls between them (not live traffic) |
| `serendip applications update <id>` | `applications_update` | `PUT /v1/applications/{id}` | Replace a Application's spec (send the version read, for a conflict check) |
| `serendip approvals approve <id>` | — | `POST /v1/approvals/{id}/approve` | Approve a request; the approval reconciler then carries it out |
| `serendip approvals get <id>` | `approvals_get` | `GET /v1/approvals/{id}` | One approval, with its decision and what it removed |
| `serendip approvals list` | `approvals_list` | `GET /v1/approvals` | Approvals the caller may read, newest first, a page at a time |
| `serendip approvals reject <id>` | — | `POST /v1/approvals/{id}/reject` | Reject a request |
| `serendip approvals request` | `approvals_request` | `POST /v1/approvals` | Ask for a delete that needs a person's approval. A person decides it; the requester never can |
| `serendip builds cancel <id>` | `builds_cancel` | `POST /v1/builds/{id}/cancel` | Stop a build that hasn't finished. What's running is unchanged |
| `serendip builds events <id>` | `builds_events` | `GET /v1/builds/{id}/events` | Platform events about a build's run, newest first. Kept for an hour after the build ends |
| `serendip builds get <id>` | `builds_get` | `GET /v1/builds/{id}` | One build: its phase, steps, commit, image and times |
| `serendip builds list <id>` | `builds_list` | `GET /v1/components/{id}/builds` | A source Component's builds, newest first, each with its phase, the commit built, its image and its times |
| `serendip builds logs <id>` | `builds_logs` | `GET /v1/builds/{id}/logs` | A build's log lines, oldest first, kept for an hour after it ends. Build output can hold secrets, so logs need their own permission |
| `serendip builds trigger <id>` | `builds_trigger` | `POST /v1/components/{id}/builds` | Build a source Component's image. It waits its turn behind a running build, and a successful build becomes the Component's new release. Needs the builds package |
| `serendip capabilities create` | — | `POST /v1/capabilities` | Create a Capability |
| `serendip capabilities delete <id>` | — | `DELETE /v1/capabilities/{id}` | Delete a Capability. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip capabilities get <id>` | — | `GET /v1/capabilities/{id}` | Get one Capability |
| `serendip capabilities list` | — | `GET /v1/capabilities` | List capabilities the caller may read |
| `serendip capabilities update <id>` | — | `PUT /v1/capabilities/{id}` | Replace a Capability's spec (send the version read, for a conflict check) |
| `serendip capabilities default get <name>` | `capability_default_get` | `GET /v1/capabilities/{name}/default` | A capability's default provider |
| `serendip capabilities default set <name>` | — | `PUT /v1/capabilities/{name}/default` | Set a capability's default provider |
| `serendip channels create` | — | `POST /v1/notificationchannels` | Create a NotificationChannel |
| `serendip channels delete <id>` | — | `DELETE /v1/notificationchannels/{id}` | Delete a NotificationChannel. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip channels get <id>` | — | `GET /v1/notificationchannels/{id}` | Get one NotificationChannel |
| `serendip channels list` | — | `GET /v1/notificationchannels` | List notificationchannels the caller may read |
| `serendip channels test <id>` | — | `POST /v1/notificationchannels/{id}/test` | Send a test notification to a channel now, whatever its minimum severity, and record the result on it |
| `serendip channels find-chat <id>` | — | `POST /v1/secrets/{id}/telegramchats` | List the Telegram chats a bot (its token in Secret `<id>`) has seen a message from, to find a channel's `chatId`. Send the bot a message first. Needs the Secret's value read |
| `serendip channels update <id>` | — | `PUT /v1/notificationchannels/{id}` | Replace a NotificationChannel's spec (send the version read, for a conflict check) |
| `serendip clusters create` | — | `POST /v1/clusters` | Create a Cluster |
| `serendip clusters delete <id>` | — | `DELETE /v1/clusters/{id}` | Delete a Cluster. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip clusters get <id>` | — | `GET /v1/clusters/{id}` | Get one Cluster |
| `serendip clusters list` | — | `GET /v1/clusters` | List clusters the caller may read |
| `serendip clusters update <id>` | — | `PUT /v1/clusters/{id}` | Replace a Cluster's spec (send the version read, for a conflict check) |
| `serendip components create` | `components_create` | `POST /v1/components` | Create a Component. Secret variables are spec.secretEnv: [{"env": NAME, "secret": a Secret ID, "key": a key of its value}]; naming a Secret needs reading its value |
| `serendip components delete <id>` | `components_delete` | `DELETE /v1/components/{id}` | Delete a Component. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip components dependencies <id>` | `components_dependencies` | `GET /v1/components/{id}/dependencies` | A Component's declared dependencies: the Components it calls, and those of its Application that call it |
| `serendip components get <id>` | `components_get` | `GET /v1/components/{id}` | Get one Component |
| `serendip components list` | `components_list` | `GET /v1/components` | List components the caller may read |
| `serendip components schemas <id>` | `components_schemas` | `GET /v1/components/{id}/schemas` | What can be configured on a Component, as JSON Schemas: its per-environment configuration (a Deployment's overrides.config) and each trait's |
| `serendip components update <id>` | `components_update` | `PUT /v1/components/{id}` | Replace a Component's spec (send the version read, for a conflict check). Secret variables are spec.secretEnv: [{"env": NAME, "secret": a Secret ID, "key": a key of its value}]; naming a Secret needs reading its value |
| `serendip deployments create` | `deployments_create` | `POST /v1/deployments` | Create a Deployment. Secret variables are spec.overrides.secretEnv: [{"env": NAME, "secret": a Secret ID, "key": a key of its value}]; naming a Secret needs reading its value |
| `serendip deployments delete <id>` | `deployments_delete` | `DELETE /v1/deployments/{id}` | Delete a Deployment. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip deployments events <id>` | `deployments_events` | `GET /v1/deployments/{id}/events` | Recent platform events about what a Deployment runs as, newest first |
| `serendip deployments get <id>` | `deployments_get` | `GET /v1/deployments/{id}` | Get one Deployment |
| `serendip deployments list` | `deployments_list` | `GET /v1/deployments` | List deployments the caller may read |
| `serendip deployments logs <id>` | `deployments_logs` | `GET /v1/deployments/{id}/logs` | Recent log lines from a Deployment's running instances, oldest first. Logs can hold sensitive data, so they need their own permission |
| `serendip deployments promote <id>` | `deployments_promote` | `POST /v1/deployments/{id}/promote` | Run the release a Deployment runs now in the next Environment of the Project's pipeline. The target keeps its own overrides |
| `serendip deployments runtime <id>` | `deployments_runtime` | `GET /v1/deployments/{id}/runtime` | What a Deployment runs as on the platform: its objects with their health, then each running instance |
| `serendip deployments update <id>` | `deployments_update` | `PUT /v1/deployments/{id}` | Replace a Deployment's spec (send the version read, for a conflict check). Secret variables are spec.overrides.secretEnv: [{"env": NAME, "secret": a Secret ID, "key": a key of its value}]; naming a Secret needs reading its value |
| `serendip environments create` | — | `POST /v1/environments` | Create a Environment |
| `serendip environments delete <id>` | — | `DELETE /v1/environments/{id}` | Delete a Environment. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip environments get <id>` | — | `GET /v1/environments/{id}` | Get one Environment |
| `serendip environments list` | — | `GET /v1/environments` | List environments the caller may read |
| `serendip environments update <id>` | — | `PUT /v1/environments/{id}` | Replace a Environment's spec (send the version read, for a conflict check) |
| `serendip graph blast-radius <kind> <id>` | `graph_blast_radius` | `GET /v1/graph/{kind}/{id}/blast-radius` | Everything deleting a resource would affect |
| `serendip graph children <kind> <id>` | `graph_children` | `GET /v1/graph/{kind}/{id}/children` | What a resource directly owns |
| `serendip graph dependents <kind> <id>` | `graph_dependents` | `GET /v1/graph/{kind}/{id}/dependents` | What depends on a resource |
| `serendip heartbeat delete` | — | `DELETE /v1/heartbeat` | Stop the heartbeat. Recorded as an Approval. The outside monitor will then raise its alarm |
| `serendip heartbeat get` | — | `GET /v1/heartbeat` | The heartbeat to an outside monitor: where it goes (the URL is a Secret), how often, and its last result |
| `serendip heartbeat set` | — | `PUT /v1/heartbeat` | Set the heartbeat: a request to an outside monitor every interval, sent only while Serendip can alert |
| `serendip heartbeat test` | — | `POST /v1/heartbeat/test` | Send one heartbeat now, whatever Serendip's health, and record the result |
| `serendip incidents create` | — | `POST /v1/incidents` | Create a Incident |
| `serendip incidents delete <id>` | — | `DELETE /v1/incidents/{id}` | Delete a Incident. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip incidents get <id>` | — | `GET /v1/incidents/{id}` | Get one Incident |
| `serendip incidents list` | — | `GET /v1/incidents` | List incidents the caller may read |
| `serendip incidents update <id>` | — | `PUT /v1/incidents/{id}` | Replace a Incident's spec (send the version read, for a conflict check) |
| `serendip logs search <id>` | `deployments_logs_search` | `GET /v1/deployments/{id}/logs/search` | Search a Deployment's stored log lines, newest first, including lines from instances that are gone. One page per call: pass next as before for the next page; only an empty next ends a search (a page can be short). Logs can hold sensitive data, so they need their own permission. Needs the observability package |
| `serendip logs search-application <id>` | `applications_logs_search` | `GET /v1/applications/{id}/logs/search` | Search the stored log lines of an Application's Components in one Environment, newest first, paged like deployments_logs_search. Only the Deployments whose logs you may read are searched; omitted counts the others. Needs the observability package |
| `serendip metrics <id>` | `deployments_metrics` | `GET /v1/deployments/{id}/metrics` | A Deployment's CPU (millicores) and memory (MiB) over a time range (the last hour by default, at least 15 minutes), against its requests and limits, and its HTTP requests (rate, 5xx rate, p50/p95/p99 latency in ms) where the network layer records them, with min, average, max and latest per series. What can't be measured is listed as unavailable, with why. Needs the observability package |
| `serendip networking show` | `networking_get` | `GET /v1/networking` | The cluster's network layer: its name and version, whether network policies are enforced, whether connection records and request metrics can be read, its pod address ranges, what isn't available and why, and when this was read |
| `serendip flows <id>` | `deployments_flows` | `GET /v1/deployments/{id}/flows` | A Deployment's connection records, newest first, each counting a minute's connections to or from it (a refused one counts the network layer's refusals, so retries can count more than once); refused ones carry the reason. The other end is named only if it's in the same Environment and you may read it (up to 20 Deployments); otherwise it's marked outside. Paged like deployments_logs_search. Needs the observability package and a network layer that records connections |
| `serendip traffic <id>` | `applications_traffic` | `GET /v1/applications/{id}/traffic` | The HTTP traffic between an Application's Components in one Environment over a time range (the last hour by default, at least a minute): requests and failed (5xx) requests per second on each edge. Only edges whose two ends you may read are shown; omitted counts the Deployments left out. Needs the observability package and a network layer that records requests |
| `serendip traces list <id>` | `deployments_traces_list` | `GET /v1/deployments/{id}/traces` | A Deployment's stored traces, newest first: each with its start, duration, root span, span count and whether a span failed. Paged like deployments_logs_search. Span data can hold sensitive values, so traces need their own permission. Needs the tracing package |
| `serendip traces show <id> <traceId>` | `deployments_traces_get` | `GET /v1/deployments/{id}/traces/{traceId}` | One trace this Deployment is part of: its spans in start order, from the Deployments in its Environment whose traces you may read; partial says it has spans you can't see. Needs the tracing package |
| `serendip nodes create` | — | `POST /v1/nodes` | Create a Node |
| `serendip nodes delete <id>` | — | `DELETE /v1/nodes/{id}` | Delete a Node. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip nodes get <id>` | — | `GET /v1/nodes/{id}` | Get one Node |
| `serendip nodes list` | — | `GET /v1/nodes` | List nodes the caller may read |
| `serendip nodes update <id>` | — | `PUT /v1/nodes/{id}` | Replace a Node's spec (send the version read, for a conflict check) |
| `serendip organizations create` | — | `POST /v1/organizations` | Create a Organization |
| `serendip organizations delete <id>` | — | `DELETE /v1/organizations/{id}` | Delete a Organization. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip organizations get <id>` | — | `GET /v1/organizations/{id}` | Get one Organization |
| `serendip organizations list` | — | `GET /v1/organizations` | List organizations the caller may read |
| `serendip organizations update <id>` | — | `PUT /v1/organizations/{id}` | Replace a Organization's spec (send the version read, for a conflict check) |
| `serendip packages list` | `packages_list` | `GET /v1/packages` | The optional packages this server offers (builds, observability): whether each is available and installable, its measured cost idle and in use (requirements; null means not measured), and what removing it loses. Install one by creating a ProviderInstance naming it |
| `serendip platform` | `platform_get` | `GET /v1/platform` | The application platform, read-only: where workloads run, and the component types and traits installed with what each allows |
| `serendip projects create` | — | `POST /v1/projects` | Create a Project |
| `serendip projects delete <id>` | — | `DELETE /v1/projects/{id}` | Delete a Project. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip projects get <id>` | — | `GET /v1/projects/{id}` | Get one Project |
| `serendip projects list` | — | `GET /v1/projects` | List projects the caller may read |
| `serendip projects update <id>` | — | `PUT /v1/projects/{id}` | Replace a Project's spec (send the version read, for a conflict check) |
| `serendip provider-instances create` | — | `POST /v1/provider-instances` | Create a ProviderInstance |
| `serendip provider-instances delete <id>` | — | `DELETE /v1/provider-instances/{id}` | Delete a ProviderInstance. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip provider-instances get <id>` | — | `GET /v1/provider-instances/{id}` | Get one ProviderInstance |
| `serendip provider-instances list` | — | `GET /v1/provider-instances` | List provider-instances the caller may read |
| `serendip provider-instances update <id>` | — | `PUT /v1/provider-instances/{id}` | Replace a ProviderInstance's spec (send the version read, for a conflict check) |
| `serendip providers create` | — | `POST /v1/providers` | Create a Provider |
| `serendip providers delete <id>` | — | `DELETE /v1/providers/{id}` | Delete a Provider. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip providers get <id>` | — | `GET /v1/providers/{id}` | Get one Provider |
| `serendip providers list` | — | `GET /v1/providers` | List providers the caller may read |
| `serendip providers update <id>` | — | `PUT /v1/providers/{id}` | Replace a Provider's spec (send the version read, for a conflict check) |
| `serendip readiness` | `readiness_get` | `GET /v1/readiness` | Readiness of each capability |
| `serendip smtp create` | — | `POST /v1/smtpservers` | Create a SMTPServer |
| `serendip smtp delete <id>` | — | `DELETE /v1/smtpservers/{id}` | Delete a SMTPServer. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip smtp get <id>` | — | `GET /v1/smtpservers/{id}` | Get one SMTPServer |
| `serendip smtp list` | — | `GET /v1/smtpservers` | List smtpservers the caller may read |
| `serendip smtp update <id>` | — | `PUT /v1/smtpservers/{id}` | Replace a SMTPServer's spec (send the version read, for a conflict check) |
| — | `resources_get` | `GET /v1/{kind}/{id}` | Get one resource of one kind |
| — | `resources_list` | `GET /v1/{kind}` | List resources of one kind (infrastructure, organisation, operations) |
| `serendip secrets create` | `secrets_create` | `POST /v1/secrets` | Create a Secret |
| `serendip secrets delete <id>` | `secrets_delete` | `DELETE /v1/secrets/{id}` | Delete a Secret. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip secrets get <id>` | `secrets_get` | `GET /v1/secrets/{id}` | Get one Secret |
| `serendip secrets list` | `secrets_list` | `GET /v1/secrets` | List secrets the caller may read |
| `serendip secrets status` | `secrets_status` | `GET /v1/secrets/status` | Whether the Secrets capability is ready, and whether the cluster encrypts Secrets at rest (on, off or unknown), with the reason. Names no Secret or value |
| `serendip secrets value get <id>` | `secrets_read_value` (value) | `GET /v1/secrets/{id}/value` | Read a Secret's value. Needs the secrets value data permission |
| `serendip secrets update <id>` | `secrets_update` | `PUT /v1/secrets/{id}` | Replace a Secret's spec (send the version read, for a conflict check) |
| `serendip secrets value set <id>` | `secrets_write_value` (value) | `POST /v1/secrets/{id}/value` | Write a Secret's value, replacing the old one. Needs the secrets value data permission |
| `serendip whoami` | `session_get` | `GET /v1/session` | Who the caller is, and how they signed in |
| `serendip sites create` | — | `POST /v1/sites` | Create a Site |
| `serendip sites delete <id>` | — | `DELETE /v1/sites/{id}` | Delete a Site. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip sites get <id>` | — | `GET /v1/sites/{id}` | Get one Site |
| `serendip sites list` | — | `GET /v1/sites` | List sites the caller may read |
| `serendip sites update <id>` | — | `PUT /v1/sites/{id}` | Replace a Site's spec (send the version read, for a conflict check) |
| `serendip teams create` | — | `POST /v1/teams` | Create a Team |
| `serendip teams delete <id>` | — | `DELETE /v1/teams/{id}` | Delete a Team. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip teams get <id>` | — | `GET /v1/teams/{id}` | Get one Team |
| `serendip teams list` | — | `GET /v1/teams` | List teams the caller may read |
| `serendip teams update <id>` | — | `PUT /v1/teams/{id}` | Replace a Team's spec (send the version read, for a conflict check) |
| `serendip template-versions create <id>` | `template_versions_create` | `POST /v1/templates/{id}/versions` | Publish a new version of a template. metadata.name is its label (such as 1.0); spec has parameters and body. It's checked by rendering it, and can't be changed afterwards |
| `serendip template-versions delete <id> <version>` | `template_versions_delete` | `DELETE /v1/templates/{id}/versions/{version}` | Delete one version of a template. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip template-versions get <id> <version>` | `template_versions_get` | `GET /v1/templates/{id}/versions/{version}` | One version of a template, by its label: its parameters (a JSON Schema object) and its body |
| `serendip template-versions list <id>` | `template_versions_list` | `GET /v1/templates/{id}/versions` | A template's versions, oldest first; the one published most recently is marked latest |
| `serendip templates create` | `templates_create` | `POST /v1/templates` | Create a Template |
| `serendip templates delete <id>` | `templates_delete` | `DELETE /v1/templates/{id}` | Delete a Template. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip templates get <id>` | `templates_get` | `GET /v1/templates/{id}` | Get one Template |
| `serendip templates list` | `templates_list` | `GET /v1/templates` | List templates the caller may read |
| `serendip templates update <id>` | `templates_update` | `PUT /v1/templates/{id}` | Replace a Template's spec (send the version read, for a conflict check) |
| `serendip volumes create` | — | `POST /v1/volumes` | Create a Volume |
| `serendip volumes delete <id>` | — | `DELETE /v1/volumes/{id}` | Delete a Volume. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip volumes get <id>` | — | `GET /v1/volumes/{id}` | Get one Volume |
| `serendip volumes list` | — | `GET /v1/volumes` | List volumes the caller may read |
| `serendip volumes update <id>` | — | `PUT /v1/volumes/{id}` | Replace a Volume's spec (send the version read, for a conflict check) |

| Route | Not on | Why |
|---|---|---|
| `/` | CLI | the catch-all route only ever answers 404 |
| `/` | MCP | the catch-all route only ever answers 404 |
| `DELETE /v1/authorization/roleAssignments/{id}` | MCP | agents never manage access (spec 018 guardrail); the API would refuse every call |
| `DELETE /v1/authorization/roleDefinitions/{id}` | MCP | agents never manage access (spec 018 guardrail); the API would refuse every call |
| `DELETE /v1/capabilities/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `DELETE /v1/clusters/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `DELETE /v1/environments/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `DELETE /v1/heartbeat` | MCP | a platform-wide setting, changed by people |
| `DELETE /v1/incidents/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `DELETE /v1/nodes/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `DELETE /v1/notificationchannels/{id}` | MCP | notification channels and SMTP relays are set up by people: they name Secrets and where alert content leaves the laptop (RP-070); read through resources_list and resources_get |
| `DELETE /v1/organizations/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `DELETE /v1/projects/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `DELETE /v1/provider-instances/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `DELETE /v1/providers/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `DELETE /v1/sites/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `DELETE /v1/smtpservers/{id}` | MCP | notification channels and SMTP relays are set up by people: they name Secrets and where alert content leaves the laptop (RP-070); read through resources_list and resources_get |
| `DELETE /v1/teams/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `DELETE /v1/volumes/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `GET /v1/authorization/directory` | MCP | directory data, readable only by those who may assign roles, which agents never may |
| `GET /v1/authorization/principals/person/{id}/groups` | MCP | directory data, readable only by those who may assign roles, which agents never may |
| `GET /v1/authorization/principals` | MCP | directory data, readable only by those who may assign roles, which agents never may |
| `GET /v1/heartbeat` | MCP | a platform-wide setting, changed by people |
| `POST /v1/approvals/{id}/approve` | MCP | only a person or an API key decides an approval (spec 019); an AI never does |
| `POST /v1/approvals/{id}/reject` | MCP | only a person or an API key decides an approval (spec 019); an AI never does |
| `POST /v1/authorization/roleAssignments` | MCP | agents never manage access (spec 018 guardrail); the API would refuse every call |
| `POST /v1/authorization/roleDefinitions` | MCP | agents never manage access (spec 018 guardrail); the API would refuse every call |
| `POST /v1/capabilities` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `POST /v1/clusters` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `POST /v1/environments` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `POST /v1/heartbeat/test` | MCP | a platform-wide setting, changed by people |
| `POST /v1/incidents` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `POST /v1/nodes` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `POST /v1/notificationchannels/{id}/test` | MCP | notification channels and SMTP relays are set up by people: they name Secrets and where alert content leaves the laptop (RP-070); read through resources_list and resources_get |
| `POST /v1/notificationchannels` | MCP | notification channels and SMTP relays are set up by people: they name Secrets and where alert content leaves the laptop (RP-070); read through resources_list and resources_get |
| `POST /v1/organizations` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `POST /v1/projects` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `POST /v1/provider-instances` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `POST /v1/providers` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `POST /v1/sites` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `POST /v1/smtpservers` | MCP | notification channels and SMTP relays are set up by people: they name Secrets and where alert content leaves the laptop (RP-070); read through resources_list and resources_get |
| `POST /v1/teams` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `POST /v1/volumes` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `PUT /v1/capabilities/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `PUT /v1/capabilities/{name}/default` | MCP | a platform-wide setting, changed by people |
| `PUT /v1/clusters/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `PUT /v1/environments/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `PUT /v1/heartbeat` | MCP | a platform-wide setting, changed by people |
| `PUT /v1/incidents/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `PUT /v1/nodes/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `PUT /v1/notificationchannels/{id}` | MCP | notification channels and SMTP relays are set up by people: they name Secrets and where alert content leaves the laptop (RP-070); read through resources_list and resources_get |
| `PUT /v1/organizations/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `PUT /v1/projects/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `PUT /v1/provider-instances/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `PUT /v1/providers/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `PUT /v1/sites/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `PUT /v1/smtpservers/{id}` | MCP | notification channels and SMTP relays are set up by people: they name Secrets and where alert content leaves the laptop (RP-070); read through resources_list and resources_get |
| `PUT /v1/teams/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `PUT /v1/volumes/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
