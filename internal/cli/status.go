package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/manaskumar3003/alror-cli/internal/cli/ui"
	"github.com/manaskumar3003/alror-cli/internal/domain"
	"github.com/manaskumar3003/alror-cli/internal/driver"
	"github.com/manaskumar3003/alror-cli/internal/rollout"
)

func statusCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:     "status [deployment-id]",
		Aliases: []string{"history", "ls"},
		Short:   "List recent deployments, or show one with its event log",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			_, st, err := project()
			if err != nil {
				return err
			}
			if len(args) == 1 {
				d, err := st.Get(args[0])
				if err != nil {
					return err
				}
				events, err := st.Events(d.ID)
				if err != nil {
					return err
				}
				if g.json {
					return printJSON(map[string]any{"deployment": d, "events": events})
				}
				ui.Reveal(renderDeployment(d, events), 12*time.Millisecond)
				return nil
			}

			all, err := st.List()
			if err != nil {
				return err
			}
			if len(all) > limit {
				all = all[:limit]
			}
			if g.json {
				return printJSON(all)
			}
			if len(all) == 0 {
				fmt.Println(ui.Dim.Render("No deployments yet."))
				fmt.Println(ui.Hint("alror deploy -s <service> -i <image>", "start one"))
				return nil
			}
			rows := make([][]string, 0, len(all))
			for _, d := range all {
				rows = append(rows, []string{
					ui.Fainter.Render(d.CreatedAt.Local().Format("Jan 02 15:04")),
					d.Service,
					ui.Dim.Render(d.Ref),
					ui.LevelStyle(d.Risk.Level).Render(fmt.Sprintf("%3d", d.Risk.Score)) + " " + ui.Meter(d.Risk.Score, 8),
					ui.Status(d.Status, d.Weight),
					ui.Fainter.Render(d.ID),
				})
			}
			fmt.Println(ui.Section("Deployments"))
			fmt.Println()
			ui.Reveal(ui.Table([]string{"WHEN", "SERVICE", "REF", "RISK", "STATUS", "ID"}, rows), 16*time.Millisecond)
			return nil
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "number of deployments to list")
	return cmd
}

func renderDeployment(d *domain.Deployment, events []domain.Event) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s\n%s\n\n", ui.Bold.Render(d.Service), ui.Dim.Render(d.Image+"  "+d.Ref), ui.Fainter.Render(d.ID))
	fmt.Fprintf(&b, "%s   risk %s %s\n\n", ui.Status(d.Status, d.Weight),
		ui.LevelStyle(d.Risk.Level).Render(fmt.Sprint(d.Risk.Score)), ui.Meter(d.Risk.Score, 12))
	fmt.Fprintln(&b, ui.Stages(d.Plan, d.StepIndex, d.Status))
	if d.Reason != "" {
		fmt.Fprintf(&b, "\n%s\n", ui.Dim.Render(d.Reason))
	}
	fmt.Fprintf(&b, "\n%s\n", ui.Section("Event log"))
	for _, e := range events {
		icon := ui.Fainter.Render("·")
		switch e.Kind {
		case domain.EventPromoted:
			icon = ui.Green.Render("✓")
		case domain.EventRolledBack, domain.EventError:
			icon = ui.Red.Render("✗")
		case domain.EventVerdict:
			if e.Verdict != nil && e.Verdict.Pass {
				icon = ui.Green.Render("●")
			} else {
				icon = ui.Amber.Render("●")
			}
		}
		fmt.Fprintf(&b, "%s %s  %s\n", ui.Fainter.Render(e.At.Local().Format("15:04:05")), icon, e.Message)
	}
	return ui.Box(strings.TrimRight(b.String(), "\n"), "Deployment")
}

func rollbackCmd() *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:   "rollback <deployment-id>",
		Short: "Roll a deployment back to the previous version",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, st, err := project()
			if err != nil {
				return err
			}
			d, err := st.Get(args[0])
			if err != nil {
				return err
			}
			if d.Status == domain.StatusRolledBack {
				fmt.Println(ui.Dim.Render(d.ID + " is already rolled back."))
				return nil
			}
			svc, ok := cfg.Service(d.Service)
			if !ok {
				return fmt.Errorf("service %q is no longer in %s", d.Service, ws.configSource())
			}
			drv, err := driver.For(svc)
			if err != nil {
				return err
			}
			eng := &rollout.Engine{Cfg: cfg, Store: st, Driver: drv}
			if err := eng.Rollback(context.Background(), d, reason); err != nil {
				return err
			}
			if g.json {
				return printJSON(d)
			}
			fmt.Println(ui.OK(fmt.Sprintf("Rolled back %s %s", ui.Bold.Render(d.Service), ui.Dim.Render(d.ID))))
			return nil
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "requested by operator", "why, recorded in the event log")
	return cmd
}
