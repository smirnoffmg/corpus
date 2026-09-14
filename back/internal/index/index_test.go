package index_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smirnoffmg/corpus/internal/corpus"
	"github.com/smirnoffmg/corpus/internal/index"
)

// recordingStore is written to from every extraction worker at once, so its own
// locking has to be sound; otherwise the race detector would report the test
// rather than the code under test.
type recordingStore struct {
	mu        sync.Mutex
	replaced  map[string]int
	sources   map[string]corpus.Source
	chunks    map[string][]corpus.Chunk
	titles    map[string]string
	pruned    map[string][]string
	indexed   map[string]string // path -> hash, as a real store would remember
	byHash    map[string]string // hash -> path
	renamed   []string
	forgotten []string
	unchanged bool
	pending   []corpus.Pending
	attempted []int64
	embedded  []int64
	stats     int
	drafts    map[string]corpus.CSL
	onStats   func(calls int) // called with the lock held, once per finished Index
}

func newStore() *recordingStore {
	return &recordingStore{
		replaced: map[string]int{},
		sources:  map[string]corpus.Source{},
		drafts:   map[string]corpus.CSL{},
		chunks:   map[string][]corpus.Chunk{},
		titles:   map[string]string{},
		pruned:   map[string][]string{},
		indexed:  map[string]string{},
		byHash:   map[string]string{},
	}
}

func (s *recordingStore) Unchanged(_ context.Context, path, hash string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.unchanged || s.indexed[path] == hash, nil
}

func (s *recordingStore) PathByHash(_ context.Context, _, hash string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, ok := s.byHash[hash]
	return path, ok, nil
}

func (s *recordingStore) Rename(_ context.Context, oldPath, newPath, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.renamed = append(s.renamed, oldPath+" -> "+newPath)
	hash := s.indexed[oldPath]
	delete(s.indexed, oldPath)
	s.indexed[newPath] = hash
	s.byHash[hash] = newPath
	return nil
}

func (s *recordingStore) Replace(_ context.Context, src corpus.Source, chunks []corpus.Chunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replaced[src.Path] = len(chunks)
	s.sources[src.Path] = src
	s.chunks[src.Path] = chunks
	s.indexed[src.Path] = src.Hash
	s.byHash[src.Hash] = src.Path
	return nil
}

func (s *recordingStore) SetTitle(_ context.Context, path, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.titles[path] = title
	return nil
}

func (s *recordingStore) Forget(_ context.Context, path string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.forgotten = append(s.forgotten, path)
	_, existed := s.indexed[path]
	delete(s.indexed, path)
	return existed, nil
}

func (s *recordingStore) Prune(_ context.Context, kind string, seen []string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruned[kind] = seen
	return 0, nil
}

func (s *recordingStore) Stats(context.Context) (int64, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats++
	if s.onStats != nil {
		s.onStats(s.stats)
	}
	return 0, 0, nil
}

func (s *recordingStore) MissingEmbeddings(context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return int64(len(s.pending)), nil
}

func (s *recordingStore) Quarantined(context.Context) (int64, error) { return 0, nil }

// PendingEmbeddings drains the queue the way the real one does: a batch at a
// time, and nothing left once every chunk has a vector.
func (s *recordingStore) PendingEmbeddings(_ context.Context, limit int) ([]corpus.Pending, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) < limit {
		limit = len(s.pending)
	}
	batch := s.pending[:limit]
	s.pending = s.pending[limit:]
	return batch, nil
}

func (s *recordingStore) CountAttempt(_ context.Context, ids []int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempted = append(s.attempted, ids...)
	return nil
}

func (s *recordingStore) SaveEmbeddings(_ context.Context, ids []int64, _ [][]float32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.embedded = append(s.embedded, ids...)
	return nil
}

// UndescribedBooks reports every replaced book without a draft, with its
// chunks as the text a real store would return.
func (s *recordingStore) UndescribedBooks(context.Context) ([]corpus.Undescribed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []corpus.Undescribed
	for path, src := range s.sources {
		if src.Kind != "book" {
			continue
		}
		if _, ok := s.drafts[src.Hash]; ok {
			continue
		}
		var text strings.Builder
		for _, c := range s.chunks[path] {
			text.WriteString(c.Body + "\n")
		}
		out = append(out, corpus.Undescribed{Path: path, Hash: src.Hash, Title: src.Title, Text: text.String()})
	}
	return out, nil
}

