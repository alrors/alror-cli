// Package api is a typed client for the Alror workspace REST API v1
// (docs/platform-contract.md, section 5).
//
// Every call takes a context. Normal calls time out after 20 s; the job claim
// long-poll gets a longer budget. Idempotent calls (GET, PUT, deployment
// upserts, job finish) are retried with exponential backoff on network errors
// and 5xx responses; non-idempotent ones (event appends, job enqueue and claim)
// are not, so they can never be applied twice.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/manaskumar3003/alror-cli/internal/config"
	"github.com/manaskumar3003/alror-cli/internal/domain"
)

// Defaults for the client.
const (
	DefaultTimeout      = 20 * time.Second
	DefaultClaimTimeout = 40 * time.Second // the server long-polls for up to 25 s
	DefaultRetries      = 3
	basePath            = "/api/v1"
)

// Client talks to one Alror workspace with one API key.
type Client struct {
	server       string // e.g. http://localhost:3000 (no trailing slash, no /api/v1)
	key          string
	http         *http.Client
	userAgent    string
	timeout      time.Duration
	claimTimeout time.Duration
	retries      int
	backoff      time.Duration
	headers      http.Header
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient replaces the underlying *http.Client (its Timeout is respected too).
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// WithUserAgent sets the User-Agent header, e.g. alror-cli/1.2.3.
func WithUserAgent(ua string) Option { return func(c *Client) { c.userAgent = ua } }

// WithTimeout sets the per-attempt timeout for normal calls (default 20 s).
func WithTimeout(d time.Duration) Option { return func(c *Client) { c.timeout = d } }

// WithClaimTimeout sets the per-attempt timeout of the job claim long-poll (default 40 s).
func WithClaimTimeout(d time.Duration) Option { return func(c *Client) { c.claimTimeout = d } }

// WithRetries sets how many times idempotent calls are retried (default 3) and the base backoff.
func WithRetries(n int, backoff time.Duration) Option {
	return func(c *Client) { c.retries, c.backoff = n, backoff }
}

// WithHeader adds a header to every request (e.g. X-Alror-Source: runner).
func WithHeader(k, v string) Option { return func(c *Client) { c.headers.Set(k, v) } }

// New returns a client for server (with or without the /api/v1 suffix) and key.
func New(server, key string, opts ...Option) *Client {
	c := &Client{
		server:       NormalizeServer(server),
		key:          key,
		http:         &http.Client{},
		userAgent:    "alror-cli/dev",
		timeout:      DefaultTimeout,
		claimTimeout: DefaultClaimTimeout,
		retries:      DefaultRetries,
		backoff:      300 * time.Millisecond,
		headers:      http.Header{},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// NormalizeServer trims whitespace, trailing slashes and a trailing /api/v1,
// and assumes http:// when no scheme is given.
func NormalizeServer(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, "/")
	s = strings.TrimSuffix(s, basePath)
	s = strings.TrimRight(s, "/")
	if s != "" && !strings.Contains(s, "://") {
		s = "http://" + s
	}
	return s
}

// Server returns the workspace root URL.
func (c *Client) Server() string { return c.server }

type callOpts struct {
	idempotent bool
	timeout    time.Duration
}

// do performs one API call with retries. It returns the final HTTP status.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any, o callOpts) (int, error) {
	if c.server == "" {
		return 0, errors.New("no Alror workspace server configured")
	}
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return 0, err
		}
	}
	u := c.server + basePath + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	timeout := o.timeout
	if timeout == 0 {
		timeout = c.timeout
	}
	attempts := 1
	if o.idempotent {
		attempts += max(0, c.retries)
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			if err := sleep(ctx, c.backoffFor(attempt)); err != nil {
				return 0, err
			}
		}
		status, retry, err := c.once(ctx, method, u, path, payload, out, timeout)
		if err == nil {
			return status, nil
		}
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		if !retry {
			return status, err
		}
		lastErr = err
	}
	return 0, &TransientError{Err: lastErr}
}

