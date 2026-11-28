# Tasks: Spec 027, Templates and a scaffolder

Status: Approved, 2026-11-28 (with `plan.md`).

[P] = can run in parallel with the task before it. [A] = subagent-eligible.
Every commit touching `core/` or `providers/` gets a `contract-guardian`
review first. Each task commit says `Closes #N` for its issue.

## T001: Spec wording, the ADR, and the provider check

- **Do:**
  - Reword `spec.md` scope item 5 and criterion 8 (built-ins readable by
    anyone signed in, plan decision 5), and item 8 and criterion 9 (one
    request, one record set by request ID, decision 9).
  - Write ADR 0025, "Templates": the two kinds, parameters as JSON
    Schema, substitution only, one transaction, and provenance as a
    field, with Template Specs as the Azure analogue and the rejected
    options.
  - Add a provider unit test showing a ComponentSpec field the provider
    doesn't map leaves the Workload unchanged.
- **Files:** `specs/027-templates-and-scaffolder/spec.md`,
  `docs/decisions/0025-templates.md`,
  `providers/application/openchoreo/*_test.go`.
- **Verify:** `go test ./providers/application/openchoreo/...` passes
  with the new test; the ADR and the spec changes are read back.

**Done as** (2026-11-28): `spec.md` scope items 5 and 8 and criteria 8
and 9 reworded (built-ins readable by anyone signed in; one request ID
ties the creation's audit rows). ADR 0025, "Templates". The provider
test is `unmapped_test.go`, table-driven: a change to a field the
provider doesn't put on the Workload (today `secretEnv`) leaves the
Workload's generation, the writes and the releases (compared whole)
unchanged. `secretEnv` is a stand-in, since `EnsureComponent` never
reads it; T002 adds the `createdFrom` rows (Component and Application)
once the field exists. The plan's risks are registered as RP-104 to
RP-108, because the ADR cites them. contract-guardian: PASS.

## T002: The kinds and their validation (core)

- **Do:**
  - Add `KindTemplate` and `KindTemplateVersion`, with their specs and
    statuses.
  - Add `TemplateOrigin` and `CreatedFrom` on `ApplicationSpec` and
    `ComponentSpec`.
  - Add `ValidateTemplateVersion` for decision 2's checks (parameters:
    the schema subset, defaults, limits) and decision 3's (body:
    unknown or unused `$param`, duplicate names, local dependencies,
    cycles, 20 Components).
  - Add the version's unique key (template ID and label).
- **Files:** `core/resource/template.go`, `core/resource/template_test.go`,
  `core/resource/app.go`.
- **Verify:** `go test ./core/...` with table-driven cases for each
  criterion 13 failure, each naming its field; `bash
  .claude/scripts/check-imports.sh`.

**Done as** (2026-11-28): `core/resource/template.go`, with
`TemplateSpec`, `TemplateVersionSpec` (parameters as raw JSON Schema,
body Components as raw JSON until rendered), `TemplateOrigin`,
`FieldErrors` (every problem, each naming its field, an
`ErrValidation`), `TemplateParams`, `ParamPlaceholder`, `StringParams`,
`ValidateTemplate` and `ValidateTemplateVersion`. The store's unique key
sees only the spec, so a version's spec carries `template` and
`version` (set by the server from the owner and the name, checked to
agree), and `TemplateVersionKey` is `<template>/<label>`. Duplicate
JSON keys are refused (`encoding/json` would keep the last silently),
which is criterion 13's "duplicate parameter". A Secret-reference
parameter has no default. `createdFrom` is on `ApplicationSpec` and
`ComponentSpec`, and `unmapped_test.go` has its Component and
Application rows. `go test ./core/... ./api/... ./providers/...` and
`check-imports.sh` pass. contract-guardian: PASS. Its findings fixed
here: a refused parameter or a duplicated Component name no longer
causes knock-on errors, and the non-list `required` and non-object
`properties` cases are tested. Number parameters also take `enum` (plan
decision 2 updated). Refusing a client-set `createdFrom` is T006's, and
rendering at publish is T003's.

## T003: Rendering and the built-ins (core) [P]

- **Do:**
  - Add `core/template` with `Render(version, params) (Rendered, []FieldError)`:
    - check the parameters against the schema (every error, with
      defaults applied);
    - whole-value and in-string substitution on the decoded tree;
    - drop omitted optional parameters;
    - decode into `ComponentSpec` with unknown fields refused.
  - Add publish-time rendering with every default (decision 3).
  - Add the three built-ins (decision 11) with fixed UUIDs.
- **Files:** `core/template/render.go`, `core/template/builtin.go`, tests.
- **Verify:** `go test ./core/template/...`, including the hostile values
  (criterion 6): `${x}`, `{"$param":…}` as text, YAML, a JSON object as
  a string, and a newline in an image. Each built-in renders and passes
  `ValidateTemplateVersion`.

