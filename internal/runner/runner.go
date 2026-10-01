// Package runner runs deploys and rollbacks queued from the Alror console,
// inside your own infrastructure (like a CI runner). It claims jobs from the
// Alror workspace over POST /jobs/claim (long-poll), runs them with the
// rollout engine against the remote store, and reports each outcome with
// POST /jobs/{id}/finish.
//
// Stopping is graceful: cancelling the context passed to Run stops claiming
// new jobs, while jobs already running finish under JobContext. A rolled-back
// release is a successful job (status done): it is an outcome, not a failure.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/manaskumar3003/alror-cli/internal/api"
	"github.com/manaskumar3003/alror-cli/internal/config"
	"github.com/manaskumar3003/alror-cli/internal/domain"
	"github.com/manaskumar3003/alror-cli/internal/driver"
	"github.com/manaskumar3003/alror-cli/internal/metrics"
	"github.com/manaskumar3003/alror-cli/internal/notify"
	"github.com/manaskumar3003/alror-cli/internal/risk"
	"github.com/manaskumar3003/alror-cli/internal/rollout"
	"github.com/manaskumar3003/alror-cli/internal/store"
	"github.com/manaskumar3003/alror-cli/internal/verify"
)

// Level classifies a log line.
type Level int

const (
	Info Level = iota
	OK
	Warn
	Error
)

// Runner claims and runs jobs. Only Client is required.
type Runner struct {
	Client      *api.Client
	Store       store.Store // default store.NewRemote(Client)
	Name        string      // reported as claimed_by; default "runner"
	Concurrency int         // parallel jobs; default 1

	// Log receives one line per event. It must be safe for concurrent use.
	Log func(Level, string)

	// JobContext bounds running jobs (default context.Background()). Cancel
	// it to abort jobs in flight: the engine then rolls the canary back.
	JobContext context.Context

	// MinBackoff and MaxBackoff bound the retry delay while the server is down (1 s, 30 s).
	MinBackoff, MaxBackoff time.Duration

	// HeartbeatInterval is how often a running job's lease is extended
	// (default 30 s). Losing the lease (409) aborts the job without finishing it.
	HeartbeatInterval time.Duration

	// Test hooks.
	Wait       func(ctx context.Context, d time.Duration, tick func(float64)) error
	NewDriver  func(config.Service) (driver.Driver, error)
	NewMetrics func(*config.Config) (metrics.Provider, error)
}

// ErrFatal wraps errors that retrying cannot fix (bad key, missing scope).
var ErrFatal = errors.New("runner cannot continue")

func (rn *Runner) defaults() {
	if rn.Store == nil {
		rn.Store = store.NewRemote(rn.Client)
	}
	if rn.Name == "" {
		rn.Name = "runner"
	}
	if rn.Concurrency < 1 {
		rn.Concurrency = 1
	}
	if rn.Log == nil {
		rn.Log = func(Level, string) {}
	}
	if rn.JobContext == nil {
		rn.JobContext = context.Background()
	}
	if rn.MinBackoff <= 0 {
		rn.MinBackoff = time.Second
	}
	if rn.MaxBackoff <= 0 {
		rn.MaxBackoff = 30 * time.Second
	}
	if rn.HeartbeatInterval <= 0 {
		rn.HeartbeatInterval = 30 * time.Second
	}
	if rn.NewDriver == nil {
		rn.NewDriver = driver.For
	}
	if rn.NewMetrics == nil {
		rn.NewMetrics = metrics.New
	}
}

// Run claims and executes jobs until ctx is cancelled. With once, it makes a
// single claim (one long-poll), runs the job if one arrived, and returns.
func (rn *Runner) Run(ctx context.Context, once bool) error {
	rn.defaults()
	n := rn.Concurrency
	if once {
		n = 1
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			if err := rn.loop(ctx, slot, once); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				cancel() // a fatal error stops every slot
			}
		}(i)
	}
	wg.Wait()
	return firstErr
}

