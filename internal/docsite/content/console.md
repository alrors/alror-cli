# Web console

The console is the web half of Alror and your **Alror workspace**: a login-protected dashboard that the CLI connects to in [connected mode](/connected). Deploys and rollbacks started from the console are executed by [`alror runner`](/connected#runner).

## Run it

The workspace runs on Next.js with PostgreSQL 16 and Redis 7 in Docker. Copy `.env.example` to `.env.local` first (`DATABASE_URL`, `REDIS_URL`, `ALROR_SESSION_SECRET`, `ALROR_PUBLIC_URL`).

```bash
cd web
docker compose up -d     # PostgreSQL and Redis
npm run db:migrate       # apply the SQL migrations
npm run db:seed          # dev only: demo org "acme", 12 services, 30 days of history and an API key
npm run dev
```

Then open `http://localhost:3000`. Sign in with the seeded owner, or sign up to create your own user and org (on an empty database, the first sign-up starts the onboarding flow):

| Field | Value |
| --- | --- |
| Email | `admin@acme.test` |
| Password | `alror-demo` |

## Connect the CLI

Create an API key under **Settings → API keys** (it is shown once), then:

```bash
alror login --server http://localhost:3000 --key alr_live_…
alror runner        # executes Deploy and Roll back actions from the console
```

See [Connected mode](/connected) for scopes, CI setup and the runner.

## Pages

| Page | Shows |
| --- | --- |
| Deployments | Every release with risk, status and traffic |
| Deployment detail | Rollout stages, verdicts per metric, the full event log |
| Services | Per-service deploy counts, last release and rollback rate |
| Insights | Deploy frequency, change failure rate, AI-assisted vs human |
