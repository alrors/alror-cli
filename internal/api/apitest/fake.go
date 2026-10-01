// Package apitest is an in-memory fake of the Alror workspace API v1
// (docs/platform-contract.md, section 5). Tests use it to drive the API
// client, store.Remote and the runner; `alror-devserver` serves it for local
// demos. It implements every endpoint except the SSE stream, with the
// contract's error envelope, scopes, prefix lookup and job long-poll.
package apitest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/manaskumar3003/alror-cli/internal/api"
	"github.com/manaskumar3003/alror-cli/internal/config"
	"github.com/manaskumar3003/alror-cli/internal/domain"
)

// DefaultKey holds every scope on a fresh Fake.
const DefaultKey = "alr_live_TestKey0123456789abcdefghijklmnop"

// AllScopes lists every API key scope in the contract.
var AllScopes = []string{api.ScopeDeployRead, api.ScopeDeployWrite, api.ScopeJobsRun, api.ScopeConfigWrite}

// Key is an API key known to the fake.
type Key struct {
	ID     string
	Label  string
	Scopes []string
}

// Request is one request the fake received, for assertions.
type Request struct {
	Method, Path, Query string
	Authorization       string
	UserAgent           string
	Source              string // X-Alror-Source header
}

// Fake is the in-memory workspace. It is safe for concurrent use.
type Fake struct {
	// ClaimWait is how long POST /jobs/claim waits for a job before 204 (default 25 s).
	ClaimWait time.Duration
	// LeaseTimeout requeues a claimed job with no heartbeat for this long (default 2 min).
	LeaseTimeout time.Duration

	mu       sync.Mutex
	org      api.Org
	keys     map[string]Key
	cfg      *config.Config
	deps     map[string]*domain.Deployment
	events   map[string][]domain.Event
	jobs     []*api.Job
	wake     chan struct{}
	failNext []int
	requests []Request
	nextJob  int
	mux      *http.ServeMux
	offset   time.Duration        // added to the wall clock; see Advance
	beats    map[string]time.Time // job id -> last claim or heartbeat
}

// New returns a fake with org "acme", DefaultKey and a two-service config.
func New() *Fake {
	cfg := config.Default("acme")
	cfg.Policy.BakeScale = 0.003
	f := &Fake{
		ClaimWait:    25 * time.Second,
		LeaseTimeout: 2 * time.Minute,
		beats:        map[string]time.Time{},
		org:          api.Org{ID: "00000000-0000-4000-8000-000000000001", Slug: "acme", Name: "Acme Inc"},
		keys:         map[string]Key{DefaultKey: {ID: "key_1", Label: "test key", Scopes: AllScopes}},
		cfg:          cfg,
		deps:         map[string]*domain.Deployment{},
		events:       map[string][]domain.Event{},
		wake:         make(chan struct{}),
	}
	f.routes()
	return f
}

// Advance moves the fake's clock forward, e.g. past a job lease.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.offset += d
}

// now is the fake's clock. Callers hold f.mu.
func (f *Fake) now() time.Time { return time.Now().Add(f.offset).UTC() }

// requeueExpiredLocked puts claimed jobs whose lease ran out back in the queue.
func (f *Fake) requeueExpiredLocked() {
	now := f.now()
	requeued := false
	for _, j := range f.jobs {
		if j.Status == api.JobClaimed && now.Sub(f.beats[j.ID]) > f.LeaseTimeout {
			j.Status, j.ClaimedBy, j.ClaimedAt = api.JobQueued, "", nil
			requeued = true
		}
	}
	if requeued {
		close(f.wake)
		f.wake = make(chan struct{})
	}
}

// Start serves the fake on a local httptest server. Call Close on the result.
func Start() (*Fake, *httptest.Server) {
	f := New()
	return f, httptest.NewServer(f)
}

// AddKey registers an API key with the given scopes.
func (f *Fake) AddKey(token, label string, scopes ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys[token] = Key{ID: "key_" + strconv.Itoa(len(f.keys)+1), Label: label, Scopes: scopes}
}

// SetOrg changes the organisation the fake reports.
func (f *Fake) SetOrg(slug, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.org.Slug, f.org.Name = slug, name
}

