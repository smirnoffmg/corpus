package store_test

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/smirnoffmg/corpus/internal/corpus"
	"github.com/smirnoffmg/corpus/internal/store"
)

// One container for the whole package, migrated once and snapshotted; each test
// restores that snapshot instead of paying for a container of its own. The
// image is the one compose runs, because the schema needs pgvector and a plain
// postgres image would fail the second migration rather than the first test.
var (
	container *postgres.PostgresContainer
	dsn       string
)

func TestMain(m *testing.M) {
	// testing.Short reads a flag, so the flags have to be parsed first.
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}

	ctx := context.Background()
	ctr, err := postgres.Run(ctx, "pgvector/pgvector:pg18",
		postgres.WithDatabase("corpus"),
		postgres.WithUsername("corpus"),
		postgres.WithPassword("corpus"),
		postgres.WithSQLDriver("pgx"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		// Loud rather than skipped: a store suite that quietly passes with no
		// database is a green run that proves nothing. `go test -short` is the
		// way to run the rest of the tests without Docker.
		fmt.Fprintf(os.Stderr, "store tests need Docker (or -short): %v\n", err)
		os.Exit(1)
	}
	container = ctr

	code := 1
	defer func() {
		_ = testcontainers.TerminateContainer(ctr)
		os.Exit(code)
	}()

	if dsn, err = ctr.ConnectionString(ctx, "sslmode=disable"); err != nil {
		fmt.Fprintf(os.Stderr, "connection string: %v\n", err)
		return
	}
	st, err := store.Open(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		return
	}
	if err := st.Migrate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		return
	}
	st.Close()

	if err := ctr.Snapshot(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "snapshot: %v\n", err)
		return
	}
	code = m.Run()
}

// open hands the test a store and a second pool of its own for inspecting rows
// without reaching inside the store. The database is returned to the migrated
// snapshot afterwards, so tests neither see nor clean up each other's rows.
func open(t *testing.T) (*store.Store, *pgxpool.Pool, context.Context) {
	t.Helper()
	if testing.Short() {
		t.Skip("-short: the store tests need a database")
	}

	ctx := context.Background()
	// Registered first, so it runs last: Restore drops the database, which
	// fails while the pools below still hold connections to it.
	t.Cleanup(func() { require.NoError(t, container.Restore(ctx)) })

	st, err := store.Open(ctx, dsn)
	require.NoError(t, err, "connect")
	t.Cleanup(st.Close)

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err, "connect")
	t.Cleanup(pool.Close)

	return st, pool, ctx
}

func TestReplaceAndSearchRussianIsStemmed(t *testing.T) {
	st, _, ctx := open(t)

	src := corpus.Source{Kind: "book", Path: "__test__/стеммер.pdf", Title: "Тестовая книга", Hash: "h1"}
	chunks := []corpus.Chunk{{
		Ord: 42, Page: 42, Printed: 42, Lang: "russian",
		Body: "Агрегат задаёт границу согласованности внутри предметной области корпускрипт.",
	}}
	require.NoError(t, st.Replace(ctx, src, chunks), "replace")

	// "агрегатов" is inflected, so a match proves the russian snowball
	// configuration is in play; the nonsense word pins the hit to this row,
	// which real books would otherwise outrank.
	hits, err := st.Search(ctx, corpus.Query{Text: "агрегатов корпускрипт", Kind: "book", Limit: 10})
	require.NoError(t, err, "search")

	got := find(t, hits, src.Path)
	// The locator is composed from the page, not stored with the chunk.
	require.Equal(t, "с. 42", got.Locator)
	require.Contains(t, got.Snippet, "<<", "snippet is not highlighted")
}

