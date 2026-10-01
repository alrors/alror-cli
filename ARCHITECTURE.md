# Alror architecture

Alror makes every release safe by default. It scores a change, rolls it out in steps, checks each step against live metrics, and rolls it back on its own when users are hurt.

The engine runs inside the `alror` CLI, usually as one step in CI. In **local mode** it keeps its state on the file system (`alror.yaml` + `.alror/`). In **connected mode** the CLI talks to your **Alror workspace** (the self-hosted web platform: Next.js, PostgreSQL, Redis) over its REST API with an API key, and **`alror runner`** executes deploys and rollbacks queued from the console inside your own infrastructure.

## System at a glance

```
                      ┌──────────────────── alror CLI (CI step or laptop) ─────────────────────┐
                      │                                                                         │
  git diff + log ────▶│  risk scorer ──▶ planner ──▶ rollout engine ──▶ verifier                │──▶ receipt + exit code
  (base...HEAD)       │      │                         │      │            ▲                    │     0 promoted
                      │      │ rollbacks in 30 days    │      │            │ samples            │     2 rolled back
                      │      ▼                         ▼      ▼            │                    │     1 error
                      │  file-system store ◀──── events    driver ──▶ metrics provider          │
                      │   (.alror/)                       (k8s, sim)  (Prometheus, Datadog,     │
                      │      │                               │          synthetic)              │
                      └──────┼───────────────────────────────┼──────────────────────────────────┘
                             │                               ▼
                             │                     your cluster: Argo Rollouts
                             ▼
              docs server (alror docs)      ──  embedded Markdown, served offline
```

In connected mode the file-system store is swapped for `store.Remote`, and the console's actions become jobs:

```
 developer / CI ── alror CLI ─────────┐  Authorization: Bearer alr_live_…
                                       ▼
 browser ── session ──▶ Alror workspace (Next.js /api/v1) ──▶ PostgreSQL (deployments, events, jobs)
                                       ▲                      Redis (job signal, live updates)
 alror runner (your infra) ── POST /jobs/claim (long-poll) ─┘
        │  runs the same engine: driver ──▶ your cluster, metrics provider, store.Remote
        └─ POST /jobs/{id}/finish  (done | failed, deployment_id)
```

The API is specified in [docs/platform-contract.md](docs/platform-contract.md).

### Design principles

1. **Rules decide, AI explains.** Every risk point comes from a named factor. Every rollback comes from a statistical test against a threshold. A language model may summarise, but it never gates a release.
2. **Keep the customer's CI.** Alror is one step after the build, not a pipeline platform.
3. **Fail safe.** Missing data never passes a step. Ctrl-C and driver errors roll the canary back. CI can tell a rollback (exit 2) from a crash (exit 1).
4. **Boring state.** Plain JSON and JSONL files, written atomically, readable by humans and tools.
5. **Nothing phones home.** No network calls happen unless you configure a metrics provider, a Slack webhook or a real target. Docs and assets are embedded.

## Release lifecycle

```
pending ──▶ rolling ─┬─ every step passes ───────────────▶ promoted
                     ├─ a step fails, auto_rollback: true ─▶ rolled_back
                     ├─ a step fails, shadow mode ──────────▶ continue, recommendation logged
                     └─ driver or provider error ───────────▶ failed (rollback attempted)
```

Each step runs in this order:

1. **Shift:** `driver.SetWeight(weight)` moves canary traffic to the step's weight.
2. **Bake:** wait for `step.Bake × policy.bake_scale`.
3. **Sample:** for each metric, the provider returns canary and baseline series for the bake window.
4. **Judge:** the verifier returns a verdict. A metric fails only when it is both:
   - worse than the baseline by more than `policy.max_regression[metric]`, and
   - statistically worse (one-sided Mann-Whitney U, p < `policy.alpha`).

   Fewer than 8 samples per side never passes.
5. **Decide:** continue, roll back, or (in shadow mode) log a recommendation.

The engine saves the deployment and appends an event on every transition, so an interrupted run leaves an accurate record.

## Risk scoring

The scorer is a pure function `Score(config, Change) → RiskReport`. `Change` is collected from `git diff --numstat base...HEAD` and `git log`, plus recent rollbacks read from the store.

