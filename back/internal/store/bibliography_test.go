package store_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smirnoffmg/corpus/internal/corpus"
	"github.com/smirnoffmg/corpus/internal/store"
)

func TestReferenceKeysFollowWhatTheSourceIs(t *testing.T) {
	st, _, ctx := open(t)
	require.NoError(t, st.Replace(ctx, book("uploads/k.pdf", "K", "hash-k"), oneChunk("text")))

	key, err := st.ReferenceKey(ctx, "book", "uploads/k.pdf")
	require.NoError(t, err)
	require.Equal(t, "hash-k", key)

	key, err = st.ReferenceKey(ctx, "docs", "scikit-learn/modules/svm.html")
	require.NoError(t, err)
	require.Equal(t, "manual:scikit-learn", key)

	_, err = st.ReferenceKey(ctx, "vault", "note.md")
	require.ErrorIs(t, err, store.ErrNoReference)
	_, err = st.ReferenceKey(ctx, "book", "absent.pdf")
	require.ErrorIs(t, err, store.ErrNoReference)
}

func TestADraftNeverOverwritesADescription(t *testing.T) {
	st, _, ctx := open(t)
	checked := corpus.CSL{"type": "book", "title": "Checked", "author": []any{map[string]any{"family": "Knuth"}}, "issued": map[string]any{"date-parts": []any{[]any{1968}}}}

	saved, err := st.SaveReference(ctx, "h1", checked, "checked")
	require.NoError(t, err)
	require.Equal(t, "knuth1968", saved.CiteKey)

	require.NoError(t, st.EnsureDraft(ctx, "h1", corpus.CSL{"title": "A guess"}))
	got, err := st.Reference(ctx, "h1")
	require.NoError(t, err)
	require.Equal(t, "Checked", got.CSL["title"])
	require.Equal(t, "checked", got.Status)

	// Editing keeps the key a paper already cites, even if the author changes.
	checked["author"] = []any{map[string]any{"family": "Someone"}}
	edited, err := st.SaveReference(ctx, "h1", checked, "checked")
	require.NoError(t, err)
	require.Equal(t, "knuth1968", edited.CiteKey)

	// A second work of the same author and year gets its own key.
	second, err := st.SaveReference(ctx, "h2", corpus.CSL{"author": []any{map[string]any{"family": "Knuth"}}, "issued": map[string]any{"date-parts": []any{[]any{1968}}}}, "draft")
	require.NoError(t, err)
	require.Equal(t, "knuth1968a", second.CiteKey)

	all, err := st.References(ctx)
	require.NoError(t, err)
	require.Len(t, all, 2)

	_, err = st.Reference(ctx, "absent")
	require.ErrorIs(t, err, store.ErrNoReference)
}

func TestADescriptionOutlivesItsSource(t *testing.T) {
	st, _, ctx := open(t)
	require.NoError(t, st.Replace(ctx, book("a.pdf", "A", "hash-a"), oneChunk("ISBN 978-1-449-37332-0")))

	undescribed, err := st.UndescribedBooks(ctx)
	require.NoError(t, err)
	require.Len(t, undescribed, 1)
	require.Contains(t, undescribed[0].Text, "ISBN 978-1-449-37332-0")

	require.NoError(t, st.EnsureDraft(ctx, "hash-a", corpus.CSL{"title": "A"}))
	undescribed, err = st.UndescribedBooks(ctx)
	require.NoError(t, err)
	require.Empty(t, undescribed)

	sources, err := st.Sources(ctx, "book", "")
	require.NoError(t, err)
	require.Equal(t, "draft", sources[0].Description)

	_, err = st.Forget(ctx, "a.pdf")
	require.NoError(t, err)
	_, err = st.Reference(ctx, "hash-a")
	require.NoError(t, err, "forgetting the source must not take the description with it")
}

func TestStylesAreStoredAndReplaced(t *testing.T) {
	st, _, ctx := open(t)
	require.NoError(t, st.SaveStyle(ctx, "nature", "Nature", "<style/>"))
	require.NoError(t, st.SaveStyle(ctx, "nature", "Nature (updated)", "<style v='2'/>"))

	styles, err := st.Styles(ctx)
	require.NoError(t, err)
	require.Equal(t, []corpus.Style{{ID: "nature", Title: "Nature (updated)"}}, styles)

	xml, err := st.StyleXML(ctx, "nature")
	require.NoError(t, err)
	require.Equal(t, "<style v='2'/>", xml)
	_, err = st.StyleXML(ctx, "absent")
	require.ErrorIs(t, err, store.ErrNoReference)
}
