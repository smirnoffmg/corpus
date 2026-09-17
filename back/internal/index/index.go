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
	"errors"
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
	UncountAttempt(ctx context.Context, keys []string) error
	RecordFailure(ctx context.Context, keys []string, reason string) error
	SaveEmbeddings(ctx context.Context, keys []string, vectors [][]float32) error
	PruneEmbeddings(ctx context.Context) (int64, error)
	CleanTextIndex(ctx context.Context) (int64, error)
	UndescribedBooks(ctx context.Context) ([]corpus.Undescribed, error)
	UnparsedPapers(ctx context.Context, parser int) ([]corpus.Unparsed, error)
	SaveCitations(ctx context.Context, paper string, parser int, cs []corpus.Citation) error
	ResolveCitations(ctx context.Context) (int64, error)
	EnsureDraft(ctx context.Context, key string, csl corpus.CSL) error
	MarkScan(ctx context.Context, scan corpus.Scan) error
	NextScan(ctx context.Context) (corpus.Scan, bool, error)
	ScanProgress(ctx context.Context, hash string, recognised int) error
	ScanFailed(ctx context.Context, hash, reason string) error
	PruneScans(ctx context.Context, kind string, seen []string) (int64, error)
}

type Embedder interface {
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
}

type Options struct {
	Books        string
	Vault        string
	Docs         string           // reference manuals, one directory per manual; empty skips them
	Papers       string           // publications, PDFs like books; empty skips them
	Batch        int              // chunks per embedding request
	Parallel     int              // extraction workers; defaults to the core count, capped
	BookSplitter extract.Splitter // how long a book page chunk may be
	NoteSplitter extract.Splitter // how long a note chunk may be
	DocsSplitter extract.Splitter // how long a manual section chunk may be
	// Bibliography is the file the bibliographic descriptions are kept in;
	// nil leaves them in the database alone.
	Bibliography Bibliography
	// OCR recognises books that have no text layer; nil leaves scans out of
	// the index, as before.
	OCR Recognizer
	// PaperText reads a publication's pages for its reference list. It is a
	// field because the reader is an external binary, and a test cannot ship a
	// PDF for every shape a bibliography comes in; nil uses poppler.
	PaperText func(ctx context.Context, path string) ([]string, error)
}

// Recognizer reads the text of scanned PDFs and keeps it under the file's
// content hash.
type Recognizer interface {
	Pages(ctx context.Context, pdf string) (int, error)
	// Cached is every page's text, once all of them have been recognised.
	Cached(hash string, pages int) ([]string, bool)
	// Recognize reads the pages not yet cached, until done or until stop
	// closes, and reports whether every page is now cached.
	Recognize(ctx context.Context, hash, pdf string, pages int, stop <-chan struct{}, progress func(done int)) (bool, error)
}

