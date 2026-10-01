package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manaskumar3003/alror-cli/internal/api"
	"github.com/manaskumar3003/alror-cli/internal/api/apitest"
	"github.com/manaskumar3003/alror-cli/internal/domain"
)

func newClient(t *testing.T) (*apitest.Fake, *api.Client) {
	t.Helper()
	f, srv := apitest.Start()
	t.Cleanup(srv.Close)
	return f, api.New(srv.URL+"/api/v1/", apitest.DefaultKey, api.WithUserAgent("alror-cli/9.9.9"), api.WithRetries(3, time.Millisecond))
}

func TestAuthHeaderAndUserAgent(t *testing.T) {
	f, c := newClient(t)
	w, err := c.Whoami(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if w.Org.Slug != "acme" || w.Actor.Type != "api_key" || !w.HasScope(api.ScopeJobsRun) {
		t.Fatalf("whoami = %+v", w)
	}
	r := f.Requests()[0]
	if r.Authorization != "Bearer "+apitest.DefaultKey {
		t.Fatalf("Authorization = %q", r.Authorization)
	}
	if r.UserAgent != "alror-cli/9.9.9" {
		t.Fatalf("User-Agent = %q", r.UserAgent)
	}
	if r.Path != "/api/v1/whoami" {
		t.Fatalf("path = %q (server URL with /api/v1/ must be normalised)", r.Path)
	}
}

func TestErrorMapping(t *testing.T) {
	f, c := newClient(t)
	ctx := context.Background()

	bad := api.New(c.Server(), "alr_live_nope")
	if _, err := bad.Whoami(ctx); !errors.Is(err, api.ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}

	f.AddKey("alr_live_readonly", "ro", api.ScopeDeployRead)
	ro := api.New(c.Server(), "alr_live_readonly")
	if _, err := ro.ClaimJob(ctx, "w"); !errors.Is(err, api.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}

	if _, err := c.GetDeployment(ctx, "dep_missing"); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}

	now := time.Now()
	for _, id := range []string{"dep_x_1", "dep_x_2"} {
		if _, err := c.CreateDeployment(ctx, &domain.Deployment{ID: id, Service: "checkout-api", CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := c.GetDeployment(ctx, "dep_x")
	if !errors.Is(err, api.ErrConflict) || api.Code(err) != "ambiguous" {
		t.Fatalf("want ErrConflict/ambiguous, got %v", err)
	}

	_, err = c.CreateDeployment(ctx, &domain.Deployment{ID: "dep_y", Service: "nope"})
	msg, ok := api.IsValidation(err)
	if !ok || !errors.Is(err, api.ErrValidation) || api.Code(err) != "unknown_service" || !strings.Contains(msg, "nope") {
		t.Fatalf("want validation error carrying the message, got %v", err)
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("error text should include the server message: %v", err)
	}
}

func TestRetriesIdempotentCallsOn5xx(t *testing.T) {
	f, c := newClient(t)
	f.FailNext(503, 502)
	if _, err := c.Config(context.Background()); err != nil {
		t.Fatalf("GET should succeed after two 5xx: %v", err)
	}
	if n := len(f.Requests()); n != 3 {
		t.Fatalf("requests = %d, want 3", n)
	}
}

func TestDoesNotRetryNonIdempotentCalls(t *testing.T) {
	f, c := newClient(t)
	ctx := context.Background()
	if _, err := c.CreateDeployment(ctx, &domain.Deployment{ID: "dep_e", Service: "checkout-api"}); err != nil {
		t.Fatal(err)
	}
	before := len(f.Requests())
	f.FailNext(500)
	err := c.AppendEvent(ctx, "dep_e", domain.Event{Kind: domain.EventStep, Message: "x"})
	if err == nil || !api.IsTransient(err) {
		t.Fatalf("want transient error, got %v", err)
	}
	if n := len(f.Requests()) - before; n != 1 {
		t.Fatalf("event append was sent %d times, want 1", n)
	}
	if len(f.Events("dep_e")) != 0 {
		t.Fatal("no event should be recorded")
	}
}

func TestGivesUpAfterRetries(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(500)
	}))
	defer srv.Close()
	c := api.New(srv.URL, "k", api.WithRetries(2, time.Millisecond))
	_, err := c.Whoami(context.Background())
	if !api.IsTransient(err) {
		t.Fatalf("want transient, got %v", err)
	}
	if hits.Load() != 3 {
		t.Fatalf("hits = %d, want 3 (1 + 2 retries)", hits.Load())
	}
}

