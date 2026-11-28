# ADR 0025: Templates are versioned, parameterized resource bodies, rendered by substitution

Status: Accepted
Date: 2026-11-28

## Context

Spec 020 left "the scaffolder and templates" out of the console. People
still make every Application and Component by hand, field by field,
and a team can't share "the way we build a web service here".

Spec 027 adds templates. Three facts shaped the choice (spec 027
`research.md`):
- **OpenChoreo's portal has no application scaffolder.** Its Backstage
  scaffolder templates are admin tools that call `openchoreo-api`, which
  Serendip doesn't expose (ADR 0019). Components come from a wizard
  driven by ComponentType schemas.
- **Azure's model is Template Specs:** a `templateSpecs` resource in a
  resource group with immutable `versions`, deployed with parameters,
  with a what-if preview. Deploying needs `templateSpecs/versions/read`
  plus every permission the deployment's resources need.
- **Serendip already has the parts:** `core/jsonschema` validates a
  JSON Schema subset (spec 020) and the console's `SchemaForm` renders
  one. The Component and Deployment handlers hold every admission rule.

## Decision

### 1. Two kinds: `Template` and `TemplateVersion`

Azure analogue: `Microsoft.Resources/templateSpecs` and
`templateSpecs/versions`.
- A `Template` is owned by a Project, or by nobody (platform-wide, at
  `/`). Actions `serendip/templates/{read,write,delete}`.
- A `TemplateVersion` is owned by its Template, named by its label
  (unique within the Template), and served only beneath it. **It has no
  update route,** so a published version never changes. Actions
  `serendip/templates/versions/{read,write,delete}`.
- Deletes go through approvals (ADR 0016). Deleting a Template cascades
  to its versions.
- Three built-ins are declared in Go with fixed IDs, read-only, and
  readable by anyone signed in, like built-in role definitions.

### 2. Parameters are a JSON Schema object

A version's `parameters` is the `core/jsonschema` subset: a top-level
object whose properties are strings, integers, numbers or booleans,
with `default`, `title`, `description`, and `x-serendip-ref: Secret`
for a Secret's ID. Nested objects and arrays are refused. Each default
must satisfy its own property.

### 3. The body is substituted, never evaluated

A version's `body` lists Components shaped like `ComponentSpec`, plus a
template-local `name`. `{"$param": "x"}` is replaced by the typed value;
`${x}` inside a string by its text. **Substitution works on the decoded
JSON tree, and a value is never parsed again,** so a value that looks
like YAML, JSON or a placeholder stays literal text. There are no
expressions, conditionals or loops. At publish, the body is rendered
with every default and must pass the same validators as a hand-made
Component.

### 4. Creating goes through the existing checks, in one transaction

`preview` and `create` on a Template render the body, then run **each
new resource's own admission** (the Component and Deployment handlers'
checks, extracted to take a view that sees the pending batch first).
A template never grants anything its user lacks. `create` writes every
resource, edge and audit row in **one multi-kind SQLite transaction**,
and events are published only after commit. Any failure creates
nothing.

### 5. Provenance is a server-set field

`ApplicationSpec` and `ComponentSpec` gain `createdFrom`: the template's
ID and name, the version's ID and label, who and when, and the
parameters. Clients can't set or change it. It names the template by
name too, so it stays meaningful after the template is deleted, and
it's not a graph edge, so it never blocks a delete. The audit trail is
one row per created resource, tied together by the request ID. The
OpenChoreo provider ignores the field.

## Alternatives rejected

- **Backstage Software Templates, or OpenChoreo's scaffolder actions.**
  Backstage is out of scope (spec 020), and the actions call
  `openchoreo-api` directly, bypassing Serendip's authorization, audit
  and approvals.
- **A template language** (Go `text/template`, CEL, Jsonnet). Text
  substitution invites injection, and conditionals and loops grow into
  a programming language that every reviewer must read.
- **Helm charts as templates.** They template cluster objects, not
  Serendip resources, so they would bypass the resource model.
- **ARM's parameter format.** JSON Schema is already validated and
  rendered here.
- **One kind with versions inside it.** Immutability would then need a
  field-level rule on update, and a version couldn't have its own
  delete or approval.
- **Provenance as a graph edge.** An edge to the template would block or
  cascade on the template's delete, and created resources must outlive
  their template.
- **Creating resources one by one with a compensating delete.** A crash
  between creates would leave half an Application; one transaction
  doesn't.

## Consequences

- No new pods, no new dependency and no new reconciler. Both kinds use
  the existing `resources` table.
- The store gains a multi-kind transaction (`Atomically`, `CreateIn`)
  behind an interface in `core/store`, so `api/` doesn't import
  `sqlite`.
- Created resources don't follow their template; a template's fix
  doesn't reach Applications made earlier (RP-107).
- "Latest" is the newest-created version, not the highest label
  (RP-104).
- `createdFrom` sits in the spec of user-writable kinds, guarded by the
  handlers (RP-105).

## Revisit

- If people ask for nested or list parameters (for example a list of
  ports), with a form design for them.
- If a template needs resources other than Components and Deployments
  (Databases, once that capability exists).
- If created resources should be able to re-apply a newer version, with
  a drift report first.