| Factor | Points |
| --- | --- |
| Diff size | +8 / +15 / +22 / +30 at 50 / 200 / 500 / 1000 lines |
| Many files | +8 (>20), +12 (>50) |
| Blast radius | +8 per extra service touched (max +20) |
| Critical service | +12 |
| Sensitive paths (auth, payment, migration, IAM, …) | +10 per area (max +20) |
| No tests changed / well tested | +10 / −5 |
| AI-authored (bot authors, `Co-Authored-By` trailers) | +10, never a block on its own |
| Recent rollbacks on the same services | +6 each (max +18) |
| Docs only | score is 2 |

| Level | Score | Plan |
| --- | --- | --- |
| Low | 0–34 | 25% → 100%, 5 min bake |
| Medium | 35–69 | 5% → 25% → 50% → 100%, 10 min bakes |
| High | 70–100 | 1% → 5% → 25% → 50% → 100%, 15 min bakes |

## Connected mode

`cli.project()` decides the mode for every command:

1. **Resolve the server and key.** Server: `--server` > `ALROR_SERVER` > `alror.yaml` `server:` > saved login. Key: `--api-key` > `ALROR_API_KEY` > saved login (`os.UserConfigDir()/alror/credentials.json`, mode 0600, written by `alror login`). A saved key is only sent to the server it was saved for. `--local` skips this step.
2. **No server:** local mode, exactly as before: validate `alror.yaml`, open `store.FS` on `.alror/`.
3. **Server and key:** fetch `GET /config` (the alror.yaml shape). A local `alror.yaml` is optional; when present, its `services[].paths` overlay the workspace's for git path mapping, and the workspace wins on everything else. The store is `store.Remote`.

`store.Remote` implements `Store` over the API with the same semantics as `store.FS`; a shared conformance suite (`internal/store/store_test.go`) runs against both, with the in-memory fake in `internal/api/apitest` standing in for the workspace:

| Store method | API call |
| --- | --- |
| `Save` | `POST /deployments` the first time an id is seen (upsert by id), then `PUT /deployments/{id}`; a 404 on PUT falls back to POST. The server owns `updated_at`: the returned timestamps are copied into the struct |
| `Get` | `GET /deployments/{id}` (unique prefix; 404 and 409 `ambiguous` map to `ErrNotFound`) |
| `List` | `GET /deployments?limit=500`, then `&before=<last id>` until a short page (everything, newest first) |
| `Append` / `Events` | `POST` / `GET /deployments/{id}/events` |
| `RecentRollbacks` | `GET /rollbacks/recent?since=` |

`internal/api` is the typed client: Bearer auth, `User-Agent: alror-cli/<version>`, `X-Alror-Source: cli | ci | runner`, a 20 s timeout per attempt (40 s for the claim long-poll), contract errors mapped to `ErrUnauthorized`, `ErrForbidden`, `ErrNotFound`, `ErrConflict` and `ErrValidation`, and exponential backoff on network errors and 5xx for idempotent calls only. Event appends, job enqueues and claims are never retried, so they cannot happen twice. Risk scoring stays local (git), with recent rollbacks from the API. Every command (`risk`, `deploy`, `status`, `rollback`, `github check`, `doctor`) works in both modes; the deploy header shows `state: acme workspace (http://…)` or `state: .alror/`.

### The runner

`alror runner` (package `internal/runner`) runs deploys and rollbacks queued from the console inside your own infrastructure, like a CI runner. It is a long-running loop, one per slot (`--concurrency`):

```
claim (POST /jobs/claim, long-poll ≤ 25 s) ─▶ 204: claim again
        │ 200 job
        ▼
deploy job:   GET /config → risk (risk_override, or medium: "no git context in runner")
              → plan → driver from the service target → Engine.Run with store.Remote
rollback job: Store.Get → Engine.Rollback
        │
        ▼
finish (POST /jobs/{id}/finish): done | failed, deployment_id, error
```

