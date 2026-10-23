package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

// Journal keeps what was searched and read, so how the library is used can be
// looked at instead of guessed.
type Journal interface {
	LogSearch(ctx context.Context, r corpus.SearchRecord) error
	LogRead(ctx context.Context, origin string, chunkID int64) error
	Usage(ctx context.Context, window time.Duration) ([]corpus.Usage, error)
	Searches(ctx context.Context, f corpus.SearchFilter) ([]corpus.LoggedSearch, error)
}

func WithJournal(j Journal) Option {
	return func(s *Service) { s.journal = j }
}

const (
	originMCP  = "mcp"
	originHTTP = "http"
	// usageWindow is what /status sums up: long enough to hold a few working
	// days of searches, short enough to show a change in how agents search.
	usageWindow = 7 * 24 * time.Hour
)

// journalSearch and journalRead never fail the request: a journal that cannot
// be written costs the record, not the answer.
func (s *Service) journalSearch(ctx context.Context, origin, mode string, q *corpus.Query, hits []corpus.Hit, err error) {
	if s.journal == nil || origin == "" {
		return
	}
	r := corpus.SearchRecord{Origin: origin, Query: q.Text, Mode: mode, Kind: q.Kind}
	for i := range hits {
		r.Hits = append(r.Hits, hits[i].ID)
	}
	if err != nil {
		r.Err = err.Error()
	}
	if logErr := s.journal.LogSearch(context.WithoutCancel(ctx), r); logErr != nil {
		slog.WarnContext(ctx, "journalling a search", "err", logErr)
	}
}

func (s *Service) journalRead(ctx context.Context, origin string, id int64) {
	if s.journal == nil {
		return
	}
	if err := s.journal.LogRead(context.WithoutCancel(ctx), origin, id); err != nil {
		slog.WarnContext(ctx, "journalling a read", "err", err)
	}
}

func (s *Service) journalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /searches", func(w http.ResponseWriter, r *http.Request) {
		if s.journal == nil {
			http.Error(w, "the search journal is not configured on this server", http.StatusServiceUnavailable)
			return
		}
		q := r.URL.Query()
		limit := atoiOrZero(q.Get("limit"))
		if limit <= 0 {
			limit = 50
		}
		searches, err := s.journal.Searches(r.Context(), corpus.SearchFilter{
			Origin: q.Get("origin"), Empty: q.Get("empty") == "1", Limit: min(limit, 500),
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if searches == nil {
			searches = []corpus.LoggedSearch{}
		}
		writeJSON(w, map[string][]corpus.LoggedSearch{"searches": searches})
	})
}
