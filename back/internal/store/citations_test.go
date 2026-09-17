package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smirnoffmg/corpus/internal/corpus"
	"github.com/smirnoffmg/corpus/internal/store"
)

func paper(path, title, hash string) corpus.Source {
	return corpus.Source{Kind: "paper", Path: path, Title: title, Hash: hash}
}

func describe(t *testing.T, st *store.Store, ctx context.Context, key string, csl corpus.CSL) {
	t.Helper()
	_, err := st.SaveReference(ctx, key, csl, "checked")
	require.NoError(t, err)
}

func cited(ord int, raw, fingerprint string) corpus.Citation {
	return corpus.Citation{Ord: ord, Raw: raw, Fingerprint: fingerprint}
}

func TestUnparsedPapersAreThoseWithoutAReferenceList(t *testing.T) {
	st, _, ctx := open(t)
	require.NoError(t, st.Replace(ctx, paper("a.pdf", "A", "pa"), oneChunk("текст статьи один")))
	require.NoError(t, st.Replace(ctx, paper("b.pdf", "B", "pb"), oneChunk("текст статьи два")))
	require.NoError(t, st.Replace(ctx, book("c.pdf", "C", "bc"), oneChunk("текст книги")))

	unparsed, err := st.UnparsedPapers(ctx, 1)
	require.NoError(t, err)
	require.Len(t, unparsed, 2, "a book has no reference list to read")

	require.NoError(t, st.SaveCitations(ctx, "pa", 1, []corpus.Citation{cited(1, "[1] Работа", "fp-1")}))
	// A paper with no references at all is parsed, not unparsed: without the
	// record it would be read again every pass.
	require.NoError(t, st.SaveCitations(ctx, "pb", 1, nil))

	unparsed, err = st.UnparsedPapers(ctx, 1)
	require.NoError(t, err)
	require.Empty(t, unparsed)
}

func TestCitationsSurviveTheFileBeingRenamed(t *testing.T) {
	st, _, ctx := open(t)
	require.NoError(t, st.Replace(ctx, paper("old.pdf", "A", "pa"), oneChunk("текст статьи")))
	require.NoError(t, st.SaveCitations(ctx, "pa", 1, []corpus.Citation{cited(1, "[1] Работа", "fp-1")}))

	require.NoError(t, st.Rename(ctx, "old.pdf", "new.pdf", "A"))

	got, err := st.Citations(ctx, "pa")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "[1] Работа", got[0].Raw)
}

// The point of matching: a citation says where in the library the work is.
func TestAResolvedCitationCarriesTheSourceItWasMatchedTo(t *testing.T) {
	st, _, ctx := open(t)
	require.NoError(t, st.Replace(ctx, book("uploads/kleppmann.pdf", "Клеппман", "bk"), oneChunk("текст книги")))
	describe(t, st, ctx, "bk", corpus.CSL{"title": "Designing data-intensive applications",
		"issued": map[string]any{"date-parts": []any{[]any{2017}}}})
	require.NoError(t, st.SaveCitations(ctx, "pa", 1, []corpus.Citation{
		{Ord: 1, Raw: "[1] Kleppmann", Title: "Designing Data-Intensive Applications", Year: 2017, Fingerprint: "fp-1"},
	}))

	_, err := st.ResolveCitations(ctx)
	require.NoError(t, err)

	got, err := st.Citations(ctx, "pa")
	require.NoError(t, err)
	require.Equal(t, "book", got[0].ResolvedKind)
	require.Equal(t, "uploads/kleppmann.pdf", got[0].ResolvedPath)
	require.Equal(t, "Клеппман", got[0].ResolvedTitle)
}

func TestSavingCitationsAgainReplacesThePreviousReading(t *testing.T) {
	st, _, ctx := open(t)
	require.NoError(t, st.SaveCitations(ctx, "pa", 1, []corpus.Citation{
		cited(1, "[1] Первая", "fp-1"), cited(2, "[2] Вторая", "fp-2"),
	}))
	require.NoError(t, st.SaveCitations(ctx, "pa", 1, []corpus.Citation{cited(1, "[1] Одна", "fp-1")}))

	got, err := st.Citations(ctx, "pa")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "[1] Одна", got[0].Raw)
}

