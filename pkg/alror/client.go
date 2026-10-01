package alror

import (
	"context"
	"net/http"
	"time"

	"github.com/manaskumar3003/alror-cli/internal/api"
)

// Sentinel errors. Match them with errors.Is.
var (
	// ErrUnauthorized means the API key is missing, invalid or revoked (HTTP 401).
	ErrUnauthorized = api.ErrUnauthorized
	// ErrForbidden means the key lacks the scope for the call (HTTP 403).
	ErrForbidden = api.ErrForbidden
	// ErrNotFound means the resource does not exist (HTTP 404).
	ErrNotFound = api.ErrNotFound
	// ErrConflict means a conflict such as an ambiguous id prefix (HTTP 409).
	ErrConflict = api.ErrConflict
	// ErrValidation means the request was rejected as invalid (HTTP 400 or 422);
	// the *APIError carries the server's code (e.g. unknown_service) and message.
	ErrValidation = api.ErrValidation
)

// APIError is a contract-shaped error response. Use errors.As to read its
// Status, Code and Message.
type APIError = api.Error

// IsTransient reports whether err is a network failure or 5xx response that
// survived the client's retries, i.e. worth retrying later.
func IsTransient(err error) bool { return api.IsTransient(err) }

// Option configures a Client.
type Option func(*clientOptions)

type clientOptions struct{ api []api.Option }

// WithHTTPClient makes the client send requests with h (for proxies, custom TLS or tests).
func WithHTTPClient(h *http.Client) Option {
	return func(o *clientOptions) { o.api = append(o.api, api.WithHTTPClient(h)) }
}

// WithUserAgent sets the User-Agent header (default "alror-go-sdk").
func WithUserAgent(ua string) Option {
	return func(o *clientOptions) { o.api = append(o.api, api.WithUserAgent(ua)) }
}

// WithTimeout sets the per-attempt timeout of normal calls (default 20 s).
// The job claim long-poll keeps its own, longer budget.
func WithTimeout(d time.Duration) Option {
	return func(o *clientOptions) { o.api = append(o.api, api.WithTimeout(d)) }
}

// Client talks to one Alror workspace with one API key. It is safe for
// concurrent use.
type Client struct {
	c *api.Client
}

// NewClient returns a client for server (for example http://localhost:3000;
// a trailing /api/v1 is accepted) authenticating with apiKey (alr_live_…).
func NewClient(server, apiKey string, opts ...Option) *Client {
	o := clientOptions{api: []api.Option{api.WithUserAgent("alror-go-sdk"), api.WithHeader("X-Alror-Source", "sdk")}}
	for _, fn := range opts {
		fn(&o)
	}
	return &Client{c: api.New(server, apiKey, o.api...)}
}

// Server returns the normalised workspace URL.
func (c *Client) Server() string { return c.c.Server() }

// WhoAmI returns the organisation, actor and scopes of the API key.
func (c *Client) WhoAmI(ctx context.Context) (*WhoAmI, error) { return c.c.Whoami(ctx) }

// Config returns the organisation's services and policy in the alror.yaml shape.
func (c *Client) Config(ctx context.Context) (*Config, error) { return c.c.Config(ctx) }

// PutConfig upserts services (by name) and the policy. It needs the
// config:write scope (admin keys) and returns the resulting config.
func (c *Client) PutConfig(ctx context.Context, cfg *Config) (*Config, error) {
	return c.c.PutConfig(ctx, cfg)
}

// ListDeployments returns one page of deployments, newest first. Page
// backwards by passing the last ID as ListOptions.Before (Limit max 1000).
func (c *Client) ListDeployments(ctx context.Context, opts ListOptions) ([]*Deployment, error) {
	return c.c.ListDeployments(ctx, opts)
}

// GetDeployment returns one deployment by id or unique id prefix. An
// ambiguous prefix returns ErrConflict.
func (c *Client) GetDeployment(ctx context.Context, id string) (*Deployment, error) {
	return c.c.GetDeployment(ctx, id)
}

// CreateOrUpdateDeployment upserts a deployment by its ID (use
// NewDeploymentID for new ones). The service must exist in the org, or the
// call fails with ErrValidation (code unknown_service). The server owns the
// timestamps: d.CreatedAt and d.UpdatedAt are set from its response.
func (c *Client) CreateOrUpdateDeployment(ctx context.Context, d *Deployment) (*Deployment, error) {
	now := time.Now().UTC()
	if d.CreatedAt.IsZero() {
		d.CreatedAt = now
	}
	d.UpdatedAt = now
	out, err := c.c.CreateDeployment(ctx, d)
	if err != nil {
		return nil, err
	}
	if !out.CreatedAt.IsZero() {
		d.CreatedAt = out.CreatedAt
	}
	if !out.UpdatedAt.IsZero() {
		d.UpdatedAt = out.UpdatedAt
	}
	return out, nil
}

// Events returns a deployment's event log in order.
func (c *Client) Events(ctx context.Context, deploymentID string) ([]Event, error) {
	return c.c.Events(ctx, deploymentID)
}

// AppendEvent appends one event to a deployment's log. It is not retried,
// so an event is never recorded twice.
func (c *Client) AppendEvent(ctx context.Context, deploymentID string, e Event) error {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	return c.c.AppendEvent(ctx, deploymentID, e)
}

// RecentRollbacks counts rollbacks per service since a time (used by risk scoring).
func (c *Client) RecentRollbacks(ctx context.Context, since time.Time) (map[string]int, error) {
	return c.c.RecentRollbacks(ctx, since)
}

// EnqueueDeploy queues a deploy job for a runner (`alror runner`) to execute.
func (c *Client) EnqueueDeploy(ctx context.Context, req DeployRequest) (*Job, error) {
	return c.c.EnqueueDeploy(ctx, req)
}

// EnqueueRollback queues a rollback of a deployment for a runner to execute.
func (c *Client) EnqueueRollback(ctx context.Context, deploymentID, reason string) (*Job, error) {
	return c.c.EnqueueRollback(ctx, deploymentID, reason)
}

// ListJobs returns jobs, optionally filtered by status ("" for all).
func (c *Client) ListJobs(ctx context.Context, status string) ([]*Job, error) {
	return c.c.ListJobs(ctx, status)
}

// ClaimJob long-polls (up to about 25 s) for the next queued job and claims it
// for the named runner. It returns (nil, nil) when no job arrived. It needs jobs:run.
func (c *Client) ClaimJob(ctx context.Context, runner string) (*Job, error) {
	return c.c.ClaimJob(ctx, runner)
}

// HeartbeatJob extends the lease on a claimed job; call it about every 30 s
// while the job runs, or the server requeues it after 2 minutes. ErrConflict
// means the lease was lost to another runner: stop the job and do not finish it.
func (c *Client) HeartbeatJob(ctx context.Context, jobID, runner string) (*Job, error) {
	return c.c.HeartbeatJob(ctx, jobID, runner)
}

// FinishJob reports the outcome of a claimed job. It needs jobs:run.
func (c *Client) FinishJob(ctx context.Context, jobID string, result JobResult) (*Job, error) {
	return c.c.FinishJob(ctx, jobID, result)
}
