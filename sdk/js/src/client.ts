import { ConflictError, NetworkError, ServerError, TimeoutError, errorFromResponse, isAlrorError } from './errors.js';
import { SSEParser } from './sse.js';
import type {
  Config,
  DeployPayload,
  Deployment,
  Event,
  Job,
  JobStatus,
  RiskOverride,
  RollbackCounts,
  RollbackPayload,
  Status,
  WhoAmI,
} from './types.js';
import { VERSION } from './version.js';

const BASE_PATH = '/api/v1';

export interface AlrorOptions {
  /** URL of your Alror workspace, e.g. "https://alror.example.com" (a trailing "/api/v1" is accepted). */
  server: string;
  /** API key (`alr_live_…`), sent as `Authorization: Bearer <key>`. */
  apiKey?: string;
  /** Custom fetch (defaults to the global fetch, Node 18+). */
  fetch?: typeof fetch;
  /** Per-attempt timeout in ms (default 20 000). */
  timeoutMs?: number;
  /** Timeout for the `claimJob` long-poll in ms (default 40 000; the server holds it up to 25 s). */
  claimTimeoutMs?: number;
  /** User-Agent header (ignored by browsers). Default "alror-sdk-js/<version>". */
  userAgent?: string;
  /** Retries for GET requests on network errors, timeouts, 429 and 5xx (default 2). */
  retries?: number;
  /** Base backoff in ms, doubled per retry with jitter, capped at 5 s (default 250). */
  retryBackoffMs?: number;
  /** Extra headers sent with every request. */
  headers?: Record<string, string>;
}

export interface RequestOptions {
  /** Abort the call. Rejects with the signal's reason (an AbortError). */
  signal?: AbortSignal;
  /** Override the per-attempt timeout for this call. */
  timeoutMs?: number;
}

export interface ListDeploymentsOptions extends RequestOptions {
  /** Page size, 1 to 1000 (server default 50). */
  limit?: number;
  status?: Status;
  service?: string;
  /** Environment name, e.g. "production". */
  environment?: string;
  /**
   * Only return deployments older than this: a deployment id (typically the
   * last id of the previous page) or an RFC 3339 time / Date.
   */
  before?: string | Date;
}

export interface IterDeploymentsOptions extends Omit<ListDeploymentsOptions, 'limit'> {
  /** Page size, 1 to 1000 (default 500). */
  pageSize?: number;
}

export interface ListJobsOptions extends RequestOptions {
  status?: JobStatus;
}

export interface EnqueueDeployInput {
  service: string;
  image: string;
  /** PR number or commit. */
  ref?: string;
  /** e.g. "production", "staging". When omitted the server uses "production". */
  environment?: string;
  shadow?: boolean;
  /**
   * Skip risk scoring: a level ("low" | "medium" | "high") or a 0-100 score
   * (number or numeric string). `alror runner` has no git context, so it
   * assumes medium risk without it.
   */
  riskOverride?: RiskOverride;
}

export interface FinishJobInput {
  status: 'done' | 'failed';
  error?: string;
  deploymentId?: string;
  /** The runner name that claimed the job (sent as `worker`); lets the server check the lease. */
  runner?: string;
}

/** A message relayed by `GET /stream`. */
export interface StreamEvent {
  /** SSE event name: "deployment.updated" | "deployment.event" | "job.updated" (or others in future). */
  type: 'deployment.updated' | 'deployment.event' | 'job.updated' | (string & {});
  /** The JSON message published by the server (parsed), or the raw string if it is not JSON. */
  data: unknown;
  /** SSE event id, when the server sends one. */
  id?: string;
}

export interface StreamOptions {
  signal?: AbortSignal;
  /** Reconnect after the stream drops (default true). Auth errors are never retried. */
  reconnect?: boolean;
  /** Delay before reconnecting in ms (default 3000; the server's `retry:` overrides it). */
  retryMs?: number;
  /** Called each time the stream is (re)connected. */
  onOpen?: () => void;
  /** Called when a connection drops and a reconnect is scheduled. */
  onError?: (err: unknown) => void;
}