- A rolled-back release is `done`: rolling back is the engine doing its job. `failed` means the job could not run (bad payload, unknown service, driver error).
- The deployment is saved before any traffic moves, so a rejected write fails the job instead of running an unrecorded rollout, and saved again at the end because the engine ignores write errors.
- **Leases:** while a job runs, the runner calls `POST /jobs/{id}/heartbeat` every 30 s; the server requeues a claimed job after 2 minutes without one. A 409 means another runner owns the job now: the runner cancels the job's context (the engine rolls the canary back) and does not call finish.
- Deployments created in connected mode carry `environment` (`deploy --env`, or the job's `environment`) and `source` (`cli`, `ci` or `runner`). Both are `omitempty`, so local files are unchanged.
- **Graceful stop:** SIGINT/SIGTERM cancels the claim context only; the running job finishes under its own context. A second signal cancels that too, and the engine rolls the canary back.
- **Outages:** claim failures back off exponentially (1 s to 30 s, with jitter). Finishing a job is retried for longer, because losing a result is worse than waiting. A 401 or a missing `jobs:run` scope stops the runner.
- Logs are one line per event in the CLI's style, with no animations, or JSON lines with `--json`.

### Public SDK

`pkg/alror` is the importable Go SDK: type aliases of `internal/domain` (so the JSON is identical), `Config`, `Job`, a `Client` with context-aware methods for every endpoint, typed errors and options (`WithHTTPClient`, `WithUserAgent`, `WithTimeout`). `pkg/alror/risk` re-exports the risk scorer and `FromGit`.

## Repository layout (file system)

```
alror/
├── ARCHITECTURE.md          this document
├── README.md                quick start
├── go.mod
├── cmd/
│   ├── alror/               the CLI binary
│   ├── alror-docs/          standalone docs server
│   └── alror-devserver/     in-memory fake of the workspace API, for demos
├── pkg/alror/               public Go SDK (client, types); pkg/alror/risk: the scorer
├── internal/
│   ├── domain/              shared types: Deployment, Plan, Step, Verdict, Event, RiskReport
│   ├── config/              alror.yaml: load, defaults, validation, path→service mapping
│   ├── risk/                git change collection + rule-based scorer
│   ├── rollout/             planner (risk → plan) and engine (state machine)
│   ├── verify/              Mann-Whitney U test and per-metric verdicts
│   ├── metrics/             Provider interface: synthetic, Prometheus, Datadog
│   ├── driver/              Driver interface: simulated, Kubernetes (Argo Rollouts), ECS (phase 2)
│   ├── store/               Store interface: FS (.alror/) and Remote (workspace API)
│   ├── api/                 typed client for the workspace API; api/apitest: in-memory fake
│   ├── credentials/         saved login and server/key precedence
│   ├── runner/              `alror runner`: claim → execute → finish
│   ├── notify/              Slack webhook
│   ├── cli/                 cobra commands (init, risk, deploy, status, rollback, doctor, docs,
│   │                        login, logout, whoami, runner, config pull/push)
│   │   └── ui/              terminal design system: palette, LED meter, stages, receipt
│   └── docsite/             docs server; content/*.md embedded with go:embed
├── examples/                alror.yaml and a GitHub workflow to copy
└── bin/                     build output (git-ignored)
```

### State on disk

Each repository gets its own state directory, next to `alror.yaml`:

```
.alror/
├── deployments/<id>.json    current state (rewritten atomically: temp file + rename)
└── events/<id>.jsonl        append-only event log, one JSON object per line
```

IDs are `dep_<UTC timestamp>_<6 hex>`. They sort by time, and every command accepts a unique prefix.

## Interfaces (extension points)

```go
type Driver interface {            // internal/driver
    SetWeight(ctx, svc, image, weight) error
    Promote(ctx, svc, image) error
    Rollback(ctx, svc) error
}

type Provider interface {          // internal/metrics
    Sample(ctx, service, metric, track, window) ([]float64, error)
}

type Store interface {             // internal/store
    Save(*Deployment) error; Get(id) (*Deployment, error); List() ([]*Deployment, error)
    Append(id, Event) error; Events(id) ([]Event, error); RecentRollbacks(since) (map[string]int, error)
}

type Notifier interface {          // internal/notify
    Notify(ctx, *Deployment, summary) error
}
```

To add a target, metrics backend or notification channel, implement one interface and register it in the matching `New`/`For` switch. The engine stays unchanged.

## Web console

The Alror workspace is a separate web app (Next.js, PostgreSQL, Redis) that serves the console and the `/api/v1` REST API described in [docs/platform-contract.md](docs/platform-contract.md). The CLI connects to it in connected mode. Console actions (Deploy, Roll back) enqueue jobs that `alror runner` executes, so the console never shells out to the CLI and never touches your infrastructure.

Connected mode reuses `domain`, `risk`, `rollout`, `verify`, `metrics` and `driver` as they are: only the `Store` and the place the engine runs (CLI or runner) change.
