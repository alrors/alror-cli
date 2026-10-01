// Typed errors. Every failure the client raises is an AlrorError (or a
// subclass), except caller-initiated aborts, which reject with the signal's
// own reason (an AbortError), as fetch does.

export type AlrorErrorCode =
  | 'unauthorized' // 401
  | 'forbidden' // 403
  | 'not_found' // 404
  | 'conflict' // 409 (e.g. server code "ambiguous")
  | 'lease_lost' // 409 from heartbeatJob/finishJob: the job is no longer claimed by this runner (a ConflictError)
  | 'validation' // 400, 422 (e.g. server code "unknown_service")
  | 'rate_limited' // 429
  | 'server' // 5xx or an unreadable response
  | 'network' // connection failed
  | 'timeout'; // no response within timeoutMs

export interface AlrorErrorInit {
  code: AlrorErrorCode;
  message: string;
  /** HTTP status, or 0 when no response was received. */
  status?: number;
  /** The `error.code` string the server sent, e.g. "unknown_service". */
  serverCode?: string;
  method?: string;
  path?: string;
  cause?: unknown;
}

export class AlrorError extends Error {
  readonly code: AlrorErrorCode;
  readonly status: number;
  readonly serverCode?: string;
  readonly method?: string;
  readonly path?: string;

  constructor(init: AlrorErrorInit) {
    super(init.message, init.cause === undefined ? undefined : { cause: init.cause });
    this.name = new.target.name;
    this.code = init.code;
    this.status = init.status ?? 0;
    this.serverCode = init.serverCode;
    this.method = init.method;
    this.path = init.path;
  }

  /** True for failures worth retrying (network, timeout, 5xx, 429). */
  get retryable(): boolean {
    return this.code === 'network' || this.code === 'timeout' || this.code === 'server' || this.code === 'rate_limited';
  }
}

export class UnauthorizedError extends AlrorError {}
export class ForbiddenError extends AlrorError {}
export class NotFoundError extends AlrorError {}
export class ConflictError extends AlrorError {}
export class ValidationError extends AlrorError {}
export class RateLimitError extends AlrorError {}
export class ServerError extends AlrorError {}
export class NetworkError extends AlrorError {}
/** A timeout is a kind of network error: `instanceof NetworkError` is true. */
export class TimeoutError extends NetworkError {}

export function isAlrorError(e: unknown): e is AlrorError {
  return e instanceof AlrorError;
}

/** Map an HTTP status to an error code. */
export function codeForStatus(status: number): AlrorErrorCode {
  switch (status) {
    case 400:
    case 422:
      return 'validation';
    case 401:
      return 'unauthorized';
    case 403:
      return 'forbidden';
    case 404:
      return 'not_found';
    case 409:
      return 'conflict';
    case 429:
      return 'rate_limited';
    default:
      return status >= 500 ? 'server' : 'validation';
  }
}

const CLASSES: Record<AlrorErrorCode, new (init: AlrorErrorInit) => AlrorError> = {
  unauthorized: UnauthorizedError,
  forbidden: ForbiddenError,
  not_found: NotFoundError,
  conflict: ConflictError,
  lease_lost: ConflictError,
  validation: ValidationError,
  rate_limited: RateLimitError,
  server: ServerError,
  network: NetworkError,
  timeout: TimeoutError,
};

export function createError(init: AlrorErrorInit): AlrorError {
  return new CLASSES[init.code](init);
}

/** Build an error from a non-2xx response body (`{"error":{"code","message"}}`). */
export function errorFromResponse(status: number, body: string, method: string, path: string): AlrorError {
  let serverCode: string | undefined;
  let message: string | undefined;
  try {
    const env = JSON.parse(body) as { error?: { code?: unknown; message?: unknown } };
    if (env && typeof env.error === 'object' && env.error) {
      if (typeof env.error.code === 'string') serverCode = env.error.code;
      if (typeof env.error.message === 'string') message = env.error.message;
    }
  } catch {
    const s = body.trim();
    if (s && s.length < 300 && !s.startsWith('<')) message = s;
  }
  const detail = message || serverCode || `HTTP ${status}`;
  return createError({
    code: codeForStatus(status),
    status,
    serverCode,
    method,
    path,
    message: `${method} ${path}: ${detail}`,
  });
}