// Postgres cannot store a NUL in text, and one NUL used to reject the whole
// batch: a 144-page survey was lost to a single figure whose font maps glyphs
// to control codes (issue #1).
func TestReplaceKeepsASourceWhoseTextHasNULBytes(t *testing.T) {
	st, pool, ctx := open(t)

	src := corpus.Source{Kind: "book", Path: "__test__/nul.pdf", Title: "Sur\x00vey", Hash: "h1"}
	chunks := []corpus.Chunk{
		{Ord: 1, Page: 1, Lang: "english", Body: "a clean page corpusnul"},
		{Ord: 2, Page: 2, Heading: "Fig\x00ure", Lang: "english", Body: "*37\x00\x00//D0$ corpus\x00nul"},
	}
	require.NoError(t, st.Replace(ctx, src, chunks), "replace")

	type row struct {
		Title   string `db:"title"`
		Heading string `db:"heading"`
		Body    string `db:"body"`
	}
	rows, err := pool.Query(ctx, `
		SELECT s.title, coalesce(c.heading, '') AS heading, c.body
		FROM chunks c JOIN sources s ON s.id = c.source_id
		WHERE s.path = $1 ORDER BY c.ord`, src.Path)
	require.NoError(t, err)
	got, err := pgx.CollectRows(rows, pgx.RowToStructByName[row])
	require.NoError(t, err)
	require.Equal(t, []row{
		{Title: "Survey", Heading: "", Body: "a clean page corpusnul"},
		{Title: "Survey", Heading: "Figure", Body: "*37//D0$ corpusnul"},
	}, got)
}

// A reranker reads the passage, not the snippet, and a bounded part of it:
// the cut counts characters, so a Russian page is not cut at half the length.
func TestBodiesReturnsTheStartOfEachChunkByID(t *testing.T) {
	st, pool, ctx := open(t)

	src := corpus.Source{Kind: "vault", Path: "__test__/bodies.md", Title: "note", Hash: "h1"}
	chunks := []corpus.Chunk{
		{Ord: 1, Heading: "A", Lang: "russian", Body: "Агрегат задаёт границу"},
		{Ord: 2, Heading: "B", Lang: "english", Body: "consistency boundary"},
	}
	require.NoError(t, st.Replace(ctx, src, chunks), "replace")
	rows, err := pool.Query(ctx, `
		SELECT c.id FROM chunks c JOIN sources s ON s.id = c.source_id
		WHERE s.path = $1 ORDER BY c.ord`, src.Path)
	require.NoError(t, err)
	chunkIDs, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	require.NoError(t, err)

	got, err := st.Bodies(ctx, append(chunkIDs, 1<<40), 7)
	require.NoError(t, err)
	require.Equal(t, map[int64]string{chunkIDs[0]: "Агрегат", chunkIDs[1]: "consist"}, got)
}

func TestReplaceIsIdempotentPerSource(t *testing.T) {
	st, pool, ctx := open(t)

	src := corpus.Source{Kind: "vault", Path: "__test__/note.md", Title: "note", Hash: "h1"}
	chunk := []corpus.Chunk{{Ord: 1, Heading: "H", Lang: "english", Body: "consistency boundary"}}
	for range 2 {
		require.NoError(t, st.Replace(ctx, src, chunk), "replace")
	}

	var n int
	err := pool.QueryRow(ctx, `
		SELECT count(*) FROM chunks c JOIN sources s ON s.id = c.source_id
		WHERE s.path = $1`, src.Path).Scan(&n)
	require.NoError(t, err)
	require.Equal(t, 1, n, "chunk count after two passes")
}

func TestSearchOrderDoesNotDependOnTheLimit(t *testing.T) {
	st, _, ctx := open(t)

	// Two chunks that name the term equally often score identically. Without a
	// tiebreaker their order came out differently at different limits, so the
	// same query answered differently depending on how many hits were asked for.
	src := corpus.Source{Kind: "vault", Path: "__test__/ties.md", Title: "ties", Hash: "h1"}
	chunks := make([]corpus.Chunk, 6)
	for i := range chunks {
		chunks[i] = corpus.Chunk{
			Ord: i + 1, Heading: "H", Lang: "russian",
			Body: "корпускрипт равнозначный кусок номер " + string(rune('а'+i)),
		}
	}
	require.NoError(t, st.Replace(ctx, src, chunks))

	var first string
	for _, limit := range []int{1, 2, 3, 4, 10, 20} {
		hits, err := st.Search(ctx, corpus.Query{Text: "корпускрипт", Kind: "vault", Limit: limit})
		require.NoError(t, err)
		require.NotEmpty(t, hits, "limit %d returned nothing", limit)
		if first == "" {
			first = hits[0].Locator + hits[0].Snippet
			continue
		}
		require.Equal(t, first, hits[0].Locator+hits[0].Snippet,
			"limit %d put a different chunk first", limit)
	}
}

