package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/manaskumar3003/alror-cli/internal/cli/ui"
	"github.com/manaskumar3003/alror-cli/internal/domain"
	"github.com/manaskumar3003/alror-cli/internal/driver"
	"github.com/manaskumar3003/alror-cli/internal/metrics"
	"github.com/manaskumar3003/alror-cli/internal/notify"
	"github.com/manaskumar3003/alror-cli/internal/risk"
	"github.com/manaskumar3003/alror-cli/internal/rollout"
	"github.com/manaskumar3003/alror-cli/internal/store"
	"github.com/manaskumar3003/alror-cli/internal/verify"
)

type deployOpts struct {
	service, image, ref, base string
	env                       string
	riskOverride              int
	regress                   []string
	fast, shadow              bool
}

func deployCmd() *cobra.Command {
	var o deployOpts
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Run a risk-scored, verified progressive rollout",
		Example: "  alror deploy -s checkout-api -i registry/checkout:1.42 --ref '#4821'\n" +
			"  alror deploy -s checkout-api -i app:v2 --regress error_rate=1.8   # watch it roll back",
		RunE: func(*cobra.Command, []string) error { return runDeploy(o) },
	}
	f := cmd.Flags()
	f.StringVarP(&o.service, "service", "s", "", "service name from alror.yaml (required)")
	f.StringVarP(&o.image, "image", "i", "", "image or artifact to roll out (required)")
	f.StringVar(&o.ref, "ref", "", "PR number or commit, shown in history and notifications")
	f.StringVar(&o.env, "env", "production", "target environment, recorded in connected mode (ignored in local mode)")
	f.StringVar(&o.base, "base", "", "git ref to diff against for risk scoring")
	f.IntVar(&o.riskOverride, "risk", -1, "skip git and use this risk score (0-100)")
	f.StringSliceVar(&o.regress, "regress", nil, "synthetic metrics only: make the canary worse, e.g. error_rate=1.8")
	f.BoolVar(&o.fast, "fast", false, "compress bake times (demo mode)")
	f.BoolVar(&o.shadow, "shadow", false, "shadow mode: recommend rollbacks but never act")
	_ = cmd.MarkFlagRequired("service")
	_ = cmd.MarkFlagRequired("image")
	return cmd
}