// Bibliography is the bibliography's system of record outside the database.
type Bibliography interface {
	Import(ctx context.Context) (int, error)
	Export(ctx context.Context) error
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

// New takes Options by value on purpose: it is called once per process, and a
// copy keeps the caller's struct from changing under a running indexer.
func New(store Store, embedder Embedder, opts Options) *Indexer { //nolint:gocritic // see above
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
	if opts.PaperText == nil {
		opts.PaperText = extract.PaperText
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

	// The file first: a description restored from it must be in the table
	// before the pass drafts one for the same source. A failure costs this
	// pass the import, not the index.
	if ix.opts.Bibliography != nil {
		if n, err := ix.opts.Bibliography.Import(ctx); err != nil {
			slog.ErrorContext(ctx, "importing the bibliography file", "err", err)
		} else if n > 0 {
			slog.InfoContext(ctx, "bibliography file applied", "changed", n)
		}
	}

	books, bookFiles, err := ix.indexKind(ctx, "book", ix.opts.Books, ".pdf", func(path string) ([]corpus.Chunk, error) {
		return ix.opts.BookSplitter.PDF(ctx, path)
	})
	if err != nil {
		return err
	}
	// Same guard as Prune: no books walked means no mount, not an empty shelf.
	if ix.opts.OCR != nil && len(bookFiles) > 0 {
		if _, pruneErr := ix.store.PruneScans(ctx, "book", bookFiles); pruneErr != nil {
			return pruneErr
		}
	}

	papers, paperFiles, err := ix.indexKind(ctx, "paper", ix.opts.Papers, ".pdf", func(path string) ([]corpus.Chunk, error) {
		return ix.opts.BookSplitter.Paper(ctx, path)
	})
	if err != nil {
		return err
	}
	if ix.opts.OCR != nil && len(paperFiles) > 0 {
		if _, pruneErr := ix.store.PruneScans(ctx, "paper", paperFiles); pruneErr != nil {
			return pruneErr
		}
	}

	notes, _, err := ix.indexKind(ctx, "vault", ix.opts.Vault, ".md", ix.opts.NoteSplitter.Markdown)
	if err != nil {
		return err
	}

	docs, _, err := ix.indexKind(ctx, "docs", ix.opts.Docs, ".html", ix.opts.DocsSplitter.HTML)
	if err != nil {
		return err
	}

	// Rewritten chunks leave their lexemes in the text index's pending list,
	// which searches scan until it is merged. Merging costs nothing when nothing
	// changed, but a pass that found nothing new has no reason to ask.
	if books+papers+notes+docs > 0 {
		if _, cleanErr := ix.store.CleanTextIndex(ctx); cleanErr != nil {
			slog.WarnContext(ctx, "merging the text index's pending list", "err", cleanErr)
		}
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
	// After drafting, so that a publication uploaded this pass can already be
	// matched by the description drafted for it.
	if citeErr := ix.readReferences(ctx); citeErr != nil && ctx.Err() == nil {
		slog.WarnContext(ctx, "reading reference lists", "err", citeErr)
	}
	if ix.opts.Bibliography != nil {
		if exportErr := ix.opts.Bibliography.Export(ctx); exportErr != nil {
			slog.ErrorContext(ctx, "writing the bibliography file", "err", exportErr)
		}
	}

	sources, chunks, err := ix.store.Stats(ctx)
	if err != nil {
		return err
	}
	// One wide event per pass rather than a line per stage: everything needed to
	// judge the pass is on it, and passes can be compared field by field.
	slog.InfoContext(ctx, "pass complete",
		"books", books,
		"papers", papers,
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
) (updated int, files []string, err error) {
	if root == "" {
		return 0, nil, nil
	}
	files, err = collect(root, ext)
	if err != nil {
		return 0, nil, err
	}

	present := make(map[string]bool, len(files))
	for _, rel := range files {
		present[rel] = true
	}

	var changed atomic.Int64
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
			if ok, err := ix.indexFile(gctx, kind, root, rel, present, parse); err != nil {
				slog.WarnContext(gctx, "skipped", "path", rel, "err", err)
			} else if ok {
				changed.Add(1)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return int(changed.Load()), files, err
	}

	if err := ctx.Err(); err != nil {
		return int(changed.Load()), files, err
	}
	// An empty walk means the mount is missing, not that every file was deleted;
	// pruning on that would wipe the whole index.
	if len(files) == 0 {
		slog.WarnContext(ctx, "no files under root, skipping prune", "kind", kind, "root", root)
		return int(changed.Load()), files, nil
	}
	if _, err := ix.store.Prune(ctx, kind, files); err != nil {
		return int(changed.Load()), files, err
	}
	return int(changed.Load()), files, nil
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
	recognised := false
	// A scan without an OCR layer yields nothing. Re-parsing it next pass costs
	// milliseconds — there is no text to pull — so it is cheaper to forget it
	// than to keep an empty source around.
	if len(chunks) == 0 {
		dropped, err := ix.store.Forget(ctx, rel)
		if err != nil || (kind != "book" && kind != "paper") || ix.opts.OCR == nil {
			if dropped {
				slog.WarnContext(ctx, "no text, dropped (a scan without OCR?)", "path", rel)
			}
			return false, err
		}
		chunks, err = ix.scan(ctx, kind, path, rel, hash)
		if err != nil || len(chunks) == 0 {
			return false, err
		}
		recognised = true
	}
	for i := range chunks {
		chunks[i].Lang = lang.Detect(chunks[i].Body)
	}

	src := corpus.Source{Kind: kind, Path: rel, Title: title, Hash: hash, Recognised: recognised}
	return true, ix.store.Replace(ctx, src, chunks)
}

// pdfRoot is the library directory a PDF of this kind is relative to. Books and
// publications are separate shelves, and a scan's path is stored relative to its
// own.
func (ix *Indexer) pdfRoot(kind string) string {
	if kind == "paper" {
		return ix.opts.Papers
	}
	return ix.opts.Books
}

// scan is a PDF with no text layer: the pages recognised from it once they
// all are, and until then no chunks and a place in the recognition queue.
func (ix *Indexer) scan(ctx context.Context, kind, path, rel, hash string) ([]corpus.Chunk, error) {
	pages, err := ix.opts.OCR.Pages(ctx, path)
	if err != nil {
		return nil, err
	}
	if text, ok := ix.opts.OCR.Cached(hash, pages); ok {
		if kind == "paper" {
			return ix.opts.BookSplitter.PaperPages(text), nil
		}
		return ix.opts.BookSplitter.Pages(text), nil
	}
	return nil, ix.store.MarkScan(ctx, corpus.Scan{Kind: kind, Hash: hash, Path: rel, Pages: pages})
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
		// Recognition goes last: it takes hours, and a scan's text is worth
		// less than the vectors of books that already have text. A book done
		// or given way to a reindex request starts the next pass at once, which
		// indexes it.
		if ix.recognise(ctx, wake) {
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

// recognise reads the next scan in the queue, and reports whether the loop
// should go straight on to a pass: the book is done, or wake asked for one.
// Pages are saved as they are read, so an interrupted book loses at most the
// pages in flight.
func (ix *Indexer) recognise(ctx context.Context, wake <-chan struct{}) bool {
	if ix.opts.OCR == nil {
		return false
	}
	scan, found, err := ix.store.NextScan(ctx)
	if err != nil {
		if ctx.Err() == nil {
			slog.ErrorContext(ctx, "finding a scan to recognise", "err", err)
		}
		return false
	}
	if !found {
		return false
	}

	stop, finished := make(chan struct{}), make(chan struct{})
	woken := make(chan bool, 1)
	go func() {
		select {
		case _, ok := <-wake:
			// A closed channel is a lost listener, not a request.
			if ok {
				close(stop)
			}
			woken <- ok
		case <-finished:
			woken <- false
		}
	}()

	start := time.Now()
	slog.InfoContext(ctx, "recognising", "path", scan.Path, "pages", scan.Pages, "recognised", scan.Recognised)
	complete, err := ix.opts.OCR.Recognize(ctx, scan.Hash, filepath.Join(ix.pdfRoot(scan.Kind), scan.Path), scan.Pages, stop,
		func(done int) {
			if progressErr := ix.store.ScanProgress(ctx, scan.Hash, done); progressErr != nil && ctx.Err() == nil {
				slog.WarnContext(ctx, "recording recognition progress", "path", scan.Path, "err", progressErr)
			}
		})
	close(finished)
	interrupted := <-woken

	switch {
	case ctx.Err() != nil:
		return false
	case err != nil:
		slog.ErrorContext(ctx, "recognition failed", "path", scan.Path, "err", err)
		if failErr := ix.store.ScanFailed(ctx, scan.Hash, err.Error()); failErr != nil {
			slog.ErrorContext(ctx, "recording a failed recognition", "path", scan.Path, "err", failErr)
		}
		// The next scan in line should not wait an interval for this one.
		return true
	}
	slog.InfoContext(ctx, "recognition stopped",
		"path", scan.Path, "complete", complete, "interrupted", interrupted,
		"took", time.Since(start).Round(time.Second).String())
	return complete || interrupted
}

// readReferences reads the list of references out of every publication that
// has not been read yet, and then points the entries at the works the library
// already holds.
//
// It is a pass of its own, not part of indexing a file: a reference list is
// filed under the paper's content hash rather than its source row, so it
// survives a re-index that rewrites every chunk, and reading one is worth doing
// once per file rather than once per pass. A failure here costs the list, not
// the pass.
func (ix *Indexer) readReferences(ctx context.Context) error {
	papers, err := ix.store.UnparsedPapers(ctx, extract.ReferenceParser)
	if err != nil {
		return err
	}
	var entries int
	for _, p := range papers {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		pages, readErr := ix.paperPages(ctx, p)
		if readErr != nil {
			slog.WarnContext(ctx, "reading a publication for its references", "path", p.Path, "err", readErr)
			continue
		}
		_, raw := extract.Bibliography(pages)
		citations := make([]corpus.Citation, 0, len(raw))
		for i, line := range raw {
			c := cite.ParseCitation(line)
			c.Ord = i + 1
			citations = append(citations, c)
		}
		if saveErr := ix.store.SaveCitations(ctx, p.Hash, extract.ReferenceParser, citations); saveErr != nil {
			return saveErr
		}
		entries += len(citations)
	}
	if len(papers) > 0 {
		slog.InfoContext(ctx, "read reference lists", "papers", len(papers), "citations", entries)
	}

	resolved, err := ix.store.ResolveCitations(ctx)
	if err != nil {
		return err
	}
	if resolved > 0 {
		slog.InfoContext(ctx, "citations matched to the library", "citations", resolved)
	}
	return nil
}

// paperPages is a publication's text: from the PDF itself, or from the OCR
// cache when the paper is a scan and has no text layer of its own.
func (ix *Indexer) paperPages(ctx context.Context, p corpus.Unparsed) ([]string, error) {
	path := filepath.Join(ix.opts.Papers, p.Path)
	if !p.Recognised || ix.opts.OCR == nil {
		return ix.opts.PaperText(ctx, path)
	}
	count, err := ix.opts.OCR.Pages(ctx, path)
	if err != nil {
		return nil, err
	}
	pages, ok := ix.opts.OCR.Cached(p.Hash, count)
	if !ok {
		return nil, nil
	}
	return pages, nil
}

// draft files a description for every book, publication and manual that has
// none: title, PDF author, ISBN or DOI for a book; title, version, address and
// publisher for a manual. They are drafts, to be checked and completed by hand.
func (ix *Indexer) draft(ctx context.Context, manuals []string) error {
	books, err := ix.store.UndescribedBooks(ctx)
	if err != nil {
		return err
	}
	for _, b := range books {
		author := extract.PDFAuthor(ctx, filepath.Join(ix.pdfRoot(b.Kind), b.Path))
		draft := cite.BookDraft(b.Title, author, b.Head, b.Tail)
		if b.Kind == "paper" {
			draft = cite.PaperDraft(b.Title, author, b.Head)
		}
		if err := ix.store.EnsureDraft(ctx, b.Hash, draft); err != nil {
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

// unavailable is how an embedder marks a failure that says nothing about the
// input — no connection, a busy or restarting ollama — as opposed to a text the
// model refuses.
type unavailable interface{ Unavailable() bool }

func isUnavailable(err error) bool {
	var u unavailable
	return errors.As(err, &u) && u.Unavailable()
}

// embedBatch embeds and saves one batch, and reports how many vectors it saved.
//
// The attempt is recorded before the call, so a crash or a timeout counts too;
// otherwise a text that kills the process is retried forever. Two failures are
// not the texts' fault and are handled apart:
//   - the embedder is unavailable: the attempt is taken back and the pass
//     stops, since three passes with ollama off would quarantine good texts;
//   - the model refuses the batch: it is retried text by text, so only the
//     refused text is counted and records why — the invalid message channel's
//     rule that the error travels with the message (EIP, с. 144) — and the rest
//     of its batch still gets vectors.
func (ix *Indexer) embedBatch(ctx context.Context, keys, bodies []string) (int, error) {
	if err := ix.store.CountAttempt(ctx, keys); err != nil {
		return 0, err
	}
	vectors, err := ix.embedder.Embed(ctx, bodies)
	if err == nil {
		return len(keys), ix.store.SaveEmbeddings(ctx, keys, vectors)
	}
	if ctx.Err() != nil {
		return 0, err
	}
	if isUnavailable(err) {
		return 0, errors.Join(err, ix.store.UncountAttempt(ctx, keys))
	}
	if len(keys) == 1 {
		slog.WarnContext(ctx, "embedder refused a text", "key", keys[0], "err", err)
		return 0, ix.store.RecordFailure(ctx, keys, err.Error())
	}
	if err := ix.store.UncountAttempt(ctx, keys); err != nil {
		return 0, err
	}
	saved := 0
	for i := range keys {
		n, err := ix.embedBatch(ctx, keys[i:i+1], bodies[i:i+1])
		saved += n
		if err != nil {
			return saved, err
		}
	}
	return saved, nil
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

		saved, err := ix.embedBatch(ctx, keys, bodies)
		if err != nil {
			return false, err
		}

		done += saved
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
	case "book", "paper":
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
