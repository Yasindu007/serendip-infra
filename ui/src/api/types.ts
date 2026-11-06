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

/** One call to another Component's port; env receives its address (spec 020). */
export interface ComponentDependency {
  component: string;
  port: string;
  env: string;
}

/** Where a Component's image is built from (spec 022). */
export interface ComponentSource {
  repository: string;
  /** Defaults to main. */
  branch?: string;
  /** The build context in the repository; defaults to ".". */
  path?: string;
  /** Relative to path; defaults to Dockerfile. */
  dockerfile?: string;
  /** The ID of a Secret holding the repository's credential. */
  credentialSecret?: string;
  autoBuild?: boolean;
}

export interface ComponentSpec {
  /** Empty when the Component is built from source. */
  image: string;
  source?: ComponentSource;
  ports?: ComponentPort[];
  env?: Record<string, string>;
  dependencies?: ComponentDependency[];
}

export interface ComponentEndpoint {
  name: string;
  url: string;
}

export interface ComponentStatus extends BaseStatus {
  endpoints?: ComponentEndpoint[];
  /** The built image a source Component runs, once a build has succeeded. */
  image?: string;
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

// Environments and deployments (spec 020): a Project is the subscription
// and an Environment the resource group a Component is deployed into.

export interface PromotionPath {
  from: string;
  to: string[];
}

export interface Pipeline {
  paths?: PromotionPath[];
}

export type Project = Resource<{ pipeline?: Pipeline }, BaseStatus>;

export type AvailabilityLevel = "single-instance" | "ha";
export type Environment = Resource<{ capabilityOverrides?: Record<string, string> }, BaseStatus & { availability?: AvailabilityLevel }>;

/** What differs in one Environment for the same release. */
export interface DeploymentOverrides {
  env?: Record<string, string>;
  config?: Record<string, unknown>;
  traits?: Record<string, Record<string, unknown>>;
}

export interface DeploymentSpec {
  component: string;
  /** Empty means the Component's latest release. */
  release?: string;
  overrides?: DeploymentOverrides;
}

export interface DeploymentStatus extends BaseStatus {
  release?: string;
  endpoints?: ComponentEndpoint[];
  /** Releases it ran before, newest first: what it may roll back to. */
  history?: string[];
}

export type Deployment = Resource<DeploymentSpec, DeploymentStatus>;

/** GET /v1/deployments/{id}/runtime */
export interface RuntimeObject {
  kind: string;
  name: string;
  /** Healthy, Progressing, Degraded, Suspended or Unknown. */
  health: string;
  message?: string;
}

/** GET /v1/deployments/{id}/events */
export interface RuntimeEvent {
  time: string;
  type: string;
  reason: string;
  message: string;
  objectKind: string;
  objectName: string;
  count?: number;
}

/** GET /v1/deployments/{id}/logs, and each line of ?follow=true. */
export interface LogLine {
  time: string;
  instance: string;
  line: string;
}

/** How a followed log stream ended on its own. */
export type LogEnd = "idle" | "maxDuration" | "ended" | "error";

/** A JSON Schema, as the platform publishes it. Only what the console reads is typed. */
export interface JSONSchema {
  $ref?: string;
  $defs?: Record<string, JSONSchema>;
  definitions?: Record<string, JSONSchema>;
  type?: string | string[];
  title?: string;
  description?: string;
  properties?: Record<string, JSONSchema>;
  required?: string[];
  enum?: unknown[];
  default?: unknown;
  minimum?: number;
  maximum?: number;
  minLength?: number;
  maxLength?: number;
  pattern?: string;
  items?: JSONSchema;
}

/** GET /v1/components/{id}/schemas */
export interface ConfigSchemas {
  environment?: JSONSchema;
  traits?: Record<string, JSONSchema>;
}

/** GET /v1/components/{id}/dependencies */
export interface ComponentLink {
  from: string;
  to: string;
  port: string;
  env: string;
}

export interface DependenciesView {
  calls: ComponentLink[];
  calledBy: ComponentLink[];
}

/** GET /v1/applications/{id}/topology: declared, not live traffic. */
export interface TopologyView {
  components: Array<{ id: string; name: string; ports: ComponentPort[] }>;
  dependencies: ComponentLink[];
}

/** GET /v1/platform */
export interface PlatformComponent {
  name: string;
  description?: string;
  parameters?: JSONSchema;
  environment?: JSONSchema;
  allowedTraits?: string[];
}

export interface PlatformView {
  runtimes: Array<{ name: string; ready: boolean; message?: string }>;
  componentTypes: PlatformComponent[];
  traits: PlatformComponent[];
}

/** GET /v1/packages */
export interface Package {
  provider: string;
  capability: string;
  description: string;
  available: boolean;
  /** Its measured cost; null until it's measured (spec 022). */
  requirements?: Requirements | null;
  /** What removing it loses, said before the request. */
  removal?: string;
  installable?: boolean;
  notInstallable?: string;
}

/** A package's measured cost on the reference machine (core/provider.Requirements). */
export interface Requirements {
  idleMemoryMiB: number;
  idleCPUMillicores: number;
  peakMemoryMiB: number;
  peakCPUMillicores: number;
  measuredOn: string;
  results: string;
  note?: string;
}

export type BuildPhase = "Queued" | "Running" | "Succeeded" | "Failed" | "Cancelled";

export interface BuildStep {
  name: string;
  phase: BuildPhase;
  startedAt?: string;
  finishedAt?: string;
  message?: string;
}

/** GET /v1/components/{id}/builds and GET /v1/builds/{id} (spec 022). */
export type Build = Resource<
  { commit?: string; cancel?: boolean },
  BaseStatus & {
    phase?: BuildPhase;
    commit?: string;
    image?: string;
    startedAt?: string;
    finishedAt?: string;
    steps?: BuildStep[];
    source?: ComponentSource;
    started?: boolean;
  }
>;

export type ProviderInstance = Resource<{ capabilityId?: string; providerId?: string; environmentId?: string }, BaseStatus>;
