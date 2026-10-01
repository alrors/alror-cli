package rollout

import (
	"context"
	"testing"
	"time"

	"github.com/manaskumar3003/alror-cli/internal/config"
	"github.com/manaskumar3003/alror-cli/internal/domain"
	"github.com/manaskumar3003/alror-cli/internal/driver"
	"github.com/manaskumar3003/alror-cli/internal/metrics"
	"github.com/manaskumar3003/alror-cli/internal/store"
	"github.com/manaskumar3003/alror-cli/internal/verify"
)

func noWait(_ context.Context, _ time.Duration, tick func(float64)) error {
	tick(1)
	return nil
}

func run(t *testing.T, regress map[string]float64, auto bool) (*domain.Deployment, *driver.Simulated, *store.FS) {
	t.Helper()
	cfg := config.Default("t")
	cfg.Dir = t.TempDir()
	cfg.Policy.AutoRollback = auto
	cfg.Policy.BakeScale = 1
	st, err := store.Open(cfg.StateDir())
	if err != nil {
		t.Fatal(err)
	}
	drv := &driver.Simulated{}
	report := domain.RiskReport{Score: 50, Level: domain.LevelMedium}
	d := &domain.Deployment{ID: store.NewID(), Service: "checkout-api", Image: "img:2", Risk: report, Plan: PlanFor(report), CreatedAt: time.Now()}
	eng := &Engine{
		Cfg: cfg, Store: st, Driver: drv, Metrics: metrics.NewSynthetic(regress), Wait: noWait,
		Verifier: verify.Verifier{MaxRegression: cfg.Policy.MaxRegression, Alpha: cfg.Policy.Alpha},
	}
	if err := eng.Run(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	return d, drv, st
}

func TestHealthyReleaseIsPromoted(t *testing.T) {
	d, drv, st := run(t, nil, true)
	if d.Status != domain.StatusPromoted {
		t.Fatalf("status = %s (%s)", d.Status, d.Reason)
	}
	if got := drv.Weights; len(got) != 4 || got[0] != 5 || got[3] != 100 {
		t.Fatalf("traffic steps = %v, want [5 25 50 100]", got)
	}
	saved, err := st.Get(d.ID)
	if err != nil || saved.Status != domain.StatusPromoted {
		t.Fatalf("store did not persist the outcome: %v %+v", err, saved)
	}
	events, _ := st.Events(d.ID)
	if events[len(events)-1].Kind != domain.EventPromoted {
		t.Fatalf("last event = %s", events[len(events)-1].Kind)
	}
}

func TestRegressionRollsBackAtFirstStep(t *testing.T) {
	d, drv, _ := run(t, map[string]float64{"error_rate": 1.8}, true)
	if d.Status != domain.StatusRolledBack || d.Weight != 5 {
		t.Fatalf("status = %s at %d%%, want rolled_back at 5%%", d.Status, d.Weight)
	}
	if last := drv.Weights[len(drv.Weights)-1]; last != 0 {
		t.Fatalf("driver should end at 0%% canary traffic, got %d", last)
	}
}

func TestShadowModeNeverActs(t *testing.T) {
	d, _, st := run(t, map[string]float64{"error_rate": 1.8}, false)
	if d.Status != domain.StatusPromoted {
		t.Fatalf("shadow mode must not roll back, got %s", d.Status)
	}
	events, _ := st.Events(d.ID)
	found := false
	for _, e := range events {
		if e.Kind == domain.EventVerdict && e.Verdict == nil {
			found = true // the "would roll back here" recommendation
		}
	}
	if !found {
		t.Fatal("shadow mode should log a rollback recommendation")
	}
}

func TestPlanForLevels(t *testing.T) {
	if n := len(PlanFor(domain.RiskReport{Level: domain.LevelLow}).Steps); n != 2 {
		t.Fatalf("low risk plan has %d steps", n)
	}
	high := PlanFor(domain.RiskReport{Level: domain.LevelHigh})
	if high.Steps[0].Weight != 1 || high.Steps[len(high.Steps)-1].Weight != 100 {
		t.Fatalf("high risk plan = %+v", high.Steps)
	}
}
