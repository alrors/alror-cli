# @alror/sdk

TypeScript SDK for the Alror platform API. Use it to read deployments and their event logs, queue deploys and rollbacks from CI, manage your workspace config, or build your own `alror runner`-style executor, all against your Alror workspace.

- Typed responses that match the API's JSON exactly (snake_case, RFC 3339 timestamps)
- Typed errors (`UnauthorizedError`, `NotFoundError`, …) with a `code` you can switch on
- Retries with backoff for GET requests on network errors, timeouts, 429 and 5xx
- Per-request timeouts and `AbortSignal` support
- ESM and CommonJS builds with type declarations; no runtime dependencies; Node 18+ (global `fetch`)

> **Status: not published to npm yet.** Until the first npm release, install the tarball attached to a GitHub release, or build one locally (see [Installing before the npm release](#installing-before-the-npm-release)).

## Install

```sh
npm i @alror/sdk
```

## Quick start

Create an API key in your Alror workspace and keep it in an environment variable. Keys look like `alr_live_…`, are shown once, and carry scopes such as `deploy:read`, `deploy:write`, `jobs:run` and `config:write`.

```ts
import { Alror, NotFoundError, bakeMs } from '@alror/sdk';

const alror = new Alror({
  server: 'https://alror.example.com', // your Alror workspace
  apiKey: process.env.ALROR_API_KEY,   // alr_live_…
});

const me = await alror.whoami();
console.log(`${me.org.name} as ${me.actor.label}, scopes: ${me.scopes?.join(', ')}`);

// Recent deployments of one service
for (const d of await alror.listDeployments({ service: 'checkout-api', limit: 10 })) {
  console.log(d.id, d.status, `risk ${d.risk.score} (${d.risk.level})`, `${d.weight}%`);
}

// One deployment (full id or unique prefix) and its event log
try {
  const d = await alror.getDeployment('dep_20261002T0930');
  const steps = d.plan.steps ?? [];
  console.log(steps.map((s) => `${s.weight}% for ${bakeMs(s) / 1000}s`).join(' → '));
  for (const ev of await alror.listEvents(d.id)) console.log(ev.at, ev.kind, ev.message);
} catch (err) {
  if (err instanceof NotFoundError) console.log('no such deployment');
  else throw err;
}

// Queue a deploy; `alror runner` picks it up and runs the verified rollout
const job = await alror.enqueueDeploy({
  service: 'checkout-api',
  image: 'registry.example.com/checkout:v2',
  ref: process.env.GITHUB_SHA,
  environment: 'production',
});
console.log(`queued ${job.id}`);
```

`Alror.fromEnv()` builds a client from `ALROR_SERVER` and `ALROR_API_KEY`.

## API

| Method | Endpoint | Scope |
| --- | --- | --- |
| `whoami()` | `GET /whoami` | any |
| `getConfig()` | `GET /config` | deploy:read |
| `putConfig(config)` | `PUT /config` | config:write |
| `listDeployments({ limit?, status?, service?, environment?, before? })` | `GET /deployments` | deploy:read |
| `iterDeployments({ pageSize?, status?, service?, environment?, before? })` | pages through `GET /deployments` | deploy:read |
| `getDeployment(idOrPrefix)` | `GET /deployments/{id}` | deploy:read |
| `upsertDeployment(deployment)` | `POST /deployments` | deploy:write |
| `updateDeployment(deployment)` | `PUT /deployments/{id}` | deploy:write |
| `listEvents(deploymentId)` | `GET /deployments/{id}/events` | deploy:read |
| `appendEvent(deploymentId, event)` | `POST /deployments/{id}/events` | deploy:write |
| `recentRollbacks(since)` | `GET /rollbacks/recent` | deploy:read |
| `enqueueDeploy({ service, image, ref?, environment?, shadow?, riskOverride? })` | `POST /jobs` (deploy) | deploy:write |
| `enqueueRollback(deploymentId, reason?)` | `POST /jobs` (rollback) | deploy:write |
| `listJobs({ status? })` | `GET /jobs` | deploy:read |
| `claimJob(runnerName)` | `POST /jobs/claim`, used by `alror runner` | jobs:run |
| `heartbeatJob(id, runnerName)` | `POST /jobs/{id}/heartbeat`, used by `alror runner` | jobs:run |
| `finishJob(id, { status, error?, deploymentId?, runner? })` | `POST /jobs/{id}/finish`, used by `alror runner` | jobs:run |
| `stream(onEvent, { signal })` | `GET /stream` (SSE) | browser session |

Every method also accepts `{ signal, timeoutMs }` as its last argument.

### Deployments

`listDeployments` returns deployments newest first (`created_at` desc, then `id` desc). `limit` is 1 to 1000 (default 50). To page backwards, pass `before`: a deployment id (normally the last id of the previous page) or an RFC 3339 time or `Date`. An unknown cursor id is a `ValidationError`. `iterDeployments` does the paging for you. It fetches 500 at a time (`pageSize`) and stops after the first short page:

```ts
for await (const d of alror.iterDeployments({ service: 'checkout-api', environment: 'production' })) {
  if (d.status === 'rolled_back') console.log(d.id, d.reason);
}
```

The server owns deployment timestamps. `updated_at` is set to the current time on every write, and `created_at` is kept from the first insert. `upsertDeployment` and `updateDeployment` resolve with the server's copy, so use the returned object. Deployments also carry two optional fields:
- `environment` defaults to `"production"` on insert.
- `source` is `"cli" | "ci" | "console" | "runner"`. The server fills it in when you omit it.

### Jobs and runners

`enqueueDeploy` queues a deploy for `alror runner`.
- `environment` defaults to `"production"` on the server.
- `riskOverride` skips risk scoring. It takes a level (`"low" | "medium" | "high"`) or a 0 to 100 score as a number or numeric string, and is stored as sent. Any other value rejects with a `TypeError` before a request is made.

`claimJob` long-polls. The API holds the request for up to 25 seconds, and the method resolves `null` when no job arrived (HTTP 204). It uses `claimTimeoutMs` (default 40 s) instead of `timeoutMs`. It is never retried, so a lost response can't claim two jobs.

A claimed job is leased to the runner that claimed it. Call `heartbeatJob` about every 30 seconds while the job runs. The server requeues a job whose last heartbeat is older than 2 minutes. If another runner took the job over, `heartbeatJob` (and `finishJob` with `runner` set) rejects with a `ConflictError` whose `code` is `"lease_lost"`. Stop working on the job and do not finish it. Heartbeats are never retried automatically. On the wire, the runner name is sent as the `worker` field.

```ts
import { Alror, ConflictError } from '@alror/sdk';

const alror = Alror.fromEnv();
const job = await alror.claimJob('runner-1');
if (job) {
  const ac = new AbortController();
  const beat = setInterval(() => {
    alror.heartbeatJob(job.id, 'runner-1').catch((err) => {
      if (err instanceof ConflictError && err.code === 'lease_lost') ac.abort(err); // stop, don't finish
    });
  }, 30_000);
  try {
    const deploymentId = await runTheJob(job, ac.signal); // your executor
    await alror.finishJob(job.id, { status: 'done', deploymentId, runner: 'runner-1' });
  } catch (err) {
    if (!ac.signal.aborted) await alror.finishJob(job.id, { status: 'failed', error: String(err), runner: 'runner-1' });
  } finally {
    clearInterval(beat);
  }
}
```

### Types

`Deployment`, `Event`, `Verdict`, `MetricResult`, `RiskReport`, `Factor`, `Plan`, `Step`, `Status`, `Level`, `Job`, `Config`, `WhoAmI`, `RiskOverride`, `DeploymentSource` and friends are exported. Field names are the API's snake_case names (`step_index`, `created_at`, `p_value`, `ai_authored`).

**`Step.bake` is a duration in integer nanoseconds**, the API's JSON encoding of Go's `time.Duration`. Use the helpers:

```ts
import { bakeMs, bakeFromMs } from '@alror/sdk';
bakeMs({ weight: 5, bake: 120_000_000_000 }); // 120000 (ms)
bakeFromMs(30_000);                           // 30000000000 (ns)
```

### Errors

Every failure is an `AlrorError` with `code`, `status` (0 when no response arrived), `serverCode` (the API's own `error.code`, such as `unknown_service` or `ambiguous`), `method` and `path`:

| `code` | Class | When |
| --- | --- | --- |
| `unauthorized` | `UnauthorizedError` | 401: missing, revoked or invalid key |
| `forbidden` | `ForbiddenError` | 403: the key lacks the scope |
| `not_found` | `NotFoundError` | 404 |
| `conflict` | `ConflictError` | 409: for example, an ambiguous id prefix |
| `lease_lost` | `ConflictError` | 409 from `heartbeatJob` or `finishJob`: the job is no longer claimed by this runner |
| `validation` | `ValidationError` | 400 or 422: for example, `unknown_service` |
| `rate_limited` | `RateLimitError` | 429 |
| `server` | `ServerError` | 5xx or an unreadable response |
| `network` | `NetworkError` | the connection failed |
| `timeout` | `TimeoutError` (extends `NetworkError`) | no response within `timeoutMs` |

GET requests are retried (default `retries: 2`, exponential backoff from `retryBackoffMs: 250` with jitter, capped at 5 s) on `network`, `timeout`, `rate_limited` and `server` errors. Other methods are never retried automatically. If you abort with your own `AbortSignal`, the call rejects with the signal's reason (an `AbortError`), not an `AlrorError`.

### Options

```ts
new Alror({
  server: 'https://alror.example.com',
  apiKey: process.env.ALROR_API_KEY,
  timeoutMs: 20_000,       // per attempt
  claimTimeoutMs: 40_000,  // claimJob long-poll
  retries: 2,              // GET only
  retryBackoffMs: 250,
  userAgent: 'my-tool/1.0',
  headers: { 'X-Request-Source': 'ci' },
  fetch: customFetch,      // defaults to globalThis.fetch
});
```

### Live stream

```ts
const ac = new AbortController();
await alror.stream(
  (ev) => {
    if (ev.type === 'deployment.updated') console.log('updated', ev.data);
  },
  { signal: ac.signal },
);
```

`stream()` is a Server-Sent Events client built on `fetch` streaming, so it works in Node and in browsers. It reconnects with `Last-Event-ID` after a drop, honours the server's `retry:` hint, and resolves when you abort. **API v1 only serves `/stream` to signed-in browser sessions.** Use it from a page on your workspace's origin, where the session cookie is sent automatically. API-key access to the stream is planned as a future token stream. The SDK already sends `Authorization: Bearer <apiKey>` so the same code will keep working. Until then, expect an API-key call to be rejected with `UnauthorizedError` or `ForbiddenError`.

## CI example: queue a deploy from GitHub Actions

Store the key as a repository secret named `ALROR_API_KEY` (it needs the `deploy:write` scope) and the workspace URL as a variable named `ALROR_SERVER`.

```yaml
# .github/workflows/deploy.yml
name: Deploy
on:
  push:
    branches: [main]

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: 20
      - run: npm i --no-save @alror/sdk
      - name: Queue a verified rollout
        env:
          ALROR_SERVER: ${{ vars.ALROR_SERVER }}
          ALROR_API_KEY: ${{ secrets.ALROR_API_KEY }}
          IMAGE: ghcr.io/${{ github.repository }}:${{ github.sha }}
        run: |
          cat > enqueue.mjs <<'EOF'
          import { Alror } from '@alror/sdk';
          const alror = Alror.fromEnv();
          const job = await alror.enqueueDeploy({
            service: 'checkout-api',
            image: process.env.IMAGE,
            ref: process.env.GITHUB_SHA,
            environment: 'production',
          });
          console.log(`Queued deploy job ${job.id}`);
          EOF
          node enqueue.mjs
```

`alror runner`, connected to the same workspace, claims the job and runs the risk-scored, verified rollout. If you would rather run the rollout inside the CI job itself, use the CLI in connected mode instead: `npx alror deploy -s checkout-api -i "$IMAGE"`. It reads `ALROR_SERVER` and `ALROR_API_KEY` and exits with 2 when the rollout is rolled back and 3 when the risk gate blocks it.

## Installing before the npm release

From a GitHub release (the release workflow attaches the packed SDK):

```sh
npm i https://github.com/manaskumar3003/alror-cli/releases/download/v0.1.0/alror-sdk-0.1.0.tgz
```

From source:

```sh
git clone https://github.com/manaskumar3003/alror-cli && cd alror-cli/sdk/js
npm ci && npm pack            # builds and writes alror-sdk-0.1.0.tgz
cd /path/to/your/project && npm i /path/to/alror-cli/sdk/js/alror-sdk-0.1.0.tgz
```

## Development

```sh
npm ci
npm test            # builds ESM + CJS, type-checks, runs node:test against an in-process fake API
npm pack --dry-run  # shows what would be published
```

## License

Apache-2.0
