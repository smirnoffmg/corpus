package store_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/smirnoffmg/corpus/internal/corpus"
	"github.com/smirnoffmg/corpus/internal/store"
)

// open connects to the compose database; without TEST_DATABASE_URL the test is
// skipped so that `go test ./...` stays runnable with no infrastructure. The
// second pool is the test's own way to inspect and clean up rows, so that the
// test does not have to reach inside the store.
func open(t *testing.T) (*store.Store, *pgxpool.Pool, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	return st, pool, ctx
}

func TestReplaceAndSearchRussianIsStemmed(t *testing.T) {
	st, pool, ctx := open(t)

	src := corpus.Source{Kind: "book", Path: "__test__/стеммер.pdf", Title: "Тестовая книга", Hash: "h1"}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM sources WHERE path = $1`, src.Path)
	})

	chunks := []corpus.Chunk{{
		Ord: 42, Page: 42, Printed: 42, Lang: "russian",
		Body: "Агрегат задаёт границу согласованности внутри предметной области корпускрипт.",
	}}
	if err := st.Replace(ctx, src, chunks); err != nil {
		t.Fatalf("replace: %v", err)
	}

	// "агрегатов" is inflected, so a match proves the russian snowball
	// configuration is in play; the nonsense word pins the hit to this row,
	// which real books would otherwise outrank.
	hits, err := st.Search(ctx, corpus.Query{Text: "агрегатов корпускрипт", Kind: "book", Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	for _, h := range hits {
		if h.Path == src.Path {
			// The locator is composed from the page, not stored with the chunk.
			if h.Locator != "с. 42" {
				t.Errorf("locator = %q, want %q", h.Locator, "с. 42")
			}
			if !strings.Contains(h.Snippet, "<<") {
				t.Errorf("snippet is not highlighted: %q", h.Snippet)
			}
			return
		}
	}
	t.Fatalf("inflected query did not match the indexed chunk; hits: %+v", hits)
}

func TestReplaceIsIdempotentPerSource(t *testing.T) {
	st, pool, ctx := open(t)

	src := corpus.Source{Kind: "vault", Path: "__test__/note.md", Title: "note", Hash: "h1"}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM sources WHERE path = $1`, src.Path)
	})

	chunk := []corpus.Chunk{{Ord: 1, Heading: "H", Lang: "english", Body: "consistency boundary"}}
	for range 2 {
		if err := st.Replace(ctx, src, chunk); err != nil {
			t.Fatalf("replace: %v", err)
		}
	}

	var n int
	err := pool.QueryRow(ctx, `
		SELECT count(*) FROM chunks c JOIN sources s ON s.id = c.source_id
		WHERE s.path = $1`, src.Path).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("chunk count after two passes = %d, want 1", n)
	}
}

func TestSearchOrderDoesNotDependOnTheLimit(t *testing.T) {
	st, pool, ctx := open(t)

	// Two chunks that name the term equally often score identically. Without a
	// tiebreaker their order came out differently at different limits, so the
	// same query answered differently depending on how many hits were asked for.
	src := corpus.Source{Kind: "vault", Path: "__test__/ties.md", Title: "ties", Hash: "h1"}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM sources WHERE path = $1`, src.Path)
	})

	chunks := make([]corpus.Chunk, 6)
	for i := range chunks {
		chunks[i] = corpus.Chunk{
			Ord: i + 1, Heading: "H", Lang: "russian",
			Body: "корпускрипт равнозначный кусок номер " + string(rune('а'+i)),
		}
	}
	if err := st.Replace(ctx, src, chunks); err != nil {
		t.Fatal(err)
	}

	var first string
	for _, limit := range []int{1, 2, 3, 4, 10, 20} {
		hits, err := st.Search(ctx, corpus.Query{Text: "корпускрипт", Kind: "vault", Limit: limit})
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) == 0 {
			t.Fatalf("limit %d returned nothing", limit)
		}
		if first == "" {
			first = hits[0].Locator + hits[0].Snippet
			continue
		}
		if got := hits[0].Locator + hits[0].Snippet; got != first {
			t.Errorf("limit %d put a different chunk first", limit)
		}
	}
}
