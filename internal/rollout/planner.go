// Package rollout turns a risk report into a plan and executes it.
package rollout

import (
	"time"

	"github.com/manaskumar3003/alror-cli/internal/domain"
)

// PlanFor returns the rollout plan for a risk report.
// Riskier changes get smaller first steps, more steps and longer bakes.
// The final step is always 100%, which means "promote".
func PlanFor(r domain.RiskReport) domain.Plan {
	switch r.Level {
	case domain.LevelHigh:
		return domain.Plan{Strategy: "canary", Steps: steps(15*time.Minute, 1, 5, 25, 50, 100)}
	case domain.LevelMedium:
		return domain.Plan{Strategy: "canary", Steps: steps(10*time.Minute, 5, 25, 50, 100)}
	default:
		return domain.Plan{Strategy: "canary", Steps: steps(5*time.Minute, 25, 100)}
	}
}

func steps(bake time.Duration, weights ...int) []domain.Step {
	out := make([]domain.Step, len(weights))
	for i, w := range weights {
		out[i] = domain.Step{Weight: w, Bake: bake}
		if w == 100 {
			out[i].Bake = 0
		}
	}
	return out
}

// TotalBake is the sum of all bake periods in a plan.
func TotalBake(p domain.Plan) time.Duration {
	var t time.Duration
	for _, s := range p.Steps {
		t += s.Bake
	}
	return t
}