**Done as** (2026-11-28): `core/template`. `Render` round-trips the
parameters through JSON, applies the defaults, checks them against the
version's schema (every problem, as `parameters.<name>`), substitutes on
the decoded tree (whole-value and `${}`; the template's own text is the
only text scanned, so a value is copied and never read again), decodes
each Component with unknown fields refused (a `createdFrom` in a body is
refused by name), runs the pure validators, and checks that each
dependency names a port of its target. Components come back in
dependency order with their local names, plus the parameters used.
`ValidatePublish` runs `ValidateTemplateVersion`, then renders twice
(optional parameters left out, then filled with stand-ins); a refusal
names the parameters that were stood in for. Parameters may give
`examples` (the first is the stand-in; each must fit its schema), so
plan decision 3 is updated. The port checks moved from
`core/reconcile` to `resource.ValidateComponentPorts`, and a new
`resource.ValidateComponentImage` (letters, digits and `._/:@-`, at most
512, later with `+[]`) refuses a newline or a space in an image. The reconciler uses both,
so a hand-made Component with such an image is now `InvalidSpec` rather
than a platform failure. The API applying them on create is T005/T006.
The built-ins (`web-server`, `service-from-source`, `web-and-api`, each
version `1.0`) have fixed IDs `0190d1c0-0027-7000-8000-0000000000NN`
(templates 01 to 03, versions 101 to 103), and `BuiltIns` returns fresh
copies. Each passes `ValidatePublish` and renders with only its required
parameters. The hostile-value tests cover `${x}`, `$${`, a `$param`
object as text, YAML, JSON objects as strings, a quote breakout and
newlines: all land literally in the field they fill, and as an image
they're refused naming `components[web]`. contract-guardian: PASS. Its
findings fixed here: the image set also takes `+` and a bracketed IPv6
registry host (`resource/app_test.go`); a required parameter whose
stand-in breaks its own schema (say `maxLength: 3`, no example) is
refused at publish asking for `examples`; and a placeholder error stops
that Component's other checks, so one mistake gives one error. By
design: the ports' `InvalidSpec` message now has the same
`resource: validation failed:` prefix as the source errors. The API not
yet checking images at write is RP-109 (T005/T006).

## T004: One transaction across kinds (store)

- **Do:**
  - Extract `createTx` from `sqlite.Store.Create`.
  - Add `Atomically` and `CreateIn`, with events published after commit
    in order (decision 8).
  - Add the `store.Atomic` and `store.TxCreator` interfaces.
  - Add a benchmark of 20 Components plus 20 Deployments in one
    transaction (RP-108).
- **Files:** `core/store/store.go`, `core/store/sqlite/store.go`,
  `core/store/sqlite/tx.go`, `core/store/sqlite/tx_test.go`.
- **Verify:** `go test ./core/store/...`:
  - a batch with a failing third create leaves no row, edge, audit
    record or event;
  - a duplicate unique key rolls back all of it;
  - a committed batch publishes one event per resource after commit;
  - existing `Create` tests unchanged.

  Record the benchmark's time.

## T005: Reusable admission (api)

- **Do:**
  - Extract `checkDependencies`, `coherent`, the secret-variable checks
    and the source check so they take a `workloadView` (decision 7).
  - The handlers pass their stores; add an overlay type that answers
    from a pending batch first.
  - Guard `createdFrom` (refused on client create, kept or refused on
    update, decision 9) in the Application and Component handlers.
- **Files:** `api/topology_handler.go`, `api/deployment_handler.go`,
  `api/secretenv_check.go`, `api/build_handler.go` (source check),
  `api/workload_view.go`, tests.
- **Verify:** `go test ./api/...` passes with **no existing test
  changed**. Add overlay unit tests (a dependency on a pending
  Component, ancestry of a pending Application) and `createdFrom` guard
  tests.

## T006: Template and version routes (api)

- **Do:**
  - Add `/v1/templates` (generic, with Ready status on write), the
    built-ins merged into list and get, and 400 on changing a built-in.
  - Add the versions routes as a `nestedPaths` kind: publish validated
    by T002 and T003, no update, and delete with an Approval.
  - Add every rule to `routeTable()`, and the two action namespaces.
- **Files:** `api/template_handler.go`, `api/authz.go`, `api/server.go`,
  tests.
- **Verify:** `go test ./api/...`:
  - the route-table and `deleteactions_test` pass;
  - a Project template is invisible to a principal with no role there;
  - a `/` template needs write at `/`;
  - built-ins: list and get for a role-less signed-in principal, 400 on
    change;
  - a version can't be changed, and a newer one is "latest";
  - deleting a Template cascades with one Approval.

