package rollout

import (
	"context"
	"fmt"
	"time"

	"github.com/manaskumar3003/alror-cli/internal/config"
	"github.com/manaskumar3003/alror-cli/internal/domain"
	"github.com/manaskumar3003/alror-cli/internal/driver"
	"github.com/manaskumar3003/alror-cli/internal/metrics"
	"github.com/manaskumar3003/alror-cli/internal/notify"
	"github.com/manaskumar3003/alror-cli/internal/store"
	"github.com/manaskumar3003/alror-cli/internal/verify"
)

// Observer receives live updates; the CLI uses it to draw progress.
type Observer interface {
	Event(domain.Event)
	Progress(step int, fraction float64)
}

// Engine executes a deployment's plan: shift traffic, bake, verify, decide.
//
// State machine:
//
//	pending → rolling ─┬─ step passes ─→ next step … → promoted
//	                   ├─ step fails, auto_rollback ─→ rolled_back
//	                   ├─ step fails, shadow mode ──→ continue (recommendation logged)
//	                   └─ driver error ─────────────→ failed (rollback attempted)
type Engine struct {
	Cfg      *config.Config
	Store    store.Store
	Driver   driver.Driver
	Metrics  metrics.Provider
	Verifier verify.Verifier
	Notifier notify.Notifier
	Observer Observer

	// Wait blocks for a bake period, reporting progress. Tests replace it.
	Wait func(ctx context.Context, d time.Duration, tick func(fraction float64)) error
}

// Run drives the deployment to a terminal status. It persists every
// transition, so an interrupted run leaves an accurate record behind.
func (e *Engine) Run(ctx context.Context, d *domain.Deployment) error {
	svc, ok := e.Cfg.Service(d.Service)
	if !ok {
		return fmt.Errorf("unknown service %q", d.Service)
	}
	if e.Notifier == nil {
		e.Notifier = notify.Nop{}
	}
	if e.Wait == nil {
		e.Wait = realWait
	}

	d.Status = domain.StatusRolling
	e.emit(d, domain.Event{Kind: domain.EventCreated, Message: fmt.Sprintf(
		"Risk %d (%s) · %s plan with %d steps", d.Risk.Score, d.Risk.Level, d.Plan.Strategy, len(d.Plan.Steps))})

	for i, step := range d.Plan.Steps {
		d.StepIndex, d.Weight = i, step.Weight

		if step.Weight >= 100 {
			if err := e.Driver.Promote(ctx, svc, d.Image); err != nil {
				return e.fail(ctx, d, svc, err)
			}
			d.Status = domain.StatusPromoted
			msg := fmt.Sprintf("Verified at every step · promoted %s to 100%%", d.Service)
			e.emit(d, domain.Event{Kind: domain.EventPromoted, Weight: 100, Message: msg})
			_ = e.Notifier.Notify(ctx, d, msg)
			return nil
		}

		if err := e.Driver.SetWeight(ctx, svc, d.Image, step.Weight); err != nil {
			return e.fail(ctx, d, svc, err)
		}
		e.emit(d, domain.Event{Kind: domain.EventStep, Weight: step.Weight, Message: fmt.Sprintf("Canary at %d%% · baking %s", step.Weight, step.Bake)})

		if err := e.Wait(ctx, e.Cfg.Bake(step.Bake), func(f float64) {
			if e.Observer != nil {
				e.Observer.Progress(i, f)
			}
		}); err != nil {
			return e.fail(ctx, d, svc, fmt.Errorf("interrupted: %w", err))
		}

		verdict, err := e.judge(ctx, d.Service, step.Bake)
		if err != nil {
			return e.fail(ctx, d, svc, err)
		}
		e.emit(d, domain.Event{Kind: domain.EventVerdict, Weight: step.Weight, Message: verdict.Summary, Verdict: &verdict})

		if verdict.Pass {
			continue
		}
		if !e.Cfg.Policy.AutoRollback {
			e.emit(d, domain.Event{Kind: domain.EventVerdict, Weight: step.Weight,
				Message: "Shadow mode: would roll back here; continuing because auto_rollback is off"})
			continue
		}
		if err := e.Driver.Rollback(ctx, svc); err != nil {
			return e.fail(ctx, d, svc, err)
		}
		d.Status, d.Reason = domain.StatusRolledBack, verdict.Summary
		e.emit(d, domain.Event{Kind: domain.EventRolledBack, Weight: step.Weight, Message: fmt.Sprintf(
			"Rolled back at %d%% traffic · %s", step.Weight, verdict.Summary)})
		_ = e.Notifier.Notify(ctx, d, verdict.Summary)
		return nil
	}
	return nil
}

// Rollback reverses a deployment on request (`alror rollback`).
func (e *Engine) Rollback(ctx context.Context, d *domain.Deployment, reason string) error {
	svc, ok := e.Cfg.Service(d.Service)
	if !ok {
		return fmt.Errorf("unknown service %q", d.Service)
	}
	if err := e.Driver.Rollback(ctx, svc); err != nil {
		return err
	}
	d.Status, d.Reason, d.Weight = domain.StatusRolledBack, reason, 0
	e.emit(d, domain.Event{Kind: domain.EventRolledBack, Message: "Manual rollback · " + reason})
	return nil
}

func (e *Engine) judge(ctx context.Context, service string, window time.Duration) (domain.Verdict, error) {
	data := map[string]verify.Samples{}
	for _, m := range metrics.Names(e.Cfg) {
		canary, err := e.Metrics.Sample(ctx, service, m, metrics.Canary, window)
		if err != nil {
			return domain.Verdict{}, err
		}
		baseline, err := e.Metrics.Sample(ctx, service, m, metrics.Baseline, window)
		if err != nil {
			return domain.Verdict{}, err
		}
		data[m] = verify.Samples{Canary: canary, Baseline: baseline}
	}
	return e.Verifier.Judge(data), nil
}

func (e *Engine) fail(ctx context.Context, d *domain.Deployment, svc config.Service, cause error) error {
	_ = e.Driver.Rollback(ctx, svc) // best effort: never leave a half-shifted canary behind
	d.Status, d.Reason = domain.StatusFailed, cause.Error()
	e.emit(d, domain.Event{Kind: domain.EventError, Weight: d.Weight, Message: cause.Error()})
	return cause
}

func (e *Engine) emit(d *domain.Deployment, ev domain.Event) {
	ev.At = time.Now().UTC()
	_ = e.Store.Save(d)
	_ = e.Store.Append(d.ID, ev)
	if e.Observer != nil {
		e.Observer.Event(ev)
	}
}

func realWait(ctx context.Context, d time.Duration, tick func(float64)) error {
	if d <= 0 {
		tick(1)
		return nil
	}
	start := time.Now()
	t := time.NewTicker(80 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-t.C:
			f := float64(now.Sub(start)) / float64(d)
			if f >= 1 {
				tick(1)
				return nil
			}
			tick(f)
		}
	}
}
