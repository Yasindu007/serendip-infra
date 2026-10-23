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
- **MCP tools answer once,** so `deployments_logs` reads recent lines and
  has no `follow`.

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
| `serendip alerts create` | — | `POST /v1/alerts` | Create a Alert |
| `serendip alerts delete <id>` | — | `DELETE /v1/alerts/{id}` | Delete a Alert. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip alerts get <id>` | — | `GET /v1/alerts/{id}` | Get one Alert |
| `serendip alerts list` | — | `GET /v1/alerts` | List alerts the caller may read |
| `serendip alerts update <id>` | — | `PUT /v1/alerts/{id}` | Replace a Alert's spec (send the version read, for a conflict check) |
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
| `serendip capabilities create` | — | `POST /v1/capabilities` | Create a Capability |
| `serendip capabilities delete <id>` | — | `DELETE /v1/capabilities/{id}` | Delete a Capability. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip capabilities get <id>` | — | `GET /v1/capabilities/{id}` | Get one Capability |
| `serendip capabilities list` | — | `GET /v1/capabilities` | List capabilities the caller may read |
| `serendip capabilities update <id>` | — | `PUT /v1/capabilities/{id}` | Replace a Capability's spec (send the version read, for a conflict check) |
| `serendip capabilities default get <name>` | `capability_default_get` | `GET /v1/capabilities/{name}/default` | A capability's default provider |
| `serendip capabilities default set <name>` | — | `PUT /v1/capabilities/{name}/default` | Set a capability's default provider |
| `serendip clusters create` | — | `POST /v1/clusters` | Create a Cluster |
| `serendip clusters delete <id>` | — | `DELETE /v1/clusters/{id}` | Delete a Cluster. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip clusters get <id>` | — | `GET /v1/clusters/{id}` | Get one Cluster |
| `serendip clusters list` | — | `GET /v1/clusters` | List clusters the caller may read |
| `serendip clusters update <id>` | — | `PUT /v1/clusters/{id}` | Replace a Cluster's spec (send the version read, for a conflict check) |
| `serendip components create` | `components_create` | `POST /v1/components` | Create a Component |
| `serendip components delete <id>` | `components_delete` | `DELETE /v1/components/{id}` | Delete a Component. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip components dependencies <id>` | `components_dependencies` | `GET /v1/components/{id}/dependencies` | A Component's declared dependencies: the Components it calls, and those of its Application that call it |
| `serendip components get <id>` | `components_get` | `GET /v1/components/{id}` | Get one Component |
| `serendip components list` | `components_list` | `GET /v1/components` | List components the caller may read |
| `serendip components schemas <id>` | `components_schemas` | `GET /v1/components/{id}/schemas` | What can be configured on a Component, as JSON Schemas: its per-environment configuration (a Deployment's overrides.config) and each trait's |
| `serendip components update <id>` | `components_update` | `PUT /v1/components/{id}` | Replace a Component's spec (send the version read, for a conflict check) |
| `serendip deployments create` | `deployments_create` | `POST /v1/deployments` | Create a Deployment |
| `serendip deployments delete <id>` | `deployments_delete` | `DELETE /v1/deployments/{id}` | Delete a Deployment. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip deployments events <id>` | `deployments_events` | `GET /v1/deployments/{id}/events` | Recent platform events about what a Deployment runs as, newest first |
| `serendip deployments get <id>` | `deployments_get` | `GET /v1/deployments/{id}` | Get one Deployment |
| `serendip deployments list` | `deployments_list` | `GET /v1/deployments` | List deployments the caller may read |
| `serendip deployments logs <id>` | `deployments_logs` | `GET /v1/deployments/{id}/logs` | Recent log lines from a Deployment's running instances, oldest first. Logs can hold sensitive data, so they need their own permission |
| `serendip deployments promote <id>` | `deployments_promote` | `POST /v1/deployments/{id}/promote` | Run the release a Deployment runs now in the next Environment of the Project's pipeline. The target keeps its own overrides |
| `serendip deployments runtime <id>` | `deployments_runtime` | `GET /v1/deployments/{id}/runtime` | What a Deployment runs as on the platform: its objects with their health, then each running instance |
| `serendip deployments update <id>` | `deployments_update` | `PUT /v1/deployments/{id}` | Replace a Deployment's spec (send the version read, for a conflict check) |
| `serendip environments create` | — | `POST /v1/environments` | Create a Environment |
| `serendip environments delete <id>` | — | `DELETE /v1/environments/{id}` | Delete a Environment. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip environments get <id>` | — | `GET /v1/environments/{id}` | Get one Environment |
| `serendip environments list` | — | `GET /v1/environments` | List environments the caller may read |
| `serendip environments update <id>` | — | `PUT /v1/environments/{id}` | Replace a Environment's spec (send the version read, for a conflict check) |
| `serendip graph blast-radius <kind> <id>` | `graph_blast_radius` | `GET /v1/graph/{kind}/{id}/blast-radius` | Everything deleting a resource would affect |
| `serendip graph children <kind> <id>` | `graph_children` | `GET /v1/graph/{kind}/{id}/children` | What a resource directly owns |
| `serendip graph dependents <kind> <id>` | `graph_dependents` | `GET /v1/graph/{kind}/{id}/dependents` | What depends on a resource |
| `serendip incidents create` | — | `POST /v1/incidents` | Create a Incident |
| `serendip incidents delete <id>` | — | `DELETE /v1/incidents/{id}` | Delete a Incident. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip incidents get <id>` | — | `GET /v1/incidents/{id}` | Get one Incident |
| `serendip incidents list` | — | `GET /v1/incidents` | List incidents the caller may read |
| `serendip incidents update <id>` | — | `PUT /v1/incidents/{id}` | Replace a Incident's spec (send the version read, for a conflict check) |
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
| `serendip packages list` | `packages_list` | `GET /v1/packages` | The optional packages this server offers (builds, observability) and whether each is available. Install one by creating a ProviderInstance naming it |
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
| — | `resources_get` | `GET /v1/{kind}/{id}` | Get one resource of one kind |
| — | `resources_list` | `GET /v1/{kind}` | List resources of one kind (infrastructure, organisation, operations) |
| `serendip secrets create` | `secrets_create` | `POST /v1/secrets` | Create a Secret |
| `serendip secrets delete <id>` | `secrets_delete` | `DELETE /v1/secrets/{id}` | Delete a Secret. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip secrets get <id>` | `secrets_get` | `GET /v1/secrets/{id}` | Get one Secret |
| `serendip secrets list` | `secrets_list` | `GET /v1/secrets` | List secrets the caller may read |
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
| `serendip volumes create` | — | `POST /v1/volumes` | Create a Volume |
| `serendip volumes delete <id>` | — | `DELETE /v1/volumes/{id}` | Delete a Volume. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request |
| `serendip volumes get <id>` | — | `GET /v1/volumes/{id}` | Get one Volume |
| `serendip volumes list` | — | `GET /v1/volumes` | List volumes the caller may read |
| `serendip volumes update <id>` | — | `PUT /v1/volumes/{id}` | Replace a Volume's spec (send the version read, for a conflict check) |

| Route | Not on | Why |
|---|---|---|
| `/` | CLI | the catch-all route only ever answers 404 |
| `/` | MCP | the catch-all route only ever answers 404 |
| `DELETE /v1/alerts/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `DELETE /v1/authorization/roleAssignments/{id}` | MCP | agents never manage access (spec 018 guardrail); the API would refuse every call |
| `DELETE /v1/authorization/roleDefinitions/{id}` | MCP | agents never manage access (spec 018 guardrail); the API would refuse every call |
| `DELETE /v1/capabilities/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `DELETE /v1/clusters/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `DELETE /v1/environments/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `DELETE /v1/incidents/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `DELETE /v1/nodes/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `DELETE /v1/organizations/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `DELETE /v1/projects/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `DELETE /v1/provider-instances/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `DELETE /v1/providers/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `DELETE /v1/sites/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `DELETE /v1/teams/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `DELETE /v1/volumes/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `GET /v1/authorization/directory` | MCP | directory data, readable only by those who may assign roles, which agents never may |
| `GET /v1/authorization/principals` | MCP | directory data, readable only by those who may assign roles, which agents never may |
| `GET /v1/authorization/principals/person/{id}/groups` | MCP | directory data, readable only by those who may assign roles, which agents never may |
| `POST /v1/alerts` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `POST /v1/approvals/{id}/approve` | MCP | only a person or an API key decides an approval (spec 019); an AI never does |
| `POST /v1/approvals/{id}/reject` | MCP | only a person or an API key decides an approval (spec 019); an AI never does |
| `POST /v1/authorization/roleAssignments` | MCP | agents never manage access (spec 018 guardrail); the API would refuse every call |
| `POST /v1/authorization/roleDefinitions` | MCP | agents never manage access (spec 018 guardrail); the API would refuse every call |
| `POST /v1/capabilities` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `POST /v1/clusters` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `POST /v1/environments` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `POST /v1/incidents` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `POST /v1/nodes` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `POST /v1/organizations` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `POST /v1/projects` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `POST /v1/provider-instances` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `POST /v1/providers` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `POST /v1/sites` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `POST /v1/teams` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `POST /v1/volumes` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `PUT /v1/alerts/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `PUT /v1/capabilities/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `PUT /v1/capabilities/{name}/default` | MCP | a platform-wide setting, changed by people |
| `PUT /v1/clusters/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `PUT /v1/environments/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `PUT /v1/incidents/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `PUT /v1/nodes/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `PUT /v1/organizations/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `PUT /v1/projects/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `PUT /v1/provider-instances/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `PUT /v1/providers/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `PUT /v1/sites/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `PUT /v1/teams/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
| `PUT /v1/volumes/{id}` | MCP | infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get |