// SetConfig replaces the org's configuration.
func (f *Fake) SetConfig(c *config.Config) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cfg = clone(c)
}

// Config returns a copy of the org's configuration.
func (f *Fake) Config() *config.Config {
	f.mu.Lock()
	defer f.mu.Unlock()
	return clone(f.cfg)
}

// FailNext makes the next len(statuses) requests fail with these statuses.
func (f *Fake) FailNext(statuses ...int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failNext = append(f.failNext, statuses...)
}

// Requests returns every request received so far.
func (f *Fake) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.requests...)
}

// Deployment returns a copy of a stored deployment, or nil.
func (f *Fake) Deployment(id string) *domain.Deployment {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.deps[id]; ok {
		c := *d
		return &c
	}
	return nil
}

// PutDeployment stores a deployment directly (test setup).
func (f *Fake) PutDeployment(d *domain.Deployment) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := *d
	f.deps[d.ID] = &c
}

// DeleteDeployment removes a deployment and its events (test setup).
func (f *Fake) DeleteDeployment(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.deps, id)
	delete(f.events, id)
}

// Events returns a copy of a deployment's events.
func (f *Fake) Events(id string) []domain.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.Event(nil), f.events[id]...)
}

// Enqueue adds a job as if it came from the console.
func (f *Fake) Enqueue(kind string, payload any) *api.Job {
	raw, _ := json.Marshal(payload)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.enqueueLocked(kind, raw)
}

// Job returns a copy of one job, or nil.
func (f *Fake) Job(id string) *api.Job {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, j := range f.jobs {
		if j.ID == id {
			c := *j
			return &c
		}
	}
	return nil
}

// Jobs returns copies of every job in creation order.
func (f *Fake) Jobs() []*api.Job {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*api.Job, len(f.jobs))
	for i, j := range f.jobs {
		c := *j
		out[i] = &c
	}
	return out
}

// ServeHTTP implements http.Handler.
func (f *Fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, Request{
		Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery,
		Authorization: r.Header.Get("Authorization"), UserAgent: r.Header.Get("User-Agent"),
		Source: r.Header.Get("X-Alror-Source"),
	})
	f.requeueExpiredLocked()
	var fail int
	if len(f.failNext) > 0 {
		fail, f.failNext = f.failNext[0], f.failNext[1:]
	}
	f.mu.Unlock()
	if fail != 0 {
		writeErr(w, fail, "injected", "injected failure")
		return
	}
	f.mux.ServeHTTP(w, r)
}

type handler func(w http.ResponseWriter, r *http.Request, k Key)

func (f *Fake) routes() {
	m := http.NewServeMux()
	h := func(pattern, scope string, fn handler) { m.HandleFunc(pattern, f.auth(scope, fn)) }
	h("GET /api/v1/whoami", "", f.whoami)
	h("GET /api/v1/config", api.ScopeDeployRead, f.getConfig)
	h("PUT /api/v1/config", api.ScopeConfigWrite, f.putConfig)
	h("GET /api/v1/deployments", api.ScopeDeployRead, f.listDeployments)
	h("POST /api/v1/deployments", api.ScopeDeployWrite, f.postDeployment)
	h("PUT /api/v1/deployments/{id}", api.ScopeDeployWrite, f.putDeployment)
	h("GET /api/v1/deployments/{id}", api.ScopeDeployRead, f.getDeployment)
	h("GET /api/v1/deployments/{id}/events", api.ScopeDeployRead, f.getEvents)
	h("POST /api/v1/deployments/{id}/events", api.ScopeDeployWrite, f.postEvent)
	h("GET /api/v1/rollbacks/recent", api.ScopeDeployRead, f.recentRollbacks)
	h("POST /api/v1/jobs", api.ScopeDeployWrite, f.postJob)
	h("GET /api/v1/jobs", api.ScopeDeployRead, f.listJobs)
	h("POST /api/v1/jobs/claim", api.ScopeJobsRun, f.claimJob)
	h("POST /api/v1/jobs/{id}/finish", api.ScopeJobsRun, f.finishJob)
	h("POST /api/v1/jobs/{id}/heartbeat", api.ScopeJobsRun, f.heartbeatJob)
	m.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { writeErr(w, 404, "not_found", "no such endpoint") })
	f.mux = m
}