// TestSearchMapsColumnsToTheRightFields pins which column lands in which field.
// Both search legs scan a nine-column row, and a scan that binds by position
// keeps compiling when two columns of the same type trade places — it just
// returns a book's path as its title, or cites the PDF page as the printed one.
// The values below are deliberately distinct per field so that a swap shows up.
func TestSearchMapsColumnsToTheRightFields(t *testing.T) {
	st, _, ctx := open(t)

	src := corpus.Source{
		Kind:  "book",
		Path:  "__test__/раскладка.pdf",
		Title: "Заголовок книги",
		Hash:  "h1",
	}
	chunks := []corpus.Chunk{{
		Ord: 1, Page: 203, Printed: 189, Lang: "russian",
		Body: "Стюард перезапускает подчинённую горутину корпускрипт.",
	}}
	require.NoError(t, st.Replace(ctx, src, chunks), "replace")

	hits, err := st.Search(ctx, corpus.Query{Text: "корпускрипт", Kind: "book", Limit: 10})
	require.NoError(t, err, "search")

	got := find(t, hits, src.Path)
	require.Equal(t, src.Title, got.Title, "Title")
	require.Equal(t, src.Kind, got.Kind, "Kind")
	require.Equal(t, 203, got.Page, "Page should be the PDF page")
	// Printed 189 differs from PDF 203, so a page/printed swap changes this.
	require.Equal(t, "с. 189 (PDF 203)", got.Locator, "Locator")
	require.Contains(t, got.Snippet, "<<", "Snippet is not the highlighted headline")
}

// find returns the hit for one path, failing the test when the search did not
// return it at all — which is a different failure from a field being wrong.
func find(t *testing.T, hits []corpus.Hit, path string) corpus.Hit {
	t.Helper()
	for i := range hits {
		if hits[i].Path == path {
			return hits[i]
		}
	}
	require.FailNowf(t, "not found", "no hit for %s among %d hits", path, len(hits))
	return corpus.Hit{}
}

// mcpd does not migrate — the indexer does — so a newer mcpd can start against
// an older schema and fail on the first query that needs a new column. It asks
// first instead.
func TestSchemaVersionsTellAnUnmigratedDatabase(t *testing.T) {
	st, pool, ctx := open(t)

	current, target, err := st.SchemaVersions(ctx)
	require.NoError(t, err)
	require.Positive(t, target)
	require.Equal(t, target, current, "the test database is migrated")

	_, err = pool.Exec(ctx, `CREATE DATABASE corpus_unmigrated`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP DATABASE IF EXISTS corpus_unmigrated WITH (FORCE)`)
	})
	fresh, err := store.Open(ctx, strings.Replace(dsn, "/corpus?", "/corpus_unmigrated?", 1))
	require.NoError(t, err)
	defer fresh.Close()

	current, target, err = fresh.SchemaVersions(ctx)
	require.NoError(t, err)
	require.Less(t, current, target)
}

// A bulk rewrite leaves its lexemes in the GIN index's pending list, which every
// search then scans in full until it is merged (Рогов, PostgreSQL 18 изнутри,
// с. 624–625). The indexer merges it after a pass that changed files.
func TestCleaningTheTextIndexEmptiesItsPendingList(t *testing.T) {
	st, pool, ctx := open(t)
	chunks := make([]corpus.Chunk, 200)
	for i := range chunks {
		chunks[i] = corpus.Chunk{Ord: i + 1, Page: i + 1, Lang: "russian", Body: fmt.Sprintf("корпускрипт страница %d с разными словами %d", i, i*7)}
	}
	require.NoError(t, st.Replace(ctx, book("__test__/bulk.pdf", "Книга", "h1"), chunks))

	_, err := st.CleanTextIndex(ctx)
	require.NoError(t, err)

	again, err := st.CleanTextIndex(ctx)
	require.NoError(t, err)
	require.Zero(t, again, "nothing is left pending after a clean")

	var scale string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT coalesce((SELECT option_value FROM pg_options_to_table(reloptions) WHERE option_name = 'autovacuum_vacuum_scale_factor'), '')
		FROM pg_class WHERE relname = 'chunks'`).Scan(&scale))
	require.Equal(t, "0.05", scale)
}
