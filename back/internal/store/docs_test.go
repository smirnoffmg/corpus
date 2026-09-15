package store_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

// A manual section is cited by its heading path like a note, and linked by its
// anchor, which every way of reaching the chunk has to carry.
func TestDocsSectionKeepsItsAnchorThroughEveryRead(t *testing.T) {
	st, pool, ctx := open(t)

	src := corpus.Source{Kind: "docs", Path: "scikit-learn/modules/svm.html", Title: "scikit-learn · 1.4. Support Vector Machines", Hash: "h1"}
	chunks := []corpus.Chunk{{
		Ord: 1, Heading: "1.4. Support Vector Machines > 1.4.1. Classification", Anchor: "classification",
		Lang: "english", Body: "SVC is a class capable of performing corpusscript classification.",
	}}
	require.NoError(t, st.Replace(ctx, src, chunks), "a docs source must pass the kind constraint")

	hits, err := st.Search(ctx, corpus.Query{Text: "corpusscript", Kind: "docs", Limit: 10})
	require.NoError(t, err)
	got := find(t, hits, src.Path)
	require.Equal(t, "classification", got.Anchor, "text search")
	require.Equal(t, "1.4. Support Vector Machines > 1.4.1. Classification", got.Locator)

	var id int64
	var key string
	require.NoError(t, pool.QueryRow(ctx, `SELECT id, embed_hash FROM chunks WHERE anchor = 'classification'`).Scan(&id, &key))
	require.NoError(t, st.SaveEmbeddings(ctx, []string{key}, [][]float32{unit(7)}))
	hits, err = st.SearchVector(ctx, unit(7), corpus.Query{Kind: "docs", Limit: 1})
	require.NoError(t, err)
	require.Equal(t, "classification", find(t, hits, src.Path).Anchor, "vector search")

	passage, err := st.Read(ctx, id, false)
	require.NoError(t, err)
	require.Equal(t, "classification", passage.Anchor, "read")
}

func TestChunksWithoutAnAnchorStoreNone(t *testing.T) {
	st, pool, ctx := open(t)

	require.NoError(t, st.Replace(ctx, book("__test__/a.pdf", "A", "h1"), oneChunk("text")))

	var anchor *string
	require.NoError(t, pool.QueryRow(ctx, `SELECT anchor FROM chunks`).Scan(&anchor))
	require.Nil(t, anchor, "an empty anchor is stored as NULL, not as ''")
}
