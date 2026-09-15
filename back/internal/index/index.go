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
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/smirnoffmg/corpus/internal/cite"
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
	CountAttempt(ctx context.Context, keys []string) error
	SaveEmbeddings(ctx context.Context, keys []string, vectors [][]float32) error
	PruneEmbeddings(ctx context.Context) (int64, error)
	UndescribedBooks(ctx context.Context) ([]corpus.Undescribed, error)
	EnsureDraft(ctx context.Context, key string, csl corpus.CSL) error
}

type Embedder interface {
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
}

type Options struct {
	Books        string
	Vault        string
	Docs         string           // reference manuals, one directory per manual; empty skips them
	Batch        int              // chunks per embedding request
	Parallel     int              // extraction workers; defaults to the core count, capped
	BookSplitter extract.Splitter // how long a book page chunk may be
	NoteSplitter extract.Splitter // how long a note chunk may be
	DocsSplitter extract.Splitter // how long a manual section chunk may be
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
	// A zero splitter means "keep the unit whole", which is what a book page
	// wants; only the note splitter gets a non-zero default.
	if opts.NoteSplitter == (extract.Splitter{}) {
		opts.NoteSplitter = extract.DefaultNoteSplitter
	}
	// A manual section is shaped like a note section — prose and code under a
	// heading — so it starts from the note sizes until the judged set says
	// otherwise.
	if opts.DocsSplitter == (extract.Splitter{}) {
		opts.DocsSplitter = extract.DefaultNoteSplitter
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
		return ix.opts.BookSplitter.PDF(ctx, path)
	})
	if err != nil {
		return err
	}

	notes, err := ix.indexKind(ctx, "vault", ix.opts.Vault, ".md", ix.opts.NoteSplitter.Markdown)
	if err != nil {
		return err
	}

	docs, err := ix.indexKind(ctx, "docs", ix.opts.Docs, ".html", ix.opts.DocsSplitter.HTML)
	if err != nil {
		return err
	}

	// Vectors are filed by text, so what changed or deleted files embedded is
	// left with no chunk to use it; once the walk is done it can go.
	pruned, err := ix.store.PruneEmbeddings(ctx)
	if err != nil {
		return err
	}

	// Descriptions are drafted after indexing, from what the index now holds; a
	// failure here costs a draft, not the pass.
	if draftErr := ix.draft(ctx, docsManuals(ix.opts.Docs)); draftErr != nil && ctx.Err() == nil {
		slog.WarnContext(ctx, "drafting descriptions", "err", draftErr)
	}

	sources, chunks, err := ix.store.Stats(ctx)
	if err != nil {
		return err
	}
	// One wide event per pass rather than a line per stage: everything needed to
	// judge the pass is on it, and passes can be compared field by field.
	slog.InfoContext(ctx, "pass complete",
		"books", books,
		"notes", notes,
		"docs", docs,
		"vectors_pruned", pruned,
		"took", time.Since(start).Round(time.Second).String(),
		"sources", sources,
		"chunks", chunks)
	return nil
}