func (f *Fake) auth(scope string, fn handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		f.mu.Lock()
		k, known := f.keys[token]
		f.mu.Unlock()
		if !ok || !known {
			writeErr(w, 401, "unauthorized", "missing or invalid API key")
			return
		}
		if scope != "" && !has(k.Scopes, scope) {
			writeErr(w, 403, "forbidden", "this key lacks the "+scope+" scope")
			return
		}
		fn(w, r, k)
	}
}

func (f *Fake) whoami(w http.ResponseWriter, _ *http.Request, k Key) {
	f.mu.Lock()
	org := f.org
	f.mu.Unlock()
	writeJSON(w, 200, api.Whoami{Org: org, Actor: api.Actor{Type: "api_key", ID: k.ID, Label: k.Label}, Scopes: k.Scopes})
}

func (f *Fake) getConfig(w http.ResponseWriter, _ *http.Request, _ Key) {
	writeJSON(w, 200, f.Config())
}

func (f *Fake) putConfig(w http.ResponseWriter, r *http.Request, _ Key) {
	var in config.Config
	if !decode(w, r, &in) {
		return
	}
	for _, s := range in.Services {
		if s.Name == "" {
			writeErr(w, 422, "invalid_config", "every service needs a name")
			return
		}
	}
	f.mu.Lock()
	for _, s := range in.Services { // upsert by name; services missing from the body are kept
		found := false
		for i := range f.cfg.Services {
			if f.cfg.Services[i].Name == s.Name {
				f.cfg.Services[i], found = s, true
			}
		}
		if !found {
			f.cfg.Services = append(f.cfg.Services, s)
		}
	}
	if in.Project != "" {
		f.cfg.Project = in.Project
	}
	if in.Policy.MaxRegression != nil || in.Policy.Alpha != 0 {
		f.cfg.Policy = in.Policy
	}
	if in.Metrics.Provider != "" {
		f.cfg.Metrics = in.Metrics
	}
	f.cfg.Notify = in.Notify
	out := clone(f.cfg)
	f.mu.Unlock()
	writeJSON(w, 200, out)
}

func (f *Fake) listDeployments(w http.ResponseWriter, r *http.Request, _ Key) {
	q := r.URL.Query()
	limit := 50
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 {
		limit = min(v, 1000)
	}
	f.mu.Lock()
	out := make([]*domain.Deployment, 0, len(f.deps))
	for _, d := range f.deps {
		if (q.Get("status") == "" || string(d.Status) == q.Get("status")) && (q.Get("service") == "" || d.Service == q.Get("service")) {
			c := *d
			out = append(out, &c)
		}
	}
	var cursor *domain.Deployment
	var beforeTime time.Time
	if b := q.Get("before"); b != "" {
		if t, err := time.Parse(time.RFC3339, b); err == nil {
			beforeTime = t
		} else if d, ok := f.deps[b]; ok {
			c := *d
			cursor = &c
		} else {
			f.mu.Unlock()
			writeErr(w, 422, "invalid_before", "before must be a deployment id or an RFC 3339 time")
			return
		}
	}
	f.mu.Unlock()
	newer := func(a, b *domain.Deployment) bool { // a sorts before b (newest first)
		if a.CreatedAt.Equal(b.CreatedAt) {
			return a.ID > b.ID
		}
		return a.CreatedAt.After(b.CreatedAt)
	}
	sort.Slice(out, func(i, j int) bool { return newer(out[i], out[j]) })
	if cursor != nil || !beforeTime.IsZero() {
		kept := out[:0]
		for _, d := range out {
			if (cursor != nil && newer(cursor, d)) || (!beforeTime.IsZero() && d.CreatedAt.Before(beforeTime)) {
				kept = append(kept, d)
			}
		}
		out = kept
	}
	if len(out) > limit {
		out = out[:limit]
	}
	writeJSON(w, 200, out)
}

func (f *Fake) knownService(name string) bool {
	_, ok := f.cfg.Service(name)
	return ok
}