func (s *recordingStore) EnsureDraft(_ context.Context, key string, csl corpus.CSL) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.drafts[key]; !ok {
		s.drafts[key] = csl
	}
	return nil
}

func (s *recordingStore) counts() (replaced int, pruned []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.replaced), s.pruned["vault"]
}

type nopEmbedder struct{}

func (nopEmbedder) Embed(context.Context, []string) ([][]float32, error) { return nil, nil }

// countingEmbedder answers with one vector per body, and can be told to fail.
type countingEmbedder struct {
	mu     sync.Mutex
	calls  int
	sizes  []int
	broken error
	onCall func(calls int)
}

func (e *countingEmbedder) Embed(_ context.Context, bodies []string) ([][]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	e.sizes = append(e.sizes, len(bodies))
	if e.onCall != nil {
		e.onCall(e.calls)
	}
	if e.broken != nil {
		return nil, e.broken
	}
	return make([][]float32, len(bodies)), nil
}

// vault writes n notes and returns the directory holding them, plus an empty
// directory to stand in for the book library.
func vault(t *testing.T, n int) (notes, books string) {
	t.Helper()
	notes, books = t.TempDir(), t.TempDir()
	for i := range n {
		body := fmt.Sprintf("---\ntags: test\n---\n\n# Заметка %d\n\nТекст заметки.\n\n## Раздел\n\nЕщё текст.\n", i)
		path := filepath.Join(notes, fmt.Sprintf("note-%03d.md", i))
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return notes, books
}

func TestIndexWalksEveryFileInParallel(t *testing.T) {
	notes, books := vault(t, 64)
	store := newStore()

	ix := index.New(store, nopEmbedder{}, index.Options{Books: books, Vault: notes, Parallel: 8})
	if err := ix.Index(context.Background()); err != nil {
		t.Fatalf("index: %v", err)
	}

	replaced, pruned := store.counts()
	if replaced != 64 {
		t.Errorf("indexed %d notes, want 64", replaced)
	}
	if len(pruned) != 64 {
		t.Errorf("prune saw %d paths, want 64", len(pruned))
	}
}

func TestUnchangedFilesStillGetTheirTitleRefreshed(t *testing.T) {
	notes, books := vault(t, 4)
	store := newStore()
	store.unchanged = true

	ix := index.New(store, nopEmbedder{}, index.Options{Books: books, Vault: notes, Parallel: 2})
	if err := ix.Index(context.Background()); err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.titles) != 4 {
		t.Errorf("refreshed %d titles, want 4 — recognising a title must not need a re-extract", len(store.titles))
	}
}

func TestIndexSkipsFilesWhoseHashIsUnchanged(t *testing.T) {
	notes, books := vault(t, 8)
	store := newStore()
	store.unchanged = true

	ix := index.New(store, nopEmbedder{}, index.Options{Books: books, Vault: notes, Parallel: 4})
	if err := ix.Index(context.Background()); err != nil {
		t.Fatal(err)
	}

	if replaced, _ := store.counts(); replaced != 0 {
		t.Errorf("rewrote %d unchanged notes, want 0", replaced)
	}
}

func TestOneUnreadableFileDoesNotStopThePass(t *testing.T) {
	notes, books := vault(t, 16)
	broken := filepath.Join(notes, "note-000.md")
	if err := os.Chmod(broken, 0o000); err != nil {
		t.Skipf("cannot make a file unreadable here: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(broken, 0o600) })

	store := newStore()
	ix := index.New(store, nopEmbedder{}, index.Options{Books: books, Vault: notes, Parallel: 8})
	if err := ix.Index(context.Background()); err != nil {
		t.Fatalf("one bad file failed the whole pass: %v", err)
	}

	if replaced, _ := store.counts(); replaced != 15 {
		t.Errorf("indexed %d notes, want the other 15", replaced)
	}
}

func TestEmptyDirectoryDoesNotPrune(t *testing.T) {
	empty := t.TempDir()
	store := newStore()

	ix := index.New(store, nopEmbedder{}, index.Options{Books: empty, Vault: empty})
	if err := ix.Index(context.Background()); err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.pruned) != 0 {
		// A missing mount looks exactly like "every file was deleted".
		t.Errorf("pruned %v on an empty directory", store.pruned)
	}
}

func TestRenamingAFileIsNotReindexing(t *testing.T) {
	notes, books := vault(t, 4)
	store := newStore()
	opts := index.Options{Books: books, Vault: notes, Parallel: 4}

	ix := index.New(store, nopEmbedder{}, opts)
	if err := ix.Index(context.Background()); err != nil {
		t.Fatal(err)
	}
	if replaced, _ := store.counts(); replaced != 4 {
		t.Fatalf("first pass indexed %d notes, want 4", replaced)
	}

	if err := os.Rename(filepath.Join(notes, "note-000.md"), filepath.Join(notes, "Настоящее название.md")); err != nil {
		t.Fatal(err)
	}

	if err := index.New(store, nopEmbedder{}, opts).Index(context.Background()); err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.renamed) != 1 {
		t.Errorf("recorded %v renames, want 1 — a rename must not re-extract", store.renamed)
	}
	if _, reindexed := store.replaced["Настоящее название.md"]; reindexed {
		t.Error("the renamed file was extracted again, discarding its embeddings")
	}
}