interface CallOptions extends RequestOptions {
  query?: Record<string, string | number | undefined>;
  body?: unknown;
}

function normalizeServer(s: string): string {
  let out = s.trim().replace(/\/+$/, '');
  if (out.endsWith(BASE_PATH)) out = out.slice(0, -BASE_PATH.length);
  return out.replace(/\/+$/, '');
}

const sleep = (ms: number, signal?: AbortSignal) =>
  new Promise<void>((resolve, reject) => {
    if (signal?.aborted) return reject(signal.reason);
    const t = setTimeout(() => {
      signal?.removeEventListener('abort', onAbort);
      resolve();
    }, ms);
    const onAbort = () => {
      clearTimeout(t);
      reject(signal!.reason);
    };
    signal?.addEventListener('abort', onAbort, { once: true });
  });

/**
 * Client for the Alror platform API (v1) of your Alror workspace.
 *
 * ```ts
 * const alror = new Alror({ server: 'https://alror.example.com', apiKey: process.env.ALROR_API_KEY });
 * const job = await alror.enqueueDeploy({ service: 'checkout-api', image: 'registry/checkout:v2' });
 * ```
 */
export class Alror {
  readonly server: string;
  private readonly apiKey?: string;
  private readonly fetchImpl: typeof fetch;
  private readonly timeoutMs: number;
  private readonly claimTimeoutMs: number;
  private readonly userAgent: string;
  private readonly retries: number;
  private readonly retryBackoffMs: number;
  private readonly extraHeaders: Record<string, string>;

  constructor(opts: AlrorOptions) {
    if (!opts || !opts.server) throw new TypeError('Alror: `server` is required');
    this.server = normalizeServer(opts.server);
    this.apiKey = opts.apiKey || undefined;
    const f = opts.fetch ?? (globalThis as { fetch?: typeof fetch }).fetch;
    if (!f) throw new TypeError('Alror: no global fetch; use Node 18+ or pass `fetch`');
    this.fetchImpl = opts.fetch ? f : f.bind(globalThis);
    this.timeoutMs = opts.timeoutMs ?? 20_000;
    this.claimTimeoutMs = opts.claimTimeoutMs ?? 40_000;
    this.userAgent = opts.userAgent ?? `alror-sdk-js/${VERSION}`;
    this.retries = Math.max(0, opts.retries ?? 2);
    this.retryBackoffMs = opts.retryBackoffMs ?? 250;
    this.extraHeaders = { ...(opts.headers ?? {}) };
  }

  /** Build a client from `ALROR_SERVER` and `ALROR_API_KEY`. */
  static fromEnv(env?: Record<string, string | undefined>, opts: Partial<AlrorOptions> = {}): Alror {
    const e = env ?? (globalThis as { process?: { env: Record<string, string | undefined> } }).process?.env ?? {};
    const server = opts.server ?? e.ALROR_SERVER;
    if (!server) throw new TypeError('Alror.fromEnv: ALROR_SERVER is not set');
    return new Alror({ ...opts, server, apiKey: opts.apiKey ?? e.ALROR_API_KEY });
  }

  // ---- identity and config -------------------------------------------------

  /** `GET /whoami`: the org, the caller and its scopes. */
  whoami(opts?: RequestOptions): Promise<WhoAmI> {
    return this.json<WhoAmI>('GET', '/whoami', opts);
  }

  /** `GET /config` (scope deploy:read): the alror.yaml shape. */
  getConfig(opts?: RequestOptions): Promise<Config> {
    return this.json<Config>('GET', '/config', opts);
  }

  /** `PUT /config` (scope config:write, or an owner/admin session): upserts services and policy. */
  putConfig(config: Config, opts?: RequestOptions): Promise<Config> {
    return this.json<Config>('PUT', '/config', { ...opts, body: config });
  }

  // ---- deployments ---------------------------------------------------------

