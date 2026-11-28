# Plan: Spec 027, Templates and a scaffolder

Status: Approved, 2026-11-28.

Spec: `spec.md` (approved 2026-11-28). Research: `research.md`. Epic #68.

## Research

No more upstream research is needed. Nothing in this spec changes a
provider or an in-cluster object. Templates live in Serendip's store and
produce the Applications, Components and Deployments that specs 011, 020,
022 and 026 already reconcile. `research.md` covers what OpenChoreo and
Azure do. One question was left open there: whether a rendered
Component's new field can reach the platform's release. The provider
maps Component fields one by one (`providers/application/openchoreo`)
and never hashes or copies the whole spec, so a new field is invisible
to it. T001 checks that with a test.

## Decisions

### 1. Two kinds: `Template` and `TemplateVersion` (spec open question 2)

This mirrors Azure's `templateSpecs` and `templateSpecs/versions`.

- **`Template`** has a description and a display name. It's owned by a
  Project, or has no owner (platform-wide, at `/`). It's served at
  `/v1/templates` with the generic list, get, create, update and delete
  routes. Its actions are `serendip/templates/{read,write,delete}`.
- **`TemplateVersion`** is owned by its Template. Its name is the
  version label (`1.0`, `2026-11`), unique within the Template through
  the store's unique key (spec 022's `SetUniqueKey`). Its spec holds
  the parameters and the body. It's a `nestedPaths` kind, like a Build,
  served only beneath its Template:
  - `GET /v1/templates/{id}/versions` and
    `GET /v1/templates/{id}/versions/{version}`;
  - `POST /v1/templates/{id}/versions`;
  - `DELETE /v1/templates/{id}/versions/{version}`, with an Approval as
    every DELETE has (spec acceptance criterion 10).

  **There's no update route,** so a published version can't change
  (criterion 5). A `PUT` answers 405, and the generic `PUT` doesn't
  exist because the kind isn't in `resourcePaths`. Version actions are
  `serendip/templates/versions/{read,write,delete}`, so a role can let
  someone publish versions without letting them rename or delete the
  Template.
- **Deleting a Template cascades to its versions**, under one Approval
  with `cascade: true`, as an Application's delete takes its
  Components.
- **"Latest"** is the version created most recently (Azure has no
  latest, and makes you name one). Labels are free text and aren't
  sorted as semver.

### 2. Parameters are a JSON Schema object (spec scope item 3)

A version's `parameters` is a JSON Schema object of the subset that
`core/jsonschema` already validates (spec 020), and the console's
`SchemaForm` already renders it. No new validator and no new form are
needed.

- **The top level** is `type: object` with `properties`. Each property
  is a parameter, and `required` lists the required ones.
- **Types:**
  - `string` (with `pattern`, `minLength`, `maxLength`, `enum`);
  - `integer` and `number` (`minimum`, `maximum`);
  - `boolean`.

  Nested objects and arrays are refused at publish (400), because the
  body substitutes whole values (decision 3) and a form for them
  wouldn't be simple.
- **Annotations:** `default`, `description` and `title`.
  `x-serendip-ref: Secret` marks a string parameter as a Secret's ID:
  the console shows a Secret picker, and decision 6 applies.
  `core/jsonschema` already passes `x-` keys through as annotations.
- **At publish, each `default` must satisfy its own property** (spec
  criterion 13). This is checked by validating it with the same schema.