func runDeploy(o deployOpts) error {
	cfg, st, err := project()
	if err != nil {
		return err
	}
	svc, ok := cfg.Service(o.service)
	if !ok {
		return fmt.Errorf("unknown service %q (check %s)", o.service, ws.configSource())
	}
	if o.fast {
		cfg.Policy.BakeScale = 0.003
	}
	if o.shadow {
		cfg.Policy.AutoRollback = false
	}

	// 1. Risk → plan.
	var report domain.RiskReport
	if o.riskOverride >= 0 {
		report = domain.RiskReport{Score: min(o.riskOverride, 100), Level: risk.LevelFor(o.riskOverride),
			Factors: []domain.Factor{{Name: "Manual score", Detail: "--risk flag", Points: o.riskOverride}}, Services: []string{svc.Name}}
	} else if report, _, err = scoreChange(cfg, st, o.base); err != nil {
		fmt.Fprintln(os.Stderr, ui.WarnLine("Could not read git diff ("+err.Error()+"); assuming medium risk"))
		report = domain.RiskReport{Score: 50, Level: domain.LevelMedium, Services: []string{svc.Name}}
	}

	// 2. Wire the engine.
	drv, err := driver.For(svc)
	if err != nil {
		return err
	}
	prov, err := metrics.New(cfg)
	if err != nil {
		return err
	}
	if len(o.regress) > 0 {
		syn, ok := prov.(*metrics.Synthetic)
		if !ok {
			return fmt.Errorf("--regress only works with the synthetic metrics provider")
		}
		if syn.Regress, err = parseRegress(o.regress); err != nil {
			return err
		}
	}
	var notifier notify.Notifier = notify.Nop{}
	if cfg.Notify.SlackWebhook != "" {
		notifier = notify.Slack{Webhook: cfg.Notify.SlackWebhook}
	}

	d := &domain.Deployment{
		ID: store.NewID(), Service: svc.Name, Image: o.image, Ref: o.ref,
		Risk: report, Plan: rollout.PlanFor(report), Status: domain.StatusPending, CreatedAt: time.Now().UTC(),
	}
	if ws.conn != nil {
		// Only connected mode records these, so local .alror/ files stay as they were.
		d.Environment, d.Source = o.env, clientSource()
	} else if o.env != "production" && !g.json {
		fmt.Fprintln(os.Stderr, ui.WarnLine("--env is only recorded in connected mode; ignored for local state"))
	}
	// Record the deployment before touching traffic: in connected mode a rejected
	// write (unknown service, missing scope) stops here, not mid-rollout.
	if err := st.Save(d); err != nil {
		return fmt.Errorf("record deployment: %w", err)
	}
	obs := &liveObserver{d: d, quiet: g.json, tty: isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())}
	eng := &rollout.Engine{
		Cfg: cfg, Store: st, Driver: drv, Metrics: prov, Notifier: notifier, Observer: obs,
		Verifier: verify.Verifier{MaxRegression: cfg.Policy.MaxRegression, Alpha: cfg.Policy.Alpha},
	}

	if !g.json {
		ui.HideCursor()
		defer ui.ShowCursor()
		obs.state = ws.stateLabel()
		obs.header(drv.Name(), prov.Name(), cfg.Policy.AutoRollback)
	}

	// 3. Run. Ctrl-C rolls the canary back instead of leaving it half-shifted.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	start := time.Now()
	runErr := eng.Run(ctx, d)
	if ws.conn != nil {
		// The engine ignores write errors; make sure the outcome reaches the server.
		if err := st.Save(d); err != nil {
			fmt.Fprintln(os.Stderr, ui.WarnLine("could not save the final state: "+err.Error()))
		}
	}
	reportDeployToGitHub(d, obs.lastVerdict, time.Since(start))

	if g.json {
		events, _ := st.Events(d.ID)
		if err := printJSON(map[string]any{"deployment": d, "events": events}); err != nil {
			return err
		}
		return deployOutcome(d, runErr)
	}
	fmt.Println()
	ui.Reveal(ui.Receipt(d, obs.lastVerdict, time.Since(start)), 45*time.Millisecond) // "printing"
	fmt.Println()
	fmt.Println(ui.Hint("alror status "+d.ID, "full event log"))
	return deployOutcome(d, runErr)
}

// errRolledBack makes the process exit with code 2, so CI can tell
// "the release was rolled back" apart from "the tool crashed" (code 1).
var errRolledBack = errors.New("release rolled back")

func deployOutcome(d *domain.Deployment, runErr error) error {
	if runErr != nil {
		return runErr
	}
	if d.Status == domain.StatusRolledBack {
		return errRolledBack
	}
	return nil
}

func parseRegress(items []string) (map[string]float64, error) {
	out := map[string]float64{}
	for _, it := range items {
		k, v, ok := strings.Cut(it, "=")
		f, err := strconv.ParseFloat(v, 64)
		if !ok || err != nil || f <= 0 {
			return nil, fmt.Errorf("--regress %q: want metric=factor, e.g. error_rate=1.8", it)
		}
		out[k] = f
	}
	return out, nil
}

// liveObserver draws the rollout as it happens.
type liveObserver struct {
	d           *domain.Deployment
	quiet       bool
	lastVerdict *domain.Verdict
	inProgress  bool
	tty         bool   // live progress bars only make sense on a terminal
	prevWeight  int    // canary traffic before the current step, for the shift animation
	state       string // where state lives: ".alror/" or "acme workspace (http://…)"
}

