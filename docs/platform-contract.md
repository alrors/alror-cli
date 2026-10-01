# Alror platform contract (v1)

This is the single source of truth shared by the Alror platform (the Next.js workspace app with the console and the platform API) and the Go CLI, runner and SDKs in this repo.

Everything is self-hosted. **PostgreSQL 16** and **Redis 7** run in Docker; no external SaaS or third-party API keys are used.

## 1. Runtime topology

```
 developer / CI ──alror CLI──┐  Authorization: Bearer alr_live_…
                             ▼
 browser ──session cookie──▶ Next.js workspace: the Alror platform / workspace
                               ├─ /app, /login, /signup …     console UI (server components)
                               ├─ /api/v1/*                    platform REST API
                               └─ /api/v1/stream               SSE live updates
                                     │                 │
                                PostgreSQL 16       Redis 7
                               (system of record)  (sessions, job queue signal, pub/sub, rate limits)
                                     ▲
 alror runner (Go) ──claims jobs over /api/v1/jobs/claim──┘   runs deploy / rollback with the engine
```

- **CLI in connected mode** (when a server is configured) reads services and policy from `GET /api/v1/config`, writes deployments and events through the API, and never touches the database directly.
- **Console actions** (Deploy, Roll back) enqueue **jobs**. An `alror runner` (Go) claims and executes them with the real engine, so the console never shells out to the CLI.
- **Local mode** (no server configured) keeps working exactly as today, using `alror.yaml` and the `.alror/` file store.

## 2. Docker

`docker-compose.yml` lives at the root of the workspace app:

| Service | Image | Port | Credentials / data |
| --- | --- | --- | --- |
| postgres | `postgres:16-alpine` | 5432 | user `alror`, password `alror`, db `alror`; volume `pgdata` |
| redis | `redis:7-alpine` | 6379 | `--appendonly yes`; volume `redisdata` |

Environment for the workspace app (`.env.local`, never committed; documented in `.env.example`):

```
DATABASE_URL=postgres://alror:alror@localhost:5432/alror
REDIS_URL=redis://localhost:6379
ALROR_SESSION_SECRET=<random 32+ bytes>      # signs nothing secret-bearing; used for CSRF tokens
ALROR_PUBLIC_URL=http://localhost:3000
```

## 3. Database schema (PostgreSQL, Drizzle ORM, SQL migrations committed)

All ids are `uuid` (`gen_random_uuid()`) except `deployments.id`, which is the CLI's `dep_<UTCyyyymmddTHHMMSS>_<6hex>` text id. Timestamps are `timestamptz`.

