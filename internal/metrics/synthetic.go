package metrics

import (
	"context"
	"hash/fnv"
	"math/rand"
	"time"
)

// Synthetic generates realistic, deterministic-per-run metric samples.
// It needs no infrastructure, so `alror deploy` works out of the box and in tests.
// Regress multiplies the canary's values for a metric (1.6 = 60% worse).
type Synthetic struct {
	Regress map[string]float64
	rng     *rand.Rand
}

// NewSynthetic returns a synthetic provider with optional injected regressions.
func NewSynthetic(regress map[string]float64) *Synthetic {
	return &Synthetic{Regress: regress, rng: rand.New(rand.NewSource(time.Now().UnixNano()))}
}

// Name implements Provider.
func (s *Synthetic) Name() string { return "synthetic" }

var baselines = map[string]struct{ mean, jitter float64 }{
	"error_rate":  {0.22, 0.03}, // percent
	"latency_p95": {186, 9},     // ms
	"slo_burn":    {0.7, 0.08},  // ×
}

// Sample implements Provider: one observation per 15 s of window, at least 12.
func (s *Synthetic) Sample(_ context.Context, service, metric string, track Track, window time.Duration) ([]float64, error) {
	b, ok := baselines[metric]
	if !ok {
		b = baselines["error_rate"]
	}
	// Each service gets a slightly different but stable baseline.
	h := fnv.New32a()
	h.Write([]byte(service + metric))
	mean := b.mean * (0.9 + float64(h.Sum32()%20)/100)

	if track == Canary {
		mean *= 0.98 // a healthy canary is roughly indistinguishable
		if f, ok := s.Regress[metric]; ok {
			mean *= f
		}
	}
	n := max(12, int(window/(15*time.Second)))
	out := make([]float64, n)
	for i := range out {
		out[i] = max(0, mean+s.rng.NormFloat64()*b.jitter)
	}
	return out, nil
}
