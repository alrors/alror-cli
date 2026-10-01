package cli

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/manaskumar3003/alror-cli/internal/cli/ui"
	"github.com/manaskumar3003/alror-cli/internal/domain"
	gh "github.com/manaskumar3003/alror-cli/internal/github"
	"github.com/manaskumar3003/alror-cli/internal/rollout"
)

func githubCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "github",
		Short: "GitHub integration: risk checks and PR comments",
	}
	cmd.AddCommand(githubCheckCmd())
	return cmd
}

func githubCheckCmd() *cobra.Command {
	var (
		base      string
		comment   bool
		failAbove int
		dryRun    bool
	)
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Score the pull request and post the result as a check run and PR comment",
		Long: "Run inside GitHub Actions on pull_request events. Scores the PR against its base\n" +
			"branch, posts an \"Alror / change-risk\" check run, keeps one sticky PR comment\n" +
			"up to date, writes the job summary and sets outputs (score, level, plan).\n\n" +
			"Needs permissions: checks: write, pull-requests: write.",
		Example: "  alror github check --comment\n  alror github check --fail-above 80     # block very risky PRs\n  alror github check --dry-run           # print the Markdown locally",
		RunE: func(*cobra.Command, []string) error {
			cfg, st, err := project()
			if err != nil {
				return err
			}
			env, err := gh.FromEnv()
			if err != nil {
				return err
			}
			if base == "" && env.BaseRef != "" {
				base = "origin/" + env.BaseRef
			}

			spin := ui.Spin("Scoring the pull request")
			report, files, err := scoreChange(cfg, st, base)
			spin.Stop("")
			if err != nil {
				return err
			}
			plan := rollout.PlanFor(report)
			md := gh.RiskMarkdown(report, plan, files, env.RunURL)
			conclusion := gh.Conclusion(report, failAbove)

			_ = env.SetOutputs(map[string]string{
				"score": strconv.Itoa(report.Score), "level": string(report.Level),
				"conclusion": conclusion, "plan": planWeights(plan),
			})
			_ = env.AppendSummary(md)

			if g.json {
				return printJSON(map[string]any{"risk": report, "plan": plan, "conclusion": conclusion, "markdown": md})
			}
			ui.Reveal(renderRisk(report, files), 10*time.Millisecond)

			if dryRun || !env.CanPost() {
				fmt.Println()
				if !dryRun {
					fmt.Println(ui.WarnLine("Not posting to GitHub: run inside GitHub Actions with GITHUB_TOKEN (or use --dry-run)."))
				}
				fmt.Println(ui.Section("Markdown that would be posted"))
				fmt.Println(md)
				return gateResult(conclusion)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			client := gh.NewClient(env)

			run := gh.CheckRun{Name: gh.CheckName, HeadSHA: env.SHA, Status: "completed", Conclusion: conclusion, DetailsURL: env.RunURL}
			run.Output.Title = gh.CheckTitle(report)
			run.Output.Summary = md
			url, err := client.CreateCheckRun(ctx, env.Owner, env.Repo, run)
			switch {
			case gh.IsForbidden(err):
				fmt.Println(ui.WarnLine("Could not create a check run (add `checks: write` to the workflow permissions). Continuing."))
			case err != nil:
				return err
			default:
				fmt.Println(ui.OK("Check run posted " + ui.Fainter.Render(url)))
			}

			if comment && env.PR > 0 {
				url, err := client.UpsertComment(ctx, env.Owner, env.Repo, env.PR, gh.CommentMarker, md)
				switch {
				case gh.IsForbidden(err):
					fmt.Println(ui.WarnLine("Could not comment (add `pull-requests: write` to the workflow permissions)."))
				case err != nil:
					return err
				default:
					fmt.Println(ui.OK("PR comment updated " + ui.Fainter.Render(url)))
				}
			}
			return gateResult(conclusion)
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "git ref to diff against (default: the PR's base branch)")
	cmd.Flags().BoolVar(&comment, "comment", false, "also keep a sticky comment on the pull request")
	cmd.Flags().IntVar(&failAbove, "fail-above", 0, "fail the check (exit 3) when the score is above this (0 = advisory)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the Markdown instead of posting")
	return cmd
}

// errRiskGate makes `alror github check` exit 3 when --fail-above trips.
var errRiskGate = fmt.Errorf("risk above the --fail-above threshold")

func gateResult(conclusion string) error {
	if conclusion == "failure" {
		return errRiskGate
	}
	return nil
}

func planWeights(p domain.Plan) string {
	s := ""
	for i, st := range p.Steps {
		if i > 0 {
			s += ","
		}
		s += strconv.Itoa(st.Weight)
	}
	return s
}

// reportDeployToGitHub writes the receipt to the job summary and sets step
// outputs when `alror deploy` runs inside GitHub Actions.
func reportDeployToGitHub(d *domain.Deployment, v *domain.Verdict, elapsed time.Duration) {
	if !gh.InActions() {
		return
	}
	env, err := gh.FromEnv()
	if err != nil {
		return
	}
	_ = env.AppendSummary(gh.ReceiptMarkdown(d, v, elapsed))
	_ = env.SetOutputs(map[string]string{
		"deployment-id": d.ID, "status": string(d.Status),
		"weight": strconv.Itoa(d.Weight), "risk": strconv.Itoa(d.Risk.Score),
	})
}
