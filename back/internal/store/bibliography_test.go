package store_test

import (
	"testing"
	"time"

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

func TestADraftsKeyFollowsItsRecordUntilChecked(t *testing.T) {
	st, _, ctx := open(t)
	draft, err := st.SaveReference(ctx, "h", corpus.CSL{"title": "Concurrency in Go"}, "draft")
	require.NoError(t, err)
	require.Equal(t, "concurrency", draft.CiteKey)

	record := corpus.CSL{"title": "Concurrency in Go", "author": []any{map[string]any{"family": "Cox-Buday"}}, "issued": map[string]any{"date-parts": []any{[]any{2017}}}}
	checked, err := st.SaveReference(ctx, "h", record, "checked")
	require.NoError(t, err)
	require.Equal(t, "coxbuday2017", checked.CiteKey)

	record["issued"] = map[string]any{"date-parts": []any{[]any{2018}}}
	again, err := st.SaveReference(ctx, "h", record, "checked")
	require.NoError(t, err)
	require.Equal(t, "coxbuday2017", again.CiteKey, "a checked key is already in papers")
}

func TestADescriptionOutlivesItsSource(t *testing.T) {
	st, _, ctx := open(t)
	require.NoError(t, st.Replace(ctx, book("a.pdf", "A", "hash-a"), oneChunk("ISBN 978-1-449-37332-0")))

	undescribed, err := st.UndescribedBooks(ctx)
	require.NoError(t, err)
	require.Len(t, undescribed, 1)
	require.Contains(t, undescribed[0].Head, "ISBN 978-1-449-37332-0")

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

func TestImportTakesAFileDescriptionOnlyWhenItIsNewer(t *testing.T) {
	st, _, ctx := open(t)
	existing, err := st.SaveReference(ctx, "h1", corpus.CSL{"title": "in the database", "author": []any{map[string]any{"family": "Knuth"}}}, "checked")
	require.NoError(t, err)

	older := corpus.Reference{Key: "h1", CiteKey: "knuth", CSL: corpus.CSL{"title": "older file"}, Status: "draft", UpdatedAt: existing.UpdatedAt.Add(-time.Hour)}
	restored := corpus.Reference{Key: "h2", CiteKey: "lost2020", CSL: corpus.CSL{"title": "restored"}, Status: "checked", UpdatedAt: existing.UpdatedAt}
	n, err := st.ImportReferences(ctx, []corpus.Reference{older, restored})
	require.NoError(t, err)
	require.EqualValues(t, 1, n)

	got, err := st.Reference(ctx, "h1")
	require.NoError(t, err)
	require.Equal(t, "in the database", got.CSL["title"])
	back, err := st.Reference(ctx, "h2")
	require.NoError(t, err)
	require.Equal(t, "lost2020", back.CiteKey, "a restored description keeps the key papers cite")
	require.True(t, back.UpdatedAt.Equal(existing.UpdatedAt), "and its own time")

	newer := corpus.Reference{Key: "h1", CiteKey: "knuth", CSL: corpus.CSL{"title": "edited by hand"}, Status: "checked", UpdatedAt: existing.UpdatedAt.Add(time.Hour)}
	n, err = st.ImportReferences(ctx, []corpus.Reference{newer})
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	got, err = st.Reference(ctx, "h1")
	require.NoError(t, err)
	require.Equal(t, "edited by hand", got.CSL["title"])
}

// A citation key another description already uses would break the table's
// uniqueness and the whole import with it; that one entry is skipped instead.
func TestImportSkipsADescriptionWhoseCitekeyIsTaken(t *testing.T) {
	st, _, ctx := open(t)
	_, err := st.SaveReference(ctx, "h1", corpus.CSL{"author": []any{map[string]any{"family": "Knuth"}}}, "checked")
	require.NoError(t, err)

	n, err := st.ImportReferences(ctx, []corpus.Reference{
		{Key: "h2", CiteKey: "knuth", CSL: corpus.CSL{"title": "clash"}, Status: "draft", UpdatedAt: time.Now()},
		{Key: "h3", CiteKey: "fine2021", CSL: corpus.CSL{"title": "fine"}, Status: "draft", UpdatedAt: time.Now()},
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	_, err = st.Reference(ctx, "h2")
	require.ErrorIs(t, err, store.ErrNoReference)
}

func TestTheBibliographySnapshotHoldsEveryDescriptionAndStyle(t *testing.T) {
	st, _, ctx := open(t)
	_, err := st.SaveReference(ctx, "h1", corpus.CSL{"title": "A"}, "draft")
	require.NoError(t, err)
	require.NoError(t, st.SaveStyle(ctx, "nature", "Nature", "<style/>"))

	n, err := st.ImportStyles(ctx, []corpus.StyleXML{{ID: "nature", Title: "Nature", XML: "<style/>"}, {ID: "cell", Title: "Cell", XML: "<style c/>"}})
	require.NoError(t, err)
	require.EqualValues(t, 1, n, "an identical style is not an import")

	var refs []corpus.Reference
	var styles []corpus.StyleXML
	require.NoError(t, st.WithBibliography(ctx, func(r []corpus.Reference, s []corpus.StyleXML) error {
		refs, styles = r, s
		return nil
	}))
	require.Len(t, refs, 1)
	require.Len(t, styles, 2)
}