  /**
   * `GET /deployments`, newest first (created_at desc, id desc). Page
   * backwards by passing the last id as `before`; see {@link iterDeployments}.
   */
  async listDeployments(opts: ListDeploymentsOptions = {}): Promise<Deployment[]> {
    const { limit, status, service, environment, before, ...rest } = opts;
    if (limit !== undefined && (!Number.isInteger(limit) || limit < 1 || limit > 1000)) {
      throw new RangeError('listDeployments: limit must be an integer from 1 to 1000');
    }
    const b = before instanceof Date ? rfc3339(before) : before;
    const query = { limit, status, service, environment, before: b };
    return (await this.json<Deployment[] | null>('GET', '/deployments', { ...rest, query })) ?? [];
  }

  /**
   * Iterate over every matching deployment, newest first. Fetches pages of
   * `pageSize` (default 500), passing the last id as `before`, until a short
   * page arrives.
   *
   * ```ts
   * for await (const d of alror.iterDeployments({ service: 'checkout-api' })) console.log(d.id);
   * ```
   */
  async *iterDeployments(opts: IterDeploymentsOptions = {}): AsyncGenerator<Deployment, void, undefined> {
    const { pageSize = 500, before, ...rest } = opts;
    let cursor: string | Date | undefined = before;
    for (;;) {
      const page = await this.listDeployments({ ...rest, limit: pageSize, before: cursor });
      for (const d of page) yield d;
      if (page.length < pageSize) return;
      cursor = page[page.length - 1]!.id;
    }
  }

  /** `GET /deployments/{id}`: accepts a full id or a unique prefix (409 conflict if ambiguous). */
  getDeployment(id: string, opts?: RequestOptions): Promise<Deployment> {
    return this.json<Deployment>('GET', `/deployments/${encodeURIComponent(id)}`, opts);
  }

  /**
   * `POST /deployments`: create or replace by id. The service must exist (422
   * `unknown_service`). The server owns the timestamps (`updated_at` is always
   * now; `created_at` is kept from the first insert), so use the returned
   * deployment rather than the one you sent.
   */
  upsertDeployment(deployment: Deployment, opts?: RequestOptions): Promise<Deployment> {
    return this.json<Deployment>('POST', '/deployments', { ...opts, body: deployment });
  }

  /** `PUT /deployments/{id}`: update an existing deployment (404 if it does not exist). Returns the server's copy. */
  updateDeployment(deployment: Deployment, opts?: RequestOptions): Promise<Deployment> {
    return this.json<Deployment>('PUT', `/deployments/${encodeURIComponent(deployment.id)}`, { ...opts, body: deployment });
  }

  /** `GET /deployments/{id}/events`, in order. */
  async listEvents(deploymentId: string, opts?: RequestOptions): Promise<Event[]> {
    return (await this.json<Event[] | null>('GET', `/deployments/${encodeURIComponent(deploymentId)}/events`, opts)) ?? [];
  }

  /** `POST /deployments/{id}/events`: append an event; resolves with the stored event. */
  async appendEvent(deploymentId: string, event: Event, opts?: RequestOptions): Promise<Event> {
    const res = await this.request('POST', `/deployments/${encodeURIComponent(deploymentId)}/events`, { ...opts, body: event });
    return (res.data as Event | undefined) ?? event;
  }

  /** `GET /rollbacks/recent`: rollback counts per service since a time. */
  async recentRollbacks(since: Date | string, opts?: RequestOptions): Promise<RollbackCounts> {
    const s = typeof since === 'string' ? since : rfc3339(since);
    return (await this.json<RollbackCounts | null>('GET', '/rollbacks/recent', { ...opts, query: { since: s } })) ?? {};
  }

  // ---- jobs ----------------------------------------------------------------