func (o *liveObserver) header(driverName, metricsName string, auto bool) {
	mode := ui.Green.Render("auto-rollback on")
	if !auto {
		mode = ui.Amber.Render("shadow mode")
	}
	level := ui.LevelStyle(o.d.Risk.Level)
	lines := []string{
		ui.Bold.Render(o.d.Service) + ui.Dim.Render("  "+o.d.Image+"  "+o.d.Ref),
		ui.Fainter.Render(o.d.ID),
		"",
		fmt.Sprintf("%s  %s  %s", level.Bold(true).Render(fmt.Sprintf("%3d", o.d.Risk.Score)), ui.Meter(o.d.Risk.Score, 20), level.Render(string(o.d.Risk.Level)+" risk")),
	}
	for i, f := range o.d.Risk.Factors {
		if i == 3 {
			break
		}
		lines = append(lines, ui.Fainter.Render(fmt.Sprintf("     %+d  %s · %s", f.Points, f.Name, f.Detail)))
	}
	lines = append(lines, "", ui.Stages(o.d.Plan, -1, domain.StatusPending), "",
		ui.Fainter.Render(fmt.Sprintf("target %s · metrics %s · ", driverName, metricsName))+mode,
		ui.Fainter.Render("state: "+o.state+envNote(o.d.Environment)))
	ui.Reveal(ui.Box(strings.Join(lines, "\n"), "Release"), 14*time.Millisecond)
	fmt.Println()
}

func (o *liveObserver) Event(e domain.Event) {
	if e.Kind == domain.EventVerdict && e.Verdict != nil {
		o.lastVerdict = e.Verdict // kept even in --json mode for the GitHub summary
	}
	if o.quiet {
		return
	}
	o.endProgress()
	ts := ui.Fainter.Render(e.At.Local().Format("15:04:05"))
	switch e.Kind {
	case domain.EventStep:
		fmt.Printf("%s  %s\n", ts, ui.Stages(o.d.Plan, o.d.StepIndex, domain.StatusRolling))
		ui.Sweep("          ", o.prevWeight, e.Weight, 30, "traffic on canary")
		o.prevWeight = e.Weight
	case domain.EventVerdict:
		if e.Verdict == nil {
			fmt.Printf("%s  %s\n", ts, ui.WarnLine(e.Message))
			return
		}
		o.lastVerdict = e.Verdict
		ui.Hold("Comparing canary vs baseline · Mann-Whitney U", 450*time.Millisecond)
		for _, r := range e.Verdict.Results {
			if ui.Animated() {
				time.Sleep(90 * time.Millisecond)
			}
			line := fmt.Sprintf("%-12s %8.3g vs %-8.3g %+6.1f%%  p=%.3f", r.Metric, r.Canary, r.Baseline, r.Delta*100, r.PValue)
			if r.Pass {
				fmt.Printf("          %s\n", ui.OK(ui.Dim.Render(line)))
			} else {
				fmt.Printf("          %s\n", ui.Fail(ui.Red.Render(line)+"  "+ui.Dim.Render(r.Reason)))
			}
		}
	case domain.EventPromoted:
		fmt.Printf("%s  %s\n", ts, ui.Stages(o.d.Plan, len(o.d.Plan.Steps), domain.StatusPromoted))
		ui.Sweep("          ", o.prevWeight, 100, 30, "promoted to all traffic")
		fmt.Printf("          %s\n", ui.OK(ui.Bold.Render(e.Message)))
	case domain.EventRolledBack:
		fmt.Printf("%s  %s\n", ts, ui.Stages(o.d.Plan, o.d.StepIndex, domain.StatusRolledBack))
		ui.Sweep("          ", o.prevWeight, 0, 30, "traffic back on stable")
		fmt.Printf("          %s\n", ui.Fail(ui.Bold.Render(e.Message)))
	case domain.EventError:
		fmt.Printf("%s  %s\n", ts, ui.Fail(e.Message))
	}
}

func (o *liveObserver) Progress(step int, f float64) {
	if o.quiet || !o.tty {
		return
	}
	o.inProgress = true
	bake := o.d.Plan.Steps[step].Bake
	left := time.Duration(float64(bake) * (1 - f)).Round(time.Second)
	fmt.Printf("\r        %s %s %s %s\x1b[K", ui.SpinFrame(), ui.Bar(f, 30), ui.Dim.Render(fmt.Sprintf("%3.0f%%", f*100)),
		ui.Fainter.Render(fmt.Sprintf("baking at %d%% · %s of %s left", o.d.Plan.Steps[step].Weight, left, bake)))
}

func (o *liveObserver) endProgress() {
	if o.inProgress {
		fmt.Println()
		o.inProgress = false
	}
}

func envNote(env string) string {
	if env == "" {
		return ""
	}
	return " · env " + env
}