func (c *Client) once(ctx context.Context, method, u, path string, payload []byte, out any, timeout time.Duration) (status int, retry bool, err error) {
	actx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var rd io.Reader
	if payload != nil {
		rd = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(actx, method, u, rd)
	if err != nil {
		return 0, false, err
	}
	req.Header = c.headers.Clone()
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return 0, true, err // network error or attempt timeout
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return res.StatusCode, true, err
	}
	switch {
	case res.StatusCode >= 500:
		return res.StatusCode, true, decodeError(res.StatusCode, method, path, raw)
	case res.StatusCode >= 400:
		return res.StatusCode, false, decodeError(res.StatusCode, method, path, raw)
	case res.StatusCode == http.StatusNoContent || out == nil || len(bytes.TrimSpace(raw)) == 0:
		return res.StatusCode, false, nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return res.StatusCode, false, fmt.Errorf("%s %s: decode response: %w", method, path, err)
	}
	return res.StatusCode, false, nil
}

func decodeError(status int, method, path string, raw []byte) error {
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	e := &Error{Status: status, Method: method, Path: path}
	if json.Unmarshal(raw, &env) == nil && (env.Error.Code != "" || env.Error.Message != "") {
		e.Code, e.Message = env.Error.Code, env.Error.Message
	} else if s := strings.TrimSpace(string(raw)); s != "" && len(s) < 300 && !strings.HasPrefix(s, "<") {
		e.Message = s
	}
	return e
}

