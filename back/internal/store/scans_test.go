package store_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

func TestAScanIsListedWithItsRecognitionProgress(t *testing.T) {
	st, _, ctx := open(t)

	require.NoError(t, st.MarkScan(ctx, corpus.Scan{Hash: "aaaa0001", Path: "uploads/Руттен.pdf", Pages: 449}))
	require.NoError(t, st.ScanProgress(ctx, "aaaa0001", 120))

	listed, err := st.Sources(ctx, "book", "uploads/")
	require.NoError(t, err)
	require.Len(t, listed, 1)
	got := listed[0]
	require.Equal(t, "uploads/Руттен.pdf", got.Path)
	require.Equal(t, "Руттен", got.Title, "a scan has no title of its own yet; its file name stands in")
	require.Zero(t, got.Chunks)
	require.Equal(t, 449, got.ScanPages)
	require.Equal(t, 120, got.ScanRecognised)
	require.False(t, got.ScanFailed)

	// Marked again on the next pass: the progress is the book's, not the pass's.
	require.NoError(t, st.MarkScan(ctx, corpus.Scan{Hash: "aaaa0001", Path: "uploads/Руттен.pdf", Pages: 449}))
	listed, err = st.Sources(ctx, "book", "uploads/")
	require.NoError(t, err)
	require.Equal(t, 120, listed[0].ScanRecognised)

	notes, err := st.Sources(ctx, "vault", "")
	require.NoError(t, err)
	require.Empty(t, notes, "scans are books")
}

func TestScansAreRecognisedInTurnAndSetAsideAfterFailing(t *testing.T) {
	st, _, ctx := open(t)
	require.NoError(t, st.MarkScan(ctx, corpus.Scan{Hash: "bbbb0002", Path: "b.pdf", Pages: 10}))
	require.NoError(t, st.MarkScan(ctx, corpus.Scan{Hash: "aaaa0001", Path: "a.pdf", Pages: 5}))

	next, ok, err := st.NextScan(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "a.pdf", next.Path)

	for range 3 {
		require.NoError(t, st.ScanFailed(ctx, "aaaa0001", "tesseract: cannot read page 3"))
	}
	next, ok, err = st.NextScan(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "b.pdf", next.Path, "a scan that failed three times no longer blocks the rest")

	listed, err := st.Sources(ctx, "book", "")
	require.NoError(t, err)
	for _, s := range listed {
		if s.Path == "a.pdf" {
			require.True(t, s.ScanFailed)
			require.Contains(t, s.ScanError, "page 3")
		}
	}
}

// Once recognised and indexed a scan is a source like any other, and the scan
// row goes; so does the row of a file that left the library.
func TestScansThatBecameSourcesOrLeftArePruned(t *testing.T) {
	st, _, ctx := open(t)
	require.NoError(t, st.MarkScan(ctx, corpus.Scan{Hash: "aaaa0001", Path: "done.pdf", Pages: 1}))
	require.NoError(t, st.MarkScan(ctx, corpus.Scan{Hash: "bbbb0002", Path: "gone.pdf", Pages: 1}))
	require.NoError(t, st.MarkScan(ctx, corpus.Scan{Hash: "cccc0003", Path: "waiting.pdf", Pages: 1}))

	src := book("done.pdf", "Done", "aaaa0001")
	src.Recognised = true
	require.NoError(t, st.Replace(ctx, src, oneChunk("распознанный текст страницы")))

	pruned, err := st.PruneScans(ctx, []string{"done.pdf", "waiting.pdf"})
	require.NoError(t, err)
	require.EqualValues(t, 2, pruned)

	listed, err := st.Sources(ctx, "book", "")
	require.NoError(t, err)
	byPath := map[string]corpus.SourceStatus{}
	for _, s := range listed {
		byPath[s.Path] = s
	}
	require.True(t, byPath["done.pdf"].OCR, "a source built from recognised pages says so")
	require.Zero(t, byPath["done.pdf"].ScanPages)
	require.Contains(t, byPath, "waiting.pdf")
	require.NotContains(t, byPath, "gone.pdf")

	hits, err := st.Search(ctx, corpus.Query{Text: "распознанный", Kind: "book", Limit: 5})
	require.NoError(t, err)
	require.True(t, find(t, hits, "done.pdf").OCR, "and so does every hit from it")
}
