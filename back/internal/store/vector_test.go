package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
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

// embedAll drains the queue with a distinct vector per text.
func embedAll(t *testing.T, st interface {
	PendingEmbeddings(ctx context.Context, limit int) ([]corpus.Pending, error)
	SaveEmbeddings(ctx context.Context, keys []string, vectors [][]float32) error
}, ctx context.Context,
) int {
	t.Helper()
	n := 0
	for {
		pending, err := st.PendingEmbeddings(ctx, 100)
		require.NoError(t, err)
		if len(pending) == 0 {
			return n
		}
		keys := make([]string, len(pending))
		vectors := make([][]float32, len(pending))
		for i, p := range pending {
			keys[i], vectors[i] = p.Key, unit((n+i)%1024)
		}
		require.NoError(t, st.SaveEmbeddings(ctx, keys, vectors))
		n += len(pending)
	}
}

func pages(bodies ...string) []corpus.Chunk {
	chunks := make([]corpus.Chunk, len(bodies))
	for i, b := range bodies {
		chunks[i] = corpus.Chunk{Ord: i + 1, Page: i + 1, Lang: "russian", Body: b}
	}
	return chunks
}

func TestTheEmbeddingQueueDrainsAsVectorsArrive(t *testing.T) {
	st, _, ctx := open(t)

	require.NoError(t, st.Replace(ctx, book("__test__/queue.pdf", "Книга", "h1"),
		pages("корпускрипт первый", "корпускрипт второй", "корпускрипт третий")))

	missing, err := st.MissingEmbeddings(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 3, missing)

	pending, err := st.PendingEmbeddings(ctx, 10)
	require.NoError(t, err)
	require.Len(t, pending, 3)
	// The queue hands out a window, so each body carries its neighbours.
	require.Equal(t, "корпускрипт первый корпускрипт второй", pending[0].Body)
	require.Equal(t, "корпускрипт первый корпускрипт второй корпускрипт третий", pending[1].Body)

	keys := []string{pending[0].Key, pending[1].Key, pending[2].Key}
	require.NoError(t, st.SaveEmbeddings(ctx, keys, [][]float32{unit(0), unit(1), unit(2)}))

	missing, err = st.MissingEmbeddings(ctx)
	require.NoError(t, err)
	require.Zero(t, missing)

	require.Error(t, st.SaveEmbeddings(ctx, keys, [][]float32{unit(0)}),
		"a vector per key, or the pairing is a guess")
}

// Short neighbours fall wholly inside each other's windows: two short pages are
// one text, embedded once. That is the cache working, not a collision.
func TestAdjacentShortChunksWithTheSameWindowShareAVector(t *testing.T) {
	st, _, ctx := open(t)
	require.NoError(t, st.Replace(ctx, book("__test__/pair.pdf", "Книга", "h1"), pages("левая", "правая")))
	require.Equal(t, 1, embedAll(t, st, ctx))
}

// A vector is filed under the text it was computed from. A changed file used
// to lose every vector it had — one edited note section, a whole note
// re-embedded; one corrected page, a whole book. Now only the chunks whose
// text or neighbours changed go back to the queue.
func TestRewritingAFileReembedsOnlyWhatChanged(t *testing.T) {
	st, _, ctx := open(t)
	src := book("__test__/rewrite.pdf", "Книга", "v1")

	require.NoError(t, st.Replace(ctx, src, pages("один", "два", "три", "четыре", "пять")))
	require.Equal(t, 5, embedAll(t, st, ctx))

	src.Hash = "v2"
	require.NoError(t, st.Replace(ctx, src, pages("один", "два", "ТРИ ИСПРАВЛЕНО", "четыре", "пять")))

	pending, err := st.PendingEmbeddings(ctx, 100)
	require.NoError(t, err)
	var bodies []string
	for _, p := range pending {
		bodies = append(bodies, p.Body)
	}
	// Each chunk is embedded with the edges of its neighbours, so the changed
	// page takes the pages on either side with it — and nothing further.
	require.Len(t, pending, 3, "queued: %q", bodies)
	for _, b := range bodies {
		require.Contains(t, b, "ТРИ ИСПРАВЛЕНО")
	}
}

