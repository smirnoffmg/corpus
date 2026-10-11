// Package evalstat is the arithmetic of comparing two search configurations on
// a judged set: whether a difference is a difference, and how much an
// approximate index loses against an exact scan.
//
// The sets are small — tens of queries — so means alone mislead: an MRR gain
// of 0.06 on 31 queries can be four queries moving up one place. "Results are
// highly variable over different documents and information needs" (Manning et
// al., IIR, с. 152), which is why the comparison is per query, and tested.
package evalstat

import (
	"maps"
	"math"
	"math/rand/v2"
	"slices"
)

// SignTest is the two-sided sign test on paired outcomes: the probability,
// were both configurations equally good, of a split at least this uneven.
// Ties carry no information and are left out, as the test prescribes.
func SignTest(wins, losses int) float64 {
	return math.Min(1, 2*SignTestOneSided(max(wins, losses), min(wins, losses)))
}

// SignTestOneSided is P(X ≥ wins) for X ~ Binomial(wins+losses, 1/2).
func SignTestOneSided(wins, losses int) float64 {
	n := wins + losses
	if n == 0 {
		return 1
	}
	p := 0.0
	for k := wins; k <= n; k++ {
		p += math.Exp(logChoose(n, k) - float64(n)*math.Ln2)
	}
	return math.Min(1, p)
}

func logChoose(n, k int) float64 {
	a, _ := math.Lgamma(float64(n + 1))
	b, _ := math.Lgamma(float64(k + 1))
	c, _ := math.Lgamma(float64(n - k + 1))
	return a - b - c
}

// randomizationSamples keeps the sampled p within about ±0.003 of the exact
// one at p = 0.05, which is finer than any decision taken on it.
const randomizationSamples = 100_000

// RandomizationTest is the two-sided paired randomization test on per-query
// differences: the share of random sign flips whose mean is at least as far
// from zero as the observed one. Unlike the sign test it weighs how far a
// query moved. Smucker et al. found the sign and Wilcoxon tests to disagree
// with it, the t-test and the bootstrap on TREC runs, and argued for this one
// (Sanderson, Test Collection Based Evaluation of IR Systems, p. 313).
//
// The seed is fixed, so a run reproduces to the digit.
func RandomizationTest(diffs []float64) float64 {
	observed := 0.0
	for _, d := range diffs {
		observed += d
	}
	if len(diffs) == 0 {
		return 1
	}
	rng := rand.New(rand.NewPCG(1, 2))
	extreme := 0
	for range randomizationSamples {
		sum := 0.0
		for _, d := range diffs {
			if rng.Uint64()&1 == 1 {
				d = -d
			}
			sum += d
		}
		// Sums of the same values in another order differ in the last bits.
		if math.Abs(sum) >= math.Abs(observed)-1e-12 {
			extreme++
		}
	}
	// The observed assignment is one of the permutations, so p is never 0.
	return float64(extreme+1) / float64(randomizationSamples+1)
}

// Comparison is a candidate configuration against a baseline, query by query.
type Comparison struct {
	Wins, Losses, Ties int
	MeanDelta          float64 // mean of candidate − baseline over shared queries
	P                  float64 // two-sided sign test on wins and losses
	PRandom            float64 // two-sided randomization test on the differences
}

// Compare pairs per-query scores — reciprocal ranks, say — by query id. A query
// scored on only one side is left out: it is not a pair.
func Compare(baseline, candidate map[string]float64) Comparison {
	var c Comparison
	sum := 0.0
	// The randomization test draws one flip per difference in order, so the
	// order must not follow the map's.
	var diffs []float64
	for _, id := range slices.Sorted(maps.Keys(baseline)) {
		b := baseline[id]
		x, ok := candidate[id]
		if !ok {
			continue
		}
		switch {
		case x > b:
			c.Wins++
		case x < b:
			c.Losses++
		default:
			c.Ties++
		}
		sum += x - b
		diffs = append(diffs, x-b)
	}
	if n := c.Wins + c.Losses + c.Ties; n > 0 {
		c.MeanDelta = sum / float64(n)
	}
	c.P = SignTest(c.Wins, c.Losses)
	c.PRandom = RandomizationTest(diffs)
	return c
}

// Recall is the share of the exact nearest neighbours the approximate search
// also returned, regardless of order.
func Recall(exact, approx []int64) float64 {
	if len(exact) == 0 {
		return 1
	}
	found := 0
	for _, id := range exact {
		if slices.Contains(approx, id) {
			found++
		}
	}
	return float64(found) / float64(len(exact))
}
