# GitHub

Alror plugs into GitHub in two places. Every pull request gets a risk check and a sticky comment, and every deploy from Actions writes a release receipt to the job summary.

## Risk check on pull requests

```yaml
jobs:
  risk:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      checks: write
      pull-requests: write
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - uses: manaskumar3003/alror-cli/actions/check@main
        with:
          comment: "true"
          fail-above: "0"
```

The PR shows an **Alror / change-risk** check with the score, every factor behind it and the rollout plan the change will get. A single comment is kept up to date on each push, so Alror never adds a new comment per push.

| Input | Default | Meaning |
| --- | --- | --- |
| `comment` | `true` | Keep a sticky PR comment |
| `fail-above` | `0` | Fail the check above this score; `0` keeps it advisory |
| `working-directory` | `.` | Folder with `alror.yaml` |
| `version` | `latest` | Alror version to install |

| Conclusion | When |
| --- | --- |
| `success` | Low risk |
| `neutral` | Medium or high risk (advisory) |
| `failure` | Score above `fail-above` |

Outputs: `score`, `level`, `plan` (e.g. `5,25,50,100`) and `conclusion`.

## Deploy from Actions

```yaml
- uses: manaskumar3003/alror-cli/actions/deploy@main
  id: alror
  with:
    service: checkout-api
    image: registry.example.com/checkout-api:${{ github.sha }}
```

The job summary gets the release receipt: status, plan, and per-metric canary vs baseline results. Outputs are `deployment-id`, `status` and `weight`. A rollback exits with code `2`, which fails the job so the bad release is visible.

## Run it yourself

The actions are thin wrappers around the CLI:

```bash
alror github check --comment          # inside Actions: posts the check and comment
alror github check --dry-run          # anywhere: prints the Markdown it would post
alror github check --fail-above 85    # exits 3 when the score is above 85
```

Alror reads `GITHUB_TOKEN` (or `ALROR_GITHUB_TOKEN`), `GITHUB_REPOSITORY`, the PR number and head commit from the event payload, and `GITHUB_API_URL`, so GitHub Enterprise Server works too.

## Permissions

| Permission | Why |
| --- | --- |
| `checks: write` | Create the check run |
| `pull-requests: write` | Create or update the sticky comment |
| `contents: read` | Read the diff |

If a permission is missing, Alror prints a warning and carries on. It never fails your build because it couldn't post.
