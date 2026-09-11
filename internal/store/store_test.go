package store

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/smirnoffmg/corpus/internal/extract"
)

// open connects to the compose database; without TEST_DATABASE_URL the test is
// skipped so that `go test ./...` stays runnable with no infrastructure.
func open(t *testing.T) (*Store, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)
	return st, ctx
}

func TestReplaceAndSearchRussianIsStemmed(t *testing.T) {
	st, ctx := open(t)

	src := Source{Kind: "book", Path: "__test__/стеммер.pdf", Title: "Тестовая книга", Hash: "h1"}
	t.Cleanup(func() {
		_, _ = st.pool.Exec(ctx, `DELETE FROM sources WHERE path = $1`, src.Path)
	})

	chunks := []extract.Chunk{{
		Ord: 42, Page: 42, Locator: "с. 42", Lang: "russian",
		Body: "Агрегат задаёт границу согласованности внутри предметной области корпускрипт.",
	}}
	if err := st.Replace(ctx, src, chunks); err != nil {
		t.Fatalf("replace: %v", err)
	}

	// "агрегатов" is inflected, so a match proves the russian snowball
	// configuration is in play; the nonsense word pins the hit to this row,
	// which real books would otherwise outrank.
	hits, err := st.Search(ctx, "агрегатов корпускрипт", "book", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	for _, h := range hits {
		if h.Path == src.Path {
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
	st, ctx := open(t)

	src := Source{Kind: "vault", Path: "__test__/note.md", Title: "note", Hash: "h1"}
	t.Cleanup(func() {
		_, _ = st.pool.Exec(ctx, `DELETE FROM sources WHERE path = $1`, src.Path)
	})

	chunk := []extract.Chunk{{Ord: 1, Locator: "H", Lang: "english", Body: "consistency boundary"}}
	for range 2 {
		if err := st.Replace(ctx, src, chunk); err != nil {
			t.Fatalf("replace: %v", err)
		}
	}

	var n int
	err := st.pool.QueryRow(ctx, `
		SELECT count(*) FROM chunks c JOIN sources s ON s.id = c.source_id
		WHERE s.path = $1`, src.Path).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("chunk count after two passes = %d, want 1", n)
	}
}
