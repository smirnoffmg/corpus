package index_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	titles    map[string]string
	pruned    map[string][]string
	indexed   map[string]string // path -> hash, as a real store would remember
	byHash    map[string]string // hash -> path
	renamed   []string
	forgotten []string
	unchanged bool
}

func newStore() *recordingStore {
	return &recordingStore{
		replaced: map[string]int{},
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

func (s *recordingStore) MissingEmbeddings(context.Context) (int64, error) { return 0, nil }
func (s *recordingStore) Quarantined(context.Context) (int64, error)       { return 0, nil }
func (s *recordingStore) PendingEmbeddings(context.Context, int) ([]corpus.Pending, error) {
	return nil, nil
}
func (s *recordingStore) CountAttempt(context.Context, []int64) error { return nil }
func (s *recordingStore) SaveEmbeddings(context.Context, []int64, [][]float32) error {
	return nil
}

func (s *recordingStore) counts() (replaced int, pruned []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.replaced), s.pruned["vault"]
}

type nopEmbedder struct{}

func (nopEmbedder) Embed(context.Context, []string) ([][]float32, error) { return nil, nil }

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
