// Package verify decides whether a canary is worse than its baseline.
package verify

import (
	"math"
	"sort"
)

// MannWhitneyGreater returns the one-sided p-value for the hypothesis that
// values in a tend to be larger than values in b (canary worse than baseline,
// for "lower is better" metrics). It uses the normal approximation with a tie
// correction, which is accurate for the sample sizes a bake period produces.
func MannWhitneyGreater(a, b []float64) float64 {
	n1, n2 := float64(len(a)), float64(len(b))
	if n1 == 0 || n2 == 0 {
		return 1
	}

	type obs struct {
		v     float64
		fromA bool
	}
	all := make([]obs, 0, len(a)+len(b))
	for _, v := range a {
		all = append(all, obs{v, true})
	}
	for _, v := range b {
		all = append(all, obs{v, false})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v < all[j].v })

	// Average ranks for ties; accumulate the tie correction term.
	ranksA, tieTerm := 0.0, 0.0
	for i := 0; i < len(all); {
		j := i
		for j < len(all) && all[j].v == all[i].v {
			j++
		}
		avg := float64(i+j+1) / 2 // ranks are 1-based: (i+1 + j) / 2
		t := float64(j - i)
		tieTerm += t*t*t - t
		for k := i; k < j; k++ {
			if all[k].fromA {
				ranksA += avg
			}
		}
		i = j
	}

	u := ranksA - n1*(n1+1)/2
	mean := n1 * n2 / 2
	n := n1 + n2
	variance := n1 * n2 / 12 * ((n + 1) - tieTerm/(n*(n-1)))
	if variance <= 0 {
		return 1 // all values identical: no evidence either way
	}
	z := (u - mean - 0.5) / math.Sqrt(variance) // continuity correction
	return 1 - normalCDF(z)
}

func normalCDF(z float64) float64 { return 0.5 * math.Erfc(-z/math.Sqrt2) }

// Median returns the median of xs (0 for an empty slice).
func Median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	m := len(s) / 2
	if len(s)%2 == 1 {
		return s[m]
	}
	return (s[m-1] + s[m]) / 2
}
