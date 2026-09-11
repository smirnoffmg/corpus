// Package index builds the corpus from files on disk: it walks the library and
// the vault, extracts text, and fills in embeddings. The store and the embedder
// arrive as interfaces, so the extraction pool — the one place here where
// goroutines share state — can be exercised by a test, and therefore by the race
// detector.
package index

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/smirnoffmg/corpus/internal/corpus"
	"github.com/smirnoffmg/corpus/internal/extract"
	"github.com/smirnoffmg/corpus/internal/lang"
)

type Store interface {
	Unchanged(ctx context.Context, path, hash string) (bool, error)
	Replace(ctx context.Context, src corpus.Source, chunks []corpus.Chunk) error
	SetTitle(ctx context.Context, path, title string) error
	Forget(ctx context.Context, path string) (bool, error)
	PathByHash(ctx context.Context, kind, hash string) (string, bool, error)
	Rename(ctx context.Context, oldPath, newPath, title string) error
	Prune(ctx context.Context, kind string, seen []string) (int64, error)
	Stats(ctx context.Context) (sources, chunks int64, err error)
	MissingEmbeddings(ctx context.Context) (int64, error)
	Quarantined(ctx context.Context) (int64, error)
	PendingEmbeddings(ctx context.Context, limit int) ([]corpus.Pending, error)
	CountAttempt(ctx context.Context, ids []int64) error
	SaveEmbeddings(ctx context.Context, ids []int64, vectors [][]float32) error
}

type Embedder interface {
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
}

type Options struct {
	Books    string
	Vault    string
	Batch    int               // chunks per embedding request
	Parallel int               // extraction workers; defaults to the core count, capped
	Titles   map[string]string // manual title overrides, keyed by relative path
}

type Indexer struct {
	store    Store
	embedder Embedder
	opts     Options
}

// maxParallel caps the extraction workers. pdftotext is CPU-bound, but each
// worker also holds a whole book's text, so the cap is below the core count on
// purpose.
func maxParallel() int { return min(runtime.NumCPU(), 8) }

func New(store Store, embedder Embedder, opts Options) *Indexer {
	if opts.Parallel <= 0 {
		opts.Parallel = maxParallel()
	}
	if opts.Batch <= 0 {
		opts.Batch = 16
	}
	return &Indexer{store: store, embedder: embedder, opts: opts}
}

// Pass indexes everything and then fills in the embeddings. Embedding is a pass
// of its own so that a slow or absent embedder never blocks the text index.
func (ix *Indexer) Pass(ctx context.Context) error {
	if err := ix.Index(ctx); err != nil {
		return err
	}
	return ix.Embed(ctx)
}

func (ix *Indexer) Index(ctx context.Context) error {
	start := time.Now()

	books, err := ix.indexKind(ctx, "book", ix.opts.Books, ".pdf", func(path string) ([]corpus.Chunk, error) {
		return extract.PDF(ctx, path)
	})
	if err != nil {
		return err
	}

	notes, err := ix.indexKind(ctx, "vault", ix.opts.Vault, ".md", extract.Markdown)
	if err != nil {
		return err
	}

	sources, chunks, err := ix.store.Stats(ctx)
	if err != nil {
		return err
	}
	log.Printf("indexed %d books, %d notes in %s; corpus: %d sources, %d chunks",
		books, notes, time.Since(start).Round(time.Second), sources, chunks)
	return nil
}