func TestCitingFindsThePapersThatPointAtAWork(t *testing.T) {
	st, _, ctx := open(t)
	require.NoError(t, st.Replace(ctx, paper("a.pdf", "Статья A", "pa"), oneChunk("текст статьи один")))
	require.NoError(t, st.Replace(ctx, paper("b.pdf", "Статья B", "pb"), oneChunk("текст статьи два")))
	doi := "10.1145/371920.372095"
	require.NoError(t, st.SaveCitations(ctx, "pa", 1, []corpus.Citation{cited(1, "[1] Melnik", doi)}))
	require.NoError(t, st.SaveCitations(ctx, "pb", 1, []corpus.Citation{
		cited(1, "[1] Другая работа", "fp-other"),
		cited(2, "[2] Melnik, Sergey", doi),
	}))

	citing, err := st.Citing(ctx, "", doi)
	require.NoError(t, err)
	require.Len(t, citing, 2)
	require.Equal(t, "a.pdf", citing[0].Path)
	require.Equal(t, "Статья A", citing[0].Title)
	require.Equal(t, "b.pdf", citing[1].Path)
	require.Equal(t, 2, citing[1].Citation.Ord)
}

func TestSharedCitationsAreWhatTwoPapersBothPointAt(t *testing.T) {
	st, _, ctx := open(t)
	require.NoError(t, st.SaveCitations(ctx, "pa", 1, []corpus.Citation{
		cited(1, "[1] Общая работа", "fp-shared"), cited(2, "[2] Только у A", "fp-a"),
	}))
	require.NoError(t, st.SaveCitations(ctx, "pb", 1, []corpus.Citation{
		cited(1, "[1] Только у B", "fp-b"), cited(2, "[2] Общая работа, иначе набранная", "fp-shared"),
	}))

	shared, err := st.SharedCitations(ctx, "pa", "pb")
	require.NoError(t, err)
	require.Len(t, shared, 1)
	require.Equal(t, "[1] Общая работа", shared[0].Raw)
}

func TestResolveCitationsMatchesTheLibraryByIdentifierAndByTitle(t *testing.T) {
	st, _, ctx := open(t)
	require.NoError(t, st.SaveCitations(ctx, "pa", 1, []corpus.Citation{
		{Ord: 1, Raw: "[1] Melnik", DOI: "10.1145/371920.372095", Fingerprint: "10.1145/371920.372095"},
		{Ord: 2, Raw: "[2] Kleppmann", Title: "Designing Data-Intensive Applications!", Year: 2017, Fingerprint: "fp-2"},
		{Ord: 3, Raw: "[3] Kleppmann, другое издание", Title: "Designing Data-Intensive Applications", Year: 2099, Fingerprint: "fp-3"},
		{Ord: 4, Raw: "[4] Никому не известная работа", Fingerprint: "fp-4"},
	}))
	describe(t, st, ctx, "melnik", corpus.CSL{
		"title": "Building a distributed full-text index", "DOI": "10.1145/371920.372095"})
	describe(t, st, ctx, "kleppmann", corpus.CSL{
		"title":  "Designing data-intensive applications",
		"issued": map[string]any{"date-parts": []any{[]any{2017}}}})

	n, err := st.ResolveCitations(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 2, n)

	got, err := st.Citations(ctx, "pa")
	require.NoError(t, err)
	require.Equal(t, "melnik", got[0].Resolved)
	require.Equal(t, "doi", got[0].MatchedBy)
	require.Equal(t, "kleppmann", got[1].Resolved)
	require.Empty(t, got[1].ResolvedPath, "a description the library has no file for still resolves")
	require.Equal(t, "title", got[1].MatchedBy, "punctuation and case are not a different work")
	require.Empty(t, got[2].Resolved, "the same title in another year is another work")
	require.Empty(t, got[3].Resolved)
}

// The library grows, so resolution is not a one-off: an entry that found
// nothing today should find the book uploaded tomorrow.
func TestResolveCitationsRunsAgainAsTheLibraryGrows(t *testing.T) {
	st, _, ctx := open(t)
	require.NoError(t, st.SaveCitations(ctx, "pa", 1, []corpus.Citation{
		{Ord: 1, Raw: "[1] Lamport", Title: "Time, clocks, and the ordering of events", Year: 1978, Fingerprint: "fp-1"},
	}))

	n, err := st.ResolveCitations(ctx)
	require.NoError(t, err)
	require.Zero(t, n)

	describe(t, st, ctx, "lamport", corpus.CSL{
		"title":  "Time, clocks, and the ordering of events",
		"issued": map[string]any{"date-parts": []any{[]any{1978}}}})

	n, err = st.ResolveCitations(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)

	n, err = st.ResolveCitations(ctx)
	require.NoError(t, err)
	require.Zero(t, n, "a second pass over settled citations should write nothing")
}

// A better parser should reach the papers read by a worse one without anyone
// having to clear anything: a list read by an older version is unread.
func TestPapersReadByAnOlderParserAreReadAgain(t *testing.T) {
	st, _, ctx := open(t)
	require.NoError(t, st.Replace(ctx, paper("a.pdf", "A", "pa"), oneChunk("текст статьи")))
	require.NoError(t, st.SaveCitations(ctx, "pa", 1, []corpus.Citation{cited(1, "[1] Работа", "fp-1")}))

	unparsed, err := st.UnparsedPapers(ctx, 1)
	require.NoError(t, err)
	require.Empty(t, unparsed)

	unparsed, err = st.UnparsedPapers(ctx, 2)
	require.NoError(t, err)
	require.Len(t, unparsed, 1)
	require.Equal(t, "pa", unparsed[0].Hash)
}

