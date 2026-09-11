package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/smirnoffmg/corpus/internal/embed"
	"github.com/smirnoffmg/corpus/internal/index"
	"github.com/smirnoffmg/corpus/internal/store"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	var (
		booksDir = flag.String("books", "/data/books", "directory with PDF books")
		vaultDir = flag.String("vault", "/data/vault", "Obsidian vault root")
		ddlDir   = flag.String("migrations", "/app/migrations", "directory of .sql migrations")
		interval = flag.Duration("interval", 0, "reindex period; 0 means index once and exit")
		ollama   = flag.String("ollama", "http://host.docker.internal:11434", "ollama base URL")
		model    = flag.String("model", "bge-m3", "embedding model")
		batch    = flag.Int("batch", 16, "chunks per embedding request")
		parallel = flag.Int("parallel", 0, "extraction workers; 0 picks a cap from the core count")
		requeue  = flag.Bool("requeue", false, "put quarantined chunks back in the embedding queue and continue")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer st.Close()

	if err := migrate(ctx, st, *ddlDir); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	if *requeue {
		n, err := st.RequeueQuarantined(ctx)
		if err != nil {
			return fmt.Errorf("requeue: %w", err)
		}
		log.Printf("requeued %d quarantined chunks", n)
	}

	indexer := index.New(st, embed.New(*ollama, *model), index.Options{
		Books:    *booksDir,
		Vault:    *vaultDir,
		Batch:    *batch,
		Parallel: *parallel,
	})

	for {
		if err := indexer.Pass(ctx); err != nil {
			log.Printf("pass: %v", err)
		}
		if *interval == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			log.Print("stopping")
			return nil
		case <-time.After(*interval):
		}
	}
}

func migrate(ctx context.Context, st *store.Store, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		ddl, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if err := st.Migrate(ctx, string(ddl)); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}