  /** `POST /jobs` with kind "deploy": queue a deploy for `alror runner` to execute. */
  async enqueueDeploy(input: EnqueueDeployInput, opts?: RequestOptions): Promise<Job<DeployPayload>> {
    const payload: DeployPayload = { service: input.service, image: input.image };
    if (input.ref !== undefined) payload.ref = input.ref;
    if (input.environment !== undefined) payload.environment = input.environment;
    if (input.shadow !== undefined) payload.shadow = input.shadow;
    if (input.riskOverride !== undefined) payload.risk_override = checkRiskOverride(input.riskOverride);
    return this.json<Job<DeployPayload>>('POST', '/jobs', { ...opts, body: { kind: 'deploy', payload } });
  }

  /** `POST /jobs` with kind "rollback": queue a rollback of a deployment. */
  enqueueRollback(deploymentId: string, reason?: string, opts?: RequestOptions): Promise<Job<RollbackPayload>> {
    const payload: RollbackPayload = { deployment_id: deploymentId };
    if (reason !== undefined) payload.reason = reason;
    return this.json<Job<RollbackPayload>>('POST', '/jobs', { ...opts, body: { kind: 'rollback', payload } });
  }

  /** `GET /jobs`. */
  async listJobs(opts: ListJobsOptions = {}): Promise<Job[]> {
    const { status, ...rest } = opts;
    return (await this.json<Job[] | null>('GET', '/jobs', { ...rest, query: { status } })) ?? [];
  }

  /**
   * `POST /jobs/claim` (scope jobs:run, used by `alror runner`): long-polls up to 25 s for the next
   * queued job. Resolves `null` when none arrived (HTTP 204). Never retried,
   * since a retry could claim a second job while the first response was lost.
   */
  async claimJob(runner: string, opts: RequestOptions = {}): Promise<Job | null> {
    const res = await this.request('POST', '/jobs/claim', {
      ...opts,
      timeoutMs: opts.timeoutMs ?? this.claimTimeoutMs,
      body: { worker: runner }, // wire field name is "worker"
    });
    if (res.status === 204 || !res.data || !(res.data as Job).id) return null;
    return res.data as Job;
  }

  /**
   * `POST /jobs/{id}/heartbeat` (scope jobs:run, used by `alror runner`):
   * extend the lease on a claimed job. Call it about every 30 s while the job
   * runs; the server requeues a job whose heartbeat is older than 2 minutes.
   *
   * Rejects with a `ConflictError` whose `code` is `"lease_lost"` (HTTP 409)
   * when the job is no longer claimed by this runner: stop working on it and
   * do not finish it. Never retried.
   */
  async heartbeatJob(id: string, runner: string, opts?: RequestOptions): Promise<Job> {
    try {
      // wire field name is "worker"
      return await this.json<Job>('POST', `/jobs/${encodeURIComponent(id)}/heartbeat`, { ...opts, body: { worker: runner } });
    } catch (err) {
      throw leaseLost(err);
    }
  }

  /**
   * `POST /jobs/{id}/finish` (scope jobs:run, used by `alror runner`): report a
   * job's outcome. Repeating the same status is idempotent. A 409 (the job is
   * no longer claimed by this runner) rejects with `ConflictError` code
   * `"lease_lost"`. Not retried automatically.
   */
  async finishJob(id: string, input: FinishJobInput, opts?: RequestOptions): Promise<Job> {
    const body: Record<string, string> = { status: input.status };
    if (input.error) body.error = input.error;
    if (input.deploymentId) body.deployment_id = input.deploymentId;
    if (input.runner) body.worker = input.runner;
    try {
      return await this.json<Job>('POST', `/jobs/${encodeURIComponent(id)}/finish`, { ...opts, body });
    } catch (err) {
      throw leaseLost(err);
    }
  }

  // ---- live stream ---------------------------------------------------------

