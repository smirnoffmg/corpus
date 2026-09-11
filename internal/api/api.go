// Package api holds the search service and its transports. The store and the
// embedder arrive as interfaces so that the behaviour that matters — which
// retrieval legs run, and what happens when one of them is down — can be tested
// without a database or a GPU.
package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/smirnoffmg/corpus/internal/corpus"
	"github.com/smirnoffmg/corpus/internal/rank"
)

type Store interface {
	Search(ctx context.Context, query, kind string, limit int) ([]corpus.Hit, error)
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

const defaultLimit = 10

type searchInput struct {
	Query     string `json:"query" jsonschema:"words to look for; supports quoted phrases and -exclusions"`
	Kind      string `json:"kind,omitempty" jsonschema:"restrict to 'book' or 'vault'; empty searches both"`
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

func (s *Service) Search(ctx context.Context, query, kind, mode string, limit, perSource int) ([]corpus.Hit, error) {
	limit = clampLimit(limit)
	// Capping per source needs a deeper list to cap: the hits being dropped have
	// to be replaced by something.
	fetch := limit
	if perSource > 0 {
		fetch = min(limit*5, 50)
	}

	var hits []corpus.Hit
	var err error
	switch mode {
	case "fts":
		hits, err = s.store.Search(ctx, query, kind, fetch)
	case "vector":
		hits, err = s.vector(ctx, query, kind, fetch)
	default:
		hits, err = s.hybrid(ctx, query, kind, fetch)
	}
	if err != nil {
		return nil, err
	}
	return truncate(capPerSource(hits, perSource), limit), nil
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

func (s *Service) vector(ctx context.Context, query, kind string, limit int) ([]corpus.Hit, error) {
	vectors, err := s.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	return s.store.SearchVector(ctx, vectors[0], kind, limit)
}

// hybrid fuses both lists by rank. If the embedder is unreachable the text index
// still answers, which is the half that needs no GPU.
func (s *Service) hybrid(ctx context.Context, query, kind string, limit int) ([]corpus.Hit, error) {
	text, err := s.store.Search(ctx, query, kind, limit*2)
	if err != nil {
		return nil, err
	}
	semantic, err := s.vector(ctx, query, kind, limit*2)
	if err != nil {
		log.Printf("vector leg unavailable, falling back to text: %v", err)
		return truncate(text, limit), nil
	}
	return truncate(rank.Fuse(text, semantic), limit), nil
}

// Compare runs each leg once and fuses the hybrid from them; running the three
// modes independently would embed the same query twice.
func (s *Service) Compare(ctx context.Context, query, kind string, limit int) (map[string][]corpus.Hit, error) {
	limit = clampLimit(limit)
	text, err := s.store.Search(ctx, query, kind, limit*2)
	if err != nil {
		return nil, err
	}
	semantic, err := s.vector(ctx, query, kind, limit*2)
	if err != nil {
		return nil, err
	}
	return map[string][]corpus.Hit{
		"fts":    truncate(text, limit),
		"vector": truncate(semantic, limit),
		"hybrid": truncate(rank.Fuse(text, semantic), limit),
	}, nil
}

func (s *Service) MCP() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "corpus", Version: "v0.3.0"}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "corpus_search",
		Description: "Search the PDF library and the Obsidian vault. Returns ranked snippets with the book page or note heading to cite.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, searchOutput, error) {
		hits, err := s.Search(ctx, in.Query, in.Kind, in.Mode, in.Limit, in.PerSource)
		if err != nil {
			return nil, searchOutput{}, err
		}
		return nil, searchOutput{Hits: hits}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "corpus_read",
		Description: "Return the full text behind a search hit: the whole book page or note section, optionally with the pages on either side. Use it to quote a source accurately instead of working from a snippet.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in readInput) (*mcp.CallToolResult, corpus.Passage, error) {
		passage, err := s.store.Read(ctx, in.ID, in.Neighbours)
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
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		hits, err := s.Search(r.Context(), q.Get("q"), q.Get("kind"), q.Get("mode"),
			atoiOrZero(q.Get("limit")), atoiOrZero(q.Get("per_source")))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, searchOutput{Hits: hits})
	})

	mux.HandleFunc("/compare", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		out, err := s.Compare(r.Context(), q.Get("q"), q.Get("kind"), atoiOrZero(q.Get("limit")))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, out)
	})

	mux.HandleFunc("/read", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		if err != nil {
			http.Error(w, "id must be a number", http.StatusBadRequest)
			return
		}
		passage, err := s.store.Read(r.Context(), id, r.URL.Query().Has("neighbours"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, passage)
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if _, _, err := s.store.Stats(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})

	return mux
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
