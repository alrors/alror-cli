package github

import (
	"fmt"
	"strings"
	"time"

	"github.com/manaskumar3003/alror-cli/internal/domain"
)

// CommentMarker identifies Alror's sticky pull-request comment.
const CommentMarker = "<!-- alror:change-risk -->"

// CheckName is the name of the check run shown on pull requests.
const CheckName = "Alror / change-risk"

// Conclusion maps a risk score to a check conclusion. Risk is advisory by
// default (neutral at medium or high); failAbove > 0 turns it into a gate.
func Conclusion(r domain.RiskReport, failAbove int) string {
	switch {
	case failAbove > 0 && r.Score > failAbove:
		return "failure"
	case r.Level == domain.LevelLow:
		return "success"
	default:
		return "neutral"
	}
}

// CheckTitle is the one-line title of the check run.
func CheckTitle(r domain.RiskReport) string {
	return fmt.Sprintf("%s risk (%d/100)", capitalize(string(r.Level)), r.Score)
}

// RiskMarkdown renders the risk report and rollout plan for a check run,
// a PR comment and the job summary.
func RiskMarkdown(r domain.RiskReport, p domain.Plan, files int, runURL string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### %s Alror change risk: **%d/100 · %s**\n\n", levelIcon(r.Level), r.Score, r.Level)
	fmt.Fprintf(&b, "`%s` %d/100\n\n", meter(r.Score, 20), r.Score)

	if len(r.Factors) == 0 {
		b.WriteString("No risk factors found.\n\n")
	} else {
		b.WriteString("| Points | Factor | Detail |\n| ---: | --- | --- |\n")
		for _, f := range r.Factors {
			fmt.Fprintf(&b, "| %+d | %s | %s |\n", f.Points, f.Name, escape(f.Detail))
		}
		b.WriteString("\n")
	}

	meta := []string{fmt.Sprintf("%d files", files)}
	if len(r.Services) > 0 {
		meta = append(meta, "services: "+strings.Join(r.Services, ", "))
	}
	if r.AI {
		meta = append(meta, "AI-authored")
	}
	fmt.Fprintf(&b, "%s\n\n", strings.Join(meta, " · "))

	b.WriteString("**Rollout plan:** ")
	b.WriteString(planLine(p))
	b.WriteString("\n")
	if runURL != "" {
		fmt.Fprintf(&b, "\n<sub>[Workflow run](%s) · scores are rule-based; AI authorship is one input, never a block on its own.</sub>\n", runURL)
	}
	return b.String()
}

// ReceiptMarkdown renders the outcome of `alror deploy` for the job summary.
func ReceiptMarkdown(d *domain.Deployment, v *domain.Verdict, elapsed time.Duration) string {
	var b strings.Builder
	switch d.Status {
	case domain.StatusPromoted:
		fmt.Fprintf(&b, "### ✅ Verified · promoted `%s` to 100%%\n\n", d.Service)
	case domain.StatusRolledBack:
		fmt.Fprintf(&b, "### ↩️ Rolled back `%s` at %d%% traffic\n\n", d.Service, d.Weight)
	default:
		fmt.Fprintf(&b, "### ❌ Release %s for `%s`\n\n", d.Status, d.Service)
	}
	fmt.Fprintf(&b, "| | |\n| --- | --- |\n| Image | `%s` |\n| Ref | %s |\n| Risk | %d · %s |\n| Plan | %s |\n| Deployment | `%s` |\n| Duration | %s |\n\n",
		d.Image, orDash(d.Ref), d.Risk.Score, d.Risk.Level, planLine(d.Plan), d.ID, elapsed.Round(time.Second))
	if d.Reason != "" {
		fmt.Fprintf(&b, "> %s\n\n", escape(d.Reason))
	}
	if v != nil && len(v.Results) > 0 {
		b.WriteString("| Metric | Canary | Baseline | Δ | p | |\n| --- | ---: | ---: | ---: | ---: | --- |\n")
		for _, r := range v.Results {
			ok := "✅"
			if !r.Pass {
				ok = "❌ " + escape(r.Reason)
			}
			fmt.Fprintf(&b, "| %s | %.3g | %.3g | %+.1f%% | %.3f | %s |\n", r.Metric, r.Canary, r.Baseline, r.Delta*100, r.PValue, ok)
		}
	}
	return b.String()
}

func planLine(p domain.Plan) string {
	parts := make([]string, len(p.Steps))
	for i, s := range p.Steps {
		parts[i] = fmt.Sprintf("%d%%", s.Weight)
	}
	bake := time.Duration(0)
	if len(p.Steps) > 0 {
		bake = p.Steps[0].Bake
	}
	return fmt.Sprintf("%s %s, %s bake per step", p.Strategy, strings.Join(parts, " → "), bake)
}

func meter(score, width int) string {
	lit := score * width / 100
	return strings.Repeat("█", lit) + strings.Repeat("░", width-lit)
}

func levelIcon(l domain.Level) string {
	switch l {
	case domain.LevelHigh:
		return "🔴"
	case domain.LevelMedium:
		return "🟠"
	default:
		return "🟢"
	}
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func escape(s string) string { return strings.NewReplacer("|", "\\|", "\n", " ").Replace(s) }

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