## T007: Preview and create (api)

- **Do:**
  - Add `POST /v1/templates/{id}/preview` and `/create` (decision 4):
    - authorize the template, then check the parameters (every error);
    - render, assign IDs, and admit through the overlay (T005);
    - check required packages;
    - write in one transaction (T004), setting `createdFrom`.
  - Add the problem `errors` extension.
- **Files:** `api/template_create.go`, `api/problem.go`, tests.
- **Verify:** `go test ./api/...` with:
  - criterion 3: an auth failure on the last Component, and an injected
    store failure, each leaving nothing;
  - criterion 4: preview equals create, minus IDs, and returns all
    errors at once;
  - criterion 7: the three 403s audited, then success with roles;
  - criterion 9: `createdFrom` and the audit rows share the request ID,
    with no Secret value;
  - a missing builds package gives 409;
  - adding to an existing Application.

## T008: CLI and MCP [P]

- **Do:** add the catalogue operations (decision 12) and their help text.
- **Files:** `api/catalog/operations.go`, `cmd/serendip/*_test.go`,
  `mcp/server_test.go`.
- **Verify:** `go test ./api/... ./cmd/serendip/... ./mcp/...`, with
  `catalog_parity_test` covering each new route.
  `serendip templates list` and `serendip template create-from --id …
  --file req.json` run against a local `serendipd`.

## T009: Console

- **Do:** build decision 12's console screens:
  - the gallery, the template page with its versions, and the
    create-from-template form (`SchemaForm` plus a Secret picker);
  - the preview list and its error display;
  - provenance on the Application page;
  - the empty-state link from Applications.
- **Files:** `ui/src/screens/Templates.tsx`, `ui/src/lib/template.ts`,
  `ui/src/api/types.ts`, `ui/src/api/client.ts`,
  `ui/src/screens/Applications.tsx`, `ui/src/lib/router.tsx`, tests.
- **Verify:** `npm test` and `npm run build` in `ui/`. Then, against a
  local `serendipd`, create from each built-in through the browser;
  preview shows errors per field.

## T010: Acceptance tests (api) [A]

- **Do:** add `spec027_acceptance_test.go` for criteria 3–11 and 13 end
  to end through the HTTP server with SQLite.
- **Files:** `api/spec027_acceptance_test.go`.
- **Verify:** `go test ./api/ -run Spec027` passes; `go vet ./...`.

## T011: Live check on kind

- **Do:** on a kind cluster with the platform and the builds package:
  - criterion 1: each built-in with its required parameters only serves
    traffic (and without builds: 409, nothing created);
  - criterion 2: from the `web` pod, `wget $API_URL` answers;
  - criterion 7 with a Contributor scoped to another Project;
  - criterion 11: edit, deploy, promote and delete a templated Component;
  - criterion 10: delete the template, and the Application's provenance
    stays;
  - criterion 12 through the console, CLI and MCP.

  Write the results into `verification.md`.
- **Files:** `specs/027-templates-and-scaffolder/verification.md`.
- **Verify:** every criterion above has a recorded result in
  `verification.md`.

## T012: Live check on the L480

- **Do:**
  - On the L480 (reinstalled for daily use), create `web-and-api` from
    the console and reach `web` from the desktop.
  - Measure `serendipd` idle RSS (criterion 14) against benchmark run
    1's 28.2 MiB and run 2's number.
  - Run T004's transaction benchmark on the node.
- **Files:** `verification.md`.
- **Verify:** criterion 14 holds (within 2 MiB). The RP-108 time is
  recorded; if it's over 50 ms, lower the Component limit and say so.

## T013: Docs [A] [P]

- **Do:**
  - Write a new `docs/architecture/templates.md`.
  - Update:
    - `resource-model.md` (the kinds, `createdFrom`, the owns edges);
    - `authorization.md` (the actions, built-in visibility, the
      combined check);
    - `cli-and-mcp.md`;
    - `application-platform.md` (a link);
    - `docs/build-vs-adopt.md` (the plan's section);
    - `docs/risk-register.md` (RP-104–108).
- **Files:** those docs.
- **Verify:** every new route and action named in the docs exists in
  `routeTable()` (grep), and the RP numbers are unique.

## T014: Close the spec

- **Do:**
  - Write `verification.md`'s summary.
  - Update `docs/PROGRESS.md`.
  - Rewrite epic #68's body (DONE, what got built, the checklist).
  - Open the `spec-027` PR (`Closes #68`) as Yasindu007.
- **Files:** `verification.md`, `docs/PROGRESS.md`.
- **Verify:** every task issue is closed and every acceptance criterion
  has a result in `verification.md` before the PR is opened.
