package store_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smirnoffmg/corpus/internal/corpus"
	"github.com/smirnoffmg/corpus/internal/store"
)

// A file rewritten with its text untouched — its images re-encoded — has a new
// content hash and nothing else new. Everything filed under the old hash follows
// it, so the next pass finds the file unchanged instead of a new book.
func TestRehashMovesWhatIsFiledUnderAFilesContent(t *testing.T) {
	st, _, ctx := open(t)
	require.NoError(t, st.Replace(ctx, paper("uploads/a.pdf", "A", "old-a"), oneChunk("текст статьи")))
	describe(t, st, ctx, "old-a", corpus.CSL{"title": "A paper", "issued": map[string]any{"date-parts": []any{[]any{2020}}}})
	before, err := st.Reference(ctx, "old-a")
	require.NoError(t, err)
	require.NoError(t, st.SaveCitations(ctx, "old-a", 1, []corpus.Citation{cited(1, "[1] Работа", "fp-1")}))
	// Another paper cites this one, and the match is filed under its hash.
	require.NoError(t, st.Replace(ctx, paper("uploads/b.pdf", "B", "pb"), oneChunk("текст другой статьи")))
	require.NoError(t, st.SaveCitations(ctx, "pb", 1, []corpus.Citation{
		{Ord: 1, Raw: "[1] A paper", Title: "A paper", Year: 2020, Fingerprint: "fp-a"},
	}))
	_, err = st.ResolveCitations(ctx)
	require.NoError(t, err)

	require.NoError(t, st.Rehash(ctx, "paper", "uploads/a.pdf", "old-a", "new-a"))

	unchanged, err := st.Unchanged(ctx, "uploads/a.pdf", "new-a", 0)
	require.NoError(t, err)
	require.True(t, unchanged, "the source is filed under the new hash")

	after, err := st.Reference(ctx, "new-a")
	require.NoError(t, err)
	require.Equal(t, before.CiteKey, after.CiteKey, "a checked citation key is typed into papers")
	require.Equal(t, "checked", after.Status)
	_, err = st.Reference(ctx, "old-a")
	require.ErrorIs(t, err, store.ErrNoReference)

	own, err := st.Citations(ctx, "new-a")
	require.NoError(t, err)
	require.Len(t, own, 1, "the reference list is not read again")
	gone, err := st.Citations(ctx, "old-a")
	require.NoError(t, err)
	require.Empty(t, gone)

	citing, err := st.Citations(ctx, "pb")
	require.NoError(t, err)
	require.Equal(t, "uploads/a.pdf", citing[0].ResolvedPath, "a citation of the paper still finds it")
}

func TestRehashMovesAScanWithItsProgress(t *testing.T) {
	st, _, ctx := open(t)
	require.NoError(t, st.MarkScan(ctx, corpus.Scan{Kind: "book", Hash: "old-s", Path: "uploads/scan.pdf", Pages: 400}))
	require.NoError(t, st.ScanProgress(ctx, "old-s", 120))

	require.NoError(t, st.Rehash(ctx, "book", "uploads/scan.pdf", "old-s", "new-s"))

	next, found, err := st.NextScan(ctx)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "new-s", next.Hash)
	require.Equal(t, 120, next.Recognised)
}

// Two copies of one file share a description: moving it to one copy's new hash
// would take it from the other.
func TestRehashRefusesAHashAnotherFileShares(t *testing.T) {
	st, _, ctx := open(t)
	require.NoError(t, st.Replace(ctx, book("uploads/one.pdf", "One", "same"), oneChunk("текст книги")))
	require.NoError(t, st.Replace(ctx, book("copy/one.pdf", "One", "same"), oneChunk("текст книги")))

	err := st.Rehash(ctx, "book", "uploads/one.pdf", "same", "new")
	require.ErrorIs(t, err, store.ErrSharedHash)

	unchanged, err := st.Unchanged(ctx, "uploads/one.pdf", "same", 0)
	require.NoError(t, err)
	require.True(t, unchanged, "nothing moved")
}
