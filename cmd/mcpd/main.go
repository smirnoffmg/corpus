package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/smirnoffmg/corpus/internal/embed"
	"github.com/smirnoffmg/corpus/internal/rank"
	"github.com/smirnoffmg/corpus/internal/store"
)

type searchInput struct {
	Query string `json:"query" jsonschema:"words to look for; supports quoted phrases and -exclusions"`
	Kind  string `json:"kind,omitempty" jsonschema:"restrict to 'book' or 'vault'; empty searches both"`
	Mode  string `json:"mode,omitempty" jsonschema:"'hybrid' (default), 'fts' for exact wording, 'vector' for meaning"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum hits to return, default 10"`
}

type searchOutput struct {
	Hits []store.Hit `json:"hits"`
}

type searcher struct {
	store    *store.Store
	embedder *embed.Client
}

func (s *searcher) run(ctx context.Context, query, kind, mode string, limit int) ([]store.Hit, error) {
	switch mode {
	case "fts":
		return s.store.Search(ctx, query, kind, limit)
	case "vector":
		return s.vector(ctx, query, kind, limit)
	default:
		return s.hybrid(ctx, query, kind, limit)
	}
}

func (s *searcher) vector(ctx context.Context, query, kind string, limit int) ([]store.Hit, error) {
	vectors, err := s.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	return s.store.SearchVector(ctx, vectors[0], kind, limit)
}

// hybrid fuses both lists by rank. If the embedder is unreachable the text
// index still answers, which is the half that needs no GPU.
func (s *searcher) hybrid(ctx context.Context, query, kind string, limit int) ([]store.Hit, error) {
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

func truncate(hits []store.Hit, limit int) []store.Hit {
	if len(hits) > limit {
		return hits[:limit]
	}
	return hits
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	ollama := flag.String("ollama", "http://host.docker.internal:11434", "ollama base URL")
	model := flag.String("model", "bge-m3", "embedding model")
	flag.Parse()

	ctx := context.Background()
	st, err := store.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer st.Close()

	s := &searcher{store: st, embedder: embed.New(*ollama, *model)}

	tool := func(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, searchOutput, error) {
		hits, err := s.run(ctx, in.Query, in.Kind, in.Mode, clampLimit(in.Limit))
		if err != nil {
			return nil, searchOutput{}, err
		}
		return nil, searchOutput{Hits: hits}, nil
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "corpus", Version: "v0.2.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "corpus_search",
		Description: "Search the PDF library and the Obsidian vault. Returns ranked snippets with the book page or note heading to cite.",
	}, tool)

	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server }, nil))

	// The same search over plain HTTP: reaching for it with curl from a shell
	// is far less ceremony than an MCP handshake.
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		hits, err := s.run(r.Context(), q.Get("q"), q.Get("kind"), q.Get("mode"), clampLimit(limit))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, searchOutput{Hits: hits})
	})

	// Side by side, for judging what each retrieval method is actually good at.
	mux.HandleFunc("/compare", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit := clampLimit(mustAtoi(q.Get("limit")))
		out := map[string][]store.Hit{}
		for _, mode := range []string{"fts", "vector", "hybrid"} {
			hits, err := s.run(r.Context(), q.Get("q"), q.Get("kind"), mode, limit)
			if err != nil {
				http.Error(w, mode+": "+err.Error(), http.StatusInternalServerError)
				return
			}
			out[mode] = hits
		}
		writeJSON(w, out)
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if _, _, err := st.Stats(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("corpus mcp listening on %s/mcp", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

func clampLimit(n int) int {
	if n <= 0 || n > 50 {
		return 10
	}
	return n
}

func mustAtoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
