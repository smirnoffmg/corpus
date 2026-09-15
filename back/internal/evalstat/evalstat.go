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
	"math"
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

// Comparison is a candidate configuration against a baseline, query by query.
type Comparison struct {
	Wins, Losses, Ties int
	MeanDelta          float64 // mean of candidate − baseline over shared queries
	P                  float64 // two-sided sign test on wins and losses
}

// Compare pairs per-query scores — reciprocal ranks, say — by query id. A query
// scored on only one side is left out: it is not a pair.
func Compare(baseline, candidate map[string]float64) Comparison {
	var c Comparison
	sum := 0.0
	for id, b := range baseline {
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
	}
	if n := c.Wins + c.Losses + c.Ties; n > 0 {
		c.MeanDelta = sum / float64(n)
	}
	c.P = SignTest(c.Wins, c.Losses)
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
