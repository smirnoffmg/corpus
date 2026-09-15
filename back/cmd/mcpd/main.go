package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/smirnoffmg/corpus/internal/api"
	"github.com/smirnoffmg/corpus/internal/bibfile"
	"github.com/smirnoffmg/corpus/internal/cite"
	"github.com/smirnoffmg/corpus/internal/embed"
	"github.com/smirnoffmg/corpus/internal/store"
	"github.com/smirnoffmg/corpus/internal/upload"
)

func main() {
	// SetDefault also redirects the log package, so anything a dependency logs
	// through it lands in the same stream, in the same shape.
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	if err := run(); err != nil {
		slog.Error("exit", "err", err)
		os.Exit(1)
	}
}

func vaultNameFrom(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Base(filepath.Clean(dir))
}

func run() error {
	addr := flag.String("addr", ":8080", "listen address")
	ollama := flag.String("ollama", "http://host.docker.internal:11434", "ollama base URL")
	model := flag.String("model", "bge-m3", "embedding model")
	efSearch := flag.Int("ef-search", 0, "hnsw.ef_search; 0 leaves the pgvector default of 40")
	books := flag.String("books", "/data/books", "book library that uploaded PDFs are saved into")
	docs := flag.String("docs", "/data/docs", "manuals directory that uploaded ZIPs are unpacked into")
	bibliography := flag.String("bibliography", "/data/bibliography", "directory of the bibliography file, the descriptions' system of record; empty keeps them in the database alone")
	uploadMax := flag.Int64("upload-max", 300<<20, "largest upload accepted, in bytes")
	allowedHosts := flag.String("allowed-hosts", "localhost,127.0.0.1,::1", "comma-separated host names requests may be addressed to; anything else is refused, against DNS rebinding")
	// Inside the container the vault is /data/vault; its Obsidian name is the
	// name of the directory on the host, which compose passes along.
	vaultName := flag.String("vault-name", vaultNameFrom(os.Getenv("CORPUS_VAULT_DIR")), "Obsidian vault name, for obsidian:// links to notes")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, os.Getenv("DATABASE_URL"), store.WithEfSearch(*efSearch))
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer st.Close()

	// Only the indexer migrates. Serving against a schema behind this build
	// fails later and obscurely, on the first query that needs a new column;
	// refusing to start says why, and compose restarts mcpd until the indexer
	// has caught the schema up.
	current, target, err := st.SchemaVersions(ctx)
	if err != nil {
		return fmt.Errorf("schema version: %w", err)
	}
	if current < target {
		return fmt.Errorf("schema is at version %d and this build needs %d: the indexer applies migrations on start", current, target)
	}

	opts := []api.Option{api.WithVault(*vaultName), api.WithBibliography(st, cite.NewLookup())}
	if *bibliography != "" {
		opts = append(opts, api.WithBibliographyExport(bibfile.New(st, *bibliography).Export))
	}
	// Uploads are an addition to search, not a condition of it: a server whose
	// library directories are missing still answers queries.
	if lib, err := upload.Open(*books, *docs, upload.Limits{}); err != nil {
		slog.WarnContext(ctx, "uploads disabled", "err", err)
	} else {
		defer lib.Close()
		opts = append(opts, api.WithLibrary(lib, *uploadMax))
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           api.Guard(api.New(st, embed.New(*ollama, *model, embed.WithRetry(1, 0)), opts...).Handler(), strings.Split(*allowedHosts, ",")),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("serve", "err", err)
			stop()
		}
	}()
	slog.Info("listening", "addr", *addr, "path", "/mcp")

	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}
