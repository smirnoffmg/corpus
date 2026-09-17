package index_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smirnoffmg/corpus/internal/corpus"
	"github.com/smirnoffmg/corpus/internal/extract"
	"github.com/smirnoffmg/corpus/internal/index"
)

// recordingStore is written to from every extraction worker at once, so its own
// locking has to be sound; otherwise the race detector would report the test
// rather than the code under test.
type recordingStore struct {
	mu         sync.Mutex
	replaced   map[string]int
	sources    map[string]corpus.Source
	chunks     map[string][]corpus.Chunk
	titles     map[string]string
	pruned     map[string][]string
	indexed    map[string]string // path -> hash, as a real store would remember
	byHash     map[string]string // hash -> path
	renamed    []string
	forgotten  []string
	unchanged  bool
	pending    []corpus.Pending
	attempted  []string
	attempts   map[string]int
	failures   map[string]string
	embedded   []string
	prunes     int
	cleans     int
	scans      map[string]corpus.Scan // by hash
	scanSeen   []string
	scanPruned string
	stats      int
	drafts     map[string]corpus.CSL
	citations  map[string][]corpus.Citation
	parsers    map[string]int
	resolves   int
	onStats    func(calls int) // called with the lock held, once per finished Index
}

func newStore() *recordingStore {
	return &recordingStore{
		replaced:  map[string]int{},
		sources:   map[string]corpus.Source{},
		attempts:  map[string]int{},
		scans:     map[string]corpus.Scan{},
		failures:  map[string]string{},
		drafts:    map[string]corpus.CSL{},
		citations: map[string][]corpus.Citation{},
		parsers:   map[string]int{},
		chunks:    map[string][]corpus.Chunk{},
		titles:    map[string]string{},
		pruned:    map[string][]string{},
		indexed:   map[string]string{},
		byHash:    map[string]string{},
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

func (s *recordingStore) CountAttempt(_ context.Context, keys []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempted = append(s.attempted, keys...)
	for _, k := range keys {
		s.attempts[k]++
	}
	return nil
}

func (s *recordingStore) UncountAttempt(_ context.Context, keys []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range keys {
		s.attempts[k]--
	}
	return nil
}

func (s *recordingStore) RecordFailure(_ context.Context, keys []string, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range keys {
		s.failures[k] = reason
	}
	return nil
}

func (s *recordingStore) SaveEmbeddings(_ context.Context, keys []string, _ [][]float32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.embedded = append(s.embedded, keys...)
	return nil
}

func (s *recordingStore) MarkScan(_ context.Context, scan corpus.Scan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if have, ok := s.scans[scan.Hash]; ok {
		scan.Recognised = have.Recognised
	}
	s.scans[scan.Hash] = scan
	return nil
}

func (s *recordingStore) NextScan(context.Context) (corpus.Scan, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var next corpus.Scan
	found := false
	for _, sc := range s.scans {
		if _, indexed := s.sources[sc.Path]; indexed {
			continue
		}
		if !found || sc.Path < next.Path {
			next, found = sc, true
		}
	}
	return next, found, nil
}

func (s *recordingStore) ScanProgress(_ context.Context, hash string, recognised int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc := s.scans[hash]
	sc.Recognised = recognised
	s.scans[hash] = sc
	return nil
}

func (s *recordingStore) ScanFailed(_ context.Context, hash, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.scans, hash)
	return nil
}

func (s *recordingStore) PruneScans(_ context.Context, kind string, seen []string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scanSeen, s.scanPruned = seen, kind
	return 0, nil
}

func (s *recordingStore) CleanTextIndex(context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleans++
	return 0, nil
}

func (s *recordingStore) PruneEmbeddings(context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prunes++
	return 0, nil
}

// UndescribedBooks reports every replaced book without a draft, with its
// chunks as the text a real store would return.
func (s *recordingStore) UndescribedBooks(context.Context) ([]corpus.Undescribed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []corpus.Undescribed
	for path, src := range s.sources {
		if src.Kind != "book" && src.Kind != "paper" {
			continue
		}
		if _, ok := s.drafts[src.Hash]; ok {
			continue
		}
		var text strings.Builder
		for _, c := range s.chunks[path] {
			text.WriteString(c.Body + "\n")
		}
		out = append(out, corpus.Undescribed{Kind: src.Kind, Path: path, Hash: src.Hash, Title: src.Title, Head: text.String()})
	}
	return out, nil
}

func (s *recordingStore) UnparsedPapers(_ context.Context, parser int) ([]corpus.Unparsed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []corpus.Unparsed
	for path, src := range s.sources {
		if src.Kind != "paper" {
			continue
		}
		if _, ok := s.citations[src.Hash]; ok && s.parsers[src.Hash] >= parser {
			continue
		}
		out = append(out, corpus.Unparsed{Path: path, Hash: src.Hash, Recognised: src.Recognised})
	}
	return out, nil
}

func (s *recordingStore) SaveCitations(_ context.Context, paper string, parser int, cs []corpus.Citation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.citations[paper] = cs
	s.parsers[paper] = parser
	return nil
}

func (s *recordingStore) ResolveCitations(context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resolves++
	return 0, nil
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
	mu          sync.Mutex
	calls       int
	sizes       []int
	broken      error
	onCall      func(calls int)
	poison      string // a body the model refuses, failing any batch holding it
	unavailable bool   // ollama away: no connection
}

// unavailableError is how the embed client marks a failure that says nothing
// about the input: ollama is off, restarting or busy.
type unavailableError struct{}

func (unavailableError) Error() string     { return "connection refused" }
func (unavailableError) Unavailable() bool { return true }

func (e *countingEmbedder) Embed(_ context.Context, bodies []string) ([][]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	e.sizes = append(e.sizes, len(bodies))
	if e.onCall != nil {
		e.onCall(e.calls)
	}
	if e.unavailable {
		return nil, unavailableError{}
	}
	for _, b := range bodies {
		if e.poison != "" && b == e.poison {
			return nil, errors.New("ollama 400 Bad Request: unsupported input")
		}
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
		out[i] = corpus.Pending{Key: fmt.Sprintf("hash-%d", i+1), Body: fmt.Sprintf("кусок %d", i+1)}
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
// The attempt is recorded before the embedder is called, so a text that kills
// the process still counts against its retries instead of being tried forever.
func TestEmbedCountsTheAttemptBeforeTheCall(t *testing.T) {
	store := newStore()
	store.pending = queued(2)
	var during int
	embedder := &countingEmbedder{onCall: func(int) { during = store.attempts["hash-1"] }}

	ix := index.New(store, embedder, index.Options{Batch: 2})
	if err := ix.Embed(t.Context()); err != nil {
		t.Fatal(err)
	}
	if during != 1 {
		t.Errorf("attempts while the call ran = %d, want 1", during)
	}
}

// ollama off or restarting says nothing about the texts. Counting those tries
// quarantined perfectly good texts after three passes of a laptop on battery.
func TestAnUnavailableEmbedderStopsThePassWithoutSpendingAttempts(t *testing.T) {
	store := newStore()
	store.pending = queued(2)
	ix := index.New(store, &countingEmbedder{unavailable: true}, index.Options{Batch: 2})

	if err := ix.Embed(t.Context()); err == nil {
		t.Fatal("an unavailable embedder must stop the pass")
	}
	for key, n := range store.attempts {
		if n != 0 {
			t.Errorf("%s: %d attempts left counted, want 0", key, n)
		}
	}
	if len(store.embedded) != 0 {
		t.Errorf("saved %d vectors without an embedder", len(store.embedded))
	}
}

// One text the model refuses failed its whole batch, and three passes put the
// batch in quarantine. The batch is retried text by text: the rest get their
// vectors, and only the refused text is counted and records why.
func TestOneRefusedTextDoesNotTakeItsBatchDown(t *testing.T) {
	store := newStore()
	store.pending = queued(4)
	embedder := &countingEmbedder{poison: "кусок 3"}
	ix := index.New(store, embedder, index.Options{Batch: 4})

	if err := ix.Embed(t.Context()); err != nil {
		t.Fatalf("a refused text failed the pass: %v", err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.embedded) != 3 {
		t.Errorf("embedded %v, want the three texts that are fine", store.embedded)
	}
	for _, key := range []string{"hash-1", "hash-2", "hash-4"} {
		if store.attempts[key] != 1 {
			t.Errorf("%s: %d attempts, want 1", key, store.attempts[key])
		}
	}
	if store.attempts["hash-3"] != 1 {
		t.Errorf("the refused text has %d attempts, want 1", store.attempts["hash-3"])
	}
	if !strings.Contains(store.failures["hash-3"], "400") {
		t.Errorf("failure recorded for the refused text = %q, want the embedder's answer", store.failures["hash-3"])
	}
	if len(store.failures) != 1 {
		t.Errorf("failures recorded for %v, want only the refused text", store.failures)
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

// Vectors are filed by text, so a changed or deleted file leaves vectors behind
// that nothing uses; each pass clears them once the walk is done.
func TestAPassPrunesVectorsNoChunkUses(t *testing.T) {
	notes, books := vault(t, 1)
	store := newStore()

	ix := index.New(store, nopEmbedder{}, index.Options{Books: books, Vault: notes})
	if err := ix.Index(context.Background()); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.prunes != 1 {
		t.Errorf("pruned %d times in a pass, want 1", store.prunes)
	}
}

type fakeBibliography struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeBibliography) Import(context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "import")
	return 0, nil
}

func (f *fakeBibliography) Export(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "export")
	return nil
}

// The file is applied before the pass drafts anything — a description restored
// from it must win over a fresh draft — and written after, so new drafts reach
// the file too.
func TestAPassImportsTheBibliographyFirstAndExportsItLast(t *testing.T) {
	notes, books := vault(t, 1)
	store := newStore()
	bib := &fakeBibliography{}
	store.onStats = func(int) {
		bib.mu.Lock()
		defer bib.mu.Unlock()
		bib.calls = append(bib.calls, "stats")
	}

	ix := index.New(store, nopEmbedder{}, index.Options{Books: books, Vault: notes, Bibliography: bib})
	if err := ix.Index(context.Background()); err != nil {
		t.Fatal(err)
	}
	bib.mu.Lock()
	defer bib.mu.Unlock()
	if strings.Join(bib.calls, ",") != "import,export,stats" {
		t.Errorf("calls = %v, want import, then export after drafting, before the pass ends", bib.calls)
	}
}

func TestAPassThatChangedFilesCleansTheTextIndex(t *testing.T) {
	notes, books := vault(t, 2)
	store := newStore()
	ix := index.New(store, nopEmbedder{}, index.Options{Books: books, Vault: notes})

	if err := ix.Index(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := ix.Index(context.Background()); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.cleans != 1 {
		t.Errorf("cleaned %d times, want once — after the pass that indexed the notes, not the one that found nothing new", store.cleans)
	}
}

// blankPDF writes a one-page PDF with no text on its page: what pdftotext makes
// of a scan.
func blankPDF(t *testing.T, dir, name string) {
	t.Helper()
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext is not installed: the container has it, this machine does not")
	}
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>",
	}
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, o := range objects {
		offsets[i] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	if err := os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// fakeRecognizer stands in for Tesseract: a scan has pages, and is recognised
// once Recognize has run on it.
type fakeRecognizer struct {
	mu         sync.Mutex
	pages      int
	lastPage   string // what the final page reads, when it is not ordinary text
	recognised map[string]bool
	calls      []string
}

func (f *fakeRecognizer) Pages(context.Context, string) (int, error) { return f.pages, nil }

func (f *fakeRecognizer) Cached(hash string, pages int) ([]string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.recognised[hash] {
		return nil, false
	}
	out := make([]string, pages)
	for i := range out {
		out[i] = fmt.Sprintf("Распознанная страница %d. %s", i+1, strings.Repeat("Текст книги со сканированной страницы. ", 6))
	}
	if f.lastPage != "" {
		out[pages-1] = f.lastPage
	}
	return out, true
}

func (f *fakeRecognizer) Recognize(_ context.Context, hash, pdf string, pages int, _ <-chan struct{}, progress func(int)) (bool, error) {
	f.mu.Lock()
	f.calls = append(f.calls, pdf)
	if f.recognised == nil {
		f.recognised = map[string]bool{}
	}
	f.recognised[hash] = true
	f.mu.Unlock()
	progress(pages)
	return true, nil
}

// A scan used to be dropped without a word; now it is recorded, so the library
// can say what it is and how far its recognition got.
func TestAScanIsRecordedRatherThanDropped(t *testing.T) {
	notes, books := vault(t, 1)
	blankPDF(t, books, "scan.pdf")
	store := newStore()
	ix := index.New(store, nopEmbedder{}, index.Options{Books: books, Vault: notes, OCR: &fakeRecognizer{pages: 3}})

	if err := ix.Index(context.Background()); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.scans) != 1 {
		t.Fatalf("scans = %v, want the one scan", store.scans)
	}
	for _, sc := range store.scans {
		if sc.Path != "scan.pdf" || sc.Pages != 3 || sc.Hash == "" {
			t.Errorf("scan = %+v", sc)
		}
	}
	if _, ok := store.sources["scan.pdf"]; ok {
		t.Error("an unrecognised scan became a source with no text")
	}
	if len(store.scanSeen) != 1 || store.scanSeen[0] != "scan.pdf" {
		t.Errorf("scans pruned against %v, want the books the pass walked", store.scanSeen)
	}
}

func TestWithoutOCRAScanIsLeftOutAsBefore(t *testing.T) {
	notes, books := vault(t, 1)
	blankPDF(t, books, "scan.pdf")
	store := newStore()
	if err := index.New(store, nopEmbedder{}, index.Options{Books: books, Vault: notes}).Index(context.Background()); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.scans) != 0 || len(store.sources) != 1 {
		t.Errorf("scans %v, sources %v; want the note alone", store.scans, store.sources)
	}
}

// The loop recognises a queued scan, then indexes the book from what it read:
// cited by page like any book, and marked as recognised.
func TestRunRecognisesAScanAndIndexesItsPages(t *testing.T) {
	notes, books := vault(t, 1)
	blankPDF(t, books, "scan.pdf")
	store := newStore()
	recognizer := &fakeRecognizer{pages: 4}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	store.onStats = func(int) {
		if src, ok := store.sources["scan.pdf"]; ok && src.Recognised {
			cancel()
		}
	}
	ix := index.New(store, nopEmbedder{}, index.Options{Books: books, Vault: notes, OCR: recognizer})

	runFor(t, ix, ctx, time.Hour, nil)

	store.mu.Lock()
	defer store.mu.Unlock()
	chunks := store.chunks["scan.pdf"]
	if len(chunks) != 4 || chunks[0].Page != 1 {
		t.Errorf("chunks = %+v, want the four recognised pages", chunks)
	}
	recognizer.mu.Lock()
	defer recognizer.mu.Unlock()
	if len(recognizer.calls) != 1 || recognizer.calls[0] != filepath.Join(books, "scan.pdf") {
		t.Errorf("recognised %v, want the scan once, by its path in the library", recognizer.calls)
	}
}

// A publication's reference list is read once per file, into entries of its
// own, and matched against the library afterwards.
func TestPapersHaveTheirReferenceListsRead(t *testing.T) {
	notes, books := vault(t, 1)
	store := newStore()
	store.sources["mapreduce.pdf"] = corpus.Source{Kind: "paper", Path: "mapreduce.pdf", Title: "MapReduce", Hash: "pa"}

	pages := []string{
		"Заключение. Мы показали, что модель работает на больших кластерах.",
		"References\n\n" +
			"[1] M. Kleppmann. Designing Data-Intensive Applications. O'Reilly, 2017.\n" +
			"[2] L. Lamport. Time, clocks, and the ordering of events in a distributed\n" +
			"    system. CACM, 1978.\n",
	}
	ix := index.New(store, nopEmbedder{}, index.Options{
		Books: books, Vault: notes,
		PaperText: func(context.Context, string) ([]string, error) { return pages, nil },
	})
	if err := ix.Index(context.Background()); err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	cs := store.citations["pa"]
	if len(cs) != 2 {
		t.Fatalf("read %d citations, want 2: %+v", len(cs), cs)
	}
	if cs[0].Ord != 1 || cs[0].Year != 2017 || cs[0].Title != "Designing Data-Intensive Applications" {
		t.Errorf("citation 1 = %+v", cs[0])
	}
	if cs[1].Ord != 2 || cs[1].Year != 1978 {
		t.Errorf("citation 2 = %+v", cs[1])
	}
	if store.resolves != 1 {
		t.Errorf("resolved %d times, want once a pass", store.resolves)
	}
}

func TestAPaperIsReadForItsReferencesOnlyOnce(t *testing.T) {
	notes, books := vault(t, 1)
	store := newStore()
	store.sources["empty.pdf"] = corpus.Source{Kind: "paper", Path: "empty.pdf", Title: "Empty", Hash: "pe"}

	reads := 0
	ix := index.New(store, nopEmbedder{}, index.Options{
		Books: books, Vault: notes,
		PaperText: func(context.Context, string) ([]string, error) {
			reads++
			return []string{"Статья без списка литературы."}, nil
		},
	})
	for range 2 {
		if err := ix.Index(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	if reads != 1 {
		t.Errorf("read the paper %d times, want 1 — a paper with no references was still read", reads)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.citations["pe"]; !ok {
		t.Error("a paper with no references needs a record saying so, or it is read again every pass")
	}
}

func TestAPaperReadByAnOlderParserIsReadAgain(t *testing.T) {
	notes, books := vault(t, 1)
	store := newStore()
	store.sources["old.pdf"] = corpus.Source{Kind: "paper", Path: "old.pdf", Title: "Old", Hash: "po"}
	store.citations["po"] = []corpus.Citation{{Ord: 1, Raw: "whole list read as one entry"}}
	store.parsers["po"] = extract.ReferenceParser - 1

	ix := index.New(store, nopEmbedder{}, index.Options{
		Books: books, Vault: notes,
		PaperText: func(context.Context, string) ([]string, error) {
			return []string{"References\n[1] A. Author. First. V, 2020.\n[2] B. Author. Second. V, 2021.\n"}, nil
		},
	})
	if err := ix.Index(context.Background()); err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if n := len(store.citations["po"]); n != 2 {
		t.Errorf("citations = %d, want the list read again into 2", n)
	}
}

// A scanned paper is a paper once recognised: read from its own shelf, and with
// its reference list kept out of the text index like any other paper's.
func TestARecognisedPaperLeavesItsReferencesOut(t *testing.T) {
	notes, books := vault(t, 1)
	papers := t.TempDir()
	blankPDF(t, papers, "naur.pdf")
	store := newStore()
	recognizer := &fakeRecognizer{pages: 3, lastPage: "References\n\n" +
		"Brooks, R. E. Studying programmer behaviour experimentally. Comm. ACM 23(4): 207-213, 1980.\n\n" +
		"Ryle, G. The Concept of Mind. Harmondsworth, England, Penguin, 1963.\n"}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	store.onStats = func(int) {
		if src, ok := store.sources["naur.pdf"]; ok && src.Recognised {
			cancel()
		}
	}
	ix := index.New(store, nopEmbedder{}, index.Options{Books: books, Vault: notes, Papers: papers, OCR: recognizer})

	runFor(t, ix, ctx, time.Hour, nil)

	store.mu.Lock()
	defer store.mu.Unlock()
	if src := store.sources["naur.pdf"]; src.Kind != "paper" {
		t.Errorf("source = %+v, want a paper", src)
	}
	for _, c := range store.chunks["naur.pdf"] {
		if strings.Contains(c.Body, "Concept of Mind") {
			t.Errorf("chunk on page %d holds the references: %q", c.Page, c.Body)
		}
	}
	recognizer.mu.Lock()
	defer recognizer.mu.Unlock()
	if len(recognizer.calls) != 1 || recognizer.calls[0] != filepath.Join(papers, "naur.pdf") {
		t.Errorf("recognised %v, want the paper by its path on the papers shelf", recognizer.calls)
	}
}

// A review's studies are read with its references, each list numbered from one.
func TestAReviewHasItsPrimaryStudiesRead(t *testing.T) {
	notes, books := vault(t, 1)
	store := newStore()
	store.sources["review.pdf"] = corpus.Source{Kind: "paper", Path: "review.pdf", Title: "Review", Hash: "pr"}

	pages := []string{
		"A systematic mapping study of technical debt in AI-based systems.",
		"References\n[1] M. Kleppmann. Designing Data-Intensive Applications. O'Reilly, 2017.\n" +
			"PRIMARY STUDIES\n" +
			"[P1] A. Agarwal, “Making contextual decisions with low technical debt,” 2016.\n" +
			"[P2] G. A. Lewis, “Component mismatches are a critical bottleneck,” 2019.\n" +
			"[P3] D. Sculley, “Hidden technical debt in machine learning systems,” 2015.\n",
	}
	ix := index.New(store, nopEmbedder{}, index.Options{
		Books: books, Vault: notes,
		PaperText: func(context.Context, string) ([]string, error) { return pages, nil },
	})
	if err := ix.Index(context.Background()); err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	var refs, studies []corpus.Citation
	for _, c := range store.citations["pr"] {
		switch c.List {
		case "references":
			refs = append(refs, c)
		case "primary":
			studies = append(studies, c)
		}
	}
	if len(refs) != 1 || len(studies) != 3 {
		t.Fatalf("references %d, studies %d; want 1 and 3: %+v", len(refs), len(studies), store.citations["pr"])
	}
	if studies[0].Ord != 1 || studies[2].Ord != 3 || studies[2].Title != "Hidden technical debt in machine learning systems" {
		t.Errorf("studies = %+v", studies)
	}
}
