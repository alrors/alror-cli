# Architecture

The full design is in `ARCHITECTURE.md` at the root of the repository. In short:

```
             ┌──────────── alror CLI (CI step or laptop) ────────────┐
 git diff ──▶│ risk scorer ─▶ planner ─▶ rollout engine ─▶ verifier  │──▶ receipt / exit code
             │                    │            │            ▲        │
             │                    ▼            ▼            │        │
             │               file-system    driver       metrics     │
             │                 store       (k8s, sim)    provider    │
             └──────────────────────┬────────────────────────────────┘
                                    │ .alror/ (JSON + JSONL)
                                    ▼
                           web console (Next.js, login)
```

| Package | Responsibility |
| --- | --- |
| `internal/domain` | Shared types: deployment, plan, verdict, event |
| `internal/config` | Loads and validates `alror.yaml` |
| `internal/risk` | Git change collection and rule-based scoring |
| `internal/rollout` | Planner and engine (state machine) |
| `internal/verify` | Mann-Whitney U test and per-metric verdicts |
| `internal/metrics` | Synthetic, Prometheus and Datadog providers |
| `internal/driver` | Simulated, Kubernetes (Argo Rollouts) and ECS (phase 2) |
| `internal/store` | File-system persistence |
| `internal/notify` | Slack webhook |
| `internal/cli` | Commands and the terminal UI |
| `internal/docsite` | This documentation server |