  /**
   * Subscribe to `GET /stream` (Server-Sent Events) and call `onEvent` for
   * every message. Resolves when `signal` aborts (or when the stream ends and
   * `reconnect` is false); rejects on auth errors.
   *
   * Note: API v1 serves `/stream` to **signed-in browser sessions only**. In
   * a browser on your Alror workspace's origin the session cookie is sent
   * automatically; API-key access is planned as a future token stream. The
   * client still sends `Authorization: Bearer <apiKey>` when a key is set.
   */
  async stream(onEvent: (event: StreamEvent) => void, opts: StreamOptions = {}): Promise<void> {
    const { signal, reconnect = true, onOpen, onError } = opts;
    let retryMs = opts.retryMs ?? 3000;
    let lastEventId = '';
    for (;;) {
      if (signal?.aborted) return;
      const parser = new SSEParser((m) => {
        if (m.retry !== undefined) retryMs = m.retry;
        if (m.data === '' && m.event === '') return;
        let data: unknown = m.data;
        try {
          data = JSON.parse(m.data);
        } catch {
          // not JSON: hand over the raw string
        }
        const ev: StreamEvent = { type: m.event, data };
        if (m.id) ev.id = m.id;
        onEvent(ev);
      });
      parser.lastEventId = lastEventId;
      try {
        const headers = this.headers({ Accept: 'text/event-stream', 'Cache-Control': 'no-cache' });
        if (lastEventId) headers['Last-Event-ID'] = lastEventId;
        let res: Response;
        try {
          res = await this.fetchImpl(`${this.server}${BASE_PATH}/stream`, { method: 'GET', headers, signal });
        } catch (err) {
          if (signal?.aborted) return;
          throw new NetworkError({ code: 'network', message: `GET /stream: ${errMessage(err)}`, method: 'GET', path: '/stream', cause: err });
        }
        if (!res.ok) {
          const body = await res.text().catch(() => '');
          throw errorFromResponse(res.status, body, 'GET', '/stream');
        }
        if (!res.body) throw new ServerError({ code: 'server', message: 'GET /stream: response has no body', status: res.status });
        onOpen?.();
        const reader = res.body.getReader();
        const decoder = new TextDecoder();
        try {
          for (;;) {
            const { value, done } = await reader.read();
            if (done) break;
            parser.push(decoder.decode(value, { stream: true }));
          }
          parser.push(decoder.decode());
        } catch (err) {
          if (signal?.aborted) return;
          throw new NetworkError({ code: 'network', message: `GET /stream: ${errMessage(err)}`, method: 'GET', path: '/stream', cause: err });
        } finally {
          parser.end();
          lastEventId = parser.lastEventId;
          reader.releaseLock?.();
        }
        if (!reconnect) return;
        onError?.(new NetworkError({ code: 'network', message: 'GET /stream: stream ended', method: 'GET', path: '/stream' }));
      } catch (err) {
        if (signal?.aborted) return;
        if (!reconnect || (isAlrorError(err) && !err.retryable)) throw err;
        onError?.(err);
      }
      try {
        await sleep(retryMs, signal);
      } catch {
        return; // aborted while waiting
      }
    }
  }

  // ---- transport -----------------------------------------------------------

  private headers(extra: Record<string, string> = {}): Record<string, string> {
    const h: Record<string, string> = { Accept: 'application/json', 'User-Agent': this.userAgent, ...this.extraHeaders, ...extra };
    if (this.apiKey) h.Authorization = `Bearer ${this.apiKey}`;
    return h;
  }

  private async json<T>(method: string, path: string, opts: CallOptions = {}): Promise<T> {
    return (await this.request(method, path, opts)).data as T;
  }

  /** One API call with timeout, error mapping and (for GET) retries. */
  private async request(method: string, path: string, opts: CallOptions = {}): Promise<{ status: number; data: unknown }> {
    let url = `${this.server}${BASE_PATH}${path}`;
    if (opts.query) {
      const q = new URLSearchParams();
      for (const [k, v] of Object.entries(opts.query)) if (v !== undefined && v !== '') q.set(k, String(v));
      const s = q.toString();
      if (s) url += `?${s}`;
    }
    const body = opts.body === undefined ? undefined : JSON.stringify(opts.body);
    const attempts = method === 'GET' ? 1 + this.retries : 1;
    let last: unknown;
    for (let attempt = 0; attempt < attempts; attempt++) {
      if (attempt > 0) await sleep(this.backoff(attempt), opts.signal);
      try {
        return await this.once(method, url, path, body, opts);
      } catch (err) {
        if (opts.signal?.aborted) throw opts.signal.reason;
        if (!(isAlrorError(err) && err.retryable)) throw err;
        last = err;
      }
    }
    throw last;
  }

