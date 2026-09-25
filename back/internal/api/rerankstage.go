package api

import (
	"cmp"
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

// Reranker scores each document against the query, in the order given.
type Reranker interface {
	Rerank(ctx context.Context, query string, documents []string) ([]float64, error)
}

// WithReranker lets a search ask for its first candidates to be reordered by a
// cross-encoder. Without it such a search answers in the fused order and says so.
func WithReranker(r Reranker) Option { return func(s *Service) { s.reranker = r } }

// Measured on the judged sets, 83 queries (docs/search-evaluation.md): 50
// candidates of 4000 characters took MRR 0.526 -> 0.612 at 3.3 s a search, 20 of
// 1500 took it to 0.593 at 1.2 s. The smaller one keeps most of the gain.
const (
	rerankDepth   = 20
	rerankChars   = 1500
	rerankTimeout = 5 * time.Second
)

const rerankNotice = "Reranking was asked for, but the reranker is unavailable: these hits are in the fused order, unreranked."

// rerank reorders the first rerankDepth hits by the reranker's score and keeps
// the rest behind them. Any failure leaves the fused order, which is a good
// answer, only a less sorted one.
func (s *Service) rerank(ctx context.Context, query string, hits []corpus.Hit, tr *trace) []corpus.Hit {
	if s.reranker == nil {
		tr.rerank = "unavailable"
		return hits
	}
	n := min(len(hits), rerankDepth)
	if n < 2 {
		tr.rerank = "ok"
		return hits
	}

	ids := make([]int64, n)
	for i := range n {
		ids[i] = hits[i].ID
	}
	bodies, err := s.store.Bodies(ctx, ids, rerankChars)
	if err != nil {
		tr.rerank = "failed"
		slog.WarnContext(ctx, "reading passages to rerank", "err", err)
		return hits
	}
	docs := make([]string, n)
	for i := range n {
		// The source's title goes with the passage, as it did when measured: a
		// page often says what it is about only in the book's name.
		docs[i] = hits[i].Title + "\n" + bodies[hits[i].ID]
	}

	ctx, cancel := context.WithTimeout(ctx, rerankTimeout)
	defer cancel()
	start := time.Now()
	scores, err := s.reranker.Rerank(ctx, query, docs)
	tr.rerankTook = time.Since(start)
	if err != nil {
		tr.rerank = "failed"
		slog.WarnContext(ctx, "reranker unavailable, answering in the fused order", "err", err)
		return hits
	}
	tr.rerank = "ok"

	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	// Stable, so equal scores keep the fused order between them.
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(scores[b], scores[a]) })
	out := make([]corpus.Hit, 0, len(hits))
	for _, i := range order {
		out = append(out, hits[i])
	}
	return append(out, hits[n:]...)
}
