// Package rank fuses result lists that carry incomparable scores.
package rank

import (
	"sort"

	"github.com/smirnoffmg/corpus/internal/store"
)

// k damps the influence of the top ranks; 60 is the value from the original
// reciprocal rank fusion paper and the usual default.
const k = 60.0

// Fuse merges ranked lists by reciprocal rank. Text relevance (ts_rank_cd) and
// cosine similarity live on different scales, so only the positions are
// comparable, never the scores themselves.
func Fuse(lists ...[]store.Hit) []store.Hit {
	scores := map[int64]float32{}
	byID := map[int64]store.Hit{}

	for _, list := range lists {
		for i, hit := range list {
			scores[hit.ID] += float32(1.0 / (k + float64(i+1)))
			if _, seen := byID[hit.ID]; !seen {
				byID[hit.ID] = hit
			}
		}
	}

	fused := make([]store.Hit, 0, len(byID))
	for id, hit := range byID {
		hit.Rank = scores[id]
		fused = append(fused, hit)
	}
	sort.Slice(fused, func(i, j int) bool {
		if fused[i].Rank != fused[j].Rank {
			return fused[i].Rank > fused[j].Rank
		}
		return fused[i].ID < fused[j].ID
	})
	return fused
}
