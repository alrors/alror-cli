package verify

import (
	"math/rand"
	"testing"
)

func noisy(rng *rand.Rand, n int, mean, jitter float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = mean + rng.NormFloat64()*jitter
	}
	return out
}

func TestMannWhitneyDetectsShift(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	worse := noisy(rng, 30, 0.40, 0.03)
	base := noisy(rng, 30, 0.22, 0.03)
	if p := MannWhitneyGreater(worse, base); p > 0.001 {
		t.Fatalf("clear regression should be significant, p=%f", p)
	}
	if p := MannWhitneyGreater(base, worse); p < 0.99 {
		t.Fatalf("improvement must not look like a regression, p=%f", p)
	}
}

func TestMannWhitneyIdenticalIsInconclusive(t *testing.T) {
	same := []float64{1, 1, 1, 1, 1, 1, 1, 1}
	if p := MannWhitneyGreater(same, same); p != 1 {
		t.Fatalf("identical samples should give p=1, got %f", p)
	}
}

func TestJudgeNeedsBothEffectAndSignificance(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	v := Verifier{MaxRegression: map[string]float64{"error_rate": 0.25}, Alpha: 0.05}

	// Significant but tiny drift (+5%): passes.
	tiny := v.Judge(map[string]Samples{"error_rate": {Canary: noisy(rng, 200, 0.231, 0.005), Baseline: noisy(rng, 200, 0.22, 0.005)}})
	if !tiny.Pass {
		t.Fatalf("a small significant drift should pass: %s", tiny.Summary)
	}

	// Large and significant (+70%): fails.
	big := v.Judge(map[string]Samples{"error_rate": {Canary: noisy(rng, 30, 0.37, 0.03), Baseline: noisy(rng, 30, 0.22, 0.03)}})
	if big.Pass {
		t.Fatal("a large significant regression must fail")
	}
}

func TestJudgeFailsSafeOnTooLittleData(t *testing.T) {
	v := Verifier{Alpha: 0.05}
	r := v.Judge(map[string]Samples{"latency_p95": {Canary: []float64{1, 2}, Baseline: []float64{1, 2}}})
	if r.Pass {
		t.Fatal("too little data must never pass a step")
	}
}

func TestMedian(t *testing.T) {
	if m := Median([]float64{3, 1, 2}); m != 2 {
		t.Fatalf("odd median = %f", m)
	}
	if m := Median([]float64{4, 1, 2, 3}); m != 2.5 {
		t.Fatalf("even median = %f", m)
	}
}
