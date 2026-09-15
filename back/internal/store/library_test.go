package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

func TestSourcesReportEachSourcesProgress(t *testing.T) {
	st, pool, ctx := open(t)

	require.NoError(t, st.Replace(ctx, book("uploads/a.pdf", "A", "h1"), []corpus.Chunk{
		{Ord: 1, Page: 1, Lang: "english", Body: "one"},
		{Ord: 2, Page: 2, Lang: "english", Body: "two"},
		{Ord: 3, Page: 3, Lang: "english", Body: "three"},
	}))
	docs := corpus.Source{Kind: "docs", Path: "nltk/howto/tokenize.html", Title: "nltk · Tokenize", Hash: "h2"}
	require.NoError(t, st.Replace(ctx, docs, []corpus.Chunk{{Ord: 1, Heading: "T", Lang: "english", Body: "tokens"}}))

	var first, second string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT (array_agg(c.embed_hash ORDER BY c.ord))[1], (array_agg(c.embed_hash ORDER BY c.ord))[2]
		FROM chunks c JOIN sources s ON s.id = c.source_id
		WHERE s.path = 'uploads/a.pdf'`).Scan(&first, &second))
	require.NoError(t, st.SaveEmbeddings(ctx, []string{first}, [][]float32{unit(1)}))
	for range 3 {
		require.NoError(t, st.CountAttempt(ctx, []string{second}))
	}

	all, err := st.Sources(ctx, "", "")
	require.NoError(t, err)
	require.Len(t, all, 2)

	books, err := st.Sources(ctx, "book", "")
	require.NoError(t, err)
	require.Len(t, books, 1)
	got := books[0]
	require.Equal(t, "uploads/a.pdf", got.Path)
	require.Equal(t, "A", got.Title)
	require.Equal(t, int64(3), got.Chunks)
	require.Equal(t, int64(1), got.Embedded)
	require.Equal(t, int64(1), got.Quarantined)
	require.WithinDuration(t, time.Now(), got.IndexedAt, time.Minute)

	manual, err := st.Sources(ctx, "", "nltk/")
	require.NoError(t, err)
	require.Len(t, manual, 1)
	require.Equal(t, "docs", manual[0].Kind)

	// A prefix is a prefix, not a pattern: an underscore must not match any
	// character.
	none, err := st.Sources(ctx, "", "uploads_")
	require.NoError(t, err)
	require.Empty(t, none)
}

func TestAReindexRequestWakesTheListener(t *testing.T) {
	st, _, ctx := open(t)

	listenCtx, cancel := context.WithCancel(ctx)
	wake, err := st.ListenReindex(listenCtx)
	require.NoError(t, err)

	require.NoError(t, st.RequestReindex(ctx))
	select {
	case _, ok := <-wake:
		require.True(t, ok, "the channel closed instead of signalling")
	case <-time.After(10 * time.Second):
		t.Fatal("no wake-up after a reindex request")
	}

	// Stopping the listener closes the channel, so a caller ranging over it or
	// selecting on it learns the listener is gone. The connection has to be
	// released before the snapshot is restored, which drops the database.
	cancel()
	select {
	case _, ok := <-wake:
		require.False(t, ok, "the channel is still open after the listener stopped")
	case <-time.After(10 * time.Second):
		t.Fatal("the listener did not stop")
	}
}