- **Limits:** at most 50 parameters; a version's whole spec at most
  256 KiB (inside the API's 1 MiB body limit).

### 3. The body: the request bodies, with whole-value and in-string placeholders (open question 1)

A version's `body` lists Components, each shaped like `ComponentSpec`
plus a template-local `name`:

```json
{
  "components": [
    {"name": "api", "image": {"$param": "apiImage"},
     "ports": [{"name": "http", "port": {"$param": "port"}}]},
    {"name": "web", "image": "nginx:${tag}",
     "ports": [{"name": "http", "port": 80, "expose": true}],
     "dependencies": [{"component": "api", "port": "http", "env": "API_URL"}],
     "secretEnv": [{"env": "DB_PASSWORD", "secret": {"$param": "dbSecret"}, "key": "password"}]}
  ]
}
```

- **Whole-value:** a JSON object that is exactly `{"$param": "<name>"}`
  is replaced by the parameter's typed value, so a port stays a number.
  An optional parameter with no value and no default removes the field
  it fills, or the list entry for an item that consists only of a
  placeholder.
- **In-string:** `${name}` inside a string is replaced by the value's
  text, for string, integer and number parameters only. `$${` is a
  literal `${`.
- **Substitution is done on the decoded JSON tree, never on text,** and
  a value is never parsed again. The rendered tree is decoded into each
  `ComponentSpec` with unknown fields refused, then checked by the
  kind's own validators. A value that looks like YAML, JSON or a
  placeholder lands as literal text in its field (spec criterion 6).
  There are no expressions, conditionals or loops.
- **Dependencies inside the template name a template-local `name`,** and
  rendering turns them into the new Components' IDs (decision 4). A
  dependency on a name the body doesn't contain, an unknown `$param`,
  a parameter declared but never used, or two Components with the same
  `name` is refused at publish (criterion 13). The local names become
  the Components' resource names, prefixed with
  `prefix` when the request gives one (decision 4).
- **At publish, the body is rendered with every default** (and a
  placeholder value for required parameters without one: the
  property's first `enum` value, or a type-appropriate dummy). Every
  rendered Component must pass the same pure validators as a hand-made
  one. A template that couldn't ever be valid is refused when it's
  published, not when it's first used.
- **Limits:** at most 20 Components per template.

### 4. Using a template: preview and create (spec scope items 6 and 7)

Routes on the Template:
`POST /v1/templates/{id}/preview` and `POST /v1/templates/{id}/create`.
The `create` name follows the existing `promote` verb, and both have
the same body:

```json
{"version": "1.0", "parameters": {...}, "project": "<id>",
 "application": "<id>" | "applicationName": "shop",
 "prefix": "shop-", "environment": "<id>"}
```

- **`version`** is optional (latest). Give exactly one of
  `application` (an existing one) and `applicationName` (a new one in
  `project`). `environment` is optional: it deploys each new Component
  to that Environment with the latest release (`release: ""`).
- **The handler holds `workloadWrites`** (the Component and Deployment
  handlers' shared lock) and works in this order:
  1. Authorize the template (decision 5).
  2. Validate the parameters against the version's schema, collecting
     **every** error (criterion 4).
  3. Render the body.
  4. Give every new resource its ID (`id.New()`), and map local names to
     IDs.
  5. Run each new resource's admission (decision 7).
  6. **Preview** returns the would-be resources (metadata and spec, IDs
     left out because they're assigned again on create). **Create**
     writes them all in one transaction (decision 8) and answers 201
     with the created resources.
- **Errors come as one problem with a list** of `{field, detail}`, such
  as `parameters.port` or `components[web].dependencies[0]`. The
  existing problem type gains an optional `errors` extension (RFC 7807
  allows members), which every client ignores today.
- **Built-ins and their packages:** a body can name the packages it
  needs (`"requires": ["builds"]`). Preview and create check them with
  the same `buildReadiness` the Component handler uses. A missing
  package is a 409 naming it, and nothing is created (criterion 1).

### 5. Authorization (spec scope item 9)

- **Reading a template** needs `serendip/templates/read` at its scope
  (the owning Project, or `/`). Reading a version is
  `serendip/templates/versions/read` at the same scope. Reader and the
  roles built on `*/read` include both, and Contributor and Owner have
  `*`, so **no built-in role changes**. `authorization.md`'s table gains
  the two action namespaces.
- **Preview and create** need `serendip/templates/versions/read` at the
  template's scope (Azure needs `templateSpecs/versions/read`), checked
  by the route rule. After that, **every created resource goes through
  the same admission its own route uses** (decision 7):
  - `serendip/applications/write` at the Project for a new Application,
    or `serendip/components/write` at the existing Application;
  - `serendip/components/write` for each Component;
  - `serendip/secrets/value/read` on each Secret it names (spec 026);
  - the source check for builds' `credentialSecret` (spec 022);
  - `serendip/deployments/write` at the Environment, with the pipeline
    admission (`resource.AdmitRelease`), so only the pipeline's first
    Environment takes a new release.

  A refusal is 403 naming the missing action and scope, audited as now
  through `recordDenied` (criterion 7). It's decided before existence
  is checked, as `alerting_authz.go` does.
- **Built-in templates** are readable by anyone signed in, like built-in
  role definitions (`GET /v1/authorization/roleDefinitions` is
  `authOnly`). This is simpler than the spec's "anyone who may create an
  Application somewhere", which the authorizer can't answer cheaply
  without walking every assignment, and a starter template isn't
  secret. Using one still needs every permission above. **spec.md's
  scope item 5 and criterion 8 change to "anyone signed in"** (T001).
- **A Template at `/` needs `serendip/templates/write` at `/`**
  (`targetResource` falls back to the root for an owner-less resource,
  as the generic handler does for platform-wide kinds).
- **Agents** follow the usual guardrails: their deletes need approval,
  and a template can't route around them, because creation is the same
  writes.

### 6. Secret parameters

A Secret parameter (`x-serendip-ref: Secret`) is only an ID, filled into
`secretEnv[].secret` or `source.credentialSecret`. The spec 026 and
spec 022 checks then see it like any hand-written one, so the
value-read rule applies (criterion 7). Values never pass through a
template: there's no "secret value" parameter type.

### 7. Admission is reused, not copied

The Component and Deployment handlers' checks read existing resources
from their stores:
- `checkDependencies` gets each target Component;
- `coherent` gets the Component and its Application;
- `secretEnvChecker` and `sourceChecker` check existence and
  ancestry.

A template's Components depend on each other before any of them exists.
So:

- **The checks take a `workloadView`:** the Component, Application and
  Deployment getters and the ancestry lookup. The handlers pass their
  stores, and the template path passes an **overlay** that answers from
  the pending batch first, then from the stores. That's an extraction
  from `topology_handler.go` and `deployment_handler.go`, with no new
  rules. The existing handler tests must still pass unchanged (T005's
  Verify).
- **A new Application's ancestry** (Project → Application → Component)
  comes from the batch's owner refs, so `secretEnvChecker`'s "the
  Secret is reachable from the Component" check sees the right Project.
- **Ordering:** Components in an order where each one's dependencies come
  first. The local dependency graph is checked for cycles at publish;
  spec 020 allows cycles between Components, but a single batch
  doesn't need them. A cycle in the body is refused at publish with
  the names. Then the Deployments.

### 8. All or nothing: one store transaction (open question 3)

`core/store/sqlite` gains a multi-kind transaction:

```go
func (db *DB) Atomically(ctx context.Context, fn func(tx *Tx) error) error
func (s *Store[S, St]) CreateIn(ctx context.Context, tx *Tx, r resource.Resource[S, St]) (resource.Resource[S, St], error)
```

- **`Create`'s body moves into a `createTx` helper** that both use: the
  insert, `graph.SyncEdges`, and the audit row. `Create` keeps its
  behaviour.
- **`Tx` collects the events,** and `Atomically` publishes them only
  after commit, in creation order, so the reconciler never sees an
  Application before the transaction that made it has committed.
- **The unique key** (Template versions, provider instances) is enforced
  inside the transaction, and a duplicate rolls back everything.
- **The interface in `core/store` gains `Atomic`** (an interface with
  `Atomically`) and `TxCreator[S, St]`, so `api/` depends on the
  interface, not on `sqlite` (ADR 0003's layering). A store without
  transactions (none today) would fail closed.

A failure after the checks (a duplicate key, a disk error) rolls back
every row, edge and audit record (criterion 3). That's tested by
injecting a failing create in the middle of a batch.

### 9. Provenance: a `createdFrom` field, not an edge (open question 4)

`ApplicationSpec` and `ComponentSpec` gain
`CreatedFrom *TemplateOrigin \`json:"createdFrom,omitempty"\``:

```go
type TemplateOrigin struct {
	Template     string         `json:"template"`     // ID
	TemplateName string         `json:"templateName"`
	Version      string         `json:"version"`      // label
	VersionID    string         `json:"versionId"`
	By           string         `json:"by"`           // the actor
	At           time.Time      `json:"at"`
	Parameters   map[string]any `json:"parameters,omitempty"` // on the Application, or each Component when added to an existing one
}
```

- **It's server-set only.** On a client's create through
  `/v1/applications` or `/v1/components`, a `createdFrom` is refused
  (400), so provenance can't be forged. On update, the field must
  equal the stored one: copied automatically if omitted, refused if
  changed.
- **It carries the template's name and version label,** so it stays
  meaningful after the template is deleted (criterion 10). No graph
  edge exists, so deletes aren't blocked or affected.
- **The audit trail:** each created resource gets its own audit row (as
  constitution 8 requires), all with the request's ID, and the
  parameters are in the Application's row (or each Component's).
  `audit_log.request_id` ties them together (there's no audit read
  route yet; that's not this spec's). Spec scope item 8's "one action"
  is the request ID: one request, one record set.
  spec.md item 8 and criterion 9 are reworded to say that (T001).
- **No secret values:** parameters are IDs, numbers and plain text, so
  only Secret IDs reach the audit (decision 6).
- **The provider ignores it,** because the Component's fields reach the
  platform one by one (Research, above). T001 adds a test that a
  `createdFrom` change doesn't change the Workload.

### 10. Built-in templates (open question 5)

- **They're declared in Go** (`core/template/builtin.go`) with fixed
  UUIDs, like `core/policy/builtins.go`. They aren't rows in the store.
- `GET /v1/templates` merges them into the list (marked
  `"builtIn": true`). Their versions are served by the same routes.
- `PUT` and `DELETE` on them, or `POST` of a version, answer 400 ("a
  built-in template can't be changed").
- **Their scope is `/`,** but their read is `authOnly` (decision 5).

### 11. The starter list (open question 6)

Each defaults to an image edge-1 already uses, so a new install needs no
extra pull for the first two. `nginx:1.27-alpine` is the benchmark's
image and is cached on the L480.

| Built-in | Components | Parameters (required in bold) |
|---|---|---|
| `web-server` | one, port 80 exposed | `image` (default `nginx:1.27-alpine`), `port` (80) |
| `service-from-source` | one, built from Git; `requires: ["builds"]` | **`repository`**, `branch` (main), `path` (.), `port` (8080), `credentialSecret` (Secret, optional) |
| `web-and-api` | `api` (port `http`) and `web` (exposed), web depends on api with `API_URL` | `webImage`, `apiImage` (both `nginx:1.27-alpine`), `apiPort` (80) |

The plan adds no "with a database" starter: there's no Database
capability yet. A secret parameter is shown by `service-from-source`'s
`credentialSecret`, and by an example in the docs.

### 12. Interfaces (spec scope item 11)

- **API:**
  - `/v1/templates` (generic routes, plus the merge in decision 10);
  - the versions routes (decision 1);
  - `preview` and `create` (decision 4).

  Every route goes into `routeTable()` with its rule.
- **CLI and MCP from the catalogue** (`api/catalog`):
  - `templates` gets the per-kind operations, plus
    `template_versions_{list,get,create,delete}`, `template_preview` and
    `template_create_from`;
  - `serendip templates create --file t.json` and
    `serendip template-versions create --id <t> --file v.json` use the
    existing `--file` body flag. JSON only: YAML would need a dependency
    or an ADR, and JSON is what the API takes;
  - `catalog_parity_test` covers every new route.
- **Console** (`ui/`):
  - **Templates** under Applications: a gallery of cards (built-ins
    first), and a template's page with its versions and their
    parameters.
  - **"Create from template":** choose a version, Project, new or
    existing Application, an optional Environment, and the parameters
    form (`SchemaForm`, with a Secret picker for `x-serendip-ref`).
    **Preview** lists what will be made, and **Create** makes it.
  - **The Application page** shows "Created from <template> <version>
    by <who> on <date>".
  - The empty state of Applications links to the gallery.
  - No template editor (spec Out).
  - A template's page shows its `ApiEquivalent`, as other screens do.

### 13. Migration

None. Both kinds use the existing `resources` table, and the version
label is the existing `unique_key` column (migration 0009). Old
Applications and Components have no `createdFrom`.

## What it costs

- **No new pods, no new dependency,** and no new reconciler.
  `TemplateVersion` and `Template` have nothing to reconcile: the
  handler sets their status to `Ready=True` on write, and the engine
  already skips events for kinds with no registered reconciler
  (`core/reconcile/engine.go:133`).
- **`serendipd` memory:** the built-ins are a few KiB, and a create holds
  at most 20 Components and their Deployments in memory. The target is
  within 2 MiB of today's idle RSS, which T012 measures on the L480.
- **A create takes the workload lock** for its checks and one
  transaction (about 20 inserts). That's milliseconds on SQLite, but it
  serializes with other Component and Deployment writes, as they
  already do with each other.

## Risks to register (RP-104 onward)

- **RP-104:** "latest" means newest created, not highest. Publishing
  `1.1` after `2.0` makes `1.1` the latest. Accepted (labels are free
  text); the console shows the creation date beside each label.
- **RP-105:** `createdFrom` in the spec of user-writable kinds. The
  handlers guard it on create and update. A future generic write path
  that skips them could forge provenance; a store-level check is the
  backstop if that happens.
- **RP-106:** built-ins are readable by anyone signed in, including an
  agent with no assignment. Accepted: they hold no data, and using one
  needs every write.
- **RP-107:** created resources don't follow their template (spec Out),
  so a template's fix doesn't reach Applications made earlier. Users
  may expect it to. The console says so on the create screen.
- **RP-108:** one transaction with up to 20 Components plus Deployments
  holds SQLite's write lock for the batch. Measured in T004's benchmark.
  If it's over 50 ms on the L480, lower the limit.

## Build vs adopt

- **Adopted (reused):** `core/jsonschema` and the console's `SchemaForm`
  for parameters; the existing admission checks; the store's
  transactions.
- **Built:** the two kinds, substitution and the batch create, because
  nothing upstream fits.
- **Rejected:**
  - Backstage Software Templates and OpenChoreo's scaffolder actions
    (Backstage is out of scope in spec 020; the actions call
    `openchoreo-api`, ADR 0019);
  - a template language (Go `text/template`, CEL, Jsonnet), because text
    substitution invites injection and conditionals invite a
    programming language;
  - Helm charts as templates (they template cluster objects, not
    Serendip resources, and would bypass the resource model);
  - ARM's parameter format (JSON Schema is already validated and
    rendered here).

## Not done (from spec Out, and the plan's own limits)

- No repo creation, no Backstage, no custom component types, no
  re-apply or drift report, no export, no environment templates, no
  catalogs synced from Git, no gallery import, and no console Project or
  Environment creation.
- **No nested objects or arrays as parameters** (decision 2). A list of
  ports, for example, is fixed in the template.
- **No semver ordering of versions** (RP-104).
- **No YAML input** for templates.
- **No cycles among a template's Components** (decision 7). Hand-made
  ones may still have them.
- **One Environment per create.** Deploying to more is a promote, as
  today.

## Tasks

`tasks.md`.
