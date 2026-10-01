package runner

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manaskumar3003/alror-cli/internal/api"
	"github.com/manaskumar3003/alror-cli/internal/api/apitest"
	"github.com/manaskumar3003/alror-cli/internal/config"
	"github.com/manaskumar3003/alror-cli/internal/domain"
	"github.com/manaskumar3003/alror-cli/internal/metrics"
)

type logbook struct {
	mu    sync.Mutex
	lines []string
}

func (l *logbook) log(lv Level, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf("%d %s", lv, msg))
}

func (l *logbook) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func noWait(_ context.Context, _ time.Duration, tick func(float64)) error {
	tick(1)
	return nil
}

func setup(t *testing.T) (*apitest.Fake, *httptest.Server, *Runner, *logbook) {
	t.Helper()
	f, srv := apitest.Start()
	t.Cleanup(srv.Close)
	f.ClaimWait = 100 * time.Millisecond
	lb := &logbook{}
	rn := &Runner{
		Client: api.New(srv.URL, apitest.DefaultKey, api.WithRetries(1, time.Millisecond)),
		Name:   "test-runner", Log: lb.log, Wait: noWait,
		MinBackoff: 5 * time.Millisecond, MaxBackoff: 20 * time.Millisecond,
	}
	return f, srv, rn, lb
}

