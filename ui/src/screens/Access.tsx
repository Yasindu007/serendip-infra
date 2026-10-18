import { useState, type FormEvent } from "react";
import { apiFetch, apiList, ApiError, isShown, NetworkError } from "../api/client";
import type { KnownPrincipal, PrincipalType, RoleAssignment, RoleDefinition, Scope } from "../api/types";
import { ApiEquivalent } from "../components/ApiEquivalent";
import { SelectField, TextField } from "../components/Field";
import { RequestError } from "../components/RequestError";
import { PageHeader } from "../components/Shell";
import { EmptyState, LoadingState, Notice } from "../components/States";
import { usePermissions } from "../hooks/usePermissions";
import { usePoll, useNow } from "../hooks/usePoll";
import { assignRoles, removeRoles, scopeParam } from "../lib/permissions";
import { formatAge } from "../lib/time";
import { FormFailure } from "./Applications";
import { UpdatedLine } from "./common";

const principalTypes: Array<{ value: PrincipalType; label: string }> = [
  { value: "person", label: "Person" },
  { value: "group", label: "Group" },
  { value: "apiKey", label: "API key" },
];

const typeLabel = (t: string) => principalTypes.find((p) => p.value === t)?.label ?? t;

/** "the whole platform" or "Project 01a1…": where an assignment applies. */
export function describeScope(s: Scope): string {
  return s.kind && s.id ? `${s.kind} ${s.id}` : "the whole platform";
}

/**
 * AccessControl is Azure's IAM blade for one scope (spec 017 plan.md
 * decision 11): who has which role here, including what's inherited from
 * above and where from; adding an assignment when the caller may; and
 * checking what a principal can do here.
 */