func (rn *Runner) loop(ctx context.Context, slot int, once bool) error {
	var delay time.Duration
	for {
		if ctx.Err() != nil {
			return nil
		}
		job, err := rn.Client.ClaimJob(ctx, rn.Name)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, api.ErrUnauthorized) {
				return fmt.Errorf("%w: the API key was rejected (%v); run `alror login`", ErrFatal, err)
			}
			if errors.Is(err, api.ErrForbidden) {
				return fmt.Errorf("%w: the API key needs the jobs:run scope (%v)", ErrFatal, err)
			}
			if once {
				return err
			}
			delay = nextBackoff(delay, rn.MinBackoff, rn.MaxBackoff)
			rn.Log(Warn, fmt.Sprintf("claim failed: %v · retrying in %s", err, delay.Round(100*time.Millisecond)))
			if sleep(ctx, delay) != nil {
				return nil
			}
			continue
		}
		if delay > 0 {
			rn.Log(OK, "workspace reachable again")
			delay = 0
		}
		if job == nil {
			if once {
				rn.Log(Info, "no job queued")
				return nil
			}
			continue // long-poll window ended; ask again
		}
		rn.handle(job)
		if once {
			return nil
		}
	}
}

// handle executes one claimed job and reports the outcome. It never panics.
func (rn *Runner) handle(job *api.Job) {
	start := time.Now()
	rn.Log(Info, fmt.Sprintf("job %s claimed · %s", short(job.ID), describe(job)))

	jobCtx, cancel := context.WithCancel(rn.JobContext)
	defer cancel()
	var lost atomic.Bool
	stopBeat := make(chan struct{})
	beatDone := make(chan struct{})
	go func() {
		defer close(beatDone)
		rn.heartbeat(job.ID, stopBeat, func() { lost.Store(true); cancel() })
	}()

	depID, err := rn.safeExecute(jobCtx, job)
	close(stopBeat)
	<-beatDone
	if lost.Load() {
		// Another runner may own the job now; finishing it here would race that runner.
		rn.Log(Error, fmt.Sprintf("job %s: lease lost; stopped without reporting%s", short(job.ID), suffix(depID)))
		return
	}
	req := api.FinishRequest{Status: api.JobDone, DeploymentID: depID}
	if err != nil {
		req.Status, req.Error = api.JobFailed, err.Error()
	}
	if ferr := rn.finish(job.ID, req); ferr != nil {
		rn.Log(Error, fmt.Sprintf("job %s: could not report the outcome: %v", short(job.ID), ferr))
		return
	}
	took := time.Since(start).Round(time.Millisecond)
	if err != nil {
		rn.Log(Error, fmt.Sprintf("job %s failed after %s · %v", short(job.ID), took, err))
		return
	}
	rn.Log(OK, fmt.Sprintf("job %s done in %s%s", short(job.ID), took, suffix(depID)))
}

// heartbeat extends the job's lease every HeartbeatInterval until stop is
// closed. On 409 (lease lost to another runner) it calls onLost once; other
// errors (including 404 from a server without the endpoint) only log a warning.
func (rn *Runner) heartbeat(id string, stop <-chan struct{}, onLost func()) {
	t := time.NewTicker(rn.HeartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), rn.HeartbeatInterval)
		_, err := rn.Client.HeartbeatJob(ctx, id, rn.Name)
		cancel()
		switch {
		case err == nil:
		case errors.Is(err, api.ErrConflict):
			rn.Log(Error, fmt.Sprintf("job %s: lease lost (%v); aborting, the canary will be rolled back", short(id), err))
			onLost()
			return
		default:
			rn.Log(Warn, fmt.Sprintf("job %s: heartbeat failed: %v", short(id), err))
		}
	}
}

func (rn *Runner) safeExecute(ctx context.Context, job *api.Job) (depID string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("runner panic: %v", r)
		}
	}()
	switch job.Kind {
	case api.JobDeploy:
		return rn.deploy(ctx, job)
	case api.JobRollback:
		return rn.rollback(ctx, job)
	default:
		return "", fmt.Errorf("unknown job kind %q", job.Kind)
	}
}