func TestNetworkErrorIsTransientAndNoRetryOn4xx(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing listens any more
	c := api.New(url, "k", api.WithRetries(1, time.Millisecond))
	if _, err := c.Whoami(context.Background()); !api.IsTransient(err) {
		t.Fatalf("want transient network error, got %v", err)
	}

	var hits atomic.Int32
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(404)
	}))
	defer srv2.Close()
	c2 := api.New(srv2.URL, "k", api.WithRetries(3, time.Millisecond))
	if _, err := c2.Whoami(context.Background()); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("plain 404 should map to ErrNotFound: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("4xx must not be retried, hits = %d", hits.Load())
	}
}

func TestTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer srv.Close()
	c := api.New(srv.URL, "k", api.WithTimeout(50*time.Millisecond), api.WithRetries(0, 0))
	start := time.Now()
	if _, err := c.Whoami(context.Background()); !api.IsTransient(err) {
		t.Fatalf("want transient timeout, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("timeout not applied")
	}
}

func TestClaimLongPollAndFinish(t *testing.T) {
	f, c := newClient(t)
	f.ClaimWait = 50 * time.Millisecond
	ctx := context.Background()

	j, err := c.ClaimJob(ctx, "w1")
	if err != nil || j != nil {
		t.Fatalf("empty queue should give (nil, nil), got %v %v", j, err)
	}

	f.ClaimWait = 2 * time.Second
	go func() {
		time.Sleep(30 * time.Millisecond)
		f.Enqueue(api.JobDeploy, map[string]any{"service": "checkout-api", "image": "app:2"})
	}()
	j, err = c.ClaimJob(ctx, "w1")
	if err != nil || j == nil || j.Status != api.JobClaimed || j.ClaimedBy != "w1" {
		t.Fatalf("claim = %+v, %v", j, err)
	}
	var p api.DeployPayload
	if err := json.Unmarshal(j.Payload, &p); err != nil || p.Image != "app:2" {
		t.Fatalf("payload = %s", j.Payload)
	}
	done, err := c.FinishJob(ctx, j.ID, api.FinishRequest{Status: api.JobDone, DeploymentID: "dep_1"})
	if err != nil || done.Status != api.JobDone || done.DeploymentID != "dep_1" {
		t.Fatalf("finish = %+v, %v", done, err)
	}
	jobs, err := c.ListJobs(ctx, api.JobDone)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("list jobs = %v, %v", jobs, err)
	}
}

func TestEnqueueAndConfigRoundTrip(t *testing.T) {
	_, c := newClient(t)
	ctx := context.Background()
	j, err := c.EnqueueDeploy(ctx, api.DeployPayload{Service: "checkout-api", Image: "app:3", RiskOverride: "high"})
	if err != nil || j.Kind != api.JobDeploy || j.Status != api.JobQueued {
		t.Fatalf("enqueue = %+v, %v", j, err)
	}
	if _, err := c.EnqueueRollback(ctx, "dep_1", "bad"); err != nil {
		t.Fatal(err)
	}
	cfg, err := c.Config(ctx)
	if err != nil || len(cfg.Services) != 2 {
		t.Fatalf("config = %+v, %v", cfg, err)
	}
	cfg.Services = append(cfg.Services, cfg.Services[0])
	cfg.Services[2].Name = "billing"
	out, err := c.PutConfig(ctx, cfg)
	if err != nil || len(out.Services) != 3 {
		t.Fatalf("put config = %+v, %v", out, err)
	}
}

func TestDeployPayloadLenientRiskOverride(t *testing.T) {
	for in, want := range map[string]string{
		`{"risk_override":"high"}`:          "high",
		`{"risk_override":72}`:              "72",
		`{"risk_override":"15"}`:            "15",
		`{"risk_override":{"score":90}}`:    "90",
		`{"risk_override":{"level":"low"}}`: "low",
	} {
		var p api.DeployPayload
		if err := json.Unmarshal([]byte(in), &p); err != nil || p.RiskOverride != want {
			t.Fatalf("%s: got %q, %v", in, p.RiskOverride, err)
		}
	}
	var p api.DeployPayload
	if err := json.Unmarshal([]byte(`{"service":"a","risk_override":null}`), &p); err != nil || p.RiskOverride != "" || p.Service != "a" {
		t.Fatalf("null override: %+v %v", p, err)
	}
	for in, want := range map[string]int{"low": 20, "Medium": 50, "high": 85, "72": 72, "0": 0} {
		got, ok, err := api.DeployPayload{RiskOverride: in}.RiskOverrideScore()
		if err != nil || !ok || got != want {
			t.Fatalf("%q: got %d %v %v", in, got, ok, err)
		}
	}
	if _, ok, err := (api.DeployPayload{}).RiskOverrideScore(); ok || err != nil {
		t.Fatal("empty override must be (0, false, nil)")
	}
	for _, bad := range []string{"extreme", "150", "-1"} {
		if _, _, err := (api.DeployPayload{RiskOverride: bad}).RiskOverrideScore(); err == nil {
			t.Fatalf("%q should be rejected", bad)
		}
	}
}

