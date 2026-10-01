# Targets and metrics

## Targets

| Target | Status | How it works |
| --- | --- | --- |
| `simulated` | Ready | Records traffic changes in memory. The default, for demos and tests. |
| `kubernetes` | Ready | Drives an Argo Rollouts resource named after the service, using `kubectl argo rollouts`. |
| `ecs` | Phase 2 | Will shift ALB target-group weights. |

## Metrics providers

| Provider | Needs |
| --- | --- |
| `synthetic` | Nothing. Realistic noise, plus `--regress` to inject failures. |
| `prometheus` | `metrics.url` and PromQL `queries`. Works with Thanos, Mimir and VictoriaMetrics. |
| `datadog` | `DD_API_KEY` and `DD_APP_KEY` env vars, plus `queries`. |