// finish reports the outcome, retrying for a while if the server is down:
// the job ran, so losing its result is worse than waiting.
func (rn *Runner) finish(id string, req api.FinishRequest) error {
	var delay time.Duration
	var err error
	for attempt := 0; attempt < 6; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		_, err = rn.Client.FinishJob(ctx, id, req)
		cancel()
		if err == nil || !api.IsTransient(err) {
			return err
		}
		delay = nextBackoff(delay, rn.MinBackoff, rn.MaxBackoff)
		rn.Log(Warn, fmt.Sprintf("job %s: finish failed (%v) · retrying in %s", short(id), err, delay.Round(100*time.Millisecond)))
		time.Sleep(delay)
	}
	return err
}

func (rn *Runner) config(ctx context.Context) (*config.Config, error) {
	cfg, err := rn.Client.Config(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch config: %w", err)
	}
	cfg.ApplyDefaults()
	return cfg, nil
}

func (rn *Runner) deploy(ctx context.Context, job *api.Job) (string, error) {
	var p api.DeployPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return "", fmt.Errorf("bad deploy payload: %w", err)
	}
	if p.Service == "" || p.Image == "" {
		return "", errors.New("deploy payload needs service and image")
	}
	cfg, err := rn.config(ctx)
	if err != nil {
		return "", err
	}
	svc, ok := cfg.Service(p.Service)
	if !ok {
		return "", fmt.Errorf("unknown service %q", p.Service)
	}
	if p.Shadow {
		cfg.Policy.AutoRollback = false
	}
	report, err := RiskFor(p, svc.Name)
	if err != nil {
		return "", err
	}

	drv, err := rn.NewDriver(svc)
	if err != nil {
		return "", err
	}
	prov, err := rn.NewMetrics(cfg)
	if err != nil {
		return "", err
	}
	var notifier notify.Notifier = notify.Nop{}
	if cfg.Notify.SlackWebhook != "" {
		notifier = notify.Slack{Webhook: cfg.Notify.SlackWebhook}
	}
	d := &domain.Deployment{
		ID: store.NewID(), Service: svc.Name, Image: p.Image, Ref: p.Ref,
		Environment: p.Environment, Source: "runner",
		Risk: report, Plan: rollout.PlanFor(report), Status: domain.StatusPending, CreatedAt: time.Now().UTC(),
	}
	// Record it before touching traffic, so a rejected write (unknown service,
	// missing scope) fails the job instead of running an unrecorded rollout.
	if err := rn.Store.Save(d); err != nil {
		return "", err
	}
	rn.Log(Info, fmt.Sprintf("%s · %s %s · risk %d (%s) · %s on %s", d.ID, d.Service, d.Image, report.Score, report.Level, planWeights(d.Plan), drv.Name()))

	eng := &rollout.Engine{
		Cfg: cfg, Store: rn.Store, Driver: drv, Metrics: prov, Notifier: notifier, Wait: rn.Wait,
		Observer: &observer{rn: rn, d: d},
		Verifier: verify.Verifier{MaxRegression: cfg.Policy.MaxRegression, Alpha: cfg.Policy.Alpha},
	}
	runErr := eng.Run(ctx, d)
	if err := rn.Store.Save(d); err != nil { // the engine ignores write errors; make sure the outcome lands
		rn.Log(Warn, fmt.Sprintf("%s: could not save the final state: %v", d.ID, err))
	}
	if runErr != nil {
		return d.ID, runErr
	}
	return d.ID, nil
}

// RiskFor builds the risk report for a deploy job. Runners have no git
// context, so without risk_override the change is treated as medium risk.
func RiskFor(p api.DeployPayload, service string) (domain.RiskReport, error) {
	score, ok, err := p.RiskOverrideScore()
	if err != nil {
		return domain.RiskReport{}, err
	}
	if ok {
		return domain.RiskReport{Score: score, Level: risk.LevelFor(score), Services: []string{service},
			Factors: []domain.Factor{{Name: "Manual risk", Detail: fmt.Sprintf("risk_override %q in the job payload", p.RiskOverride), Points: score}}}, nil
	}
	return domain.RiskReport{Score: 50, Level: domain.LevelMedium, Services: []string{service},
		Factors: []domain.Factor{{Name: "Default score", Detail: "no git context in runner; assuming medium risk", Points: 50}}}, nil
}