export function AccessControl({ scope, name, crumbs }: { scope: Scope; name?: string; crumbs?: Array<{ label: string; to: string }> }) {
  const now = useNow();
  const param = scopeParam(scope);
  const assignments = usePoll(
    (s) => apiList<RoleAssignment>(`/v1/authorization/roleAssignments?scope=${encodeURIComponent(param)}`, s),
    15_000,
    `assignments-${param}`,
  );
  const roles = usePoll((s) => apiList<RoleDefinition>("/v1/authorization/roleDefinitions", s), 60_000, "role-definitions");
  const access = usePermissions(scope);
  const canAssign = access.can(assignRoles);
  // The directory is only for callers who can assign somewhere; others get
  // 403 and see subjects instead of names.
  const principals = usePoll((s) => apiList<KnownPrincipal>("/v1/authorization/principals", s), 60_000, "principals");

  const roleName = (id: string) => roles.data?.find((r) => r.metadata.id === id)?.spec.roleName ?? id;
  const who = (type: string, id: string) => principals.data?.find((p) => p.type === type && p.id === id)?.displayName;

  const canRemove = access.can(removeRoles);
  const [removing, setRemoving] = useState<RoleAssignment>();

  return (
    <>
      <PageHeader {...(crumbs ? { crumbs } : {})} title={`Access control (IAM)${name ? `: ${name}` : ""}`} meta={<UpdatedLine at={assignments.updatedAt} now={now} />} />
      <p>
        Roles assigned here apply to {name && scope.kind ? `${scope.kind} ${name}` : describeScope(scope)} and everything it owns. Roles assigned higher up apply here too, and a role
        assigned lower down never takes access away.
      </p>

      <section className="section" aria-labelledby="ac-check">
        <h2 id="ac-check" className="section-title">
          Check access
        </h2>
        <CheckAccess assignments={assignments.data ?? []} roles={roles.data ?? []} principals={principals.data ?? []} />
      </section>

      <section className="section" aria-labelledby="ac-assignments">
        <h2 id="ac-assignments" className="section-title">
          Role assignments
        </h2>
        {assignments.error ? (
          <RequestError error={assignments.error} what="role assignments" />
        ) : !assignments.data ? (
          <LoadingState what="role assignments" />
        ) : assignments.data.length === 0 ? (
          <EmptyState title="No role assignments apply here">Only Owners at the whole platform can act here until someone is assigned a role.</EmptyState>
        ) : (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th scope="col">Principal</th>
                  <th scope="col">Type</th>
                  <th scope="col">Role</th>
                  <th scope="col">Assigned at</th>
                  <th scope="col">Description</th>
                  <th scope="col">
                    <span className="visually-hidden">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {assignments.data.map((a) => {
                  const p = a.spec.principal;
                  const display = who(p.type, p.id);
                  return (
                    <tr key={a.metadata.id}>
                      <td>
                        {display ?? <span className="mono">{p.id}</span>}
                        {display && (
                          <>
                            {" "}
                            <span className="mono field-hint">{p.id}</span>
                          </>
                        )}
                      </td>
                      <td>{typeLabel(p.type)}</td>
                      <td>{roleName(a.spec.roleDefinitionId)}</td>
                      <td>{a.inherited ? `Inherited from ${describeScope(a.spec.scope)}` : "This scope"}</td>
                      <td>{a.spec.description ?? ""}</td>
                      <td>
                        {/* As in Azure, an inherited assignment is removed where it was made, not here. */}
                        {!a.inherited && canRemove && (
                          <button type="button" className="button" onClick={() => setRemoving(a)} aria-label={`Remove ${roleName(a.spec.roleDefinitionId)} from ${display ?? p.id}`}>
                            Remove
                          </button>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
        {removing && (
          <RemoveAssignment
            assignment={removing}
            label={`${roleName(removing.spec.roleDefinitionId)} for ${who(removing.spec.principal.type, removing.spec.principal.id) ?? removing.spec.principal.id}`}
            onDone={() => {
              setRemoving(undefined);
              assignments.refresh();
            }}
            onCancel={() => setRemoving(undefined)}
          />
        )}

        <h3 className="section-title">Add a role assignment</h3>
        {canAssign ? (
          <AddAssignment scope={scope} roles={roles.data ?? []} principals={principals.data ?? []} onAdded={assignments.refresh} />
        ) : (
          <p>{canAssign === undefined ? "Checking whether you can assign roles here…" : "Your roles here don't include assigning roles."}</p>
        )}
      </section>

      <section className="section" aria-labelledby="ac-roles">
        <h2 id="ac-roles" className="section-title">
          Roles
        </h2>
        <RoleList roles={roles} />
      </section>
    </>
  );
}

/**
 * RemoveAssignment confirms before revoking access, and shows the request.
 * The server refuses removing the last Owner of the whole platform (409),
 * and says why.
 */
function RemoveAssignment({ assignment, label, onDone, onCancel }: { assignment: RoleAssignment; label: string; onDone: () => void; onCancel: () => void }) {
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<ApiError | NetworkError>();
  const path = `/v1/authorization/roleAssignments/${encodeURIComponent(assignment.metadata.id)}`;

  async function remove() {
    setBusy(true);
    setFailure(undefined);
    try {
      await apiFetch<void>("DELETE", path);
      onDone();
    } catch (err) {
      if (isShown(err)) setFailure(err);
      setBusy(false);
    }
  }

  return (
    <div className="panel" role="group" aria-label="Confirm removing a role assignment">
      <p>
        Remove {label} at this scope? They lose what this role gives here and below, unless another role grants it. You can assign it again
        later.
      </p>
      {failure &&
        (failure instanceof ApiError && failure.status === 409 ? (
          <Notice tone="danger">
            <p>{failure.detail ?? failure.title}</p>
          </Notice>
        ) : (
          <FormFailure error={failure} />
        ))}
      <div className="form-actions">
        <button type="button" className="button button--primary" onClick={remove} disabled={busy}>
          {busy ? "Removing…" : "Remove assignment"}
        </button>
        <button type="button" className="button" onClick={onCancel} disabled={busy}>
          Cancel
        </button>
      </div>
      <ApiEquivalent request={{ method: "DELETE", path }} />
    </div>
  );
}

/** RoleList is Azure's Roles view: what each role allows. */
function RoleList({ roles }: { roles: { data: RoleDefinition[] | undefined; error: ApiError | NetworkError | undefined } }) {
  if (roles.error) return <RequestError error={roles.error} what="roles" />;
  if (!roles.data) return <LoadingState what="roles" />;
  return (
    <div className="table-wrap">
      <table className="table">
        <thead>
          <tr>
            <th scope="col">Role</th>
            <th scope="col">Type</th>
            <th scope="col">Description</th>
          </tr>
        </thead>
        <tbody>
          {roles.data.map((r) => (
            <tr key={r.metadata.id}>
              <td>{r.spec.roleName}</td>
              {/* A non-breaking hyphen: "Built-in" must not wrap at the hyphen. */}
              <td>{r.builtIn ? "Built‑in" : "Custom"}</td>
              <td>{r.spec.description ?? ""}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/**
 * resolvePrincipal reads what was typed into a principal field: a known
 * subject, or the name of exactly one known principal, which resolves to
 * its subject. Anything else is taken as typed.
 */
export function resolvePrincipal(principals: KnownPrincipal[], type: PrincipalType, text: string): { id: string; name?: string } {
  const t = text.trim();
  const ofType = principals.filter((p) => p.type === type);
  const byId = ofType.find((p) => p.id === t);
  if (byId) return byId.displayName ? { id: t, name: byId.displayName } : { id: t };
  const byName = ofType.filter((p) => p.displayName === t);
  if (byName.length === 1) return { id: byName[0]!.id, name: t };
  return { id: t };
}

/**
 * PrincipalSuggestions offers known principals by name, with the subject
 * beside it. A name shared by two principals is offered by subject, so
 * picking it can't be ambiguous.
 */
function PrincipalSuggestions({ id, principals, type }: { id: string; principals: KnownPrincipal[]; type: PrincipalType }) {
  const ofType = principals.filter((p) => p.type === type);
  const shared = (n: string | undefined) => n !== undefined && ofType.filter((p) => p.displayName === n).length > 1;
  return (
    <datalist id={id}>
      {ofType.map((p) =>
        p.displayName && !shared(p.displayName) ? (
          <option key={p.id} value={p.displayName} label={p.id} />
        ) : (
          <option key={p.id} value={p.id} label={p.displayName ?? ""} />
        ),
      )}
    </datalist>
  );
}

/** The subject a typed name resolves to, shown under the field. */
function ResolvedSubject({ text, resolved }: { text: string; resolved: { id: string; name?: string } }) {
  if (!resolved.name || resolved.id === text.trim()) return null;
  return (
    <p className="field-hint">
      Subject <span className="mono">{resolved.id}</span>
    </p>
  );
}

function AddAssignment({ scope, roles, principals, onAdded }: { scope: Scope; roles: RoleDefinition[]; principals: KnownPrincipal[]; onAdded: () => void }) {
  const [type, setType] = useState<PrincipalType>("person");
  const [id, setId] = useState("");
  const [roleId, setRoleId] = useState("");
  const [description, setDescription] = useState("");
  const [touched, setTouched] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<ApiError | NetworkError>();
  const [added, setAdded] = useState<string>();

  const role = roleId || roles[0]?.metadata.id || "";
  const idError = touched && id.trim() === "" ? "Enter who gets the role." : undefined;
  const resolved = resolvePrincipal(principals, type, id);
  const body = {
    metadata: {},
    spec: { principal: { type, id: resolved.id }, roleDefinitionId: role, scope, ...(description ? { description } : {}) },
  };

  async function submit(e: FormEvent) {
    e.preventDefault();
    setTouched(true);
    if (id.trim() === "" || role === "") return;
    setBusy(true);
    setFailure(undefined);
    setAdded(undefined);
    try {
      await apiFetch<RoleAssignment>("POST", "/v1/authorization/roleAssignments", body);
      setAdded(`${roles.find((r) => r.metadata.id === role)?.spec.roleName ?? "The role"} assigned to ${resolved.name ?? resolved.id}.`);
      setId("");
      setDescription("");
      setTouched(false);
      onAdded();
    } catch (err) {
      if (isShown(err)) setFailure(err);
    }
    setBusy(false);
  }

  return (
    <form className="form" onSubmit={submit} noValidate aria-label="Add a role assignment">
      <SelectField id="ra-type" label="Principal type" value={type} onChange={(e) => setType(e.target.value as PrincipalType)} options={principalTypes} />
      <TextField
        id="ra-principal"
        label={type === "person" ? "Person" : type === "group" ? "Group name" : "API key name"}
        hint={
          type === "person"
            ? "The name of someone who has signed in, or their subject at the identity provider."
            : type === "group"
              ? "The group's name as the identity provider sends it."
              : "The key's name in SERENDIP_API_KEYS."
        }
        list="ra-suggestions"
        value={id}
        onChange={(e) => setId(e.target.value)}
        onBlur={() => setTouched(true)}
        {...(idError ? { error: idError } : {})}
        required
      />
      <ResolvedSubject text={id} resolved={resolved} />
      <PrincipalSuggestions id="ra-suggestions" principals={principals} type={type} />
      <SelectField
        id="ra-role"
        label="Role"
        value={role}
        onChange={(e) => setRoleId(e.target.value)}
        options={roles.map((r) => ({ value: r.metadata.id, label: r.builtIn ? r.spec.roleName : `${r.spec.roleName} (custom)` }))}
      />
      <TextField id="ra-description" label="Description" hint="Why they need it. Recorded with the assignment." value={description} onChange={(e) => setDescription(e.target.value)} />
      {failure && <FormFailure error={failure} />}
      {added && (
        <p role="status" className="field-hint">
          {added}
        </p>
      )}
      <div className="form-actions">
        <button className="button button--primary" type="submit" disabled={busy || roles.length === 0}>
          {busy ? "Assigning…" : "Assign role"}
        </button>
      </div>
      <ApiEquivalent request={{ method: "POST", path: "/v1/authorization/roleAssignments", body }} />
    </form>
  );
}

/**
 * CheckAccess shows what one principal can do here: the assignments that
 * apply to it, directly or, for a person, through the groups they had at
 * their last sign-in, and what each role allows.
 */
function CheckAccess({ assignments, roles, principals }: { assignments: RoleAssignment[]; roles: RoleDefinition[]; principals: KnownPrincipal[] }) {
  const [type, setType] = useState<PrincipalType>("person");
  const [id, setId] = useState("");
  const [checked, setChecked] = useState<{ type: PrincipalType; id: string; name?: string }>();
  const resolved = resolvePrincipal(principals, type, id);
  const who = checked ? (checked.name ?? checked.id) : "";

  const now = useNow();
  const known = checked?.type === "person" ? principals.find((p) => p.type === "person" && p.id === checked.id) : undefined;
  const groups = known?.groups ?? [];
  const applying = checked
    ? assignments.filter((a) => {
        const p = a.spec.principal;
        return (p.type === checked.type && p.id === checked.id) || (p.type === "group" && groups.includes(p.id));
      })
    : [];

  return (
    <>
      <form
        className="form"
        aria-label="Check access"
        onSubmit={(e) => {
          e.preventDefault();
          if (id.trim()) setChecked({ type, ...resolved });
        }}
      >
        <SelectField id="ca-type" label="Principal type" value={type} onChange={(e) => setType(e.target.value as PrincipalType)} options={principalTypes} />
        <TextField id="ca-principal" label="Principal" hint="A name or a subject." list="ca-suggestions" value={id} onChange={(e) => setId(e.target.value)} />
        <ResolvedSubject text={id} resolved={resolved} />
        <PrincipalSuggestions id="ca-suggestions" principals={principals} type={type} />
        <div className="form-actions">
          <button className="button" type="submit">
            Check access
          </button>
        </div>
      </form>
      {checked?.type === "person" && (
        <p className="field-hint" role="status">
          {known ? (
            <>
              Groups come from {who}'s sign-ins. At their last sign-in (<time dateTime={known.lastSeen}>{formatAge(known.lastSeen, now)}</time>) they were
              in {groups.length ? groups.join(", ") : "no groups"}. A change to their groups shows here after they next sign in.
            </>
          ) : (
            <>{who} hasn't signed in to Serendip yet, so their groups aren't known: only roles assigned to them directly are shown.</>
          )}
        </p>
      )}
      {checked &&
        (applying.length === 0 ? (
          <EmptyState title="No access here">
            No role assignment applies to {who} at this scope{checked.type === "person" ? ", directly or through a group they're known to be in" : ""}.
          </EmptyState>
        ) : (
          <>
            <p>They can do anything any of these roles allows here.</p>
            <div className="table-wrap">
              <table className="table">
                <thead>
                  <tr>
                    <th scope="col">Role</th>
                    <th scope="col">Through</th>
                    <th scope="col">Assigned at</th>
                    <th scope="col">Actions</th>
                    <th scope="col">Data actions</th>
                  </tr>
                </thead>
                <tbody>
                  {applying.map((a) => {
                    const role = roles.find((r) => r.metadata.id === a.spec.roleDefinitionId);
                    const perms = role?.spec.permissions ?? [];
                    const list = (pick: (p: (typeof perms)[number]) => [string[] | undefined, string[] | undefined]) =>
                      perms
                        .flatMap((p) => {
                          const [yes, no] = pick(p);
                          return [...(yes ?? []), ...(no ?? []).map((n) => `not ${n}`)];
                        })
                        .join(", ") || "None";
                    return (
                      <tr key={a.metadata.id}>
                        <td>{role?.spec.roleName ?? a.spec.roleDefinitionId}</td>
                        <td>{a.spec.principal.type === "group" && checked.type !== "group" ? `Group ${a.spec.principal.id}` : "Direct"}</td>
                        <td>{a.inherited ? `Inherited from ${describeScope(a.spec.scope)}` : "This scope"}</td>
                        <td className="mono">{list((p) => [p.actions, p.notActions])}</td>
                        <td className="mono">{list((p) => [p.dataActions, p.notDataActions])}</td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </>
        ))}
    </>
  );
}