func TestNormalizeServer(t *testing.T) {
	for in, want := range map[string]string{
		"http://x:3000/":       "http://x:3000",
		"http://x:3000/api/v1": "http://x:3000",
		"https://x/api/v1/":    "https://x",
		" localhost:3000 ":     "http://localhost:3000",
		"":                     "",
	} {
		if got := api.NormalizeServer(in); got != want {
			t.Errorf("NormalizeServer(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHeartbeatAndLeaseRequeue(t *testing.T) {
	f, c := newClient(t)
	f.ClaimWait = 50 * time.Millisecond
	ctx := context.Background()
	f.Enqueue(api.JobDeploy, map[string]any{"service": "checkout-api", "image": "app:2"})
	j, err := c.ClaimJob(ctx, "r1")
	if err != nil || j == nil || j.ClaimedBy != "r1" {
		t.Fatalf("claim = %+v %v", j, err)
	}
	if _, err := c.HeartbeatJob(ctx, j.ID, "r1"); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if _, err := c.HeartbeatJob(ctx, j.ID, "someone-else"); !errors.Is(err, api.ErrConflict) {
		t.Fatalf("heartbeat by a non-owner: want ErrConflict, got %v", err)
	}

	f.Advance(90 * time.Second) // within the 2-minute lease
	if got, _ := c.ClaimJob(ctx, "r2"); got != nil {
		t.Fatal("a live lease must not be requeued")
	}
	if _, err := c.HeartbeatJob(ctx, j.ID, "r1"); err != nil {
		t.Fatalf("heartbeat renews the lease: %v", err)
	}
	f.Advance(3 * time.Minute) // no heartbeat: the lease expires
	j2, err := c.ClaimJob(ctx, "r2")
	if err != nil || j2 == nil || j2.ID != j.ID || j2.ClaimedBy != "r2" {
		t.Fatalf("expired job should be requeued and claimed by r2: %+v %v", j2, err)
	}
	if _, err := c.HeartbeatJob(ctx, j.ID, "r1"); !errors.Is(err, api.ErrConflict) {
		t.Fatalf("old owner must get ErrConflict, got %v", err)
	}
	if _, err := c.HeartbeatJob(ctx, "nope", "r1"); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("unknown job: %v", err)
	}
}

func TestListBeforeAndServerTimestamps(t *testing.T) {
	f, c := newClient(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 5 {
		if _, err := c.CreateDeployment(ctx, &domain.Deployment{ID: fmt.Sprintf("dep_%d", i), Service: "checkout-api", CreatedAt: base.Add(time.Duration(i) * time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := c.ListDeployments(ctx, api.ListOptions{Limit: 2})
	if err != nil || len(page) != 2 || page[0].ID != "dep_4" || page[1].ID != "dep_3" {
		t.Fatalf("first page = %v %v", ids(page), err)
	}
	page, err = c.ListDeployments(ctx, api.ListOptions{Limit: 2, Before: "dep_3"})
	if err != nil || len(page) != 2 || page[0].ID != "dep_2" || page[1].ID != "dep_1" {
		t.Fatalf("second page = %v %v", ids(page), err)
	}
	page, err = c.ListDeployments(ctx, api.ListOptions{Before: base.Add(90 * time.Minute).Format(time.RFC3339)})
	if err != nil || len(page) != 2 || page[0].ID != "dep_1" {
		t.Fatalf("before time = %v %v", ids(page), err)
	}
	if r := f.Requests(); !strings.Contains(r[len(r)-1].Query, "before=") {
		t.Fatalf("query = %q", r[len(r)-1].Query)
	}

	f.Advance(time.Hour)
	out, err := c.CreateDeployment(ctx, &domain.Deployment{ID: "dep_0", Service: "checkout-api", UpdatedAt: base})
	if err != nil || !out.CreatedAt.Equal(base) || time.Until(out.UpdatedAt) < 50*time.Minute {
		t.Fatalf("server must own updated_at and keep created_at: %+v %v", out, err)
	}
}

func ids(ds []*domain.Deployment) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.ID
	}
	return out
}
