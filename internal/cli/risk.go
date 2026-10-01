package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/manaskumar3003/alror-cli/internal/cli/ui"
	"github.com/manaskumar3003/alror-cli/internal/config"
	"github.com/manaskumar3003/alror-cli/internal/domain"
	"github.com/manaskumar3003/alror-cli/internal/risk"
	"github.com/manaskumar3003/alror-cli/internal/rollout"
	"github.com/manaskumar3003/alror-cli/internal/store"
)

func riskCmd() *cobra.Command {
	var base string
	cmd := &cobra.Command{
		Use:   "risk",
		Short: "Score the risk of the current change and show the rollout plan",
		Long:  "Scores the diff between --base (default: main) and HEAD, or the uncommitted\nchanges when nothing is committed yet.",
		RunE: func(*cobra.Command, []string) error {
			cfg, st, err := project()
			if err != nil {
				return err
			}
			spin := ui.Spin("Reading the diff and scoring the change")
			report, files, err := scoreChange(cfg, st, base)
			spin.Stop("")
			if err != nil {
				return err
			}
			plan := rollout.PlanFor(report)
			if g.json {
				return printJSON(map[string]any{"risk": report, "plan": plan, "files": files})
			}
			ui.Reveal(renderRisk(report, files), 18*time.Millisecond)
			fmt.Println()
			fmt.Println(ui.Section("Rollout plan"))
			fmt.Println(ui.Stages(plan, -1, domain.StatusPending))
			fmt.Println(ui.Dim.Render(fmt.Sprintf("%s · %d steps · %s of bake time", plan.Strategy, len(plan.Steps), rollout.TotalBake(plan))))
			return nil
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "git ref to diff against (default: origin/main, main, …)")
	return cmd
}

// scoreChange reads the git diff and history and returns the risk report.
func scoreChange(cfg *config.Config, st store.Store, base string) (domain.RiskReport, int, error) {
	change, err := risk.FromGit(cfg.Dir, base)
	if err != nil {
		return domain.RiskReport{}, 0, err
	}
	change.RecentRollbacks, _ = st.RecentRollbacks(time.Now().AddDate(0, 0, -30))
	return risk.Score(cfg, change), len(change.Files), nil
}

func renderRisk(r domain.RiskReport, files int) string {
	level := ui.LevelStyle(r.Level)
	head := fmt.Sprintf("%s  %s  %s",
		level.Bold(true).Render(fmt.Sprintf("%3d", r.Score)),
		ui.Meter(r.Score, 24),
		level.Render(strings.ToUpper(string(r.Level))+" RISK"))

	var rows [][]string
	for _, f := range r.Factors {
		pts := fmt.Sprintf("%+d", f.Points)
		if f.Points > 0 {
			pts = ui.Amber.Render(pts)
		} else {
			pts = ui.Green.Render(pts)
		}
		rows = append(rows, []string{pts, f.Name, ui.Dim.Render(f.Detail)})
	}
	body := head + "\n\n"
	if len(rows) == 0 {
		body += ui.Dim.Render("No risk factors found.")
	} else {
		body += ui.Table([]string{"PTS", "FACTOR", "DETAIL"}, rows)
	}
	meta := fmt.Sprintf("%d files", files)
	if len(r.Services) > 0 {
		meta += " · services: " + strings.Join(r.Services, ", ")
	}
	if r.AI {
		meta += " · AI-authored"
	}
	return ui.Box(body+"\n\n"+ui.Fainter.Render(meta), "Change risk")
}
