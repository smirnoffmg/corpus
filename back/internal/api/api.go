// Package api holds the search service and its transports. The store and the
// embedder arrive as interfaces so that the behaviour that matters — which
// retrieval legs run, and what happens when one of them is down — can be tested
// without a database or a GPU.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/smirnoffmg/corpus/internal/corpus"
	"github.com/smirnoffmg/corpus/internal/rank"
)

type Store interface {
	Search(ctx context.Context, q corpus.Query) ([]corpus.Hit, error)
	SearchVector(ctx context.Context, vector []float32, kind string, limit int) ([]corpus.Hit, error)
	Read(ctx context.Context, id int64, neighbours bool) (corpus.Passage, error)
	Stats(ctx context.Context) (int64, int64, error)
}

type Embedder interface {
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
}

type Service struct {
	store    Store
	embedder Embedder
}

func New(store Store, embedder Embedder) *Service {
	return &Service{store: store, embedder: embedder}
}

const (
	defaultLimit = 10
	// defaultTitleBoost is added to the rank when the source title matches the
	// query. Measured on the judged set: exact-query MRR 0.938 -> 1.000 and P@5
	// 0.300 -> 0.400, with nothing else moving. The effect saturates here, so
	// this is the smallest value that buys all of it.
	defaultTitleBoost = 0.3
)

type searchInput struct {
	Query     string `json:"query" jsonschema:"words to look for; supports quoted phrases and -exclusions"`
	Kind      string `json:"kind,omitempty" jsonschema:"restrict to 'book', 'vault' or 'docs' (reference manuals); empty searches all"`
	Mode      string `json:"mode,omitempty" jsonschema:"'hybrid' (default), 'fts' for exact wording, 'vector' for meaning"`
	PerSource int    `json:"per_source,omitempty" jsonschema:"at most this many hits from one book or note; 0 means no limit. Use 1 to see which sources match at all"`
	Limit     int    `json:"limit,omitempty" jsonschema:"maximum hits to return, default 10"`
}

type searchOutput struct {
	Hits []corpus.Hit `json:"hits"`
}

type readInput struct {
	ID         int64 `json:"id" jsonschema:"the id of a hit returned by corpus_search"`
	Neighbours bool  `json:"neighbours,omitempty" jsonschema:"also return the pages before and after, for a passage cut by a page break"`
}

func (s *Service) Search(ctx context.Context, q corpus.Query) ([]corpus.Hit, error) {
	limit := clampLimit(q.Limit)
	// Capping per source needs a deeper list to cap: the hits being dropped have
	// to be replaced by something.
	q.Limit = limit
	if q.PerSource > 0 {
		q.Limit = min(limit*5, 50)
	}

	var hits []corpus.Hit
	var err error
	switch q.Mode {
	case "fts":
		hits, err = s.store.Search(ctx, q)
	case "vector":
		hits, err = s.vector(ctx, q)
	default:
		hits, err = s.hybrid(ctx, q)
	}
	if err != nil {
		return nil, err
	}
	return truncate(capPerSource(hits, q.PerSource), limit), nil
}

// capPerSource keeps a single book or note from filling the whole page. Ten
// paragraphs of one chapter answer "where is this discussed" ten times over.
func capPerSource(hits []corpus.Hit, perSource int) []corpus.Hit {
	if perSource <= 0 {
		return hits
	}
	seen := make(map[string]int, len(hits))
	kept := hits[:0:0]
	for _, h := range hits {
		if seen[h.Path] >= perSource {
			continue
		}
		seen[h.Path]++
		kept = append(kept, h)
	}
	return kept
}

func (s *Service) vector(ctx context.Context, q corpus.Query) ([]corpus.Hit, error) {
	vectors, err := s.embedder.Embed(ctx, []string{q.Text})
	if err != nil {
		return nil, err
	}
	return s.store.SearchVector(ctx, vectors[0], q.Kind, q.Limit)
}

// hybrid fuses both lists by rank. If the embedder is unreachable the text index
// still answers, which is the half that needs no GPU.
func (s *Service) hybrid(ctx context.Context, q corpus.Query) ([]corpus.Hit, error) {
	limit := q.Limit
	deep := q
	deep.Limit = limit * 2

	text, err := s.store.Search(ctx, deep)
	if err != nil {
		return nil, err
	}
	semantic, err := s.vector(ctx, deep)
	if err != nil {
		slog.WarnContext(ctx, "vector leg unavailable, falling back to text", "err", err)
		return truncate(text, limit), nil
	}
	return truncate(rank.Fuse(text, semantic), limit), nil
}

