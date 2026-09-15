// Package rank fuses result lists that carry incomparable scores.
package rank

import (
	"cmp"
	"slices"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

// k damps the influence of the top ranks; 60 is the value from the original
// reciprocal rank fusion paper and the usual default.
const k = 60.0

// Fuse merges ranked lists by reciprocal rank. Text relevance (ts_rank_cd) and
// cosine similarity live on different scales, so only the positions are
// comparable, never the scores themselves.
func Fuse(lists ...[]corpus.Hit) []corpus.Hit {
	at := map[int64]int{} // position in fused
	var fused []corpus.Hit

	for _, list := range lists {
		for i := range list {
			score := float32(1.0 / (k + float64(i+1)))
			if j, seen := at[list[i].ID]; seen {
				fused[j].Rank += score
				continue
			}
			at[list[i].ID] = len(fused)
			fused = append(fused, list[i])
			fused[len(fused)-1].Rank = score
		}
	}
	slices.SortFunc(fused, func(a, b corpus.Hit) int {
		if c := cmp.Compare(b.Rank, a.Rank); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return fused
}
