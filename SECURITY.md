# Security policy

Alror sits in the path to production, so we treat security reports as a priority.

## Reporting a vulnerability

Please **do not open a public issue**. Instead, use GitHub's private vulnerability reporting: **Security → Report a vulnerability** in this repository. Include the affected version, the steps to reproduce and the impact you see. We aim to acknowledge reports within 3 business days.

## Scope

- The `alror` CLI and worker, the `pkg/alror` SDK and the GitHub actions in this repository.
- How credentials are handled: API keys live in `~/.config/alror/credentials.json` (or `%APPDATA%\alror\credentials.json` on Windows) with user-only permissions, and are never printed or logged.

## Supported versions

Security fixes go to the latest minor release.