func TestDeployJobPromoted(t *testing.T) {
	f, _, rn, lb := setup(t)
	j := f.Enqueue(api.JobDeploy, map[string]any{"service": "checkout-api", "image": "app:2", "ref": "#12", "environment": "staging"})
	if err := rn.Run(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	got := f.Job(j.ID)
	if got.Status != api.JobDone || got.DeploymentID == "" || got.ClaimedBy != "test-runner" {
		t.Fatalf("job = %+v\n%s", got, lb)
	}
	d := f.Deployment(got.DeploymentID)
	if d == nil || d.Status != domain.StatusPromoted || d.Weight != 100 || d.Ref != "#12" || d.Environment != "staging" || d.Source != "runner" {
		t.Fatalf("deployment = %+v", d)
	}
	if d.Risk.Level != domain.LevelMedium || !strings.Contains(d.Risk.Factors[0].Detail, "no git context in runner") {
		t.Fatalf("default risk = %+v", d.Risk)
	}
	ev := f.Events(d.ID)
	if len(ev) == 0 || ev[0].Kind != domain.EventCreated || ev[len(ev)-1].Kind != domain.EventPromoted {
		t.Fatalf("events = %+v", ev)
	}
	if !strings.Contains(lb.String(), "promoted") {
		t.Fatalf("log should mention the promotion:\n%s", lb)
	}
}

func TestDeployJobRiskOverrideAndRolledBackIsDone(t *testing.T) {
	f, _, rn, _ := setup(t)
	rn.NewMetrics = func(*config.Config) (metrics.Provider, error) {
		return metrics.NewSynthetic(map[string]float64{"error_rate": 1.8}), nil
	}
	j := f.Enqueue(api.JobDeploy, map[string]any{"service": "checkout-api", "image": "app:bad", "risk_override": 85})
	if err := rn.Run(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	got := f.Job(j.ID)
	if got.Status != api.JobDone || got.Error != "" {
		t.Fatalf("a rolled-back release is an outcome, not a failure: %+v", got)
	}
	d := f.Deployment(got.DeploymentID)
	if d.Status != domain.StatusRolledBack || d.Risk.Score != 85 || d.Risk.Level != domain.LevelHigh || d.Plan.Steps[0].Weight != 1 {
		t.Fatalf("deployment = %+v", d)
	}
}

func TestRollbackJob(t *testing.T) {
	f, _, rn, _ := setup(t)
	f.PutDeployment(&domain.Deployment{ID: "dep_20260101T000000_abcdef", Service: "checkout-api", Image: "app:1",
		Status: domain.StatusPromoted, Weight: 100, CreatedAt: time.Now()})
	j := f.Enqueue(api.JobRollback, map[string]any{"deployment_id": "dep_20260101T000000_abc", "reason": "pager went off"})
	if err := rn.Run(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	got := f.Job(j.ID)
	if got.Status != api.JobDone || got.DeploymentID != "dep_20260101T000000_abcdef" {
		t.Fatalf("job = %+v", got)
	}
	d := f.Deployment(got.DeploymentID)
	if d.Status != domain.StatusRolledBack || d.Weight != 0 || d.Reason != "pager went off" {
		t.Fatalf("deployment = %+v", d)
	}
	ev := f.Events(d.ID)
	if len(ev) != 1 || ev[0].Kind != domain.EventRolledBack {
		t.Fatalf("events = %+v", ev)
	}
}

func TestFailurePaths(t *testing.T) {
	f, _, rn, _ := setup(t)
	cfg := f.Config()
	cfg.Services = append(cfg.Services, config.Service{Name: "legacy", Target: "ecs"})
	f.SetConfig(cfg)

	ecs := f.Enqueue(api.JobDeploy, map[string]any{"service": "legacy", "image": "app:2"})
	missing := f.Enqueue(api.JobRollback, map[string]any{"deployment_id": "dep_nope"})
	odd := f.Enqueue("rollback", map[string]any{"deployment_id": ""}) // invalid payload
	for range 3 {
		if err := rn.Run(context.Background(), true); err != nil {
			t.Fatal(err)
		}
	}

	got := f.Job(ecs.ID)
	if got.Status != api.JobFailed || got.DeploymentID == "" || !strings.Contains(got.Error, "not implemented") {
		t.Fatalf("driver failure should fail the job with the deployment id: %+v", got)
	}
	if d := f.Deployment(got.DeploymentID); d == nil || d.Status != domain.StatusFailed {
		t.Fatalf("deployment should be failed: %+v", d)
	}
	if got := f.Job(missing.ID); got.Status != api.JobFailed || !strings.Contains(got.Error, "not found") {
		t.Fatalf("missing deployment: %+v", got)
	}
	if got := f.Job(odd.ID); got.Status != api.JobFailed || !strings.Contains(got.Error, "deployment_id") {
		t.Fatalf("bad payload: %+v", got)
	}
}

func TestOnceWithEmptyQueue(t *testing.T) {
	_, _, rn, lb := setup(t)
	if err := rn.Run(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lb.String(), "no job queued") {
		t.Fatalf("log:\n%s", lb)
	}
}

func TestBadKeyIsFatal(t *testing.T) {
	_, srv, rn, _ := setup(t)
	rn.Client = api.New(srv.URL, "alr_live_wrong")
	err := rn.Run(context.Background(), false)
	if !errors.Is(err, ErrFatal) {
		t.Fatalf("want ErrFatal, got %v", err)
	}
}

func TestBacksOffWhileServerIsDown(t *testing.T) {
	f, _, rn, lb := setup(t)
	f.FailNext(503, 503, 503)
	j := f.Enqueue(api.JobDeploy, map[string]any{"service": "web-frontend", "image": "web:2"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- rn.Run(ctx, false) }()
	deadline := time.Now().Add(5 * time.Second)
	for f.Job(j.ID).Status != api.JobDone && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if f.Job(j.ID).Status != api.JobDone {
		t.Fatalf("job never completed:\n%s", lb)
	}
	log := lb.String()
	if strings.Count(log, "retrying in") != 3 || !strings.Contains(log, "reachable again") {
		t.Fatalf("expected three backoffs and a recovery:\n%s", log)
	}
}

func TestGracefulStopFinishesCurrentJob(t *testing.T) {
	f, _, rn, _ := setup(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	rn.Wait = func(ctx context.Context, _ time.Duration, tick func(float64)) error {
		once.Do(func() { close(started) })
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		tick(1)
		return nil
	}
	first := f.Enqueue(api.JobDeploy, map[string]any{"service": "checkout-api", "image": "app:2"})

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- rn.Run(ctx, false) }()

	<-started
	second := f.Enqueue(api.JobDeploy, map[string]any{"service": "checkout-api", "image": "app:3"})
	stop() // SIGINT: stop claiming, but let the running job finish
	close(release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not stop")
	}
	if got := f.Job(first.ID); got.Status != api.JobDone {
		t.Fatalf("running job should complete: %+v", got)
	}
	if d := f.Deployment(f.Job(first.ID).DeploymentID); d.Status != domain.StatusPromoted {
		t.Fatalf("deployment = %+v", d)
	}
	if got := f.Job(second.ID); got.Status != api.JobQueued {
		t.Fatalf("no new job may be claimed after stop: %+v", got)
	}
}

func TestHardStopAbortsJobAndReportsFailure(t *testing.T) {
	f, _, rn, _ := setup(t)
	jobCtx, kill := context.WithCancel(context.Background())
	rn.JobContext = jobCtx
	rn.Wait = func(ctx context.Context, _ time.Duration, _ func(float64)) error {
		kill() // second Ctrl-C while baking
		<-ctx.Done()
		return ctx.Err()
	}
	j := f.Enqueue(api.JobDeploy, map[string]any{"service": "checkout-api", "image": "app:2"})
	if err := rn.Run(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	got := f.Job(j.ID)
	if got.Status != api.JobFailed || !strings.Contains(got.Error, "interrupted") {
		t.Fatalf("job = %+v", got)
	}
	if d := f.Deployment(got.DeploymentID); d.Status != domain.StatusFailed {
		t.Fatalf("deployment = %+v", d)
	}
}

func TestConcurrency(t *testing.T) {
	f, _, rn, _ := setup(t)
	rn.Concurrency = 3
	var ids []string
	for i := range 6 {
		ids = append(ids, f.Enqueue(api.JobDeploy, map[string]any{"service": "checkout-api", "image": fmt.Sprintf("app:%d", i)}).ID)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- rn.Run(ctx, false) }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		n := 0
		for _, id := range ids {
			if f.Job(id).Status == api.JobDone {
				n++
			}
		}
		if n == len(ids) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	for _, id := range ids {
		if s := f.Job(id).Status; s != api.JobDone {
			t.Fatalf("job %s = %s", id, s)
		}
	}
}

func TestRiskFor(t *testing.T) {
	r, err := RiskFor(api.DeployPayload{RiskOverride: "high"}, "x")
	if err != nil || r.Score != 85 || r.Level != domain.LevelHigh || !strings.Contains(r.Factors[0].Detail, `"high"`) {
		t.Fatalf("level override = %+v %v", r, err)
	}
	r, err = RiskFor(api.DeployPayload{RiskOverride: "30"}, "x")
	if err != nil || r.Score != 30 || r.Level != domain.LevelLow {
		t.Fatalf("score override = %+v %v", r, err)
	}
	r, err = RiskFor(api.DeployPayload{}, "x")
	if err != nil || r.Score != 50 || r.Level != domain.LevelMedium || r.Services[0] != "x" {
		t.Fatalf("default = %+v %v", r, err)
	}
	if _, err := RiskFor(api.DeployPayload{RiskOverride: "extreme"}, "x"); err == nil {
		t.Fatal("invalid override must fail the job")
	}
}

func TestDeployJobWithLevelOverride(t *testing.T) {
	f, _, rn, _ := setup(t)
	j := f.Enqueue(api.JobDeploy, map[string]any{"service": "web-frontend", "image": "web:2", "risk_override": "low"})
	if err := rn.Run(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	got := f.Job(j.ID)
	d := f.Deployment(got.DeploymentID)
	if got.Status != api.JobDone || d.Risk.Level != domain.LevelLow || len(d.Plan.Steps) != 2 {
		t.Fatalf("job = %+v deployment = %+v", got, d)
	}
}

func TestHeartbeatKeepsTheLease(t *testing.T) {
	f, _, rn, _ := setup(t)
	rn.HeartbeatInterval = 10 * time.Millisecond
	rn.Wait = func(ctx context.Context, _ time.Duration, tick func(float64)) error {
		// Two 90 s jumps add up to more than the 2 min lease, but heartbeats
		// in between keep renewing it.
		for range 2 {
			f.Advance(90 * time.Second)
			time.Sleep(60 * time.Millisecond)
		}
		tick(1)
		return ctx.Err()
	}
	j := f.Enqueue(api.JobDeploy, map[string]any{"service": "web-frontend", "image": "web:2", "risk_override": "low"})
	if err := rn.Run(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if got := f.Job(j.ID); got.Status != api.JobDone || got.ClaimedBy != "test-runner" {
		t.Fatalf("job = %+v", got)
	}
	beats := 0
	for _, r := range f.Requests() {
		if strings.HasSuffix(r.Path, "/heartbeat") {
			beats++
		}
	}
	if beats < 2 {
		t.Fatalf("heartbeats = %d", beats)
	}
}

func TestLeaseLostAbortsWithoutFinishing(t *testing.T) {
	f, srv, rn, lb := setup(t)
	rn.HeartbeatInterval = 10 * time.Millisecond
	other := api.New(srv.URL, apitest.DefaultKey)
	stolen := make(chan struct{})
	rn.Wait = func(ctx context.Context, _ time.Duration, _ func(float64)) error {
		// Simulate a long network partition: the lease expires and another
		// runner claims the job.
		for i := 0; i < 50; i++ {
			f.Advance(3 * time.Minute)
			if j, _ := other.ClaimJob(context.Background(), "other-runner"); j != nil {
				close(stolen)
				break
			}
		}
		<-ctx.Done() // the heartbeat's 409 cancels the job
		return ctx.Err()
	}
	j := f.Enqueue(api.JobDeploy, map[string]any{"service": "checkout-api", "image": "app:2"})
	done := make(chan error)
	go func() { done <- rn.Run(context.Background(), true) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("runner did not abort the job:\n%s", lb)
	}
	select {
	case <-stolen:
	default:
		t.Fatal("test setup: the job was never reclaimed")
	}
	got := f.Job(j.ID)
	if got.Status != api.JobClaimed || got.ClaimedBy != "other-runner" || got.FinishedAt != nil {
		t.Fatalf("the new owner's job must be left alone: %+v", got)
	}
	for _, r := range f.Requests() {
		if strings.HasSuffix(r.Path, "/finish") {
			t.Fatal("a runner that lost its lease must not call finish")
		}
	}
	var dep *domain.Deployment
	for _, r := range f.Requests() {
		if r.Method == "PUT" {
			dep = f.Deployment(strings.TrimPrefix(r.Path, "/api/v1/deployments/"))
		}
	}
	if dep == nil || dep.Status != domain.StatusFailed {
		t.Fatalf("the aborted rollout should be failed (canary rolled back): %+v", dep)
	}
	if !strings.Contains(lb.String(), "lease lost") {
		t.Fatalf("log:\n%s", lb)
	}
}
