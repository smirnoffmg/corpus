package store_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smirnoffmg/corpus/internal/corpus"
	"github.com/smirnoffmg/corpus/internal/store"
)

func book(path, title, hash string) corpus.Source {
	return corpus.Source{Kind: "book", Path: path, Title: title, Hash: hash}
}

func oneChunk(body string) []corpus.Chunk {
	return []corpus.Chunk{{Ord: 1, Page: 1, Printed: 1, Lang: "russian", Body: body}}
}

func TestUnchangedComparesTheStoredHash(t *testing.T) {
	st, _, ctx := open(t)

	src := book("__test__/hash.pdf", "Книга", "h1")
	require.NoError(t, st.Replace(ctx, src, oneChunk("корпускрипт один")))

	same, err := st.Unchanged(ctx, src.Path, "h1")
	require.NoError(t, err)
	require.True(t, same)

	same, err = st.Unchanged(ctx, src.Path, "h2")
	require.NoError(t, err)
	require.False(t, same, "a different hash is a changed file")

	same, err = st.Unchanged(ctx, "__test__/never-seen.pdf", "h1")
	require.NoError(t, err)
	require.False(t, same, "an unknown path was never indexed")
}

func TestPathByHashFindsAFileThatMoved(t *testing.T) {
	st, _, ctx := open(t)

	src := book("__test__/старое-имя.pdf", "Книга", "deadbeef")
	require.NoError(t, st.Replace(ctx, src, oneChunk("корпускрипт два")))

	path, found, err := st.PathByHash(ctx, "book", "deadbeef")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, src.Path, path)

	_, found, err = st.PathByHash(ctx, "vault", "deadbeef")
	require.NoError(t, err)
	require.False(t, found, "the hash belongs to a book, not a note")

	_, found, err = st.PathByHash(ctx, "book", "nosuchhash")
	require.NoError(t, err)
	require.False(t, found)
}

// Renaming must keep the chunks, and with them the embeddings: re-extracting a
// moved book would throw away work that cost minutes of GPU.
func TestRenameKeepsTheChunks(t *testing.T) {
	st, pool, ctx := open(t)

	src := book("__test__/до.pdf", "Старое название", "h1")
	require.NoError(t, st.Replace(ctx, src, oneChunk("корпускрипт три")))

	var before int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id FROM sources WHERE path = $1`, src.Path).Scan(&before))

	require.NoError(t, st.Rename(ctx, src.Path, "__test__/после.pdf", "Новое название"))

	var after int64
	var title string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id, title FROM sources WHERE path = $1`, "__test__/после.pdf").Scan(&after, &title))
	require.Equal(t, before, after, "rename must not create a new source row")
	require.Equal(t, "Новое название", title)

	var chunks int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM chunks WHERE source_id = $1`, after).Scan(&chunks))
	require.Equal(t, 1, chunks)
}

func TestSetTitleLeavesEverythingElseAlone(t *testing.T) {
	st, pool, ctx := open(t)

	src := book("__test__/title.pdf", "Старое", "h1")
	require.NoError(t, st.Replace(ctx, src, oneChunk("корпускрипт четыре")))
	require.NoError(t, st.SetTitle(ctx, src.Path, "Новое"))

	var title, hash string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT title, hash FROM sources WHERE path = $1`, src.Path).Scan(&title, &hash))
	require.Equal(t, "Новое", title)
	require.Equal(t, "h1", hash, "a title refresh is not a reindex")
}

func TestForgetReportsWhetherItRemovedAnything(t *testing.T) {
	st, _, ctx := open(t)

	src := book("__test__/forget.pdf", "Книга", "h1")
	require.NoError(t, st.Replace(ctx, src, oneChunk("корпускрипт пять")))

	dropped, err := st.Forget(ctx, src.Path)
	require.NoError(t, err)
	require.True(t, dropped)

	dropped, err = st.Forget(ctx, src.Path)
	require.NoError(t, err)
	require.False(t, dropped, "the second call has nothing left to remove")
}