// Compare runs each leg once and fuses the hybrid from them; running the three
// modes independently would embed the same query twice.
func (s *Service) Compare(ctx context.Context, q corpus.Query) (map[string][]corpus.Hit, error) {
	limit := clampLimit(q.Limit)
	deep := q
	deep.Limit = limit * 2

	text, err := s.store.Search(ctx, deep)
	if err != nil {
		return nil, err
	}
	semantic, err := s.vector(ctx, deep)
	if err != nil {
		slog.WarnContext(ctx, "vector leg unavailable, comparing text alone", "err", err)
		return map[string][]corpus.Hit{
			"fts":    truncate(text, limit),
			"vector": {},
			"hybrid": truncate(text, limit),
		}, nil
	}
	return map[string][]corpus.Hit{
		"fts":    truncate(text, limit),
		"vector": truncate(semantic, limit),
		"hybrid": truncate(rank.Fuse(text, semantic), limit),
	}, nil
}

// Read returns the full text behind a hit.
func (s *Service) Read(ctx context.Context, id int64, neighbours bool) (corpus.Passage, error) {
	return s.store.Read(ctx, id, neighbours)
}

func (s *Service) MCP() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "corpus", Version: "v0.3.0"}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name: "corpus_search",
		Description: "Search the PDF library, the Obsidian vault and reference manuals (Sphinx HTML such as scikit-learn and NLTK). Returns ranked snippets with the book page, or the note or manual heading, to cite. " +
			"The corpus is half Russian and half English. Full-text search works inside one language only — ask a Russian question about an English book and 'fts' returns nothing, every time — so cross the language barrier with the default 'hybrid' or with 'vector'. " +
			"'fts' also joins your words with AND: a question phrased as a sentence usually returns nothing, while the one term you actually want returns plenty.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, searchOutput, error) {
		hits, err := s.Search(ctx, corpus.Query{
			Text: in.Query, Kind: in.Kind, Mode: in.Mode,
			Limit: in.Limit, PerSource: in.PerSource,
			TitleBoost: defaultTitleBoost,
		})
		if err != nil {
			return nil, searchOutput{}, err
		}
		return nil, searchOutput{Hits: hits}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "corpus_read",
		Description: "Return the full text behind a search hit: the whole book page or note section, optionally with the pages on either side. Use it to quote a source accurately instead of working from a snippet.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in readInput) (*mcp.CallToolResult, corpus.Passage, error) {
		passage, err := s.Read(ctx, in.ID, in.Neighbours)
		if err != nil {
			return nil, corpus.Passage{}, err
		}
		return nil, passage, nil
	})

	return server
}

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	server := s.MCP()

	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server }, nil))

	// The same search over plain HTTP: reaching for it with curl from a shell is
	// far less ceremony than an MCP handshake.
	mux.HandleFunc("GET /search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		hits, err := s.Search(r.Context(), queryFromURL(q))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, searchOutput{Hits: hits})
	})

	mux.HandleFunc("GET /compare", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		out, err := s.Compare(r.Context(), queryFromURL(q))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, out)
	})

	mux.HandleFunc("GET /read", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		if err != nil {
			http.Error(w, "id must be a number", http.StatusBadRequest)
			return
		}
		passage, err := s.Read(r.Context(), id, r.URL.Query().Has("neighbours"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, passage)
	})

	// /healthz answers "is this process alive", and stays 200 while the embedder
	// is away: search still works on full text, and a liveness probe that fails
	// on a degraded-but-working service invites a restart that fixes nothing.
	// /status is where the degradation is visible.
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		sources, chunks, err := s.store.Stats(r.Context())
		status := map[string]any{"sources": sources, "chunks": chunks, "db": "ok"}
		if err != nil {
			status["db"] = err.Error()
		}

		status["embedder"] = "ok"
		if _, err := s.embedder.Embed(r.Context(), []string{"проверка"}); err != nil {
			status["embedder"] = "unreachable"
			status["degraded"] = "search is running on full text alone; vectors are not being written"
		}
		writeJSON(w, status)
	})

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if _, _, err := s.store.Stats(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})

	return mux
}

// queryFromURL reads a search off the query string. `norm` is the ts_rank_cd
// length normalisation: a knob that exists so it can be measured against the
// judged set before anyone changes the default (see docs/search-evaluation.md).
func queryFromURL(v url.Values) corpus.Query {
	return corpus.Query{
		Text:          v.Get("q"),
		Kind:          v.Get("kind"),
		Mode:          v.Get("mode"),
		Limit:         atoiOrZero(v.Get("limit")),
		PerSource:     atoiOrZero(v.Get("per_source")),
		Normalization: atoiOrZero(v.Get("norm")),
		// Absent means the default; an explicit 0 turns it off, which is how the
		// sweep measures the alternative.
		TitleBoost: floatOr(v, "title_boost", defaultTitleBoost),
	}
}

func truncate(hits []corpus.Hit, limit int) []corpus.Hit {
	if len(hits) > limit {
		return hits[:limit]
	}
	return hits
}

func clampLimit(n int) int {
	if n <= 0 || n > 50 {
		return defaultLimit
	}
	return n
}

// atoiOrZero reads an optional numeric query parameter; absent and malformed
// both mean "unset", which the caller turns into the default.
func floatOr(v url.Values, key string, fallback float64) float64 {
	if !v.Has(key) {
		return fallback
	}
	f, _ := strconv.ParseFloat(v.Get(key), 64)
	return f
}

func atoiOrZero(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
