# State on the file system

In the MVP, Alror keeps all state in plain files next to `alror.yaml`. You can read them, back them up, or upload them as CI artifacts.

```
.alror/
  deployments/<id>.json    current state of each deployment (atomic rewrite)
  events/<id>.jsonl        append-only event log, one JSON object per line
```

- **Atomic writes:** each deployment file is written to a temp file, then renamed.
- **Append-only events:** history is never rewritten.
- **IDs sort by time:** `dep_<UTC timestamp>_<random>`, and commands accept any unique prefix.
- **Feeds risk scoring:** rollbacks in the last 30 days raise the score of new changes to the same service.

This is **local mode**. In [connected mode](/connected), `store.Remote` implements the same `Store` interface over your Alror workspace API, which keeps deployments and events in PostgreSQL; `.alror/` is not used. Pass `--local` to use the files even when a server is configured.
