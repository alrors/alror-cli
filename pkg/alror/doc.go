// Package alror is the public Go SDK for Alror, the release-safety tool that
// scores the risk of a change, rolls it out in verified steps and rolls it
// back on its own when it hurts users.
//
// # Connected and local usage
//
// Alror runs in one of two modes, and this SDK serves both:
//
//   - Connected mode. Your Alror workspace (the self-hosted Alror web
//     platform) stores services, policy, deployments, events and jobs
//     behind a REST API (/api/v1). Use [NewClient] with the workspace URL
//     and an API key (alr_live_…) to read deployments, append events,
//     enqueue deploy and rollback jobs, or build your own runner with
//     [Client.ClaimJob] and [Client.FinishJob]. The `alror` CLI and
//     `alror runner` use the same API. A runner of your own must call
//     [Client.HeartbeatJob] about every 30 s while a job runs, and stop
//     the job without finishing it when that returns ErrConflict.
//
//   - Local mode. With no server, the `alror` CLI reads alror.yaml and keeps
//     state in .alror/ next to it. Library code can load the same file with
//     [LoadConfig] and score changes offline with the risk subpackage
//     (pkg/alror/risk), which needs only git.
//
// # Types and JSON
//
// [Deployment], [Event], [Verdict], [RiskReport], [Plan] and friends are type
// aliases of the types the CLI and the Alror workspace exchange, so their JSON
// is byte-for-byte what the API sends: snake_case keys, RFC 3339 times and
// Step.Bake as integer nanoseconds.
//
// # Errors
//
// API errors match the sentinel errors with errors.Is: [ErrUnauthorized]
// (401), [ErrForbidden] (403), [ErrNotFound] (404), [ErrConflict] (409, e.g.
// an ambiguous id prefix) and [ErrValidation] (400 or 422). Use errors.As with
// *[APIError] for the server's code and message, and [IsTransient] to detect
// network failures and 5xx responses that survived the client's retries.
//
// # Retries and timeouts
//
// Every method takes a context. Normal calls time out after 20 s per attempt
// (see [WithTimeout]); the job claim long-poll gets a longer budget.
// Idempotent calls (reads, PUTs, deployment upserts, job finish) are retried
// with exponential backoff on network errors and 5xx; event appends, job
// enqueues and claims are never retried, so they cannot be applied twice.
package alror
