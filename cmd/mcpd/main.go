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

	"github.com/smirnoffmg/corpus/internal/store"
)

type searchInput struct {
	Query string `json:"query" jsonschema:"words to look for; supports quoted phrases and -exclusions"`
	Kind  string `json:"kind,omitempty" jsonschema:"restrict to 'book' or 'vault'; empty searches both"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum hits to return, default 10"`
}

type searchOutput struct {
	Hits []store.Hit `json:"hits"`
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	ctx := context.Background()
	st, err := store.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer st.Close()

	search := func(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, searchOutput, error) {
		if in.Limit <= 0 || in.Limit > 50 {
			in.Limit = 10
		}
		hits, err := st.Search(ctx, in.Query, in.Kind, in.Limit)
		if err != nil {
			return nil, searchOutput{}, err
		}
		return nil, searchOutput{Hits: hits}, nil
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "corpus", Version: "v0.1.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "corpus_search",
		Description: "Full-text search over the PDF library and the Obsidian vault. Returns ranked snippets with the book page or note heading to cite.",
	}, search)

	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server }, nil))
	// The same search over plain HTTP: reaching for it with curl from a shell
	// is far less ceremony than an MCP handshake.
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 50 {
			limit = 10
		}
		hits, err := st.Search(r.Context(), r.URL.Query().Get("q"), r.URL.Query().Get("kind"), limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(searchOutput{Hits: hits})
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
