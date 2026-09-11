package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/smirnoffmg/corpus/internal/corpus"
	"github.com/smirnoffmg/corpus/internal/embed"
	"github.com/smirnoffmg/corpus/internal/extract"
	"github.com/smirnoffmg/corpus/internal/lang"
	"github.com/smirnoffmg/corpus/internal/store"
)

// maxParallel bounds the extraction workers. pdftotext is CPU-bound, but each
// worker also holds a whole book's text, so the cap is lower than the core
// count on purpose.
var maxParallel = min(runtime.NumCPU(), 8)

func main() {
	var (
		booksDir = flag.String("books", "/data/books", "directory with PDF books")
		vaultDir = flag.String("vault", "/data/vault", "Obsidian vault root")
		ddlDir   = flag.String("migrations", "/app/migrations", "directory of .sql migrations")
		interval = flag.Duration("interval", 0, "reindex period; 0 means index once and exit")
		ollama   = flag.String("ollama", "http://host.docker.internal:11434", "ollama base URL")
		model    = flag.String("model", "bge-m3", "embedding model")
		batch    = flag.Int("batch", 16, "chunks per embedding request")
		requeue  = flag.Bool("requeue", false, "put quarantined chunks back in the embedding queue and continue")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer st.Close()

	if err := migrate(ctx, st, *ddlDir); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	if *requeue {
		n, err := st.RequeueQuarantined(ctx)
		if err != nil {
			log.Fatalf("requeue: %v", err)
		}
		log.Printf("requeued %d quarantined chunks", n)
	}

	embedder := embed.New(*ollama, *model)

	for {
		if err := indexAll(ctx, st, *booksDir, *vaultDir); err != nil {
			log.Printf("index: %v", err)
		}
		if err := embedAll(ctx, st, embedder, *batch); err != nil {
			log.Printf("embed: %v", err)
		}
		if *interval == 0 {
			return
		}
		select {
		case <-ctx.Done():
			log.Print("stopping")
			return
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

// embedAll fills in embeddings for chunks that have none. It is a separate pass
// from extraction so that a slow or absent ollama never blocks the text index.
func embedAll(ctx context.Context, st *store.Store, embedder *embed.Client, batch int) error {
	pending, err := st.MissingEmbeddings(ctx)
	if err != nil || pending == 0 {
		return err
	}
	log.Printf("embedding %d chunks", pending)

	start := time.Now()
	done := 0
	for ctx.Err() == nil {
		chunks, err := st.PendingEmbeddings(ctx, batch)
		if err != nil {
			return err
		}
		if len(chunks) == 0 {
			break
		}

		bodies := make([]string, len(chunks))
		ids := make([]int64, len(chunks))
		for i, c := range chunks {
			bodies[i], ids[i] = c.Body, c.ID
		}

		if err := st.CountAttempt(ctx, ids); err != nil {
			return err
		}
		vectors, err := embedder.Embed(ctx, bodies)
		if err != nil {
			return err
		}
		if err := st.SaveEmbeddings(ctx, ids, vectors); err != nil {
			return err
		}

		done += len(chunks)
		if done%(batch*20) == 0 {
			rate := float64(done) / time.Since(start).Seconds()
			left := time.Duration(float64(int(pending)-done)/rate) * time.Second
			log.Printf("embedded %d/%d (%.1f chunks/s, ~%s left)",
				done, pending, rate, left.Round(time.Minute))
		}
	}
	log.Printf("embedded %d chunks in %s", done, time.Since(start).Round(time.Second))
	if n, err := st.Quarantined(ctx); err == nil && n > 0 {
		log.Printf("%d chunks quarantined after repeated embedding failures", n)
	}
	return ctx.Err()
}

func indexAll(ctx context.Context, st *store.Store, booksDir, vaultDir string) error {
	start := time.Now()

	books, err := indexKind(ctx, st, "book", booksDir, ".pdf", func(path string) ([]corpus.Chunk, error) {
		return extract.PDF(ctx, path)
	})
	if err != nil {
		return err
	}

	notes, err := indexKind(ctx, st, "vault", vaultDir, ".md", extract.Markdown)
	if err != nil {
		return err
	}

	sources, chunks, err := st.Stats(ctx)
	if err != nil {
		return err
	}
	log.Printf("indexed %d books, %d notes in %s; corpus: %d sources, %d chunks",
		books, notes, time.Since(start).Round(time.Second), sources, chunks)
	return nil
}

func indexKind(
	ctx context.Context,
	st *store.Store,
	kind, root, ext string,
	parse func(string) ([]corpus.Chunk, error),
) (int, error) {
	files, err := collect(root, ext)
	if err != nil {
		return 0, err
	}

	// A counting semaphore, as in TGPL 8.6: a vacant slot is a token entitling
	// one worker to proceed.
	tokens := make(chan struct{}, maxParallel)
	var (
		wg      sync.WaitGroup
		updated atomic.Int64
	)
	for _, rel := range files {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case tokens <- struct{}{}:
				defer func() { <-tokens }()
			case <-ctx.Done():
				return
			}
			// One unreadable file must not end the pass: a fault in a single
			// document is not a failure of the index.
			if changed, err := indexFile(ctx, st, kind, root, rel, parse); err != nil {
				log.Printf("skip %s: %v", rel, err)
			} else if changed {
				updated.Add(1)
			}
		}()
	}
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return int(updated.Load()), err
	}
	// An empty walk means the mount is missing, not that every file was
	// deleted; pruning on that would wipe the whole index.
	if len(files) == 0 {
		log.Printf("no %s files under %s, skipping prune", kind, root)
		return int(updated.Load()), nil
	}
	if _, err := st.Prune(ctx, kind, files); err != nil {
		return int(updated.Load()), err
	}
	return int(updated.Load()), nil
}

func indexFile(
	ctx context.Context,
	st *store.Store,
	kind, root, rel string,
	parse func(string) ([]corpus.Chunk, error),
) (bool, error) {
	path := filepath.Join(root, rel)

	hash, err := hashFile(path)
	if err != nil {
		return false, err
	}
	unchanged, err := st.Unchanged(ctx, rel, hash)
	if err != nil || unchanged {
		return false, err
	}

	chunks, err := parse(path)
	if err != nil {
		return false, err
	}
	for i := range chunks {
		chunks[i].Lang = lang.Detect(chunks[i].Body)
	}

	src := corpus.Source{
		Kind:  kind,
		Path:  rel,
		Title: strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel)),
		Hash:  hash,
	}
	return true, st.Replace(ctx, src, chunks)
}

func collect(root, ext string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ext) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	})
	return files, err
}

func skipDir(name string) bool {
	return name == ".git" || name == ".obsidian" || name == ".trash" || name == "node_modules"
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
