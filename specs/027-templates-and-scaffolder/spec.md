# Spec 027: Templates and a scaffolder

Status: Approved, 2026-11-28.

Epic #68. Spec 020 left "the scaffolder and templates" out
(`specs/020-openchoreo-console-features/spec.md:185`): creating
Components stayed on Serendip's own forms, one Component at a time. The
research (`research.md`) found that OpenChoreo's portal has **no
application scaffolder** to copy. Its scaffolder templates are
platform-admin tools, and developers create Components through a wizard
driven by a ComponentType's parameter schema. A Serendip template is
therefore Serendip's own design. Its Azure model is **Template Specs**
(`Microsoft.Resources/templateSpecs`): a versioned template stored as a
resource, shared through RBAC, written by platform teams and deployed by
developers who fill in its parameters. Azure Deployment Environments
(a catalog platform engineers write, developers fill in) is the second
model, for the catalog idea.

## Problem

- **Every application is built by hand, one field at a time.** A new
  Application means creating it, then each Component with its image or
  source, ports, variables, dependencies and secret variables, then a
  Deployment per Environment. Nothing captures "this is how our team
  runs a web service" so the next one starts from it.
- **Platform teams can't hand developers a paved road.** There's no way
  to say "use this shape, and choose only these few things": the
  registry, the port convention, the required variables, which Secret
  holds the database password.
- **A first-time user has nowhere to start.** The console's empty state
  is an empty form. A new install should offer a few ready-made starting
  points that work on edge-1 as installed.
- **Multi-Component applications are the hardest to get right by hand.**
  A front end that calls an API needs the dependency, the port name and
  the variable to match across two Components (spec 020); a template can
  carry that once.
- **Nothing records where an application came from.** Once created, an
  Application doesn't say which template, or which version of it, made
  it.

## Scope

### In

1. **A `Template` resource** (Azure: a Template Spec). It describes one
   or more Components of an Application (their image or source build,
   ports, plain variables, dependencies between them, and secret
   variables) with **parameters** in place of the values a user chooses.
   It has a name, a description and the parameters' definitions.
2. **Versions.** A Template has versions (Azure: `templateSpecs/versions`).
   A published version never changes. A change is a new version, and
   older versions stay usable until deleted. Using a template names a
   version, or gets the latest.
3. **Parameters.** Each has a name, a type (text, number, yes/no, a
   Secret reference, and the choice of one of a fixed list), an optional
   default, whether it's required, and a description. Text can be
   constrained (a pattern, a length). Parameters are filled in by
   substitution only: a template runs no code and has no expression
   language.
4. **Where templates live, and who sees them.** A Template belongs to a
   Project, and the Project's role assignments decide who reads, writes
   and deletes it, as for everything a Project owns (Azure: a Template
   Spec in a resource group). Platform-wide templates are kept at the
   root scope `/` and need write there.