func TestAFileWithNoTextIsNotIndexed(t *testing.T) {
	notes, books := vault(t, 3)
	// A note that parses to nothing stands in for a scan without an OCR layer.
	if err := os.WriteFile(filepath.Join(notes, "empty.md"), []byte("\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	store := newStore()
	ix := index.New(store, nopEmbedder{}, index.Options{Books: books, Vault: notes, Parallel: 4})
	if err := ix.Index(context.Background()); err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if _, indexed := store.replaced["empty.md"]; indexed {
		t.Error("a file with no text was stored as a source")
	}
	if len(store.forgotten) != 1 || store.forgotten[0] != "empty.md" {
		t.Errorf("forgotten = %v, want [empty.md]", store.forgotten)
	}
}

func queued(n int) []corpus.Pending {
	out := make([]corpus.Pending, n)
	for i := range out {
		out[i] = corpus.Pending{ID: int64(i + 1), Body: fmt.Sprintf("кусок %d", i+1)}
	}
	return out
}

func TestEmbedDrainsTheQueueInBatches(t *testing.T) {
	store := newStore()
	store.pending = queued(5)
	embedder := &countingEmbedder{}

	ix := index.New(store, embedder, index.Options{Batch: 2})
	if err := ix.Embed(t.Context()); err != nil {
		t.Fatalf("embed: %v", err)
	}

	if got, want := len(store.embedded), 5; got != want {
		t.Errorf("embedded %d chunks, want %d", got, want)
	}
	if got := embedder.sizes; len(got) != 3 || got[0] != 2 || got[2] != 1 {
		t.Errorf("batch sizes = %v, want [2 2 1]", got)
	}
}

// The attempt is recorded before the embedder is called, so a chunk that kills
// the process still counts against its retries instead of being tried forever.
func TestEmbedCountsTheAttemptBeforeTheCallThatMayFail(t *testing.T) {
	store := newStore()
	store.pending = queued(2)
	embedder := &countingEmbedder{broken: errors.New("ollama is down")}

	ix := index.New(store, embedder, index.Options{Batch: 2})
	err := ix.Embed(t.Context())

	if err == nil {
		t.Fatal("a failing embedder must stop the pass")
	}
	if len(store.attempted) != 2 {
		t.Errorf("attempts recorded = %d, want 2", len(store.attempted))
	}
	if len(store.embedded) != 0 {
		t.Errorf("nothing should have been saved, got %d", len(store.embedded))
	}
}

func TestEmbedWithAnEmptyQueueDoesNotCallTheEmbedder(t *testing.T) {
	store := newStore()
	embedder := &countingEmbedder{}

	ix := index.New(store, embedder, index.Options{Batch: 4})
	if err := ix.Embed(t.Context()); err != nil {
		t.Fatalf("embed: %v", err)
	}
	if embedder.calls != 0 {
		t.Errorf("embedder called %d times on an empty queue", embedder.calls)
	}
}

// Pass is Index and then Embed: the text index must be complete before a slow
// or absent embedder gets a chance to hold the run up.
func TestPassIndexesAndThenEmbeds(t *testing.T) {
	notes, books := vault(t, 3)
	store := newStore()
	store.pending = queued(3)
	embedder := &countingEmbedder{}

	ix := index.New(store, embedder, index.Options{Books: books, Vault: notes, Parallel: 2, Batch: 8})
	if err := ix.Pass(t.Context()); err != nil {
		t.Fatalf("pass: %v", err)
	}

	replaced, _ := store.counts()
	if replaced != 3 {
		t.Errorf("indexed %d notes, want 3", replaced)
	}
	if len(store.embedded) != 3 {
		t.Errorf("embedded %d chunks, want 3", len(store.embedded))
	}
}

// Templater sources are code, not knowledge, and .obsidian is the vault's own
// bookkeeping: both would otherwise be indexed as notes and answer searches.
func TestWalkSkipsTemplatesAndVaultInternals(t *testing.T) {
	notes, books := vault(t, 1)
	for _, dir := range []string{"99 - templates", ".obsidian", ".git"} {
		if err := os.MkdirAll(filepath.Join(notes, dir), 0o750); err != nil {
			t.Fatal(err)
		}
		body := "# Шаблон\n\nТекст.\n"
		if err := os.WriteFile(filepath.Join(notes, dir, "skipped.md"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	store := newStore()
	ix := index.New(store, nopEmbedder{}, index.Options{Books: books, Vault: notes, Parallel: 2})
	if err := ix.Index(t.Context()); err != nil {
		t.Fatalf("index: %v", err)
	}

	replaced, seen := store.counts()
	if replaced != 1 {
		t.Errorf("indexed %d notes, want only the real one", replaced)
	}
	for _, path := range seen {
		if strings.Contains(path, "templates") || strings.HasPrefix(path, ".") {
			t.Errorf("%s should not have been walked", path)
		}
	}
}

// manual lays out a scikit-learn-shaped documentation tree: one real page, and
// the build by-products Sphinx leaves next to it.
func manual(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	page := `<html><head><title>1.4. Support Vector Machines &#8212; scikit-learn documentation</title></head><body>
<article><section id="svm"><h1>1.4. Support Vector Machines</h1><p>Intro.</p>
<section id="classification"><h2>1.4.1. Classification</h2><p>SVC classifies.</p></section></section></article></body></html>`
	files := map[string]string{
		"scikit-learn/modules/svm.html":             page,
		"scikit-learn/genindex.html":                page,
		"scikit-learn/search.html":                  page,
		"scikit-learn/py-modindex.html":             page,
		"scikit-learn/_modules/sklearn/svm.html":    page,
		"scikit-learn/_static/theme.html":           page,
		"scikit-learn/_sources/modules/svm.rst.txt": "SVM\n===\n",
		".upload-tmp/0123abcd/index.html":           page,
	}
	for rel, body := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestDocsAreIndexedBySectionAndNamedAfterTheirManual(t *testing.T) {
	notes, books := vault(t, 1)
	store := newStore()

	ix := index.New(store, nopEmbedder{}, index.Options{Books: books, Vault: notes, Docs: manual(t)})
	if err := ix.Index(context.Background()); err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	const page = "scikit-learn/modules/svm.html"
	src, ok := store.sources[page]
	if !ok {
		t.Fatalf("page not indexed; indexed %v", store.sources)
	}
	if src.Kind != "docs" {
		t.Errorf("kind = %q, want docs", src.Kind)
	}
	if want := "scikit-learn · 1.4. Support Vector Machines"; src.Title != want {
		t.Errorf("title = %q, want %q", src.Title, want)
	}
	chunks := store.chunks[page]
	if len(chunks) != 2 || chunks[1].Anchor != "classification" || chunks[1].Lang != "english" {
		t.Errorf("chunks = %+v, want two sections, the second anchored at classification", chunks)
	}
	if got := store.pruned["docs"]; len(got) != 1 || got[0] != page {
		t.Errorf("prune saw %v, want only the page", got)
	}
}

func TestSphinxByProductsAreNotSources(t *testing.T) {
	store := newStore()
	empty := t.TempDir()

	ix := index.New(store, nopEmbedder{}, index.Options{Books: empty, Vault: empty, Docs: manual(t)})
	if err := ix.Index(context.Background()); err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	for path := range store.sources {
		if strings.Contains(path, "/_") || strings.Contains(path, "index.html") || strings.HasSuffix(path, "search.html") ||
			strings.HasPrefix(path, ".upload-tmp") {
			t.Errorf("indexed %s: an index, search page, build directory or half-unpacked upload is not a source", path)
		}
	}
}

func TestNoDocsRootSkipsTheKind(t *testing.T) {
	notes, books := vault(t, 1)
	store := newStore()

	ix := index.New(store, nopEmbedder{}, index.Options{Books: books, Vault: notes})
	if err := ix.Index(context.Background()); err != nil {
		t.Fatalf("an unset docs root failed the pass: %v", err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.pruned["docs"]; ok {
		t.Error("pruned docs although no docs root was given")
	}
}

func listenOn(wake <-chan struct{}) func(context.Context) (<-chan struct{}, error) {
	return func(context.Context) (<-chan struct{}, error) { return wake, nil }
}

// runFor runs the loop and fails the test if it does not stop by itself — every
// test below cancels it from inside, once it has seen what it waits for.
func runFor(t *testing.T, ix *index.Indexer, ctx context.Context, interval time.Duration, listen func(context.Context) (<-chan struct{}, error)) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- ix.Run(ctx, interval, listen) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the loop did not reach the expected pass")
	}
}

// Embedding a whole manual takes hours; an upload made meanwhile must not wait
// for the queue to drain before its text is searchable.
func TestAReindexRequestInterruptsEmbeddingBetweenBatches(t *testing.T) {
	notes, books := vault(t, 1)
	store := newStore()
	store.pending = queued(10)
	wake := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	remaining := -1
	store.onStats = func(calls int) {
		if calls == 2 {
			remaining = len(store.pending)
			cancel()
		}
	}
	embedder := &countingEmbedder{onCall: func(calls int) {
		if calls == 1 {
			wake <- struct{}{}
		}
	}}
	ix := index.New(store, embedder, index.Options{Books: books, Vault: notes, Batch: 2})

	runFor(t, ix, ctx, time.Hour, listenOn(wake))

	store.mu.Lock()
	defer store.mu.Unlock()
	if remaining != 8 {
		t.Errorf("the second pass started with %d chunks queued, want 8 — right after the batch in flight", remaining)
	}
}

func TestAReindexRequestWhileIdleDoesNotWaitForTheInterval(t *testing.T) {
	notes, books := vault(t, 1)
	store := newStore()
	wake := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	store.onStats = func(calls int) {
		switch calls {
		case 1:
			wake <- struct{}{}
		case 2:
			cancel()
		}
	}
	ix := index.New(store, nopEmbedder{}, index.Options{Books: books, Vault: notes})

	runFor(t, ix, ctx, time.Hour, listenOn(wake))
}

// A listener whose connection dropped closes its channel. A closed channel is
// always ready, so a loop that kept selecting on it would index flat out.
func TestALostListenerFallsBackToTheInterval(t *testing.T) {
	notes, books := vault(t, 1)
	store := newStore()
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()

	var mu sync.Mutex
	listens := 0
	listen := func(context.Context) (<-chan struct{}, error) {
		mu.Lock()
		defer mu.Unlock()
		listens++
		if listens == 1 {
			closed := make(chan struct{})
			close(closed)
			return closed, nil
		}
		return nil, errors.New("database restarting")
	}
	ix := index.New(store, nopEmbedder{}, index.Options{Books: books, Vault: notes})

	runFor(t, ix, ctx, 50*time.Millisecond, listen)

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.stats > 12 {
		t.Errorf("%d passes in 300ms on a 50ms interval: the loop spun on the closed channel", store.stats)
	}
	mu.Lock()
	defer mu.Unlock()
	if listens < 2 {
		t.Errorf("listened %d times, want the listener re-established", listens)
	}
}

func TestIndexDraftsADescriptionForEachManual(t *testing.T) {
	notes, books := vault(t, 1)
	store := newStore()
	docs := manual(t)
	if err := os.WriteFile(filepath.Join(docs, "scikit-learn", "index.html"), []byte(`<html><head><title>Home &#8212; scikit-learn 1.9.1 documentation</title><link rel="canonical" href="https://scikit-learn.org/stable/index.html"/></head><body><article><p>x</p></article></body></html>`), 0o600); err != nil {
		t.Fatal(err)
	}

	ix := index.New(store, nopEmbedder{}, index.Options{Books: books, Vault: notes, Docs: docs})
	if err := ix.Index(context.Background()); err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	draft, ok := store.drafts["manual:scikit-learn"]
	if !ok {
		t.Fatalf("no draft for the manual; drafts = %v", store.drafts)
	}
	if draft["version"] != "1.9.1" || draft["URL"] != "https://scikit-learn.org/stable/" {
		t.Errorf("draft = %v", draft)
	}
	if _, ok := store.drafts["manual:.upload-tmp"]; ok {
		t.Error("an upload in progress is not a manual")
	}
}