// Filling in a list from a registry is not reading it again: the parser that
// read it stays on record, and so do the matches already made.
func TestUpdatingCitationsKeepsTheirParserAndMatches(t *testing.T) {
	st, _, ctx := open(t)
	require.NoError(t, st.Replace(ctx, paper("a.pdf", "A", "pa"), oneChunk("текст статьи")))
	require.NoError(t, st.SaveCitations(ctx, "pa", 2, []corpus.Citation{cited(1, "[1] Работа", "fp-1")}))

	got, err := st.Citations(ctx, "pa")
	require.NoError(t, err)
	got[0].Title, got[0].Resolved, got[0].MatchedBy = "Работа", "bk", "doi"
	require.NoError(t, st.UpdateCitations(ctx, "pa", got))

	unparsed, err := st.UnparsedPapers(ctx, 2)
	require.NoError(t, err)
	require.Empty(t, unparsed)
	got, err = st.Citations(ctx, "pa")
	require.NoError(t, err)
	require.Equal(t, "Работа", got[0].Title)
	require.Equal(t, "bk", got[0].Resolved)
}

// A paper's own list can name its own proceedings volume, or the paper itself
// in a later version; neither makes it a citation of itself.
func TestACitationNeverResolvesToItsOwnPaper(t *testing.T) {
	st, _, ctx := open(t)
	describe(t, st, ctx, "pa", corpus.CSL{"type": "book", "ISBN": "9781450335317", "title": "Proceedings"})
	require.NoError(t, st.SaveCitations(ctx, "pa", 1, []corpus.Citation{
		{Ord: 1, Raw: "[1] Other paper. ISBN 978-1-4503-3531-7.", ISBN: "9781450335317", Fingerprint: "isbn:9781450335317"},
	}))

	n, err := st.ResolveCitations(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
}

// An ISBN names a book. A description of an article that carries one — its
// proceedings' — is not what a citation with that ISBN points at.
func TestAnISBNResolvesOnlyToABook(t *testing.T) {
	st, _, ctx := open(t)
	describe(t, st, ctx, "article", corpus.CSL{"type": "article-journal", "ISBN": "9781450356633", "title": "A paper"})
	require.NoError(t, st.SaveCitations(ctx, "pa", 1, []corpus.Citation{
		{Ord: 1, Raw: "[1] Another paper of the volume.", ISBN: "9781450356633", Fingerprint: "isbn:9781450356633"},
	}))

	n, err := st.ResolveCitations(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
}

func pageOne(body string) []corpus.Chunk {
	return []corpus.Chunk{
		{Ord: 1, Page: 1, Lang: "english", Body: body},
		{Ord: 2, Page: 2, Lang: "english", Body: "Later text where any phrase at all may appear, such as Natural language processing for code comments."},
	}
}

// A paper prints its own title at the top of its first page, so a citation is
// matched to it without any description: the library knows what it holds by
// what the file says, and a draft rarely knows the year.
func TestACitationIsMatchedByTheTitleOnAFirstPage(t *testing.T) {
	st, _, ctx := open(t)
	require.NoError(t, st.Replace(ctx, paper("uploads/sculley2015.pdf", "sculley2015", "ps"),
		pageOne("Hidden Technical Debt in Machine Learning Systems\nD. Sculley, Gary Holt\nGoogle, Inc.")))
	require.NoError(t, st.SaveCitations(ctx, "pa", 1, []corpus.Citation{
		{Ord: 1, Raw: "[1] D. Sculley et al.", Title: "Hidden technical debt in machine learning systems", Year: 2015, Fingerprint: "fp-1"},
		{Ord: 2, Raw: "[2] Natural language processing.", Title: "Natural language processing", Fingerprint: "fp-2"},
		{Ord: 3, Raw: "[3] A later phrase.", Title: "Natural language processing for code comments", Fingerprint: "fp-3"},
	}))

	n, err := st.ResolveCitations(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)

	got, err := st.Citations(ctx, "pa")
	require.NoError(t, err)
	require.Equal(t, "ps", got[0].Resolved)
	require.Equal(t, "page", got[0].MatchedBy)
	require.Equal(t, "uploads/sculley2015.pdf", got[0].ResolvedPath)
	require.Empty(t, got[1].Resolved, "three words are a phrase any abstract can hold")
	require.Empty(t, got[2].Resolved, "a title has to stand at the top of the page, not further in")

	citing, err := st.Citing(ctx, "ps", "")
	require.NoError(t, err)
	require.Len(t, citing, 0, "the citing paper is not a source here, so it is not listed")
}

// The same work kept twice — a book-shelf copy and a publication — is one
// work; the publication is the one with a card and a reference list.
func TestAFirstPageMatchPrefersThePublication(t *testing.T) {
	st, _, ctx := open(t)
	title := "Hidden Technical Debt in Machine Learning Systems\nD. Sculley"
	require.NoError(t, st.Replace(ctx, book("Hidden Technical Debt.pdf", "Hidden", "bs"), pageOne(title)))
	require.NoError(t, st.Replace(ctx, paper("uploads/sculley2015.pdf", "sculley2015", "ps"), pageOne(title)))
	require.NoError(t, st.SaveCitations(ctx, "pa", 1, []corpus.Citation{
		{Ord: 1, Raw: "[1] D. Sculley et al.", Title: "Hidden technical debt in machine learning systems", Fingerprint: "fp-1"},
	}))

	_, err := st.ResolveCitations(ctx)
	require.NoError(t, err)
	got, err := st.Citations(ctx, "pa")
	require.NoError(t, err)
	require.Equal(t, "ps", got[0].Resolved)
}

// An identifier is exact, and a title on a page is only very likely.
func TestADescriptionMatchByDOIOutranksAFirstPage(t *testing.T) {
	st, _, ctx := open(t)
	require.NoError(t, st.Replace(ctx, paper("copy.pdf", "copy", "pc"),
		pageOne("Hidden Technical Debt in Machine Learning Systems\nD. Sculley")))
	describe(t, st, ctx, "original", corpus.CSL{"title": "Hidden technical debt", "DOI": "10.5555/2969442.2969519"})
	require.NoError(t, st.SaveCitations(ctx, "pa", 1, []corpus.Citation{
		{Ord: 1, Raw: "[1] Sculley.", Title: "Hidden technical debt in machine learning systems", DOI: "10.5555/2969442.2969519", Fingerprint: "10.5555/2969442.2969519"},
	}))

	_, err := st.ResolveCitations(ctx)
	require.NoError(t, err)
	got, err := st.Citations(ctx, "pa")
	require.NoError(t, err)
	require.Equal(t, "original", got[0].Resolved)
	require.Equal(t, "doi", got[0].MatchedBy)
}

// The same file can sit on two shelves — a book-shelf copy of a paper — under
// one content hash. It is one work, and it is listed once.
func TestAWorkKeptOnTwoShelvesIsListedOnce(t *testing.T) {
	st, _, ctx := open(t)
	title := "Hidden Technical Debt in Machine Learning Systems\nD. Sculley"
	require.NoError(t, st.Replace(ctx, book("Hidden Technical Debt.pdf", "Hidden", "same"), pageOne(title)))
	require.NoError(t, st.Replace(ctx, paper("uploads/sculley2015.pdf", "Sculley 2015", "same"), pageOne(title)))
	require.NoError(t, st.Replace(ctx, paper("citing.pdf", "Citing", "pa"), oneChunk("текст цитирующей статьи")))
	require.NoError(t, st.SaveCitations(ctx, "pa", 1, []corpus.Citation{
		{Ord: 1, Raw: "[1] D. Sculley et al.", Title: "Hidden technical debt in machine learning systems", Fingerprint: "fp-1"},
	}))
	_, err := st.ResolveCitations(ctx)
	require.NoError(t, err)

	cited, err := st.Citations(ctx, "pa")
	require.NoError(t, err)
	require.Len(t, cited, 1)
	require.Equal(t, "paper", cited[0].ResolvedKind, "the publication, which has a card")

	citing, err := st.Citing(ctx, "same", "")
	require.NoError(t, err)
	require.Len(t, citing, 1)
}

// A review's two lists are numbered each from one, and kept apart.
func TestAReviewKeepsItsTwoListsApart(t *testing.T) {
	st, _, ctx := open(t)
	require.NoError(t, st.SaveCitations(ctx, "pa", 1, []corpus.Citation{
		{List: "references", Ord: 1, Raw: "[1] A reference.", Fingerprint: "fp-1"},
		{List: "primary", Ord: 1, Raw: "[P1] A study.", Label: "[P1]", Fingerprint: "fp-2"},
		{List: "primary", Ord: 2, Raw: "[P2] Another study.", Label: "[P2]", Fingerprint: "fp-3"},
	}))

	got, err := st.Citations(ctx, "pa")
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, "references", got[0].List)
	require.Equal(t, "primary", got[1].List)
	require.Equal(t, "[P2] Another study.", got[2].Raw)
}
