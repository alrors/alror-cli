# Alror

Alror ships every change at the speed of AI, safely. It scores the risk of each change, rolls it out in steps, verifies each step against live metrics and rolls back on its own when a release hurts users.

```
merge ──▶ risk score ──▶ rollout plan ──▶ canary 5% ─▶ bake ─▶ verify ─┬─▶ next step … ─▶ promoted
                                                                      └─▶ rolled back (with a reason)
```

## What's in the box

| Piece | What it does |
| --- | --- |
| `alror` CLI | Scores changes, runs rollouts from CI or a laptop, shows history, rolls back |
| Engine | Risk scorer, rollout planner, statistical verifier, drivers and metrics providers |
| File-system store | `.alror/` holds every deployment and its append-only event log |
| Web console | Login-protected dashboard over the same state ([Web console](/console)) |
| Docs server | This site, embedded in the binary: `alror docs` |

## Principles

- **Rules decide, AI explains.** Every risk point and every rollback comes from an auditable rule or a statistical test.
- **Keep your CI.** Alror runs as one step after your build.
- **Fail safe.** Too little data never passes a step, and Ctrl-C rolls the canary back.

Start with the [Quickstart](/quickstart).