| Table | Columns |
| --- | --- |
| `users` | id, email (unique, lowercased), name, password_hash (`scrypt$N$r$p$saltB64$hashB64`), created_at, last_login_at |
| `orgs` | id, slug (unique), name, plan (`free`\|`team`\|`business`, default `team`), created_at |
| `memberships` | org_id, user_id, role (`owner`\|`admin`\|`member`), created_at; PK (org_id, user_id) |
| `invites` | id, org_id, email, role, token_hash (sha256 hex), invited_by, expires_at, accepted_at, created_at |
| `api_keys` | id, org_id, name, prefix (first 12 chars of the token, shown in the UI), key_hash (sha256 hex, unique), scopes text[] (`deploy:read`, `deploy:write`, `jobs:run` (used by `alror runner`), `config:write`), created_by, created_at, last_used_at, revoked_at |
| `environments` | id, org_id, name (`production`, `staging`, …), protected bool; unique (org_id, name) |
| `services` | id, org_id, name, paths text[], target (`simulated`\|`kubernetes`\|`ecs`), cluster, namespace, critical bool, policy_override jsonb null, created_at, archived_at null; unique (org_id, name) |
| `org_policies` | org_id PK, max_regression jsonb (default `{"error_rate":0.25,"latency_p95":0.15}`), alpha numeric (0.05), auto_rollback bool (true), bake_scale numeric (1), metrics jsonb (default `{"provider":"synthetic"}`), slack_webhook text null, updated_at, updated_by |
| `deployments` | id text PK, org_id, service_id, environment_id, service (text, denormalised name), image, ref, status (`pending`\|`rolling`\|`promoted`\|`rolled_back`\|`failed`), step_index int, weight int, reason text, risk jsonb, plan jsonb, source (`cli`\|`console`\|`ci`\|`runner`), created_by_user null, created_by_key null, created_at, updated_at; indexes (org_id, created_at desc), (org_id, service_id, created_at desc), (org_id, status) |
| `deployment_events` | id bigserial, deployment_id, at, kind (`created`\|`step`\|`verdict`\|`promoted`\|`rolled_back`\|`error`), message, weight int null, verdict jsonb null; index (deployment_id, id) |
| `jobs` | id, org_id, kind (`deploy`\|`rollback`), payload jsonb, status (`queued`\|`claimed`\|`done`\|`failed`\|`canceled`), requested_by_user null, requested_by_key null, claimed_by text null (the runner's name), claimed_by_key null, claimed_at, heartbeat_at null, attempts int (default 0), finished_at, error text, deployment_id text null, created_at |
| `audit_log` | id bigserial, org_id, actor_type (`user`\|`api_key`\|`system`), actor_id, actor_label, action (e.g. `service.create`, `policy.update`, `api_key.create`, `deployment.rollback`, `member.invite`), target, meta jsonb, at |
| `notifications` | id, org_id, user_id null (null = everyone in the org), kind, title, body, href, created_at, read_at null (for user-specific rows; per-user read state for org-wide rows lives in `notification_reads(notification_id, user_id, read_at)`) |

Deleting an org cascades to everything it owns.

Usage numbers in the console are **computed** from these tables (deploying services in the cycle, deploys this month, verified rollouts, auto-rollbacks, seats = memberships), with plan limits from a constant per plan.

## 4. Auth

- **Passwords:** Node `crypto.scrypt` (N=16384, r=8, p=1, 64-byte key, 16-byte salt), compared with `timingSafeEqual`.
- **Sessions:** opaque 32-byte random id in the cookie `alror_sid` (HttpOnly, SameSite=Lax, Secure in production, Path=/). Redis key `sess:<id>` holds `{userId, orgId, createdAt}`, with a 7-day sliding TTL. Logout deletes the key. Switching org updates `orgId`.
- **Login rate limit:** Redis `rl:login:<ip>` and `rl:login:<email>`, 10 failed attempts per 15 minutes (a successful login clears the email counter).
- **First run:** when there are no users, `/signup` creates the first user, the org and default environments (production, staging), and becomes the owner. Afterwards `/signup` creates a new user plus a new org. Joining an existing org happens through an **invite** link `/invite/<token>`; the token is shown once and is valid for 7 days.
- **API keys:** the token format is `alr_live_` followed by 32 base62 characters. It is shown once at creation. The `prefix` is the first 12 characters. Lookup is `sha256(token)`. Requests send `Authorization: Bearer <token>`; `last_used_at` is updated at most once a minute.
- **Roles:** owner and admin can manage members, keys, services and policies. A member can view everything and trigger deploys and rollbacks.
- **Console sessions on the API** get the scopes `deploy:read` and `deploy:write`, plus `config:write` for owners and admins (never `jobs:run`). Cookie-authenticated writes with a cross-site `Origin` are rejected.
- `/app/*` is guarded by `src/proxy.ts` (cookie present) **and** by session validation in the layout, the data layer and every action and route.

## 5. REST API v1 (`/api/v1`, JSON)

Auth is either the session cookie (console) or a Bearer API key (CLI, CI, runner). The org is implied by the key or the session. Errors look like `{"error":{"code":"not_found","message":"…"}}` with status 400 (malformed JSON), 401, 403, 404, 409, 422 (validation; also `unknown_service`, `unknown_environment`) or 429 (`rate_limited`, login only).

**Domain JSON is byte-compatible with the Go `internal/domain` structs:** snake_case keys in struct order, `bake` is integer nanoseconds, times are RFC 3339 (`RFC3339Nano` with trailing zeros trimmed, millisecond precision), `ref`, `reason`, event `weight` and `verdict` are omitted when empty.

| Method and path | Scope | Body / query | Response |
| --- | --- | --- | --- |
| `GET /whoami` | any | | `{org:{id,slug,name}, actor:{type,id,label,role?}, scopes}` |
| `GET /config` | deploy:read | | alror.yaml shape: `{project, services:[{name,paths,target,cluster,namespace,critical}], metrics:{provider,url,queries}, policy:{max_regression,alpha,auto_rollback,bake_scale}, notify:{slack_webhook}}` (`project` is the org slug) |
| `PUT /config` | admin key (scope `config:write`) or owner/admin session | same shape as GET; upserts services (by name; services not listed are left alone) and policy | 200 config |
| `GET /deployments` | deploy:read | `?limit=50` (1 to 1000) `&status=&service=&environment=&before=<deployment id or RFC3339>` | `Deployment[]`, newest first (created_at desc, id desc); `before` pages backwards and an unknown cursor id is 422 |
| `POST /deployments` | deploy:write | Deployment | 201 Deployment (upsert by id; the service must exist, or 422 `unknown_service`; an id owned by another org is 409) |
| `PUT /deployments/{id}` | deploy:write | Deployment | 200 Deployment; 404 if the id does not exist in the org (the client then POSTs) |
| `GET /deployments/{id}` | deploy:read | id or unique prefix | Deployment (404, or 409 `ambiguous`) |
| `GET /deployments/{id}/events` | deploy:read | | `Event[]` in order |
| `POST /deployments/{id}/events` | deploy:write | Event | 201 Event |
| `GET /rollbacks/recent` | deploy:read | `?since=RFC3339` (default 30 days ago) | `{"<service>": count}` of `rolled_back` deployments with server `updated_at` after `since` |
| `POST /jobs` | deploy:write | `{kind:"deploy", payload:{service, image, ref, environment, shadow?, risk_override?}}` or `{kind:"rollback", payload:{deployment_id, reason}}` | 201 Job |
| `POST /jobs/claim` | jobs:run | `{worker:"<runner name>"}` (`runner` accepted as an alias); optional `wait` seconds (0 to 25); long-poll up to 25 s (Redis `BRPOP jobs:<orgId>`; Postgres row claimed with `FOR UPDATE SKIP LOCKED`) | 200 Job, or 204 |
| `POST /jobs/{id}/heartbeat` | jobs:run | `{worker:"<runner name>"}` | 200 Job; 409 when the job is no longer claimed by this runner |
| `POST /jobs/{id}/finish` | jobs:run | `{status:"done"\|"failed", error?, deployment_id?, worker?}` | 200 Job; repeating the same status is idempotent (200); 409 when not claimed by this runner |
| `GET /jobs` | deploy:read | `?status=` | `Job[]`, newest first |
| `GET /stream` | session only | | `text/event-stream` |

`Deployment` and `Event` match Go: `{id, service, image, ref, risk, plan, status, step_index, weight, reason, created_at, updated_at}` and `{at, kind, message, weight?, verdict?}`. Deployments also carry two **optional** platform fields after `updated_at`:

- `environment`: environment name. On write it defaults to `production` on insert and is left unchanged on update when absent; an unknown name is 422 `unknown_environment`.
- `source`: `cli` \| `ci` \| `console` \| `runner`. On write, if the body has no `source`, the server takes it from the `X-Alror-Source` request header the CLI sends (`cli`, `ci` or `runner`), else from the auth type (session → `console`, API key → `cli`).

**Timestamps:** the server sets `updated_at = now()` on every deployment write and ignores the client's value. `created_at` is taken from the client on the first insert (now() when missing) and never changes afterwards.

`Job` = `{id, kind, payload, status, claimed_by, deployment_id, error, created_at, claimed_at, finished_at, heartbeat_at, attempts}`.

**Deploy job payload:** `environment` defaults to `production` (validated, 422 `unknown_environment`); the runner copies it onto the deployment. `risk_override` is a level (`"low"`, `"medium"`, `"high"`) or a 0 to 100 score (number or numeric string), stored as sent.

**Job leases:** a claim sets `heartbeat_at = now()` and increments `attempts`. A runner sends `POST /jobs/{id}/heartbeat` while it works (every 30 s). A job still `claimed` whose `heartbeat_at` is older than **2 minutes** is claimable again (the new claim replaces `claimed_by` and increments `attempts`). The claimer is identified by the `worker` name when the request sends one, otherwise by the API key. A heartbeat or finish from a runner that lost its lease returns 409, and that runner must stop the job without finishing it.

**Side effects on writes:** every deployment upsert or event append publishes a JSON message to Redis channel `org:<orgId>:events`, with `type` one of `deployment.updated` (`{type, deployment, at}`), `deployment.event` (`{type, deployment_id, event, at}`) or `job.updated` (`{type, job, at}`; on enqueue, claim, finish and cancel). The stream relays these as SSE (`event: <type>`, `data: <the JSON message>`), with a `: ping` comment every 15 s. A terminal status (promoted / rolled_back / failed) creates a notification and an audit row.

**Auth helper endpoints** (JSON counterparts of the console forms, used by scripts and tests): `POST /auth/signup {email, password, name?, org?}` → 201 and sets the session cookie; `POST /auth/login {email, password}` → 200 and sets the cookie (429 when rate limited); `POST /auth/logout` → 204; `GET /auth/session` → `{user, org, role, orgs}` or 401.

## 6. CLI and runner (Go, repo `alror`)

- **Server selection:**
  - The server URL comes from `alror.yaml` `server:`, the `ALROR_SERVER` env var or `~/.config/alror/credentials.json` (on Windows `%APPDATA%\alror\credentials.json`).
  - The key comes from `ALROR_API_KEY` or the credentials file.
  - `alror login --server URL --key alr_live_…` verifies the key with `/whoami` and saves both (mode 0600). `alror logout` removes them.
- **Connected mode** (server and key present):
  - The store is `store.Remote`, which implements the existing `store.Store` over the API.
  - Config is fetched from `/config`, and `alror.yaml` becomes optional (local `services[].paths` may still be used for git path mapping if present; the server wins).
  - Risk scoring is still computed locally from git; recent rollbacks come from `/rollbacks/recent`.
  - Every command works in connected mode, and every request sends `X-Alror-Source`.
- **`alror runner [--name host] [--once]`:** loops claim → execute (heartbeat every 30 s) → finish.
  - A deploy job builds the Deployment with risk (`risk_override`, or medium when no git context is available) and the job's `environment`, and runs the engine with the remote store.
  - A rollback job loads the deployment and calls `Engine.Rollback`.
  - It handles SIGINT by finishing the current job, stops a job whose heartbeat returns 409, logs with the CLI UI style, and backs off when the server is unreachable.
- **`alror config pull`** writes the server config to `alror.yaml`; **`alror config push`** uploads services and policy from `alror.yaml` (admin keys only, `PUT /config`).

## 7. Seed and demo

The seeded `.alror/` files stop being the console's data source. `npm run db:migrate` applies migrations; `npm run db:seed` (dev only, idempotent: it deletes and recreates the org) creates a demo org "acme" with an owner `admin@acme.test` and a member `dev@acme.test` (both `alror-demo`), 12 services, policy, environments and 30 days of history written **through the same data layer** as real writes, a few audit and notification rows, a pending invite, plus one API key (all four scopes) printed to the terminal and written to `.alror-dev-key` in the workspace app (git-ignored). With an empty database, `/login` points to `/signup`, which sets up the first account and org.
