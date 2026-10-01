# Quickstart

Five minutes, no infrastructure needed: the default target and metrics provider are simulated.

## 1. Build

```bash
cd alror
go build -o bin/ ./cmd/...
```

This produces `alror` (the CLI) and `alror-docs` (the standalone docs server) in `bin/`. On Windows they are `alror.exe` and `alror-docs.exe`.

## 2. Initialise a repository

```bash
cd your-repo
alror init
```

`alror init` writes `alror.yaml`, creates `.alror/` and adds it to `.gitignore`.

## 3. Score your change

```bash
alror risk
```

You get a 0–100 score, the factors behind it and the rollout plan it implies.

## 4. Roll out

```bash
alror deploy -s checkout-api -i registry/checkout:1.42 --ref "#4821"
```

Watch it step through the canary, verify each bake and print a release receipt.

## 5. Watch a rollback

```bash
alror deploy -s checkout-api -i registry/checkout:1.43 --regress error_rate=1.8
```

`--regress` makes the simulated canary's error rate 80% worse. The verifier catches it and the engine rolls back. The command exits with code `2`, so CI can tell a rollback from a crash.

## 6. Look back

```bash
alror status            # recent deployments
alror status dep_2026   # one deployment and its event log (prefix match)
```