// Prune deletes what the walk no longer sees — and only within its own kind,
// because a pass over the library must not touch the vault.
func TestPruneRemovesOnlyUnseenSourcesOfItsKind(t *testing.T) {
	st, _, ctx := open(t)

	kept := book("__test__/kept.pdf", "Оставить", "h1")
	gone := book("__test__/gone.pdf", "Удалить", "h2")
	note := corpus.Source{Kind: "vault", Path: "__test__/note.md", Title: "Заметка", Hash: "h3"}
	for _, src := range []corpus.Source{kept, gone, note} {
		require.NoError(t, st.Replace(ctx, src, oneChunk("корпускрипт "+src.Path)))
	}

	removed, err := st.Prune(ctx, "book", []string{kept.Path})
	require.NoError(t, err)
	require.EqualValues(t, 1, removed)

	_, found, err := st.PathByHash(ctx, "book", "h1")
	require.NoError(t, err)
	require.True(t, found, "the seen book stays")

	_, found, err = st.PathByHash(ctx, "book", "h2")
	require.NoError(t, err)
	require.False(t, found, "the unseen book goes")

	_, found, err = st.PathByHash(ctx, "vault", "h3")
	require.NoError(t, err)
	require.True(t, found, "pruning books must not touch the vault")
}

func TestStatsCountsSourcesAndChunks(t *testing.T) {
	st, _, ctx := open(t)

	sources, chunks, err := st.Stats(ctx)
	require.NoError(t, err)
	require.Zero(t, sources)
	require.Zero(t, chunks)

	require.NoError(t, st.Replace(ctx, book("__test__/stats.pdf", "Книга", "h1"),
		[]corpus.Chunk{
			{Ord: 1, Page: 1, Lang: "russian", Body: "корпускрипт раз"},
			{Ord: 2, Page: 2, Lang: "russian", Body: "корпускрипт два"},
		}))

	sources, chunks, err = st.Stats(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, sources)
	require.EqualValues(t, 2, chunks)
}

// Read answers with the whole page behind a hit, and on request with the pages
// on either side — a definition cut by a page break is the normal case here.
func TestReadReturnsThePageAndItsNeighbours(t *testing.T) {
	st, pool, ctx := open(t)

	src := book("__test__/read.pdf", "Книга для чтения", "h1")
	require.NoError(t, st.Replace(ctx, src, []corpus.Chunk{
		{Ord: 1, Page: 1, Printed: 10, Lang: "russian", Body: "страница до"},
		{Ord: 2, Page: 2, Printed: 11, Lang: "russian", Body: "корпускрипт середина"},
		{Ord: 3, Page: 3, Printed: 12, Lang: "russian", Body: "страница после"},
	}))

	var id int64
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT c.id FROM chunks c JOIN sources s ON s.id = c.source_id
		WHERE s.path = $1 AND c.ord = 2`, src.Path).Scan(&id))

	bare, err := st.Read(ctx, id, false)
	require.NoError(t, err)
	require.Equal(t, "корпускрипт середина", bare.Body)
	require.Equal(t, src.Title, bare.Title)
	require.Equal(t, "с. 11 (PDF 2)", bare.Locator)
	require.Equal(t, 2, bare.Page, "the PDF page, which is what opens the file at the passage")
	require.Empty(t, bare.Previous, "neighbours were not asked for")
	require.Empty(t, bare.Next)

	wide, err := st.Read(ctx, id, true)
	require.NoError(t, err)
	require.Equal(t, "страница до", wide.Previous)
	require.Equal(t, "страница после", wide.Next)

	_, err = st.Read(ctx, -1, false)
	require.Error(t, err, "there is no chunk -1")
}

func TestOptionsAreApplied(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: the store tests need a database")
	}
	ctx := t.Context()

	st, err := store.Open(ctx, dsn, store.WithEfSearch(64), store.WithWindow(1000, 100))
	require.NoError(t, err)
	t.Cleanup(st.Close)

	// The flag existed for months and did nothing: the option was stored and
	// never applied, so every search ran at pgvector's default of 40.
	ef, err := st.EfSearch(ctx)
	require.NoError(t, err)
	require.Equal(t, 64, ef, "WithEfSearch sets hnsw.ef_search on the store's connections")

	// The window is only observable through what the embedding queue hands out.
	require.NoError(t, st.Replace(ctx, book("__test__/window.pdf", "Книга", "h1"),
		[]corpus.Chunk{{Ord: 1, Page: 1, Lang: "russian", Body: strings.Repeat("я", 3000)}}))
	t.Cleanup(func() { _, _ = st.Forget(ctx, "__test__/window.pdf") })

	pending, err := st.PendingEmbeddings(ctx, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Len(t, []rune(pending[0].Body), 1000, "WithWindow caps the body handed to the embedder")
}