5. **Built-in starter templates** shipped with Serendip, read-only, the
   same on every install (as built-in roles are), and visible to anyone
   who may create an Application somewhere. They work on edge-1 as
   installed. The plan settles the list; at least:
   - a web server from a container image;
   - a service built from a Git repository (needs the builds package,
     and says so when it's missing);
   - a front end and an API, with the dependency between them.
6. **Creating from a template** (Azure: deploying a Template Spec). The
   user picks a template and version, fills in its parameters, and picks
   a Project and either a new Application or an existing one. Serendip
   validates everything first, then creates the Application (if new) and
   all its Components through the normal resource path, **all or
   nothing**. Optionally, it also deploys the new Components to one
   Environment. What it creates are ordinary resources, reconciled like
   any other (constitution 3); after creation they don't follow the
   template.
7. **Preview** (Azure: what-if). The same request, without creating
   anything, returns what would be created, or every validation error
   at once. The console form uses it.
8. **Provenance.** Each resource made from a template records the
   template, the version and who used it. The Application's page shows
   it. The audit log records the creation as one action with its
   parameters (a Secret parameter is recorded as the Secret's ID, never
   a value).
9. **Authorization.** New actions `serendip/templates/{read,write,delete}`
   in the built-in roles the usual way (Reader reads, Contributor and
   Owner write and delete). Creating from a template needs
   `serendip/templates/read` on the template (as Azure needs
   `templateSpecs/versions/read`) **and** every permission the created
   resources would need on their own: Application and Component write in
   the target, Deployment write in the Environment when deploying, and
   `serendip/secrets/value/read` on each Secret a parameter names (spec
   026's rule). A template never grants anything its user doesn't have.
10. **Deletes.** Deleting a Template or one of its versions goes through
    approvals like every delete (constitution 5, ADR 0016). Resources
    made from it are untouched; their provenance keeps the name and
    version.
11. **Interface parity** (constitution 7): API, CLI, MCP and the console.
    - The console gets a template gallery and a "Create from template"
      form generated from the parameters (OpenChoreo's ComponentType
      wizard is the idea; ADR 0019 means none of its code), plus screens
      to view a template's versions.
    - Writing a template is done as a file through the API and CLI
      (`serendip template create -f`), so platform teams can keep
      templates in their own Git repo. A console editor for templates is
      out.
12. **Docs:** an ADR for the template model, `application-platform.md`
    (or a new `templates.md`), `resource-model.md`, `authorization.md`,
    `cli-and-mcp.md`, `docs/build-vs-adopt.md` and
    `docs/risk-register.md`.

### Out

- **Creating Git repositories** or copying starter code into one (azd's
  `azd init -t`). Constitution 10 rules out a Git server; a GitHub or
  GitLab integration is a separate spec, if ever. A source-build
  template points at a repository that already exists.
- **Backstage, its Software Templates, and OpenChoreo's scaffolder
  actions** (they call `openchoreo-api`, which Serendip bypasses, ADR
  0019).
- **Custom component types** (OpenChoreo's ComponentType, Trait and
  Workflow authoring). Serendip keeps its one `serendip-service` type;
  port-less workers stay unsupported (`application-platform.md`).
- **Keeping created resources in step with their template.** Updating a
  template doesn't change what was made from it, and there's no
  "re-apply" or drift report (Azure behaves the same way).
- **Exporting an existing Application as a template** (Azure's "Export
  template"). A follow-up.
- **Templates of whole Environments or infrastructure** (Azure
  Deployment Environments' IaC side). There's no Database capability yet.
- **Catalogs synced from a Git repository** (Deployment Environments'
  catalog). Templates are pushed through the API; syncing is a follow-up.
- **A community gallery** or importing templates from URLs.
- **Creating Projects or Environments from the console** (the known gap
  on the L480; a `bug` issue, fixed in spec 035 or sooner on request).
  The template form picks an existing Project and Environment.

## Affected resources and capabilities

- **Resources:** a new `Template` (with its versions), owned by a
  Project or kept at `/`. `Application` and `Component` gain provenance
  (which template and version made them). Created resources are the
  existing kinds, unchanged in shape.
- **Capabilities:** Application Platform (unchanged; it reconciles what
  is created), Authorization (new actions, the combined check on
  creation), Secrets (Secret parameters follow spec 026's value-read
  rule), Audit (one record per creation).
- **Interfaces:** `/v1/templates` (and its versions, preview and create
  routes), CLI, MCP tools, console.

## Acceptance criteria

Run on a real cluster (kind, then the L480) unless a unit test is named.

1. **Starter templates work as installed.** On a fresh edge-1 install,
   each built-in template can be used with only its required parameters,
   and the result serves traffic (the source-build one with the builds
   package installed; without it, preview and create say the package is
   needed and create nothing).
2. **Multi-Component.** The front-end-and-API template creates two
   Components whose dependency resolves: from inside the front end's
   pod, the variable holds the API's address and a request to it
   succeeds.
3. **All or nothing.** A create that fails validation, or authorization
   on any one resource, leaves nothing behind (no Application, no
   Component, no Deployment). A failure while creating does the same.
   Unit-tested and checked live.
4. **Preview matches create.** For the same request, preview lists
   exactly what create then makes, and reports every invalid or missing
   parameter at once, naming each. Unit-tested.
5. **Versions are immutable.** Changing a published version is refused;
   publishing a new version leaves the older one usable; using a template
   without a version gets the latest. Unit-tested.
6. **Parameters are only substituted.** A parameter value that looks like
   template syntax, YAML or JSON lands as literal text in the field it
   fills and is validated as that field (no injection into other fields
   or other Components). Unit-tested with hostile values.
7. **No escalation.** A user with `serendip/templates/read` but without
   Component write in the target Project gets 403, as does one naming a
   Secret they can't read the value of, and one deploying to an
   Environment they can't deploy to; each is audited and creates
   nothing. With the needed roles, the same request succeeds.
   Unit-tested and checked live.
8. **Scopes.** A Project's template is invisible to someone with no role
   in that Project; a `/` template needs write at `/` to create or
   change; built-ins can't be changed or deleted (400) and are visible to
   anyone who may create an Application.
9. **Provenance and audit.** An Application made from a template shows the
   template and version in the API and on its console page, and the audit
   log has one creation record with the parameters; no Secret value
   appears in it, the response, events or `serendipd`'s log.
10. **Deletes.** Deleting a template leaves an Approval (ADR 0016) and
    doesn't touch what was made from it; that Application's provenance
    still names the deleted template and version.
11. **Ordinary afterwards.** A Component made from a template can be
    edited, deployed, promoted and deleted exactly like a hand-made one.
12. **Parity.** Listing, viewing, preview and create work through the API,
    CLI, MCP and console; creating, versioning and deleting a template
    work through the API, CLI and MCP.
13. **Validation.** A template with an unknown parameter reference, an
    unused or duplicate parameter, a default that fails its own
    constraint, a dependency on a Component the template doesn't
    contain, or more than the limit of Components or parameters, is
    refused with 400 or 422 naming the field. Unit-tested.
14. **Lightweight** (constitution 4): no new pods, no new Go dependency
    (or an ADR), and `serendipd` idle RSS within a few MiB of today's
    (28.2 MiB on the L480, benchmark run 1).

## Open questions (for the plan)

1. **The template body's format.** A fixed shape mirroring the
   Component and Deployment request bodies with `${param}` placeholders
   in string fields, versus a typed tree where any field can name a
   parameter. Either way: no expressions, and numbers and yes/no values
   need typed substitution (a port is a number, not text).
2. **How versions are stored.** Child resources (`Template` +
   `TemplateVersion`, as Azure does), or a list inside one `Template`
   record. It affects deletes (a version's delete needs its own
   approval) and the graph.
3. **All-or-nothing creation.** One store transaction across the
   Application, Components and Deployment, versus create-then-roll-back.
   The store already does multi-row transactions for cascade deletes;
   check that the audit and event paths fit one transaction.
4. **Where provenance lives.** A field on the resources (set only on
   creation, read-only after) or a `created-from` edge in the graph to
   the template. An edge would block or warn on template deletes, which
   scope item 10 says shouldn't happen.
5. **Built-ins at `/`.** How "visible to anyone who may create an
   Application" maps onto the authorizer, which today scopes reads by
   role assignment. Built-in role definitions are the precedent.
6. **The starter list** and the images they use, so each works offline
   after install where possible (edge-1 users may be air-gapped).