  private async once(method: string, url: string, path: string, body: string | undefined, opts: CallOptions) {
    const timeoutMs = opts.timeoutMs ?? this.timeoutMs;
    const ctrl = new AbortController();
    let timedOut = false;
    const timer = setTimeout(() => {
      timedOut = true;
      ctrl.abort();
    }, timeoutMs);
    const onAbort = () => ctrl.abort(opts.signal!.reason);
    if (opts.signal) {
      if (opts.signal.aborted) {
        clearTimeout(timer);
        throw opts.signal.reason;
      }
      opts.signal.addEventListener('abort', onAbort, { once: true });
    }
    try {
      const headers = this.headers(body !== undefined ? { 'Content-Type': 'application/json' } : {});
      let res: Response;
      let text: string;
      try {
        res = await this.fetchImpl(url, { method, headers, body, signal: ctrl.signal });
        text = await res.text();
      } catch (err) {
        if (opts.signal?.aborted) throw opts.signal.reason;
        if (timedOut) {
          throw new TimeoutError({ code: 'timeout', message: `${method} ${path}: no response within ${timeoutMs} ms`, method, path, cause: err });
        }
        throw new NetworkError({ code: 'network', message: `${method} ${path}: ${errMessage(err)}`, method, path, cause: err });
      }
      if (res.status >= 400) throw errorFromResponse(res.status, text, method, path);
      if (res.status === 204 || text.trim() === '') return { status: res.status, data: undefined };
      try {
        return { status: res.status, data: JSON.parse(text) as unknown };
      } catch (err) {
        throw new ServerError({ code: 'server', status: res.status, method, path, message: `${method} ${path}: invalid JSON response`, cause: err });
      }
    } finally {
      clearTimeout(timer);
      opts.signal?.removeEventListener('abort', onAbort);
    }
  }

  private backoff(attempt: number): number {
    const d = Math.min(this.retryBackoffMs * 2 ** (attempt - 1), 5000);
    return d / 2 + Math.random() * (d / 2);
  }
}

/** RFC 3339 in UTC, without milliseconds when they are zero. */
function rfc3339(d: Date): string {
  return d.toISOString().replace(/\.000Z$/, 'Z');
}

const LEVELS = new Set<string>(['low', 'medium', 'high']);

function checkRiskOverride(v: RiskOverride): RiskOverride {
  if (typeof v === 'string' && LEVELS.has(v)) return v;
  const n = typeof v === 'number' ? v : typeof v === 'string' && /^\d+(\.\d+)?$/.test(v.trim()) ? Number(v) : NaN;
  if (Number.isFinite(n) && n >= 0 && n <= 100) return v;
  throw new TypeError(`riskOverride must be "low", "medium", "high" or a score from 0 to 100, got ${JSON.stringify(v)}`);
}

/** Map a 409 from heartbeat/finish to a ConflictError with code "lease_lost". */
function leaseLost(err: unknown): unknown {
  if (err instanceof ConflictError && err.status === 409) {
    return new ConflictError({
      code: 'lease_lost',
      status: 409,
      serverCode: err.serverCode,
      method: err.method,
      path: err.path,
      message: err.message,
      cause: err,
    });
  }
  return err;
}

function errMessage(err: unknown): string {
  if (err instanceof Error) {
    const cause = (err as { cause?: unknown }).cause;
    return cause instanceof Error && cause.message ? `${err.message} (${cause.message})` : err.message;
  }
  return String(err);
}
