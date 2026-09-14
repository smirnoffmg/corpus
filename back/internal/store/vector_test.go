package store_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

// unit returns a 1024-dimension basis vector, which is the shape the schema
// declares and the length ollama returns.
func unit(axis int) []float32 {
	v := make([]float32, 1024)
	v[axis] = 1
	return v
}

func TestTheEmbeddingQueueDrainsAsVectorsArrive(t *testing.T) {
	st, _, ctx := open(t)

	require.NoError(t, st.Replace(ctx, book("__test__/queue.pdf", "Книга", "h1"),
		[]corpus.Chunk{
			{Ord: 1, Page: 1, Lang: "russian", Body: "корпускрипт первый"},
			{Ord: 2, Page: 2, Lang: "russian", Body: "корпускрипт второй"},
		}))

	missing, err := st.MissingEmbeddings(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 2, missing)

	pending, err := st.PendingEmbeddings(ctx, 10)
	require.NoError(t, err)
	require.Len(t, pending, 2)
	// The queue hands out a window, so each body carries its neighbours.
	require.Contains(t, pending[0].Body, "первый")
	require.Contains(t, pending[0].Body, "второй")

	ids := []int64{pending[0].ID, pending[1].ID}
	require.NoError(t, st.SaveEmbeddings(ctx, ids, [][]float32{unit(0), unit(1)}))

	missing, err = st.MissingEmbeddings(ctx)
	require.NoError(t, err)
	require.Zero(t, missing)

	require.Error(t, st.SaveEmbeddings(ctx, ids, [][]float32{unit(0)}),
		"a vector per id, or the pairing is a guess")
}

func TestSearchVectorReturnsTheNearestChunkFirst(t *testing.T) {
	st, _, ctx := open(t)

	require.NoError(t, st.Replace(ctx, book("__test__/near.pdf", "Книга", "h1"),
		[]corpus.Chunk{
			{Ord: 1, Page: 1, Lang: "russian", Body: "ось ноль"},
			{Ord: 2, Page: 2, Lang: "russian", Body: "ось один"},
		}))
	require.NoError(t, st.Replace(ctx, corpus.Source{
		Kind: "vault", Path: "__test__/near.md", Title: "Заметка", Hash: "h2",
	}, []corpus.Chunk{{Ord: 1, Heading: "H", Lang: "russian", Body: "заметка на оси два"}}))

	pending, err := st.PendingEmbeddings(ctx, 10)
	require.NoError(t, err)
	require.Len(t, pending, 2, "the queue works one source at a time")
	require.NoError(t, st.SaveEmbeddings(ctx,
		[]int64{pending[0].ID, pending[1].ID}, [][]float32{unit(0), unit(1)}))

	rest, err := st.PendingEmbeddings(ctx, 10)
	require.NoError(t, err)
	require.Len(t, rest, 1)
	require.NoError(t, st.SaveEmbeddings(ctx, []int64{rest[0].ID}, [][]float32{unit(2)}))

	hits, err := st.SearchVector(ctx, unit(1), "", 10)
	require.NoError(t, err)
	require.NotEmpty(t, hits)
	require.Equal(t, "ось один", hits[0].Snippet)
	require.InDelta(t, 1.0, hits[0].Rank, 1e-5, "an identical vector is at distance zero")

	books, err := st.SearchVector(ctx, unit(2), "book", 10)
	require.NoError(t, err)
	for _, h := range books {
		require.Equal(t, "book", h.Kind, "the kind filter must hold even for the nearest note")
	}
}

// A chunk the embedder keeps refusing is set aside rather than left at the head
// of the queue, where it would block every chunk behind it forever.
func TestRepeatedFailuresQuarantineAChunkUntilItIsRequeued(t *testing.T) {
	st, _, ctx := open(t)

	require.NoError(t, st.Replace(ctx, book("__test__/quarantine.pdf", "Книга", "h1"),
		[]corpus.Chunk{{Ord: 1, Page: 1, Lang: "russian", Body: "корпускрипт отказ"}}))

	pending, err := st.PendingEmbeddings(ctx, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	ids := []int64{pending[0].ID}

	for range 3 {
		require.NoError(t, st.CountAttempt(ctx, ids))
	}

	quarantined, err := st.Quarantined(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, quarantined)

	missing, err := st.MissingEmbeddings(ctx)
	require.NoError(t, err)
	require.Zero(t, missing, "a quarantined chunk is out of the queue")

	pending, err = st.PendingEmbeddings(ctx, 10)
	require.NoError(t, err)
	require.Empty(t, pending)

	requeued, err := st.RequeueQuarantined(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, requeued)

	pending, err = st.PendingEmbeddings(ctx, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1, "requeueing puts it back at the head")
}

// When a chunk got its vector is what makes two evaluation runs comparable: the
// vector leg only sees embedded chunks, so a figure taken mid-queue measures how
// far the queue got. Without a timestamp that is unrecoverable after the fact.
func TestSavingAnEmbeddingRecordsWhenItHappened(t *testing.T) {
	st, pool, ctx := open(t)

	require.NoError(t, st.Replace(ctx, book("__test__/when.pdf", "Книга", "h1"),
		[]corpus.Chunk{{Ord: 1, Page: 1, Lang: "russian", Body: "корпускрипт время"}}))

	pending, err := st.PendingEmbeddings(ctx, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)

	var before *time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT embedded_at FROM chunks WHERE id = $1`, pending[0].ID).Scan(&before))
	require.Nil(t, before, "a chunk with no vector has no embedding time")

	start := time.Now().UTC()
	require.NoError(t, st.SaveEmbeddings(ctx, []int64{pending[0].ID}, [][]float32{unit(0)}))

	var after *time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT embedded_at FROM chunks WHERE id = $1`, pending[0].ID).Scan(&after))
	require.NotNil(t, after, "saving a vector must record when")
	require.False(t, after.UTC().Before(start.Add(-time.Second)), "stamped in the past: %s", after)
}