func (f *Fake) postDeployment(w http.ResponseWriter, r *http.Request, _ Key) {
	var d domain.Deployment
	if !decode(w, r, &d) {
		return
	}
	if d.ID == "" {
		writeErr(w, 400, "invalid_deployment", "id is required")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.knownService(d.Service) {
		writeErr(w, 422, "unknown_service", fmt.Sprintf("service %q does not exist in this org", d.Service))
		return
	}
	f.stampLocked(&d)
	f.deps[d.ID] = &d
	writeJSON(w, 201, d)
}

func (f *Fake) putDeployment(w http.ResponseWriter, r *http.Request, _ Key) {
	var d domain.Deployment
	if !decode(w, r, &d) {
		return
	}
	if d.ID != r.PathValue("id") {
		writeErr(w, 400, "invalid_deployment", "id in body does not match the URL")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.deps[d.ID]; !ok {
		writeErr(w, 404, "not_found", "deployment "+d.ID+" not found")
		return
	}
	if !f.knownService(d.Service) {
		writeErr(w, 422, "unknown_service", fmt.Sprintf("service %q does not exist in this org", d.Service))
		return
	}
	f.stampLocked(&d)
	f.deps[d.ID] = &d
	writeJSON(w, 200, d)
}

// stampLocked applies server-owned timestamps: updated_at is always the
// server's clock; created_at is kept from the first write.
func (f *Fake) stampLocked(d *domain.Deployment) {
	if old, ok := f.deps[d.ID]; ok && !old.CreatedAt.IsZero() {
		d.CreatedAt = old.CreatedAt
	} else if d.CreatedAt.IsZero() {
		d.CreatedAt = f.now()
	}
	d.UpdatedAt = f.now()
}

// lookup resolves an id or unique prefix. Callers hold f.mu.
func (f *Fake) lookup(w http.ResponseWriter, id string) *domain.Deployment {
	if d, ok := f.deps[id]; ok {
		return d
	}
	var match []*domain.Deployment
	for k, d := range f.deps {
		if strings.HasPrefix(k, id) {
			match = append(match, d)
		}
	}
	switch len(match) {
	case 1:
		return match[0]
	case 0:
		writeErr(w, 404, "not_found", "deployment "+id+" not found")
	default:
		writeErr(w, 409, "ambiguous", fmt.Sprintf("%q matches %d deployments", id, len(match)))
	}
	return nil
}

func (f *Fake) getDeployment(w http.ResponseWriter, r *http.Request, _ Key) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d := f.lookup(w, r.PathValue("id")); d != nil {
		writeJSON(w, 200, d)
	}
}

func (f *Fake) getEvents(w http.ResponseWriter, r *http.Request, _ Key) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := f.lookup(w, r.PathValue("id"))
	if d == nil {
		return
	}
	out := f.events[d.ID]
	if out == nil {
		out = []domain.Event{}
	}
	writeJSON(w, 200, out)
}

func (f *Fake) postEvent(w http.ResponseWriter, r *http.Request, _ Key) {
	var e domain.Event
	if !decode(w, r, &e) {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	id := r.PathValue("id")
	if _, ok := f.deps[id]; !ok {
		writeErr(w, 404, "not_found", "deployment "+id+" not found")
		return
	}
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	f.events[id] = append(f.events[id], e)
	writeJSON(w, 201, e)
}

func (f *Fake) recentRollbacks(w http.ResponseWriter, r *http.Request, _ Key) {
	since := time.Now().AddDate(0, 0, -30)
	if s := r.URL.Query().Get("since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			writeErr(w, 400, "invalid_since", "since must be RFC 3339")
			return
		}
		since = t
	}
	out := map[string]int{}
	f.mu.Lock()
	for _, d := range f.deps {
		if d.Status == domain.StatusRolledBack && d.UpdatedAt.After(since) {
			out[d.Service]++
		}
	}
	f.mu.Unlock()
	writeJSON(w, 200, out)
}

func (f *Fake) enqueueLocked(kind string, payload json.RawMessage) *api.Job {
	f.nextJob++
	j := &api.Job{
		ID:   fmt.Sprintf("%08x-0000-4000-9000-%012d", 0xa1000000+f.nextJob, f.nextJob),
		Kind: kind, Payload: payload, Status: api.JobQueued, CreatedAt: time.Now().UTC(),
	}
	f.jobs = append(f.jobs, j)
	close(f.wake) // wake every waiting claim
	f.wake = make(chan struct{})
	c := *j
	return &c
}

