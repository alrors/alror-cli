package alror_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/manaskumar3003/alror-cli/internal/api/apitest"
	"github.com/manaskumar3003/alror-cli/pkg/alror"
)

func start(t *testing.T, opts ...alror.Option) (*apitest.Fake, *alror.Client) {
	t.Helper()
	f, srv := apitest.Start()
	t.Cleanup(srv.Close)
	f.ClaimWait = 50 * time.Millisecond
	return f, alror.NewClient(srv.URL, apitest.DefaultKey, opts...)
}

func TestPublicSurfaceRoundTrip(t *testing.T) {
	f, c := start(t)
	ctx := context.Background()

	cfg, err := c.Config(ctx)
	if err != nil || len(cfg.Services) != 2 {
		t.Fatalf("config: %v %+v", err, cfg)
	}
	cfg.Services = append(cfg.Services, alror.Service{Name: "billing", Paths: []string{"billing/"}, Target: "simulated"})
	if out, err := c.PutConfig(ctx, cfg); err != nil || len(out.Services) != 3 {
		t.Fatalf("put config: %v", err)
	}

	d := &alror.Deployment{
		ID: alror.NewDeploymentID(), Service: "billing", Image: "billing:2", Status: alror.StatusRolling, Weight: 5,
		Risk:      alror.RiskReport{Score: 40, Level: alror.LevelMedium, Factors: []alror.Factor{{Name: "Diff size", Points: 15}}},
		Plan:      alror.Plan{Strategy: "canary", Steps: []alror.Step{{Weight: 5, Bake: time.Minute}, {Weight: 100}}},
		CreatedAt: time.Now().UTC(),
	}
	if !strings.HasPrefix(d.ID, "dep_") {
		t.Fatalf("id = %s", d.ID)
	}
	if _, err := c.CreateOrUpdateDeployment(ctx, d); err != nil {
		t.Fatal(err)
	}
	d.Status = alror.StatusPromoted
	if _, err := c.CreateOrUpdateDeployment(ctx, d); err != nil {
		t.Fatal(err)
	}
	got, err := c.GetDeployment(ctx, d.ID[:10])
	if err != nil || got.Status != alror.StatusPromoted || got.Plan.Steps[0].Bake != time.Minute {
		t.Fatalf("get: %v %+v", err, got)
	}
	if err := c.AppendEvent(ctx, d.ID, alror.Event{Kind: alror.EventPromoted, Message: "ok", Weight: 100,
		Verdict: &alror.Verdict{Pass: true, Results: []alror.MetricResult{{Metric: "error_rate", Pass: true}}}}); err != nil {
		t.Fatal(err)
	}
	ev, err := c.Events(ctx, d.ID)
	if err != nil || len(ev) != 1 || ev[0].At.IsZero() || ev[0].Verdict.Results[0].Metric != "error_rate" {
		t.Fatalf("events: %v %+v", err, ev)
	}
	list, err := c.ListDeployments(ctx, alror.ListOptions{Service: "billing", Status: string(alror.StatusPromoted), Limit: 5})
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %d", err, len(list))
	}

	d.Status = alror.StatusRolledBack
	if _, err := c.CreateOrUpdateDeployment(ctx, d); err != nil {
		t.Fatal(err)
	}
	rb, err := c.RecentRollbacks(ctx, time.Now().Add(-time.Hour))
	if err != nil || rb["billing"] != 1 {
		t.Fatalf("rollbacks: %v %v", err, rb)
	}

	// Jobs: enqueue, claim, finish, list.
	job, err := c.EnqueueRollback(ctx, d.ID, "because")
	if err != nil || job.Kind != alror.JobKindRollback || job.Status != alror.JobQueued {
		t.Fatalf("enqueue rollback: %v %+v", err, job)
	}
	claimed, err := c.ClaimJob(ctx, "sdk-runner")
	if err != nil || claimed == nil || claimed.ID != job.ID || claimed.Status != alror.JobClaimed {
		t.Fatalf("claim: %v %+v", err, claimed)
	}
	var p alror.RollbackRequest
	if err := json.Unmarshal(claimed.Payload, &p); err != nil || p.Reason != "because" {
		t.Fatalf("payload: %s", claimed.Payload)
	}
	if none, err := c.ClaimJob(ctx, "sdk-runner"); err != nil || none != nil {
		t.Fatalf("empty claim: %v %+v", err, none)
	}
	fin, err := c.FinishJob(ctx, job.ID, alror.JobResult{Status: alror.JobDone, DeploymentID: d.ID})
	if err != nil || fin.Status != alror.JobDone {
		t.Fatalf("finish: %v %+v", err, fin)
	}
	jobs, err := c.ListJobs(ctx, alror.JobDone)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("list jobs: %v %d", err, len(jobs))
	}
	if f.Job(job.ID).DeploymentID != d.ID {
		t.Fatal("finish did not record the deployment")
	}
}