func (ix *Indexer) indexKind(
	ctx context.Context,
	kind, root, ext string,
	parse func(string) ([]corpus.Chunk, error),
) (int, error) {
	if root == "" {
		return 0, nil
	}
	files, err := collect(root, ext)
	if err != nil {
		return 0, err
	}

	present := make(map[string]bool, len(files))
	for _, rel := range files {
		present[rel] = true
	}

	var updated atomic.Int64
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(ix.opts.Parallel)
	for _, rel := range files {
		if gctx.Err() != nil {
			break
		}
		g.Go(func() error {
			// One unreadable file must not end the pass: a fault in a single
			// document is not a failure of the index. Returning the error here
			// would cancel the group and abandon the rest of the library, so
			// the file is logged and the walk goes on.
			if changed, err := ix.indexFile(gctx, kind, root, rel, present, parse); err != nil {
				slog.WarnContext(gctx, "skipped", "path", rel, "err", err)
			} else if changed {
				updated.Add(1)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return int(updated.Load()), err
	}

	if err := ctx.Err(); err != nil {
		return int(updated.Load()), err
	}
	// An empty walk means the mount is missing, not that every file was deleted;
	// pruning on that would wipe the whole index.
	if len(files) == 0 {
		slog.WarnContext(ctx, "no files under root, skipping prune", "kind", kind, "root", root)
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
		slog.InfoContext(ctx, "renamed", "from", old, "to", rel)
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
			slog.WarnContext(ctx, "no text, dropped (a scan without OCR?)", "path", rel)
		}
		return false, err
	}
	for i := range chunks {
		chunks[i].Lang = lang.Detect(chunks[i].Body)
	}

	src := corpus.Source{Kind: kind, Path: rel, Title: title, Hash: hash}
	return true, ix.store.Replace(ctx, src, chunks)
}

// Run indexes and embeds every interval, and starts a pass at once whenever
// listen's channel delivers — an upload has landed and should not wait for the
// next scheduled pass. listen may be nil, and is called again whenever its
// channel closes, so a dropped database connection only costs the wake-ups
// until the next pass. Run returns when ctx ends.
func (ix *Indexer) Run(ctx context.Context, interval time.Duration, listen func(context.Context) (<-chan struct{}, error)) error {
	var wake <-chan struct{}
	for ctx.Err() == nil {
		if wake == nil && listen != nil {
			w, err := listen(ctx)
			if err != nil && ctx.Err() == nil {
				slog.WarnContext(ctx, "reindex requests unavailable, indexing on the interval only", "err", err)
			}
			wake = w
		}

		if ix.pass(ctx, wake) {
			continue
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		case _, ok := <-wake:
			timer.Stop()
			if !ok {
				// A closed channel is always ready; selecting on it again would
				// turn the loop into a spin.
				wake = nil
			}
		}
	}
	return nil
}

// pass is one Index and Embed for Run, which logs failures rather than
// stopping on them: the next pass is the retry. It reports whether embedding
// gave way to a reindex request.
func (ix *Indexer) pass(ctx context.Context, wake <-chan struct{}) (interrupted bool) {
	if err := ix.Index(ctx); err != nil && ctx.Err() == nil {
		slog.ErrorContext(ctx, "pass", "err", err)
	}
	interrupted, err := ix.embed(ctx, wake)
	if err != nil && ctx.Err() == nil {
		slog.ErrorContext(ctx, "pass", "err", err)
	}
	return interrupted
}

// draft files a description for every book and manual that has none: title,
// PDF author, ISBN or DOI for a book; title, version, address and publisher
// for a manual. They are drafts, to be checked and completed by hand.
func (ix *Indexer) draft(ctx context.Context, manuals []string) error {
	books, err := ix.store.UndescribedBooks(ctx)
	if err != nil {
		return err
	}
	for _, b := range books {
		author := extract.PDFAuthor(ctx, filepath.Join(ix.opts.Books, b.Path))
		if err := ix.store.EnsureDraft(ctx, b.Hash, cite.BookDraft(b.Title, author, b.Head, b.Tail)); err != nil {
			return err
		}
	}
	for _, m := range manuals {
		draft := extract.ManualDraft(filepath.Join(ix.opts.Docs, m), m, time.Now())
		if err := ix.store.EnsureDraft(ctx, "manual:"+m, draft); err != nil {
			return err
		}
	}
	if len(books) > 0 {
		slog.InfoContext(ctx, "drafted descriptions", "books", len(books))
	}
	return nil
}

// docsManuals lists the manual directories under the docs root.
func docsManuals(root string) []string {
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && !skipDir(e.Name()) && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	return out
}

// Embed fills in embeddings for chunks that have none.
func (ix *Indexer) Embed(ctx context.Context) error {
	_, err := ix.embed(ctx, nil)
	return err
}

// embed drains the queue, and gives way between batches when wake delivers:
// a whole manual takes hours to embed, and a book uploaded meanwhile should be
// searchable by its text in seconds. The batch in flight is finished first, so
// no attempt is spent on an interrupted call.
func (ix *Indexer) embed(ctx context.Context, wake <-chan struct{}) (interrupted bool, err error) {
	pending, err := ix.store.MissingEmbeddings(ctx)
	if err != nil || pending == 0 {
		return false, err
	}
	slog.InfoContext(ctx, "embedding", "pending", pending)

	start := time.Now()
	done := 0
	for ctx.Err() == nil {
		select {
		case _, ok := <-wake:
			if ok {
				slog.InfoContext(ctx, "embedding paused for a reindex request", "done", done, "pending", pending)
				return true, nil
			}
			wake = nil
		default:
		}

		chunks, err := ix.store.PendingEmbeddings(ctx, ix.opts.Batch)
		if err != nil {
			return false, err
		}
		if len(chunks) == 0 {
			break
		}

		bodies := make([]string, len(chunks))
		keys := make([]string, len(chunks))
		for i, c := range chunks {
			bodies[i], keys[i] = c.Body, c.Key
		}

		// The attempt is recorded before the call, so a crash or a timeout counts
		// too; otherwise a chunk that kills the process is retried forever.
		if attemptErr := ix.store.CountAttempt(ctx, keys); attemptErr != nil {
			return false, attemptErr
		}
		vectors, err := ix.embedder.Embed(ctx, bodies)
		if err != nil {
			return false, err
		}
		if err := ix.store.SaveEmbeddings(ctx, keys, vectors); err != nil {
			return false, err
		}

		done += len(chunks)
		if done%(ix.opts.Batch*20) == 0 {
			rate := float64(done) / time.Since(start).Seconds()
			left := time.Duration(float64(int(pending)-done)/rate) * time.Second
			slog.InfoContext(ctx, "embedding progress",
				"done", done, "pending", pending,
				"rate", math.Round(rate*10)/10, "left", left.Round(time.Minute).String())
		}
	}
	slog.InfoContext(ctx, "embedded", "chunks", done, "took", time.Since(start).Round(time.Second).String())
	if n, err := ix.store.Quarantined(ctx); err == nil && n > 0 {
		slog.WarnContext(ctx, "chunks quarantined after repeated embedding failures", "chunks", n)
	}
	return false, ctx.Err()
}

// title asks the document what it is called; a note is named by its filename,
// which in a vault is the note's real name. A manual page is named after its
// manual too: "Installation" is a page of every manual there is.
func (ix *Indexer) title(ctx context.Context, kind, path, rel string) string {
	name := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	switch kind {
	case "book":
		return extract.PDFTitle(ctx, path, name)
	case "docs":
		page := extract.HTMLTitle(path, name)
		if manual, _, nested := strings.Cut(filepath.ToSlash(rel), "/"); nested {
			return manual + " · " + page
		}
		return page
	}
	return name
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
		if !strings.EqualFold(filepath.Ext(path), ext) || skipFile(d.Name()) {
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
	switch name {
	case ".git", ".obsidian", ".trash", "node_modules":
		return true
	case "99 - templates":
		// Templater sources are code, not knowledge.
		return true
	case "_static", "_sources", "_modules", "_images", "_downloads":
		// Sphinx build by-products: theme assets, the reST sources, and the
		// highlighted source code of every module, which the API pages already
		// document.
		return true
	case ".upload-tmp":
		// A manual being unpacked; it is renamed into place once complete.
		return true
	}
	return false
}

// skipFile leaves out the pages Sphinx generates from the others: an index of
// every term would match every query, and the search page has no content.
func skipFile(name string) bool {
	switch name {
	case "genindex.html", "search.html", "py-modindex.html":
		return true
	}
	return false
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
