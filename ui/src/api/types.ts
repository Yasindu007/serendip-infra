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
