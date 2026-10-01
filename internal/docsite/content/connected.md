# Connected mode

By default Alror runs in **local mode**: it reads `alror.yaml` and keeps state in `.alror/`. Connect the CLI to your **Alror workspace** (the self-hosted Alror web platform) with an API key, and the same commands read services and policy from the workspace and write deployments and events through its API (`/api/v1`). The console then shows every release live and can queue deploys and rollbacks for an **`alror runner`** to execute inside your own infrastructure.

| | Local mode | Connected mode |
| --- | --- | --- |
| Services and policy | `alror.yaml` | the workspace (`GET /api/v1/config`); local `services[].paths` still map git paths |
| Deployments and events | `.alror/` files | the workspace, through `store.Remote` |
| Recent rollbacks (risk) | `.alror/` | `GET /api/v1/rollbacks/recent` |
| Risk scoring | local git | local git (unchanged) |
| Console Deploy / Roll back | n/a | jobs, executed by `alror runner` |

## Connect

Create an API key in the console (**Settings → API keys**). It starts with `alr_live_` and is shown once.

```bash
alror login --server http://localhost:3000 --key alr_live_…
```

`login` verifies the key with `GET /whoami`, prints the workspace and actor, and saves the server and key to `credentials.json` in your user config directory (`~/.config/alror/` on Linux, `~/Library/Application Support/alror/` on macOS, `%APPDATA%\alror\` on Windows) with mode `0600`. On a terminal it prompts for anything you leave out, and the key is not echoed.

```bash
alror whoami     # server, workspace, actor, key prefix and scopes
alror logout     # remove the saved credentials
```

### Where the server and key come from

Highest first:

| | Server | Key |
| --- | --- | --- |
| 1 | `--server` flag | `--api-key` flag |
| 2 | `ALROR_SERVER` | `ALROR_API_KEY` |
| 3 | `server:` in `alror.yaml` | |
| 4 | saved login | saved login |

A saved key is only sent to the server it was saved for. If `alror.yaml` points at another server, set `ALROR_API_KEY` or log in to that server. `ALROR_CREDENTIALS` overrides the credentials file path.

In CI, set `ALROR_SERVER` and `ALROR_API_KEY` as secrets; no login step is needed.

## Connected vs local

When a server and a key resolve, every command runs connected: `risk`, `deploy`, `status`, `rollback`, `github check` and `doctor`. With no server, nothing changes from local mode. Pass `--local` to force local mode for one command.

- `alror.yaml` is optional when connected. If present, its `services[].paths` are used for git path mapping; the workspace wins on everything else.
- The deploy header shows where state lives: `state: acme workspace (http://localhost:3000) · env production` or `state: .alror/`.
- `alror deploy --env staging` records the target environment (default `production`), and each deployment records its source: `cli`, `ci` (when `CI` or `GITHUB_ACTIONS` is set), `console` or `runner`. Local `.alror/` files do not store either.
- The workspace owns `updated_at`; the CLI takes the timestamps the server returns.
- `alror doctor` prints `connected to acme at http://localhost:3000`, checks that the key is valid and lists its scopes.

| Scope | Needed for |
| --- | --- |
| `deploy:read` | `status`, `risk`, config |
| `deploy:write` | `deploy`, `rollback` |
| `jobs:run` | `alror runner` |
| `config:write` | `alror config push` (admin keys) |

## Runner

`alror runner` runs deploys and rollbacks queued from the Alror console, inside your own infrastructure, like a CI runner. It needs network access to the workspace and to your targets (for example `kubectl` for Kubernetes), and nothing needs to reach into your network.

```bash
alror runner                          # name defaults to the hostname
alror runner --name prod-runner-1 --concurrency 2
alror runner --once                   # one long-poll; run a job if one arrives, then exit
```

- **Deploy jobs** take `service`, `image`, `ref`, `environment` (copied onto the deployment, whose source is `runner`), `shadow` and `risk_override` (`low`, `medium` or `high`, scored as 20, 50 or 85, or a score from 0 to 100). A runner has no git context, so without `risk_override` the release is scored as medium risk with the factor "no git context in runner". The driver comes from the service's target and metrics from the workspace config.
- **Rollback jobs** load the deployment and roll it back through its driver.
- A release that the engine **rolls back** is still a job with status `done`: the rollback is an outcome, not a failure. Jobs are `failed` only when they could not run (bad payload, driver error, unknown service).
- **Leases:** while a job runs, the runner sends a heartbeat every 30 s. A claimed job with no heartbeat for 2 minutes is requeued for another runner. If a heartbeat returns 409 (the lease was lost to another runner), the runner aborts the job, the engine rolls the canary back, and the runner does not report an outcome.
- **Stopping:** Ctrl-C or `SIGTERM` stops claiming and lets the current job finish. A second Ctrl-C aborts it, and the engine rolls the canary back.
- **Workspace down:** the runner retries with exponential backoff (1 s up to 30 s) and logs when the workspace is reachable again. A rejected key or a missing `jobs:run` scope stops it.
- Logs are one line per event, with no animations. Add `--json` for JSON lines.

```
14:02:11  · runner prod-runner-1 · acme workspace (http://localhost:3000) · concurrency 1
14:02:30  · job 3f2a91c0 claimed · deploy checkout-api registry/checkout:1.42 → production
14:02:30  · dep_20261002T140230_a1b2c3 · checkout-api registry/checkout:1.42 · risk 50 (medium) · 5%→25%→50%→100% on kubernetes
14:12:31  ✓ dep_20261002T140230_a1b2c3 · 5% verified · No regression across 2 metrics
…
14:42:33  ✓ job 3f2a91c0 done in 40m3s · dep_20261002T140230_a1b2c3
```

## Sync alror.yaml

```bash
alror config pull             # write alror.yaml from the workspace (keeps your server: line)
alror config push             # upsert services and policy from alror.yaml (admin keys)
alror config push --dry-run   # only show the diff
```

Both print a diff summary: `+` added, `-` removed, `~` changed. `push` never deletes services that exist only in the workspace, and it leaves metrics and notification settings as they are.

## Try it without a workspace

`alror-devserver` serves an in-memory fake of the workspace API for demos and development. State is lost when it exits.

```bash
go build -o bin/ ./cmd/...
bin/alror-devserver &                                   # http://127.0.0.1:3100
bin/alror login --server http://127.0.0.1:3100 --key alr_live_TestKey0123456789abcdefghijklmnop
bin/alror deploy -s checkout-api -i app:v2 --risk 20
```

## Library use

The Go SDK in `pkg/alror` wraps the same API (`alror.NewClient(server, key)`), and `pkg/alror/risk` exposes the risk scorer. See the package docs.