// The key is what the embedder is handed, and it is computed twice: in Go when
// chunks are stored, and in SQL by the migration that filed existing vectors
// under it. If the two drift, every vector in the corpus silently misses.
func TestTheKeyIsTheHashOfTheTextHandedToTheEmbedder(t *testing.T) {
	st, pool, ctx := open(t)

	// Multi-byte runes on both sides of the 500-character edges: SQL counts
	// characters, and a byte-counting Go version would cut elsewhere.
	long := strings.Repeat("ё", 499) + "Ж" + strings.Repeat("я", 600) + "край"
	require.NoError(t, st.Replace(ctx, book("__test__/key.pdf", "Книга", "h1"),
		pages(long, "середина "+long, "конец "+long)))

	pending, err := st.PendingEmbeddings(ctx, 10)
	require.NoError(t, err)
	require.Len(t, pending, 3)
	for _, p := range pending {
		sum := sha256.Sum256([]byte(p.Body))
		require.Equal(t, hex.EncodeToString(sum[:]), p.Key)
	}

	rows, err := pool.Query(ctx, `
		SELECT c.embed_hash,
		       encode(sha256(convert_to(concat_ws(' ',
		           nullif(right(lag(c.body) OVER w, 500), ''),
		           left(c.body, 5000),
		           nullif(left(lead(c.body) OVER w, 500), '')), 'UTF8')), 'hex')
		FROM chunks c JOIN sources s ON s.id = c.source_id
		WHERE s.path = '__test__/key.pdf'
		WINDOW w AS (PARTITION BY c.source_id ORDER BY c.ord)`)
	require.NoError(t, err)
	defer rows.Close()
	n := 0
	for rows.Next() {
		var stored, computed string
		require.NoError(t, rows.Scan(&stored, &computed))
		require.Equal(t, computed, stored, "the Go key and the migration's SQL key differ")
		n++
	}
	require.NoError(t, rows.Err())
	require.Equal(t, 3, n)
}

func TestIdenticalTextIsEmbeddedOnceAndFoundInEverySource(t *testing.T) {
	st, _, ctx := open(t)

	require.NoError(t, st.Replace(ctx, book("__test__/copy-a.pdf", "Копия А", "a"), pages("одна и та же страница")))
	require.NoError(t, st.Replace(ctx, book("__test__/copy-b.pdf", "Копия Б", "b"), pages("одна и та же страница")))

	require.Equal(t, 1, embedAll(t, st, ctx), "two copies of a page are one text")

	hits, err := st.SearchVector(ctx, unit(0), corpus.Query{Limit: 10})
	require.NoError(t, err)
	var paths []string
	for _, h := range hits {
		paths = append(paths, h.Path)
	}
	require.ElementsMatch(t, []string{"__test__/copy-a.pdf", "__test__/copy-b.pdf"}, paths)
}

func TestVectorsNoChunkUsesArePruned(t *testing.T) {
	st, pool, ctx := open(t)

	require.NoError(t, st.Replace(ctx, book("__test__/keep.pdf", "Оставить", "k"), pages("общая страница")))
	require.NoError(t, st.Replace(ctx, book("__test__/gone.pdf", "Удалить", "g"), pages("только здесь")))
	require.NoError(t, st.Replace(ctx, book("__test__/also-gone.pdf", "Удалить тоже", "g2"), pages("и здесь")))
	embedAll(t, st, ctx)
	_, err := st.Forget(ctx, "__test__/also-gone.pdf")
	require.NoError(t, err)

	_, err = st.Forget(ctx, "__test__/gone.pdf")
	require.NoError(t, err)
	pruned, err := st.PruneEmbeddings(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 2, pruned, "the vectors of the forgotten books go")

	var left int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM embeddings`).Scan(&left))
	require.Equal(t, 1, left, "the page the kept book still uses stays")
}

func TestSearchVectorReturnsTheNearestChunkFirst(t *testing.T) {
	st, _, ctx := open(t)

	// Three pages, so the windows of the first two differ.
	require.NoError(t, st.Replace(ctx, book("__test__/near.pdf", "Книга", "h1"), pages("ось ноль", "ось один", "ось запасная")))
	require.NoError(t, st.Replace(ctx, corpus.Source{
		Kind: "vault", Path: "__test__/near.md", Title: "Заметка", Hash: "h2",
	}, []corpus.Chunk{{Ord: 1, Heading: "H", Lang: "russian", Body: "заметка на оси два"}}))

	pending, err := st.PendingEmbeddings(ctx, 10)
	require.NoError(t, err)
	require.Len(t, pending, 3, "the queue works one source at a time")
	require.NoError(t, st.SaveEmbeddings(ctx,
		[]string{pending[0].Key, pending[1].Key, pending[2].Key}, [][]float32{unit(0), unit(1), unit(3)}))

	rest, err := st.PendingEmbeddings(ctx, 10)
	require.NoError(t, err)
	require.Len(t, rest, 1)
	require.NoError(t, st.SaveEmbeddings(ctx, []string{rest[0].Key}, [][]float32{unit(2)}))

	hits, err := st.SearchVector(ctx, unit(1), corpus.Query{Limit: 10})
	require.NoError(t, err)
	require.NotEmpty(t, hits)
	require.Equal(t, "ось один", hits[0].Snippet)
	require.InDelta(t, 1.0, hits[0].Rank, 1e-5, "an identical vector is at distance zero")

	books, err := st.SearchVector(ctx, unit(2), corpus.Query{Kind: "book", Limit: 10})
	require.NoError(t, err)
	for _, h := range books {
		require.Equal(t, "book", h.Kind, "the kind filter must hold even for the nearest note")
	}
}

// A text the embedder keeps refusing is set aside rather than left at the head
// of the queue, where it would block every chunk behind it forever. The count
// belongs to the text, so rewriting the file does not buy it fresh attempts.
func TestRepeatedFailuresQuarantineATextUntilItIsRequeued(t *testing.T) {
	st, _, ctx := open(t)
	src := book("__test__/quarantine.pdf", "Книга", "h1")
	require.NoError(t, st.Replace(ctx, src, pages("корпускрипт отказ")))

	pending, err := st.PendingEmbeddings(ctx, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	keys := []string{pending[0].Key}

	for range 3 {
		require.NoError(t, st.CountAttempt(ctx, keys))
	}

	quarantined, err := st.Quarantined(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, quarantined)

	missing, err := st.MissingEmbeddings(ctx)
	require.NoError(t, err)
	require.Zero(t, missing, "a quarantined text is out of the queue")

	src.Hash = "h2"
	require.NoError(t, st.Replace(ctx, src, pages("корпускрипт отказ")))
	pending, err = st.PendingEmbeddings(ctx, 10)
	require.NoError(t, err)
	require.Empty(t, pending, "the same text in a rewritten file stays quarantined")

	requeued, err := st.RequeueQuarantined(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, requeued)

	pending, err = st.PendingEmbeddings(ctx, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1, "requeueing puts it back at the head")
}

// When a text got its vector is what makes two evaluation runs comparable: the
// vector leg only sees embedded chunks, so a figure taken mid-queue measures how
// far the queue got. Without a timestamp that is unrecoverable after the fact.
func TestSavingAnEmbeddingRecordsWhenItHappened(t *testing.T) {
	st, pool, ctx := open(t)

	require.NoError(t, st.Replace(ctx, book("__test__/when.pdf", "Книга", "h1"), pages("корпускрипт время")))

	pending, err := st.PendingEmbeddings(ctx, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)

	start := time.Now().UTC()
	require.NoError(t, st.SaveEmbeddings(ctx, []string{pending[0].Key}, [][]float32{unit(0)}))

	var after *time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT embedded_at FROM embeddings WHERE hash = $1`, pending[0].Key).Scan(&after))
	require.NotNil(t, after, "saving a vector must record when")
	require.False(t, after.UTC().Before(start.Add(-time.Second)), "stamped in the past: %s", after)
}

