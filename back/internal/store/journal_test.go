package store_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

func TestUsageCountsSearchesByOriginAndWhichOfThemWereRead(t *testing.T) {
	st, pool, ctx := open(t)

	searches := []corpus.SearchRecord{
		{Origin: "mcp", Query: "walking skeleton", Mode: "fts", Kind: "book", Hits: []int64{1, 2}},
		{Origin: "mcp", Query: "how do teams slice a release into stages", Mode: "fts"},
		{Origin: "mcp", Query: "outcome", Mode: "hybrid", Hits: []int64{3}},
		{Origin: "mcp", Query: "broken", Mode: "hybrid", Err: "embedder down"},
		{Origin: "http", Query: "eval query", Mode: "hybrid", Hits: []int64{1}},
	}
	for _, r := range searches {
		require.NoError(t, st.LogSearch(ctx, r))
	}
	require.NoError(t, st.LogRead(ctx, "mcp", 2))
	// The same chunk read over HTTP is someone else's read.
	require.NoError(t, st.LogRead(ctx, "http", 3))

	// A search older than the window is not counted, nor is a read made long
	// after a search, which is a new look rather than its follow-up.
	_, err := pool.Exec(ctx, `INSERT INTO search_log (at, origin, query, mode, hits) VALUES (now() - interval '30 days', 'mcp', 'old', 'fts', '{9}')`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO search_log (at, origin, query, mode, hits) VALUES (now() - interval '2 hours', 'mcp', 'stale', 'fts', '{8}')`)
	require.NoError(t, err)
	require.NoError(t, st.LogRead(ctx, "mcp", 8))

	usage, err := st.Usage(ctx, 7*24*time.Hour)
	require.NoError(t, err)
	require.Equal(t, []corpus.Usage{
		{Origin: "http", Searches: 1, Empty: 0, Failed: 0, Read: 0},
		{Origin: "mcp", Searches: 5, Empty: 1, Failed: 1, Read: 1},
	}, usage)
}

// Steady State (Release It!, p. 124): whatever accumulates needs something
// that drains it, so each write takes away what has aged out.
func TestWritingTheJournalDropsWhatHasAgedOut(t *testing.T) {
	st, pool, ctx := open(t)

	for _, q := range []string{
		`INSERT INTO search_log (at, origin, query, mode) VALUES (now() - interval '200 days', 'mcp', 'aged', 'fts')`,
		`INSERT INTO search_log (at, origin, query, mode) VALUES (now() - interval '100 days', 'mcp', 'kept', 'fts')`,
		`INSERT INTO read_log (at, origin, chunk_id) VALUES (now() - interval '200 days', 'mcp', 1)`,
		`INSERT INTO read_log (at, origin, chunk_id) VALUES (now() - interval '100 days', 'mcp', 2)`,
	} {
		_, err := pool.Exec(ctx, q)
		require.NoError(t, err)
	}

	require.NoError(t, st.LogSearch(ctx, corpus.SearchRecord{Origin: "mcp", Query: "new", Mode: "fts"}))
	require.NoError(t, st.LogRead(ctx, "mcp", 3))

	var searches, reads []string
	rows, err := pool.Query(ctx, `SELECT query FROM search_log ORDER BY at`)
	require.NoError(t, err)
	searches, err = pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	rows, err = pool.Query(ctx, `SELECT chunk_id::text FROM read_log ORDER BY at`)
	require.NoError(t, err)
	reads, err = pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	require.Equal(t, []string{"kept", "new"}, searches)
	require.Equal(t, []string{"2", "3"}, reads)
}

func TestSearchesListsTheLatestFirstAndCanKeepToTheEmptyOnes(t *testing.T) {
	st, _, ctx := open(t)

	require.NoError(t, st.LogSearch(ctx, corpus.SearchRecord{Origin: "mcp", Query: "found", Mode: "hybrid", Hits: []int64{1}}))
	require.NoError(t, st.LogSearch(ctx, corpus.SearchRecord{Origin: "mcp", Query: "nothing here", Mode: "fts"}))
	require.NoError(t, st.LogSearch(ctx, corpus.SearchRecord{Origin: "http", Query: "nothing either", Mode: "fts"}))

	all, err := st.Searches(ctx, corpus.SearchFilter{Limit: 10})
	require.NoError(t, err)
	require.Len(t, all, 3)
	require.Equal(t, "nothing either", all[0].Query)
	require.Equal(t, 1, all[2].Hits)

	empty, err := st.Searches(ctx, corpus.SearchFilter{Origin: "mcp", Empty: true, Limit: 10})
	require.NoError(t, err)
	require.Len(t, empty, 1)
	require.Equal(t, "nothing here", empty[0].Query)
	require.Equal(t, "fts", empty[0].Mode)
	require.False(t, empty[0].At.IsZero())
}
