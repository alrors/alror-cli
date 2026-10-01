# CLI reference

Global flags work on every command.

| Flag | Meaning |
| --- | --- |
| `-C, --dir <path>` | Run as if started in this directory |
| `--json` | Machine-readable output |
| `--no-color` | Plain output (also honours `NO_COLOR`) |
| `--color auto\|always\|never` | Force colour on or off, e.g. `always` for CI logs that render ANSI |
| `--no-anim` | Turn off terminal animations (also `ALROR_NO_ANIM=1`) |
| `--server <url>` | Alror workspace URL to connect to (overrides `ALROR_SERVER`, `alror.yaml` and the saved login) |
| `--api-key <key>` | Workspace API key (overrides `ALROR_API_KEY` and the saved login) |
| `--local` | Force local mode (`alror.yaml` and `.alror/`) even when a server is configured |

Animations (spinners, traffic sweeps, the printed receipt) only run on an interactive terminal. In CI (`CI` is set), with `--json`, when piped, or with `NO_COLOR`, output is plain and instant.

## `alror init`

Creates `alror.yaml`, `.alror/` and a `.gitignore` entry. `--force` overwrites an existing config.

## `alror risk`

Scores the diff between `--base` (default `origin/main`, then `main`) and `HEAD`. If nothing is committed yet, it scores uncommitted changes.

## `alror deploy`

| Flag | Meaning |
| --- | --- |
| `-s, --service` | Service from `alror.yaml` or the workspace config (required) |
| `-i, --image` | Image or artifact to roll out (required) |
| `--ref` | PR or commit, shown in history and Slack |
| `--risk N` | Skip git and use this score |
| `--regress m=f` | Synthetic metrics only: make the canary worse |
| `--fast` | Compress bake times for demos |
| `--shadow` | Recommend rollbacks without acting |
| `--env` | Target environment (default `production`); recorded in connected mode, ignored in local mode |

Exit codes: `0` promoted, `2` rolled back, `1` error.

## `alror status [id]`

Lists recent deployments, or shows one with its full event log. Alias: `history`.

## `alror github check`

Scores a pull request and posts an **Alror / change-risk** check run, plus a sticky comment with `--comment`. `--fail-above N` exits `3` when the score is above `N`, and `--dry-run` prints the Markdown instead of posting. See [GitHub](/github).

## `alror rollback <id>`

Reverses a deployment through its target driver. `--reason` is recorded in the log.

## `alror doctor`

Checks git, `alror.yaml`, the state directory, target tools (for example `kubectl`) and metrics connectivity. When connected, it also checks that the workspace is reachable, that the key is valid and which scopes it has.

## `alror login`

Connects to an Alror workspace: verifies an API key with `/whoami` and saves the server and key for your user (mode `0600`). `--server` and `--key` are prompted for on a terminal when missing. See [Connected mode](/connected).

## `alror logout`

Removes the saved server and key.

## `alror whoami`

Shows the connected server, workspace, actor, key prefix and scopes, and where the server and key came from.

## `alror runner`

Runs deploys and rollbacks queued from the Alror console, inside your own infrastructure (like a CI runner). It claims jobs from the workspace and runs them with the rollout engine.

| Flag | Meaning |
| --- | --- |
| `--name` | Reported as `claimed_by` (default: hostname) |
| `--once` | One claim (long-poll); run the job if one arrives, then exit |
| `--concurrency N` | Jobs to run in parallel (default `1`) |

Ctrl-C stops claiming and lets the current job finish; a second Ctrl-C aborts it, and the canary is rolled back. A rolled-back release is job status `done`.

## `alror config pull` / `alror config push`

`pull` writes `alror.yaml` from the workspace config and keeps its `server:` line. `push` upserts the services and policy from `alror.yaml` (needs `config:write`). Both print a diff summary; `--dry-run` writes or pushes nothing.

## `alror docs`

Serves these docs at `http://127.0.0.1:4100`. Use `--open` to launch a browser and `--addr` to change the port.
