package verify

import (
	"fmt"
	"sort"
	"strings"

	"github.com/manaskumar3003/alror-cli/internal/domain"
)

// MinSamples is the fewest observations per track the verifier will judge.
// With less data it refuses to pass the step (fail-safe).
const MinSamples = 8

// Verifier compares canary and baseline samples for each metric.
// A metric fails only when the canary is both meaningfully worse
// (relative regression above the threshold) and statistically worse
// (p-value below alpha). Requiring both keeps false rollbacks rare.
type Verifier struct {
	MaxRegression map[string]float64 // e.g. {"error_rate": 0.25}
	Alpha         float64
}

// Samples holds observations for one metric.
type Samples struct {
	Canary   []float64
	Baseline []float64
}

// Judge returns the verdict for one bake period.
func (v Verifier) Judge(data map[string]Samples) domain.Verdict {
	metrics := make([]string, 0, len(data))
	for m := range data {
		metrics = append(metrics, m)
	}
	sort.Strings(metrics)

	verdict := domain.Verdict{Pass: true}
	var failed []string
	for _, m := range metrics {
		r := v.judgeMetric(m, data[m])
		verdict.Results = append(verdict.Results, r)
		if !r.Pass {
			verdict.Pass = false
			failed = append(failed, fmt.Sprintf("%s %s", m, r.Reason))
		}
	}
	if verdict.Pass {
		verdict.Summary = fmt.Sprintf("No regression across %d metrics", len(metrics))
	} else {
		verdict.Summary = "Regression: " + strings.Join(failed, "; ")
	}
	return verdict
}

func (v Verifier) judgeMetric(name string, s Samples) domain.MetricResult {
	r := domain.MetricResult{Metric: name, Canary: Median(s.Canary), Baseline: Median(s.Baseline), PValue: 1}
	if len(s.Canary) < MinSamples || len(s.Baseline) < MinSamples {
		r.Reason = fmt.Sprintf("insufficient data (%d/%d samples, need %d)", len(s.Canary), len(s.Baseline), MinSamples)
		return r
	}
	if r.Baseline > 0 {
		r.Delta = (r.Canary - r.Baseline) / r.Baseline
	}
	r.PValue = MannWhitneyGreater(s.Canary, s.Baseline)

	limit, ok := v.MaxRegression[name]
	if !ok {
		limit = 0.20
	}
	alpha := v.Alpha
	if alpha == 0 {
		alpha = 0.05
	}
	if r.Delta > limit && r.PValue < alpha {
		r.Reason = fmt.Sprintf("up %.0f%% vs baseline (limit %.0f%%, p=%.3f)", r.Delta*100, limit*100, r.PValue)
		return r
	}
	r.Pass = true
	return r
}