func (f *Fake) postJob(w http.ResponseWriter, r *http.Request, _ Key) {
	var in struct {
		Kind    string          `json:"kind"`
		Payload json.RawMessage `json:"payload"`
	}
	if !decode(w, r, &in) {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch in.Kind {
	case api.JobDeploy:
		var p api.DeployPayload
		if err := json.Unmarshal(in.Payload, &p); err != nil || p.Service == "" || p.Image == "" {
			writeErr(w, 422, "invalid_payload", "deploy jobs need service and image")
			return
		}
		if !f.knownService(p.Service) {
			writeErr(w, 422, "unknown_service", fmt.Sprintf("service %q does not exist in this org", p.Service))
			return
		}
		if _, _, err := p.RiskOverrideScore(); err != nil {
			writeErr(w, 422, "invalid", err.Error())
			return
		}
	case api.JobRollback:
		var p api.RollbackPayload
		if err := json.Unmarshal(in.Payload, &p); err != nil || p.DeploymentID == "" {
			writeErr(w, 422, "invalid_payload", "rollback jobs need deployment_id")
			return
		}
	default:
		writeErr(w, 422, "invalid_kind", "kind must be deploy or rollback")
		return
	}
	writeJSON(w, 201, f.enqueueLocked(in.Kind, in.Payload))
}

func (f *Fake) listJobs(w http.ResponseWriter, r *http.Request, _ Key) {
	status := r.URL.Query().Get("status")
	out := []*api.Job{}
	for _, j := range f.Jobs() {
		if status == "" || j.Status == status {
			out = append(out, j)
		}
	}
	writeJSON(w, 200, out)
}

func (f *Fake) claimJob(w http.ResponseWriter, r *http.Request, _ Key) {
	var in api.ClaimRequest
	if !decode(w, r, &in) {
		return
	}
	deadline := time.NewTimer(f.ClaimWait)
	defer deadline.Stop()
	for {
		f.mu.Lock()
		f.requeueExpiredLocked()
		for _, j := range f.jobs {
			if j.Status == api.JobQueued {
				now := f.now()
				f.beats[j.ID] = now
				j.Status, j.ClaimedBy, j.ClaimedAt = api.JobClaimed, in.Runner, &now
				c := *j
				f.mu.Unlock()
				writeJSON(w, 200, c)
				return
			}
		}
		wake := f.wake
		f.mu.Unlock()
		select {
		case <-wake:
		case <-deadline.C:
			w.WriteHeader(http.StatusNoContent)
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (f *Fake) heartbeatJob(w http.ResponseWriter, r *http.Request, _ Key) {
	var in api.ClaimRequest
	if !decode(w, r, &in) {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, j := range f.jobs {
		if j.ID != r.PathValue("id") {
			continue
		}
		if j.Status != api.JobClaimed || j.ClaimedBy != in.Runner {
			writeErr(w, 409, "lease_lost", "job is no longer claimed by "+in.Runner)
			return
		}
		f.beats[j.ID] = f.now()
		writeJSON(w, 200, j)
		return
	}
	writeErr(w, 404, "not_found", "job not found")
}

func (f *Fake) finishJob(w http.ResponseWriter, r *http.Request, _ Key) {
	var in api.FinishRequest
	if !decode(w, r, &in) {
		return
	}
	if in.Status != api.JobDone && in.Status != api.JobFailed {
		writeErr(w, 400, "invalid_status", "status must be done or failed")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, j := range f.jobs {
		if j.ID != r.PathValue("id") {
			continue
		}
		if j.Status == in.Status && j.FinishedAt != nil { // idempotent repeat
			writeJSON(w, 200, j)
			return
		}
		if j.Status != api.JobClaimed {
			writeErr(w, 409, "not_claimed", "job is "+j.Status)
			return
		}
		now := time.Now().UTC()
		j.Status, j.Error, j.DeploymentID, j.FinishedAt = in.Status, in.Error, in.DeploymentID, &now
		writeJSON(w, 200, j)
		return
	}
	writeErr(w, 404, "not_found", "job not found")
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, 400, "invalid_json", err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

func has(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func clone(c *config.Config) *config.Config {
	raw, _ := json.Marshal(c)
	var out config.Config
	_ = json.Unmarshal(raw, &out)
	return &out
}
