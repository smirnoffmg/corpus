package shrink_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smirnoffmg/corpus/internal/shrink"
)

type rehash struct{ kind, path, old, new string }

type fakeStore struct {
	calls []rehash
	err   error
}

func (f *fakeStore) Rehash(_ context.Context, kind, path, old, new string) error {
	f.calls = append(f.calls, rehash{kind, path, old, new})
	return f.err
}

func hashOf(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// shelf puts a book with a large image into a fresh library and returns the
// library root, the book's path in it and its original bytes.
func shelf(t *testing.T) (root, rel string, original []byte) {
	t.Helper()
	root = t.TempDir()
	rel = "uploads/book.pdf"
	src := writePDF(t, "book.pdf", book, true)
	original, err := os.ReadFile(src)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "uploads"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, rel), original, 0o600))
	return root, rel, original
}

// textOnly recodes a book into the same pages without the image: the text is
// the same, the file a fraction of the size.
func textOnly(t *testing.T) shrink.Shrinker {
	smaller := writePDF(t, "smaller.pdf", book, false)
	return shrink.Shrinker{Recode: func(_ context.Context, _, out string, _ int) error {
		return copyFile(smaller, out)
	}}
}

func TestReshelvingReplacesTheFileAndMovesWhatIsFiledUnderIt(t *testing.T) {
	needTools(t, "pdftotext", "pdfinfo")
	root, rel, _ := shelf(t)
	path := filepath.Join(root, rel)
	oldHash := hashOf(t, path)
	ocr := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ocr, oldHash), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ocr, oldHash, "00001.txt"), []byte("распознано"), 0o600))
	store := &fakeStore{}
	exported := 0

	m := shrink.Reshelver{Shrinker: textOnly(t), Store: store, OCR: ocr,
		Export: func(context.Context) error { exported++; return nil }}
	res, err := m.File(context.Background(), "book", root, rel)
	require.NoError(t, err)
	require.True(t, res.Kept, res.Reason)

	newHash := hashOf(t, path)
	require.NotEqual(t, oldHash, newHash)
	require.Equal(t, []rehash{{"book", rel, oldHash, newHash}}, store.calls)
	page, err := os.ReadFile(filepath.Join(ocr, newHash, "00001.txt"))
	require.NoError(t, err)
	require.Equal(t, "распознано", string(page), "recognised pages are not recognised again")
	require.Equal(t, 1, exported, "the bibliography file follows the moved description")
	require.Equal(t, []string{"uploads/book.pdf"}, tree(t, root), "no temporary file is left in the library")
}

func TestAFailedRehashPutsTheOriginalBack(t *testing.T) {
	needTools(t, "pdftotext", "pdfinfo")
	root, rel, original := shelf(t)
	ocr := t.TempDir()
	store := &fakeStore{err: errors.New("connection lost")}

	m := shrink.Reshelver{Shrinker: textOnly(t), Store: store, OCR: ocr}
	_, err := m.File(context.Background(), "book", root, rel)
	require.Error(t, err)

	got, err := os.ReadFile(filepath.Join(root, rel))
	require.NoError(t, err)
	require.Equal(t, original, got)
	require.Equal(t, []string{"uploads/book.pdf"}, tree(t, root))
	require.Empty(t, tree(t, ocr), "no cache is left under a hash no file has")
}

func TestADryRunChangesNothing(t *testing.T) {
	needTools(t, "pdftotext", "pdfinfo")
	root, rel, original := shelf(t)
	store := &fakeStore{}

	m := shrink.Reshelver{Shrinker: textOnly(t), Store: store, DryRun: true}
	res, err := m.File(context.Background(), "book", root, rel)
	require.NoError(t, err)
	require.True(t, res.Kept)
	require.Less(t, res.After, res.Before)

	got, err := os.ReadFile(filepath.Join(root, rel))
	require.NoError(t, err)
	require.Equal(t, original, got)
	require.Empty(t, store.calls)
	require.Equal(t, []string{"uploads/book.pdf"}, tree(t, root))
}

func TestAFileThatDoesNotShrinkIsLeftAlone(t *testing.T) {
	needTools(t, "pdftotext", "pdfinfo")
	root, rel, original := shelf(t)
	store := &fakeStore{}
	same := shrink.Shrinker{Recode: func(_ context.Context, in, out string, _ int) error { return copyFile(in, out) }}

	res, err := shrink.Reshelver{Shrinker: same, Store: store}.File(context.Background(), "book", root, rel)
	require.NoError(t, err)
	require.False(t, res.Kept)

	got, err := os.ReadFile(filepath.Join(root, rel))
	require.NoError(t, err)
	require.Equal(t, original, got)
	require.Empty(t, store.calls)
	require.Equal(t, []string{"uploads/book.pdf"}, tree(t, root))
}

func TestAFailedExportIsReportedAfterTheMove(t *testing.T) {
	needTools(t, "pdftotext", "pdfinfo")
	root, rel, _ := shelf(t)
	store := &fakeStore{}

	m := shrink.Reshelver{Shrinker: textOnly(t), Store: store,
		Export: func(context.Context) error { return errors.New("disk full") }}
	_, err := m.File(context.Background(), "book", root, rel)
	require.ErrorContains(t, err, "disk full")
	require.Len(t, store.calls, 1, "the move is committed; only the file is behind, and the next export catches up")
}

// Recognised pages already under the new hash belong to a file uploaded
// shrunk; a move that fails must not take them away.
func TestAFailedMoveLeavesACacheItDidNotMake(t *testing.T) {
	needTools(t, "pdftotext", "pdfinfo")
	root, rel, _ := shelf(t)
	oldHash := hashOf(t, filepath.Join(root, rel))
	sh := textOnly(t)
	probe := filepath.Join(t.TempDir(), "probe.pdf")
	_, err := sh.Shrink(context.Background(), filepath.Join(root, rel), probe)
	require.NoError(t, err)
	newHash := hashOf(t, probe)

	ocr := t.TempDir()
	for _, h := range []string{oldHash, newHash} {
		require.NoError(t, os.MkdirAll(filepath.Join(ocr, h), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(ocr, h, "00001.txt"), []byte(h), 0o600))
	}
	m := shrink.Reshelver{Shrinker: sh, Store: &fakeStore{err: errors.New("connection lost")}, OCR: ocr}
	_, err = m.File(context.Background(), "book", root, rel)
	require.Error(t, err)

	page, err := os.ReadFile(filepath.Join(ocr, newHash, "00001.txt"))
	require.NoError(t, err)
	require.Equal(t, newHash, string(page))
}

func tree(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	require.NoError(t, filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	}))
	return files
}