func TestTypedErrors(t *testing.T) {
	f, c := start(t)
	ctx := context.Background()

	if _, err := alror.NewClient(c.Server(), "alr_live_bad").WhoAmI(ctx); !errors.Is(err, alror.ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized: %v", err)
	}
	f.AddKey("alr_live_ro", "ro", alror.ScopeDeployRead)
	if _, err := alror.NewClient(c.Server(), "alr_live_ro").EnqueueRollback(ctx, "x", ""); !errors.Is(err, alror.ErrForbidden) {
		t.Fatalf("want ErrForbidden: %v", err)
	}
	if _, err := c.GetDeployment(ctx, "dep_nope"); !errors.Is(err, alror.ErrNotFound) {
		t.Fatalf("want ErrNotFound: %v", err)
	}
	_, err := c.EnqueueDeploy(ctx, alror.DeployRequest{Service: "ghost", Image: "x"})
	var apiErr *alror.APIError
	if !errors.Is(err, alror.ErrValidation) || !errors.As(err, &apiErr) || apiErr.Code != "unknown_service" || apiErr.Status != 422 {
		t.Fatalf("want validation error: %v", err)
	}
	for _, id := range []string{"dep_a1", "dep_a2"} {
		if _, err := c.CreateOrUpdateDeployment(ctx, &alror.Deployment{ID: id, Service: "checkout-api"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.GetDeployment(ctx, "dep_a"); !errors.Is(err, alror.ErrConflict) {
		t.Fatalf("want ErrConflict: %v", err)
	}
	if _, err := alror.NewClient("http://127.0.0.1:1", "k", alror.WithTimeout(200*time.Millisecond)).WhoAmI(ctx); !alror.IsTransient(err) {
		t.Fatalf("want transient: %v", err)
	}
}

type countingTransport struct{ n int }

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n++
	return http.DefaultTransport.RoundTrip(r)
}

func TestOptions(t *testing.T) {
	tr := &countingTransport{}
	f, c := start(t, alror.WithUserAgent("my-tool/1.0"), alror.WithHTTPClient(&http.Client{Transport: tr}), alror.WithTimeout(5*time.Second))
	if _, err := c.WhoAmI(context.Background()); err != nil {
		t.Fatal(err)
	}
	r := f.Requests()[0]
	if r.UserAgent != "my-tool/1.0" || r.Authorization != "Bearer "+apitest.DefaultKey || r.Source != "sdk" {
		t.Fatalf("request = %+v", r)
	}
	if tr.n != 1 {
		t.Fatalf("custom HTTP client not used: %d", tr.n)
	}

	_, c2 := start(t)
	if _, err := c2.WhoAmI(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDeployRequestJSONMatchesContract(t *testing.T) {
	raw, _ := json.Marshal(alror.DeployRequest{Service: "s", Image: "i", Environment: "staging", Shadow: true, RiskOverride: "high"})
	want := `{"service":"s","image":"i","environment":"staging","shadow":true,"risk_override":"high"}`
	if string(raw) != want {
		t.Fatalf("got %s\nwant %s", raw, want)
	}
	raw, _ = json.Marshal(alror.Step{Weight: 5, Bake: time.Second})
	if string(raw) != `{"weight":5,"bake":1000000000}` {
		t.Fatalf("bake must be integer nanoseconds: %s", raw)
	}
}

func TestPagingHeartbeatAndServerTimestamps(t *testing.T) {
	f, c := start(t)
	ctx := context.Background()
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	for i := range 3 {
		d := &alror.Deployment{ID: fmt.Sprintf("dep_p%d", i), Service: "checkout-api", CreatedAt: base.Add(time.Duration(i) * time.Minute), Environment: "staging", Source: "sdk"}
		if _, err := c.CreateOrUpdateDeployment(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	page, err := c.ListDeployments(ctx, alror.ListOptions{Limit: 1, Before: "dep_p2"})
	if err != nil || len(page) != 1 || page[0].ID != "dep_p1" || page[0].Environment != "staging" || page[0].Source != "sdk" {
		t.Fatalf("page = %+v %v", page, err)
	}

	f.Advance(time.Hour)
	d := &alror.Deployment{ID: "dep_p0", Service: "checkout-api"}
	if _, err := c.CreateOrUpdateDeployment(ctx, d); err != nil {
		t.Fatal(err)
	}
	if !d.CreatedAt.Equal(base) || time.Until(d.UpdatedAt) < 50*time.Minute {
		t.Fatalf("timestamps should come from the server: %+v", d)
	}

	job, _ := c.EnqueueRollback(ctx, "dep_p0", "x")
	if _, err := c.ClaimJob(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.HeartbeatJob(ctx, job.ID, "r1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.HeartbeatJob(ctx, job.ID, "r2"); !errors.Is(err, alror.ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}
