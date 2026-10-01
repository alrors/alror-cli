# Rollouts and verification

The engine is a small state machine. It persists every transition, so an interrupted run leaves an accurate record behind.

```
pending → rolling ─┬─ step passes ──────────────▶ next step … → promoted
                   ├─ step fails, auto_rollback ─▶ rolled_back
                   ├─ step fails, shadow mode ───▶ continue (recommendation logged)
                   └─ driver error ──────────────▶ failed (rollback attempted)
```

## Each step

1. The driver shifts the canary to the step's weight.
2. The engine waits for the bake period, scaled by `bake_scale`.
3. The metrics provider returns canary and baseline samples for each metric.
4. The verifier judges each metric.

## The verifier

A metric fails only when both of these are true:

- the canary median is worse than the baseline median by more than `max_regression`, and
- a one-sided Mann-Whitney U test gives p < `alpha`.

Requiring both keeps false rollbacks rare: a noisy blip that isn't significant passes, and so does a significant but tiny drift. With fewer than 8 samples per side, the step does not pass. Missing data is never treated as healthy.
