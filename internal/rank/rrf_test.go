package rank

import (
	"testing"

	"github.com/smirnoffmg/corpus/internal/store"
)

func ids(hits []store.Hit) []int64 {
	out := make([]int64, len(hits))
	for i, h := range hits {
		out[i] = h.ID
	}
	return out
}

func TestFusePrefersAgreementOverEitherTopHit(t *testing.T) {
	fts := []store.Hit{{ID: 1, Rank: 9}, {ID: 2, Rank: 8}, {ID: 3, Rank: 7}}
	vec := []store.Hit{{ID: 4, Rank: 0.9}, {ID: 2, Rank: 0.8}, {ID: 5, Rank: 0.7}}

	got := ids(Fuse(fts, vec))
	if got[0] != 2 {
		t.Errorf("first = %d, want 2 (the only hit both lists agree on); order: %v", got[0], got)
	}
	if len(got) != 5 {
		t.Errorf("fused %d hits, want 5 distinct", len(got))
	}
}

func TestFuseKeepsOrderOfASingleList(t *testing.T) {
	list := []store.Hit{{ID: 7}, {ID: 8}, {ID: 9}}
	got := ids(Fuse(list))
	for i, want := range []int64{7, 8, 9} {
		if got[i] != want {
			t.Fatalf("order = %v, want [7 8 9]", got)
		}
	}
}

func TestFuseIgnoresRawScoreScale(t *testing.T) {
	// Cosine similarities are ~1.0 while ts_rank_cd runs to single digits; if
	// the raw numbers leaked into the fusion the text list would always win.
	fts := []store.Hit{{ID: 1, Rank: 100}}
	vec := []store.Hit{{ID: 2, Rank: 0.42}}
	if got := ids(Fuse(fts, vec)); got[0] != 1 || got[1] != 2 {
		t.Errorf("order = %v, want [1 2] by position, not by score", got)
	}
}
