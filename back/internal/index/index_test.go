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
}

func newStore() *recordingStore {
	return &recordingStore{
		replaced: map[string]int{},
		sources:  map[string]corpus.Source{},
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

func (s *recordingStore) Stats(context.Context) (int64, int64, error) { return 0, 0, nil }

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
}

func (e *countingEmbedder) Embed(_ context.Context, bodies []string) ([][]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	e.sizes = append(e.sizes, len(bodies))
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
		if strings.Contains(path, "/_") || strings.Contains(path, "index.html") || strings.HasSuffix(path, "search.html") {
			t.Errorf("indexed %s: an index, search page or build directory is not a source", path)
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
