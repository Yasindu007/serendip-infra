// Wire types for the parts of the Serendip API the console uses. They
// mirror core/resource (Go) exactly; the console never invents fields.

export type ConditionStatus = "True" | "False" | "Unknown";

export interface Condition {
  type: string;
  status: ConditionStatus;
  reason?: string;
  message?: string;
  lastTransitionTime: string;
}

export interface OwnerRef {
  kind: string;
  id: string;
}

export interface Relationship {
  type: string;
  targetKind: string;
  targetId: string;
}

export interface Metadata {
  id: string;
  name: string;
  kind: string;
  labels?: Record<string, string>;
  ownerRefs?: OwnerRef[];
  relationships?: Relationship[];
  createdAt: string;
  updatedAt: string;
  resourceVersion: number;
}

export interface Resource<Spec, Status> {
  metadata: Metadata;
  spec: Spec;
  status: Status;
}

export interface BaseStatus {
  conditions?: Condition[];
}

export type ApplicationSpec = Record<string, never>;
export type Application = Resource<ApplicationSpec, BaseStatus>;

export interface ComponentPort {
  name: string;
  port: number;
  expose?: boolean;
}

export interface ComponentSpec {
  image: string;
  ports?: ComponentPort[];
  env?: Record<string, string>;
}

export interface ComponentEndpoint {
  name: string;
  url: string;
}

export interface ComponentStatus extends BaseStatus {
  endpoints?: ComponentEndpoint[];
}

export type Component = Resource<ComponentSpec, ComponentStatus>;

export type NodeResource = Resource<Record<string, unknown>, BaseStatus>;

/** GET /v1/readiness: capability name to its readiness. */
export interface CapabilityReadiness {
  ready: boolean;
  reason: string;
  message?: string;
  since: string;
}
export type Readiness = Record<string, CapabilityReadiness>;

/** GET /v1/session */
export interface SessionInfo {
  actor: string;
  method: "session" | "bearer";
  subject?: string;
  expiresAt?: string;
  idleExpiresAt?: string;
}

/** RFC 7807 problem+json, as api/problem.go writes it. */
export interface Problem {
  title: string;
  status: number;
  detail?: string;
  instance?: string;
}

// Authorization (spec 017): Azure-style roles at scopes.

/** "/" (the zero value: no kind, no id) or one resource. */
export interface Scope {
  kind?: string;
  id?: string;
}

export interface Permission {
  actions?: string[];
  notActions?: string[];
  dataActions?: string[];
  notDataActions?: string[];
}

export interface RoleDefinitionSpec {
  roleName: string;
  description?: string;
  permissions: Permission[];
  assignableScopes?: Scope[];
}

/**
 * GET /v1/authorization/roleDefinitions: built-ins are marked, and the
 * server says which roles an agent may hold (spec 018).
 */
export type RoleDefinition = Resource<RoleDefinitionSpec, BaseStatus> & { builtIn: boolean; assignableToAgents?: boolean };

export type PrincipalType = "person" | "group" | "apiKey" | "agent";

export interface PrincipalRef {
  type: PrincipalType;
  id: string;
  /** The person accountable for an agent; required for, and only on, an agent. */
  sponsor?: PrincipalRef;
}

export interface RoleAssignmentSpec {
  principal: PrincipalRef;
  roleDefinitionId: string;
  scope: Scope;
  description?: string;
}

/** Listed at a scope, an assignment made higher up is marked inherited. */
export type RoleAssignment = Resource<RoleAssignmentSpec, BaseStatus> & { inherited?: boolean };

/** GET /v1/authorization/permissions?scope= */
export interface PermissionsView {
  scope: string;
  permissions: Permission[];
}

/** GET /v1/authorization/principals */
export interface KnownPrincipal {
  type: PrincipalType;
  id: string;
  displayName?: string;
  groups?: string[];
  firstSeen: string;
  lastSeen: string;
}

/** Where a directory answer came from: the identity provider now, or sign-ins. */
export type DirectorySource = "live" | "signedIn";

/** One person or group that can be granted a role. */
export interface DirectoryItem {
  type: PrincipalType;
  id: string;
  displayName?: string;
}

/** GET /v1/authorization/directory?type=&q= */
export interface DirectoryView {
  source: DirectorySource;
  truncated: boolean;
  items: DirectoryItem[];
}

/** GET /v1/authorization/principals/person/{id}/groups */
export interface GroupsView {
  source: DirectorySource;
  groups: string[];
}

// Approvals (spec 019): the record of every destructive action.

export type ApprovalPhase = "Pending" | "Approved" | "Rejected" | "Expired" | "Executed" | "Failed";

export interface ApprovalAction {
  verb: "delete";
  target: Scope;
  cascade?: boolean;
}

export interface ApprovalSpec {
  action: ApprovalAction;
  reason?: string;
}

/** Who asked or decided: the audit actor, and the principal behind it when there is one. */
export interface ApprovalActor {
  actor: string;
  principal?: PrincipalRef;
  subject?: string;
  sponsor?: string;
}

/** One resource the action removes. Its last spec never leaves the server. */
export interface ApprovalRef {
  kind: string;
  id: string;
  name?: string;
  cleanedUp?: boolean;
}

export interface ApprovalDecision {
  byPolicy?: boolean;
  by?: ApprovalActor;
  at: string;
  comment?: string;
  evidence?: string;
}

export interface ApprovalStatus extends BaseStatus {
  phase: ApprovalPhase;
  requestedBy: ApprovalActor;
  requestedAt: string;
  expiresAt?: string;
  requestDecision?: string;
  targetName?: string;
  targetVersion?: number;
  ownerChain?: Scope[];
  blastRadius?: ApprovalRef[];
  decision?: ApprovalDecision;
  executedAt?: string;
  settled?: boolean;
}

export type Approval = Resource<ApprovalSpec, ApprovalStatus>;

/** GET /v1/graph/{kind}/{id}/children: core/graph.ResourceRef has no JSON tags. */
export interface GraphRef {
  Kind: string;
  ID: string;
}

export type SecretSpec = { path: string };
export type Secret = Resource<SecretSpec, BaseStatus>;
