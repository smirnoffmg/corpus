package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/smirnoffmg/corpus/internal/embed"
	"github.com/smirnoffmg/corpus/internal/extract"
	"github.com/smirnoffmg/corpus/internal/index"
	"github.com/smirnoffmg/corpus/internal/store"
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

func run() error {
	var (
		booksDir   = flag.String("books", "/data/books", "directory with PDF books")
		vaultDir   = flag.String("vault", "/data/vault", "Obsidian vault root")
		docsDir    = flag.String("docs", "/data/docs", "reference manuals, one directory of HTML per manual; empty skips them")
		interval   = flag.Duration("interval", 0, "reindex period; 0 means index once and exit")
		ollama     = flag.String("ollama", "http://host.docker.internal:11434", "ollama base URL")
		model      = flag.String("model", "bge-m3", "embedding model")
		batch      = flag.Int("batch", 16, "chunks per embedding request")
		parallel   = flag.Int("parallel", 0, "extraction workers; 0 picks a cap from the core count")
		bookAbove  = flag.Int("book-split-above", 0, "split a book page longer than this; 0 keeps the measured default")
		bookTarget = flag.Int("book-split-target", 0, "size a book part aims for")
		noteAbove  = flag.Int("note-split-above", 0, "split a note section longer than this; 0 keeps the measured default")
		noteTarget = flag.Int("note-split-target", 0, "size a note part aims for")
		docsAbove  = flag.Int("docs-split-above", 0, "split a manual section longer than this; 0 keeps the note default")
		docsTarget = flag.Int("docs-split-target", 0, "size a manual part aims for")
		requeue    = flag.Bool("requeue", false, "put quarantined chunks back in the embedding queue and continue")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	if *requeue {
		n, err := st.RequeueQuarantined(ctx)
		if err != nil {
			return fmt.Errorf("requeue: %w", err)
		}
		slog.InfoContext(ctx, "requeued quarantined chunks", "chunks", n)
	}

	indexer := index.New(st, embed.New(*ollama, *model), index.Options{
		Books:        *booksDir,
		Vault:        *vaultDir,
		Docs:         *docsDir,
		Batch:        *batch,
		Parallel:     *parallel,
		BookSplitter: extract.Splitter{Above: *bookAbove, Target: *bookTarget},
		NoteSplitter: extract.Splitter{Above: *noteAbove, Target: *noteTarget},
		DocsSplitter: extract.Splitter{Above: *docsAbove, Target: *docsTarget},
	})

	for {
		if err := indexer.Pass(ctx); err != nil {
			slog.ErrorContext(ctx, "pass", "err", err)
		}
		if *interval == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			slog.InfoContext(ctx, "stopping")
			return nil
		case <-time.After(*interval):
		}
	}
}