func TestAFailureIsRecordedAndAnAttemptCanBeTakenBack(t *testing.T) {
	st, pool, ctx := open(t)
	require.NoError(t, st.Replace(ctx, book("__test__/fail.pdf", "Книга", "h1"), pages("отказ")))
	pending, err := st.PendingEmbeddings(ctx, 10)
	require.NoError(t, err)
	keys := []string{pending[0].Key}

	require.NoError(t, st.CountAttempt(ctx, keys))
	require.NoError(t, st.CountAttempt(ctx, keys))
	require.NoError(t, st.UncountAttempt(ctx, keys))
	require.NoError(t, st.RecordFailure(ctx, keys, "ollama 400 Bad Request"))

	var attempts int
	var reason *string
	require.NoError(t, pool.QueryRow(ctx, `SELECT attempts, last_error FROM embeddings WHERE hash = $1`, keys[0]).Scan(&attempts, &reason))
	require.Equal(t, 1, attempts)
	require.NotNil(t, reason)
	require.Equal(t, "ollama 400 Bad Request", *reason)

	require.NoError(t, st.UncountAttempt(ctx, keys))
	require.NoError(t, st.UncountAttempt(ctx, keys))
	require.NoError(t, pool.QueryRow(ctx, `SELECT attempts FROM embeddings WHERE hash = $1`, keys[0]).Scan(&attempts))
	require.Zero(t, attempts, "attempts do not go below zero")

	require.NoError(t, st.SaveEmbeddings(ctx, keys, [][]float32{unit(0)}))
	require.NoError(t, pool.QueryRow(ctx, `SELECT last_error FROM embeddings WHERE hash = $1`, keys[0]).Scan(&reason))
	require.Nil(t, reason, "a vector clears the failure it overcame")
}

// Recall is measured against an exact scan, and ef_search is swept to find the
// smallest value that keeps it; both are per-search settings for that.
func TestExactSearchAndEfSearchCanBeAskedFor(t *testing.T) {
	st, _, ctx := open(t)
	require.NoError(t, st.Replace(ctx, book("__test__/exact.pdf", "Книга", "h1"), pages("а", "б", "в", "г", "д")))
	embedAll(t, st, ctx)

	exact, err := st.SearchVector(ctx, unit(2), corpus.Query{Limit: 3, Exact: true})
	require.NoError(t, err)
	approx, err := st.SearchVector(ctx, unit(2), corpus.Query{Limit: 3, EfSearch: 100})
	require.NoError(t, err)
	require.Len(t, exact, 3)
	require.Equal(t, exact[0].ID, approx[0].ID)
	require.InDelta(t, 1.0, exact[0].Rank, 1e-5)

	_, err = st.SearchVector(ctx, unit(2), corpus.Query{Limit: 3, EfSearch: 5000})
	require.Error(t, err, "ef_search beyond what pgvector accepts is refused, not clamped silently")
}
