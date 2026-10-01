# alror

The [Alror](https://github.com/manaskumar3003/alror-cli) CLI for Node.js users. Alror scores the risk of each change, rolls it out in steps, checks it against live metrics and rolls it back on its own when it hurts users.

This package does not reimplement the CLI in JavaScript. It ships the same native Go binary that GitHub releases publish, using the pattern esbuild, turbo and biome use: one small launcher package plus one `@alror/cli-<os>-<cpu>` package per platform, selected through `optionalDependencies`.

> **Status: not published to npm yet.** Until the first npm release, install from a GitHub release or from local tarballs (see [Installing before the npm release](#installing-before-the-npm-release)).

## Install

```sh
npx alror --help            # run without installing
npm i -g alror              # global install
npm i -D alror              # per-project (CI, npm scripts)
```

Supported platforms: Linux, macOS and Windows on x64 and arm64. Node.js 18 or later.

## Usage

```sh
npx alror init
npx alror risk
npx alror deploy -s checkout-api -i registry.example.com/checkout:v2
npx alror status
npx alror-docs              # offline documentation
```

Run `alror --help` for every command, or see the [documentation](https://github.com/manaskumar3003/alror-cli).

Everything runs locally by default. To use your Alror workspace (connected mode), set `ALROR_SERVER` and `ALROR_API_KEY`, or run `alror login`. To call the Alror platform API from JavaScript, use [`@alror/sdk`](https://github.com/manaskumar3003/alror-cli/tree/main/sdk/js).

### Exit codes

The launcher passes the binary's exit code and signals through unchanged, so you can use them in scripts and CI:

| Code | Meaning |
| --- | --- |
| 0 | success (for `deploy`: promoted) |
| 1 | error |
| 2 | the rollout was rolled back |
| 3 | blocked by the risk gate |

## How the binary is found

1. `ALROR_BINARY_PATH` (or `ALROR_DOCS_BINARY_PATH` for `alror-docs`), if set.
2. The optional dependency `@alror/cli-<os>-<cpu>` for this machine.
3. A binary downloaded by the `postinstall` fallback into `node_modules/alror/vendor/`.

The fallback runs only when the platform package is missing, for example after `npm i --omit=optional`. It downloads `alror_<version>_<os>_<arch>` from the GitHub release, checks its SHA-256 against `checksums.txt` and extracts it, using only Node built-ins. It never fails the install: if the download fails, it prints a warning and tries again on the first run. If your package manager skips lifecycle scripts (pnpm, bun, `--ignore-scripts`), the download also happens on the first run.

Environment variables:

| Variable | Effect |
| --- | --- |
| `ALROR_SKIP_DOWNLOAD=1` | never download (air-gapped installs; bring your own binary with `ALROR_BINARY_PATH`) |
| `ALROR_DOWNLOAD_BASE` | download from a mirror instead of `https://github.com/manaskumar3003/alror-cli/releases/download/v<version>` |
| `HTTPS_PROXY`, `NO_PROXY` | download through an HTTP(S) proxy |
| `ALROR_BINARY_PATH` | use this binary and skip resolution |

## Installing before the npm release

From the launcher tarball attached to each GitHub release. The platform packages are not on npm yet, so the postinstall fallback downloads and verifies the binary from the same release:

```sh
npm i -g https://github.com/manaskumar3003/alror-cli/releases/download/v0.1.0/alror-0.1.0.tgz
```

With the install scripts, without npm:

```sh
curl -fsSL https://raw.githubusercontent.com/manaskumar3003/alror-cli/main/scripts/install.sh | sh
# Windows (PowerShell)
irm https://raw.githubusercontent.com/manaskumar3003/alror-cli/main/scripts/install.ps1 | iex
```

From local tarballs built from a release's archives:

```sh
git clone https://github.com/manaskumar3003/alror-cli && cd alror-cli
gh release download v0.1.0 --dir dist --pattern 'alror_*' --pattern checksums.txt
node npm/scripts/build-packages.mjs --version 0.1.0 --dist dist
npm pack ./npm/out/alror ./npm/out/cli-linux-x64       # pick your platform's folder
npm i -g ./alror-0.1.0.tgz ./alror-cli-linux-x64-0.1.0.tgz
```

## License

Apache-2.0