func (ix *Indexer) indexKind(
	ctx context.Context,
	kind, root, ext string,
	parse func(string) ([]corpus.Chunk, error),
) (int, error) {
	files, err := collect(root, ext)
	if err != nil {
		return 0, err
	}

	// A counting semaphore, as in TGPL 8.6: a vacant slot is a token entitling
	// one worker to proceed.
	present := make(map[string]bool, len(files))
	for _, rel := range files {
		present[rel] = true
	}

	tokens := make(chan struct{}, ix.opts.Parallel)
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
			if changed, err := ix.indexFile(ctx, kind, root, rel, present, parse); err != nil {
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
	// An empty walk means the mount is missing, not that every file was deleted;
	// pruning on that would wipe the whole index.
	if len(files) == 0 {
		log.Printf("no %s files under %s, skipping prune", kind, root)
		return int(updated.Load()), nil
	}
	if _, err := ix.store.Prune(ctx, kind, files); err != nil {
		return int(updated.Load()), err
	}
	return int(updated.Load()), nil
}

func (ix *Indexer) indexFile(
	ctx context.Context,
	kind, root, rel string,
	present map[string]bool,
	parse func(string) ([]corpus.Chunk, error),
) (bool, error) {
	path := filepath.Join(root, rel)

	hash, err := hashFile(path)
	if err != nil {
		return false, err
	}
	title := ix.title(ctx, kind, path, rel)

	unchanged, err := ix.store.Unchanged(ctx, rel, hash)
	if err != nil {
		return false, err
	}
	if unchanged {
		// The file is the same, but the title may have been recognised better
		// since; that is a column update, not a reason to re-extract.
		return false, ix.store.SetTitle(ctx, rel, title)
	}

	// Nothing is indexed under this path, but the same bytes may already be
	// indexed under another one that has since disappeared from the walk: that
	// is a rename, and re-extracting would throw away the embeddings.
	old, found, err := ix.store.PathByHash(ctx, kind, hash)
	if err != nil {
		return false, err
	}
	if found && !present[old] {
		log.Printf("renamed: %s -> %s", old, rel)
		return false, ix.store.Rename(ctx, old, rel, title)
	}

	chunks, err := parse(path)
	if err != nil {
		return false, err
	}
	// A scan without an OCR layer yields nothing. Re-parsing it next pass costs
	// milliseconds — there is no text to pull — so it is cheaper to forget it
	// than to keep an empty source around.
	if len(chunks) == 0 {
		dropped, err := ix.store.Forget(ctx, rel)
		if dropped {
			log.Printf("no text in %s: dropped (a scan without OCR?)", rel)
		}
		return false, err
	}
	for i := range chunks {
		chunks[i].Lang = lang.Detect(chunks[i].Body)
	}

	src := corpus.Source{Kind: kind, Path: rel, Title: title, Hash: hash}
	return true, ix.store.Replace(ctx, src, chunks)
}

// Embed fills in embeddings for chunks that have none.
func (ix *Indexer) Embed(ctx context.Context) error {
	pending, err := ix.store.MissingEmbeddings(ctx)
	if err != nil || pending == 0 {
		return err
	}
	log.Printf("embedding %d chunks", pending)

	start := time.Now()
	done := 0
	for ctx.Err() == nil {
		chunks, err := ix.store.PendingEmbeddings(ctx, ix.opts.Batch)
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

		// The attempt is recorded before the call, so a crash or a timeout counts
		// too; otherwise a chunk that kills the process is retried forever.
		if attemptErr := ix.store.CountAttempt(ctx, ids); attemptErr != nil {
			return attemptErr
		}
		vectors, err := ix.embedder.Embed(ctx, bodies)
		if err != nil {
			return err
		}
		if err := ix.store.SaveEmbeddings(ctx, ids, vectors); err != nil {
			return err
		}

		done += len(chunks)
		if done%(ix.opts.Batch*20) == 0 {
			rate := float64(done) / time.Since(start).Seconds()
			left := time.Duration(float64(int(pending)-done)/rate) * time.Second
			log.Printf("embedded %d/%d (%.1f chunks/s, ~%s left)",
				done, pending, rate, left.Round(time.Minute))
		}
	}
	log.Printf("embedded %d chunks in %s", done, time.Since(start).Round(time.Second))
	if n, err := ix.store.Quarantined(ctx); err == nil && n > 0 {
		log.Printf("%d chunks quarantined after repeated embedding failures", n)
	}
	return ctx.Err()
}

// title asks the document what it is called; a note is named by its filename,
// which in a vault is the note's real name.
func (ix *Indexer) title(ctx context.Context, kind, path, rel string) string {
	name := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	if kind != "book" {
		return name
	}
	return extract.PDFTitle(ctx, path, name)
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
