// Wire types for the Alror platform API (v1).
//
// These mirror the Go structs in the CLI (`internal/domain`, `internal/api`,
// `internal/config`) byte for byte: snake_case keys, RFC 3339 timestamps as
// strings, and durations as integer **nanoseconds** (see `Step.bake`).

/** Lifecycle state of a deployment. Terminal states: promoted, rolled_back, failed. */
export type Status = 'pending' | 'rolling' | 'promoted' | 'rolled_back' | 'failed';

/** Risk bucket. */
export type Level = 'low' | 'medium' | 'high';

/** RFC 3339 timestamp, e.g. "2026-10-02T09:30:00Z". */
export type Timestamp = string;

/** One signal that contributed to a risk score. */
export interface Factor {
  name: string;
  detail: string;
  points: number;
}

/** Output of the risk scorer for one change. */
export interface RiskReport {
  /** 0..100 */
  score: number;
  level: Level;
  factors: Factor[] | null;
  services: string[] | null;
  ai_authored: boolean;
}

/** One stage of a progressive rollout. */
export interface Step {
  /** Percent of traffic on the canary. */
  weight: number;
  /**
   * How long to observe before deciding, in **nanoseconds** (Go `time.Duration`).
   * Use {@link bakeMs} to convert to milliseconds.
   */
  bake: number;
}

/** Rollout plan derived from a risk report. */
export interface Plan {
  /** "canary" | "blue-green" */
  strategy: 'canary' | 'blue-green' | (string & {});
  steps: Step[] | null;
}

/** Verdict for one metric at one step. */
export interface MetricResult {
  metric: string;
  canary: number;
  baseline: number;
  /** Relative change, canary vs baseline. */
  delta: number;
  p_value: number;
  pass: boolean;
  reason?: string;
}

/** Decision taken after a bake period. */
export interface Verdict {
  pass: boolean;
  results: MetricResult[] | null;
  summary: string;
}

/** The persisted record of one release. */
export interface Deployment {
  /** `dep_<UTCyyyymmddTHHMMSS>_<6hex>` */
  id: string;
  service: string;
  image: string;
  /** PR number or commit. */
  ref?: string;
  risk: RiskReport;
  plan: Plan;
  status: Status;
  step_index: number;
  weight: number;
  reason?: string;
  /** Kept from the first insert; the server ignores later values. */
  created_at: Timestamp;
  /** Set by the server on every write; your value is ignored. */
  updated_at: Timestamp;
  /** Environment name. Defaults to "production" on insert; left unchanged on update when absent. */
  environment?: string;
  /** Where the deployment came from. When absent, the server fills it from `X-Alror-Source` or the auth type. */
  source?: DeploymentSource;
}

export type DeploymentSource = 'cli' | 'ci' | 'console' | 'runner';

export type EventKind = 'created' | 'step' | 'verdict' | 'promoted' | 'rolled_back' | 'error';

/** One append-only entry in a deployment's history. */
export interface Event {
  at: Timestamp;
  kind: EventKind;
  message: string;
  weight?: number;
  verdict?: Verdict | null;
}

export type JobKind = 'deploy' | 'rollback';
export type JobStatus = 'queued' | 'claimed' | 'done' | 'failed' | 'canceled';

/** A risk override: a level, or a 0-100 score as a number or numeric string. */
export type RiskOverride = Level | number | `${number}`;

/** Payload of a deploy job. */
export interface DeployPayload {
  service: string;
  image: string;
  ref?: string;
  /** Defaults to "production" on the server. */
  environment?: string;
  shadow?: boolean;
  /** Skip risk scoring: "low" | "medium" | "high" or a 0-100 score. Stored as sent. */
  risk_override?: RiskOverride;
}

/** Payload of a rollback job. */
export interface RollbackPayload {
  deployment_id: string;
  reason?: string;
}

/** A unit of work enqueued from the Alror web app or the API and executed by `alror runner`. */
export interface Job<P = DeployPayload | RollbackPayload> {
  id: string;
  kind: JobKind;
  payload: P;
  status: JobStatus;
  claimed_by?: string | null;
  deployment_id?: string | null;
  error?: string | null;
  created_at: Timestamp;
  claimed_at?: Timestamp | null;
  finished_at?: Timestamp | null;
  /** Last lease renewal by the claiming runner. */
  heartbeat_at?: Timestamp | null;
  /** How many times the job has been claimed. */
  attempts?: number;
}

export type Scope = 'deploy:read' | 'deploy:write' | 'jobs:run' | 'config:write';

export interface Org {
  id: string;
  slug: string;
  name: string;
}

export interface Actor {
  type: 'api_key' | 'user' | 'system';
  id: string;
  label: string;
  /** Workspace role, when the caller is a user session. */
  role?: 'owner' | 'admin' | 'member' | (string & {});
}

/** Response of `GET /whoami`. */
export interface WhoAmI {
  org: Org;
  actor: Actor;
  scopes: (Scope | (string & {}))[] | null;
}

/** One service in `alror.yaml`. */
export interface ServiceConfig {
  name: string;
  /** Glob-ish source path prefixes, e.g. "services/checkout/". */
  paths: string[] | null;
  /** "simulated" | "kubernetes" | "ecs" */
  target: 'simulated' | 'kubernetes' | 'ecs' | (string & {});
  /** Kube context or ECS cluster. */
  cluster: string;
  /** Kube namespace. */
  namespace: string;
  /** Raises risk when touched. */
  critical: boolean;
}

export interface MetricsConfig {
  /** "synthetic" | "prometheus" | "datadog" */
  provider: string;
  url?: string;
  queries?: Record<string, string>;
}

export interface PolicyConfig {
  /** Max relative regression per metric, e.g. `{ error_rate: 0.25, latency_p95: 0.15 }`. */
  max_regression: Record<string, number> | null;
  alpha: number;
  auto_rollback: boolean;
  bake_scale?: number;
}

export interface NotifyConfig {
  slack_webhook?: string;
}

/** The `alror.yaml` shape served by `GET /config` and accepted by `PUT /config`. */
export interface Config {
  project: string;
  services: ServiceConfig[] | null;
  metrics: MetricsConfig;
  policy: PolicyConfig;
  notify: NotifyConfig;
}

/** Map of service name to rollback count, from `GET /rollbacks/recent`. */
export type RollbackCounts = Record<string, number>;

/** Nanoseconds per millisecond, for `Step.bake`. */
export const NANOS_PER_MS = 1_000_000;

/** A step's bake time in milliseconds (`bake` is Go nanoseconds on the wire). */
export function bakeMs(step: Pick<Step, 'bake'>): number {
  return step.bake / NANOS_PER_MS;
}

/** Convert milliseconds to a wire `bake` value (integer nanoseconds). */
export function bakeFromMs(ms: number): number {
  return Math.round(ms * NANOS_PER_MS);
}

/** Whether no further transitions are possible. */
export function isTerminal(status: Status): boolean {
  return status === 'promoted' || status === 'rolled_back' || status === 'failed';
}
