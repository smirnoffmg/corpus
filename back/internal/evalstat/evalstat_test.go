package evalstat_test

import (
	"math"
	"testing"

	"github.com/smirnoffmg/corpus/internal/evalstat"
)

func TestSignTest(t *testing.T) {
	cases := []struct {
		wins, losses int
		want         float64
	}{
		// Cormack et al. report RRF beating Condorcet 7 times of 7 at p ≈ 0.008:
		// that is the one-sided value; two-sided it is 2·(1/2)^7.
		{7, 0, 0.015625},
		{4, 0, 0.125}, // MRR 0.938 → 1.000 on the judged set: four queries moved
		{2, 0, 0.5},
		{0, 0, 1},
		{5, 5, 1},
		{10, 2, 0.03857421875},
	}
	for _, c := range cases {
		if got := evalstat.SignTest(c.wins, c.losses); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("SignTest(%d, %d) = %v, want %v", c.wins, c.losses, got, c.want)
		}
	}
	if got := evalstat.SignTestOneSided(7, 0); math.Abs(got-0.0078125) > 1e-9 {
		t.Errorf("one-sided 7:0 = %v, want 0.0078125, Cormack et al.'s p ≈ 0.008", got)
	}
}

func TestCompareCountsWinsLossesAndTiesPerQuery(t *testing.T) {
	baseline := map[string]float64{"a": 1, "b": 0.5, "c": 0, "d": 0.25, "e": 1}
	candidate := map[string]float64{"a": 1, "b": 1, "c": 0.5, "d": 0.2, "e": 1}

	got := evalstat.Compare(baseline, candidate)
	if got.Wins != 2 || got.Losses != 1 || got.Ties != 2 {
		t.Errorf("got %+v, want 2 wins, 1 loss, 2 ties", got)
	}
	if math.Abs(got.MeanDelta-(0.5+0.5-0.05)/5) > 1e-9 {
		t.Errorf("mean delta = %v", got.MeanDelta)
	}
	if math.Abs(got.P-evalstat.SignTest(2, 1)) > 1e-9 {
		t.Errorf("p = %v, want the sign test on the wins and losses", got.P)
	}
}

func TestCompareIgnoresQueriesOnlyOneSideMeasured(t *testing.T) {
	got := evalstat.Compare(map[string]float64{"a": 1, "b": 0}, map[string]float64{"a": 0})
	if got.Wins+got.Losses+got.Ties != 1 {
		t.Errorf("got %+v, want the one shared query", got)
	}
}

func TestRecall(t *testing.T) {
	exact := []int64{1, 2, 3, 4, 5}
	if got := evalstat.Recall(exact, []int64{1, 2, 3, 9, 5}); got != 0.8 {
		t.Errorf("Recall = %v, want 0.8", got)
	}
	if got := evalstat.Recall(exact, []int64{5, 4, 3, 2, 1}); got != 1 {
		t.Errorf("order does not matter to recall: %v", got)
	}
	if got := evalstat.Recall(nil, []int64{1}); got != 1 {
		t.Errorf("nothing to find is found: %v", got)
	}
}