func (c *Client) backoffFor(attempt int) time.Duration {
	d := c.backoff << (attempt - 1)
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	if d <= 0 {
		return 0
	}
	return d/2 + rand.N(d/2+1) // jitter
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

var idem = callOpts{idempotent: true}

// Whoami returns the org, actor and scopes of the API key.
func (c *Client) Whoami(ctx context.Context) (*Whoami, error) {
	var w Whoami
	_, err := c.do(ctx, http.MethodGet, "/whoami", nil, nil, &w, idem)
	return &w, err
}

// Config fetches the org's services and policy in the alror.yaml shape.
func (c *Client) Config(ctx context.Context) (*config.Config, error) {
	var cfg config.Config
	if _, err := c.do(ctx, http.MethodGet, "/config", nil, nil, &cfg, idem); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// PutConfig upserts services (by name) and policy. Needs config:write or an admin session.
func (c *Client) PutConfig(ctx context.Context, cfg *config.Config) (*config.Config, error) {
	var out config.Config
	if _, err := c.do(ctx, http.MethodPut, "/config", nil, cfg, &out, idem); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListDeployments returns deployments, newest first.
func (c *Client) ListDeployments(ctx context.Context, o ListOptions) ([]*domain.Deployment, error) {
	q := url.Values{}
	if o.Limit > 0 {
		q.Set("limit", strconv.Itoa(o.Limit))
	}
	if o.Status != "" {
		q.Set("status", o.Status)
	}
	if o.Service != "" {
		q.Set("service", o.Service)
	}
	if o.Before != "" {
		q.Set("before", o.Before)
	}
	var out []*domain.Deployment
	_, err := c.do(ctx, http.MethodGet, "/deployments", q, nil, &out, idem)
	return out, err
}

// GetDeployment loads one deployment by id or unique prefix (409 ambiguous).
func (c *Client) GetDeployment(ctx context.Context, id string) (*domain.Deployment, error) {
	var d domain.Deployment
	if _, err := c.do(ctx, http.MethodGet, "/deployments/"+url.PathEscape(id), nil, nil, &d, idem); err != nil {
		return nil, err
	}
	return &d, nil
}

// CreateDeployment upserts a deployment by id (POST). The service must exist (422 unknown_service).
func (c *Client) CreateDeployment(ctx context.Context, d *domain.Deployment) (*domain.Deployment, error) {
	var out domain.Deployment
	if _, err := c.do(ctx, http.MethodPost, "/deployments", nil, d, &out, idem); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateDeployment replaces a deployment (PUT /deployments/{id}).
func (c *Client) UpdateDeployment(ctx context.Context, d *domain.Deployment) (*domain.Deployment, error) {
	var out domain.Deployment
	if _, err := c.do(ctx, http.MethodPut, "/deployments/"+url.PathEscape(d.ID), nil, d, &out, idem); err != nil {
		return nil, err
	}
	return &out, nil
}

// Events returns a deployment's event log in order.
func (c *Client) Events(ctx context.Context, id string) ([]domain.Event, error) {
	var out []domain.Event
	_, err := c.do(ctx, http.MethodGet, "/deployments/"+url.PathEscape(id)+"/events", nil, nil, &out, idem)
	return out, err
}

// AppendEvent appends one event. It is not retried, so it is never recorded twice.
func (c *Client) AppendEvent(ctx context.Context, id string, e domain.Event) error {
	_, err := c.do(ctx, http.MethodPost, "/deployments/"+url.PathEscape(id)+"/events", nil, e, nil, callOpts{})
	return err
}

// RecentRollbacks counts rollbacks per service since a time.
func (c *Client) RecentRollbacks(ctx context.Context, since time.Time) (map[string]int, error) {
	out := map[string]int{}
	q := url.Values{"since": {since.UTC().Format(time.RFC3339)}}
	_, err := c.do(ctx, http.MethodGet, "/rollbacks/recent", q, nil, &out, idem)
	return out, err
}

// EnqueueDeploy creates a deploy job for a runner to execute.
func (c *Client) EnqueueDeploy(ctx context.Context, p DeployPayload) (*Job, error) {
	return c.enqueue(ctx, EnqueueRequest{Kind: JobDeploy, Payload: p})
}

// EnqueueRollback creates a rollback job for a runner to execute.
func (c *Client) EnqueueRollback(ctx context.Context, deploymentID, reason string) (*Job, error) {
	return c.enqueue(ctx, EnqueueRequest{Kind: JobRollback, Payload: RollbackPayload{DeploymentID: deploymentID, Reason: reason}})
}

func (c *Client) enqueue(ctx context.Context, r EnqueueRequest) (*Job, error) {
	var j Job
	if _, err := c.do(ctx, http.MethodPost, "/jobs", nil, r, &j, callOpts{}); err != nil {
		return nil, err
	}
	return &j, nil
}

// ListJobs returns jobs, optionally filtered by status.
func (c *Client) ListJobs(ctx context.Context, status string) ([]*Job, error) {
	q := url.Values{}
	if status != "" {
		q.Set("status", status)
	}
	var out []*Job
	_, err := c.do(ctx, http.MethodGet, "/jobs", q, nil, &out, idem)
	return out, err
}

// ClaimJob long-polls for the next queued job. It returns (nil, nil) when
// none arrived in the server's window (204). It is never retried: a retry
// could claim a second job while the first response was lost.
func (c *Client) ClaimJob(ctx context.Context, runner string) (*Job, error) {
	var j Job
	status, err := c.do(ctx, http.MethodPost, "/jobs/claim", nil, ClaimRequest{Runner: runner}, &j, callOpts{timeout: c.claimTimeout})
	if err != nil {
		return nil, err
	}
	if status == http.StatusNoContent || j.ID == "" {
		return nil, nil
	}
	return &j, nil
}

// HeartbeatJob extends the lease on a claimed job. Runners call it while a
// job executes (every 30 s). ErrConflict means the lease was lost: the job was
// requeued and possibly claimed by another runner, so this runner must stop
// the job and must not finish it.
func (c *Client) HeartbeatJob(ctx context.Context, id, runner string) (*Job, error) {
	var j Job
	if _, err := c.do(ctx, http.MethodPost, "/jobs/"+url.PathEscape(id)+"/heartbeat", nil, ClaimRequest{Runner: runner}, &j, idem); err != nil {
		return nil, err
	}
	return &j, nil
}

// FinishJob reports a job's outcome. Finishing is idempotent, so it is retried.
func (c *Client) FinishJob(ctx context.Context, id string, r FinishRequest) (*Job, error) {
	var j Job
	if _, err := c.do(ctx, http.MethodPost, "/jobs/"+url.PathEscape(id)+"/finish", nil, r, &j, idem); err != nil {
		return nil, err
	}
	return &j, nil
}