func (rn *Runner) rollback(ctx context.Context, job *api.Job) (string, error) {
	var p api.RollbackPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return "", fmt.Errorf("bad rollback payload: %w", err)
	}
	if p.DeploymentID == "" {
		return "", errors.New("rollback payload needs deployment_id")
	}
	d, err := rn.Store.Get(p.DeploymentID)
	if err != nil {
		return "", err
	}
	if d.Status == domain.StatusRolledBack {
		rn.Log(Info, d.ID+" is already rolled back")
		return d.ID, nil
	}
	cfg, err := rn.config(ctx)
	if err != nil {
		return d.ID, err
	}
	svc, ok := cfg.Service(d.Service)
	if !ok {
		return d.ID, fmt.Errorf("service %q is no longer configured", d.Service)
	}
	drv, err := rn.NewDriver(svc)
	if err != nil {
		return d.ID, err
	}
	reason := p.Reason
	if reason == "" {
		reason = "requested from the Alror console"
	}
	eng := &rollout.Engine{Cfg: cfg, Store: rn.Store, Driver: drv, Observer: &observer{rn: rn, d: d}}
	if err := eng.Rollback(ctx, d, reason); err != nil {
		return d.ID, err
	}
	return d.ID, nil
}

// observer turns engine events into log lines (no progress bars: this is a daemon).
type observer struct {
	rn *Runner
	d  *domain.Deployment
}

func (o *observer) Progress(int, float64) {}

func (o *observer) Event(e domain.Event) {
	id := o.d.ID
	switch e.Kind {
	case domain.EventStep:
		o.rn.Log(Info, fmt.Sprintf("%s · %s", id, e.Message))
	case domain.EventVerdict:
		if e.Verdict != nil && e.Verdict.Pass {
			o.rn.Log(OK, fmt.Sprintf("%s · %d%% verified · %s", id, e.Weight, e.Message))
		} else {
			o.rn.Log(Warn, fmt.Sprintf("%s · %s", id, e.Message))
		}
	case domain.EventPromoted:
		o.rn.Log(OK, fmt.Sprintf("%s · promoted · %s", id, e.Message))
	case domain.EventRolledBack:
		o.rn.Log(Warn, fmt.Sprintf("%s · rolled back · %s", id, e.Message))
	case domain.EventError:
		o.rn.Log(Error, fmt.Sprintf("%s · %s", id, e.Message))
	}
}

func describe(j *api.Job) string {
	switch j.Kind {
	case api.JobDeploy:
		var p api.DeployPayload
		if json.Unmarshal(j.Payload, &p) == nil {
			s := "deploy " + p.Service + " " + p.Image
			if p.Ref != "" {
				s += " " + p.Ref
			}
			if p.Environment != "" {
				s += " → " + p.Environment
			}
			return s
		}
	case api.JobRollback:
		var p api.RollbackPayload
		if json.Unmarshal(j.Payload, &p) == nil {
			return "rollback " + p.DeploymentID
		}
	}
	return j.Kind
}

func planWeights(p domain.Plan) string {
	parts := make([]string, len(p.Steps))
	for i, s := range p.Steps {
		parts[i] = fmt.Sprintf("%d%%", s.Weight)
	}
	return strings.Join(parts, "→")
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func suffix(depID string) string {
	if depID == "" {
		return ""
	}
	return " · " + depID
}

func nextBackoff(cur, lo, hi time.Duration) time.Duration {
	if cur <= 0 {
		cur = lo
	} else {
		cur *= 2
	}
	if cur > hi {
		cur = hi
	}
	return cur - cur/5 + rand.N(cur/5+1) // ±20% jitter so many runners don't stampede
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
