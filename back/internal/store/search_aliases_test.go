package store_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

// A Russian note rarely repeats the English term it is about, which it keeps
// as an alias. The alias makes the note a candidate, through its first section
// only, and counts as a title for the title boost.
func TestAnAliasFindsItsNote(t *testing.T) {
	st, _, ctx := open(t)

	src := corpus.Source{Kind: "vault", Path: "__test__/Кросс-энтропия.md", Title: "Кросс-энтропия", Hash: "a1",
		Aliases: []string{"cross-entropy", "log loss"}}
	require.NoError(t, st.Replace(ctx, src, []corpus.Chunk{
		{Ord: 1, Heading: "Что это", Lang: "russian", Body: "Мера расхождения двух распределений."},
		{Ord: 2, Heading: "Пример", Lang: "russian", Body: "Классификатор ошибается уверенно."},
	}))

	hits, err := st.Search(ctx, corpus.Query{Text: "cross-entropy", Limit: 10, TitleBoost: 0.3})
	require.NoError(t, err)
	var mine []corpus.Hit
	for _, h := range hits {
		if h.Path == src.Path {
			mine = append(mine, h)
		}
	}
	require.Len(t, mine, 1, "the alias reaches the first section, not every section")
	require.Equal(t, "Что это", mine[0].Locator)
	require.Greater(t, mine[0].Rank, float32(0.3), "an alias match earns the title boost")

	hits, err = st.Search(ctx, corpus.Query{Text: "log losses", Limit: 10, TitleBoost: 0.3})
	require.NoError(t, err)
	found := false
	for _, h := range hits {
		found = found || h.Path == src.Path
	}
	require.True(t, found, "an alias is stemmed like the text")
}

// An alias is a name: a query it merely contains is not that name. A note on
// RLVR whose alias spells out "обучение с подкреплением с проверяемыми
// наградами" must not outrank the note on reinforcement learning itself.
func TestALongerAliasIsNotTheQuerysName(t *testing.T) {
	st, _, ctx := open(t)

	rl := corpus.Source{Kind: "vault", Path: "__test__/Обучение с подкреплением.md", Title: "Обучение с подкреплением", Hash: "r1"}
	rlvr := corpus.Source{Kind: "vault", Path: "__test__/RLVR.md", Title: "RLVR", Hash: "r2",
		Aliases: []string{"обучение с подкреплением с проверяемыми наградами"}}
	require.NoError(t, st.Replace(ctx, rl, []corpus.Chunk{{Ord: 1, Lang: "russian", Body: "Обучение с подкреплением: агент учится на наградах среды."}}))
	require.NoError(t, st.Replace(ctx, rlvr, []corpus.Chunk{{Ord: 1, Lang: "russian", Body: "Награда проверяется программой."}}))

	hits, err := st.Search(ctx, corpus.Query{Text: "обучение с подкреплением", Limit: 10, TitleBoost: 0.3})
	require.NoError(t, err)
	require.NotEmpty(t, hits)
	require.Equal(t, rl.Path, hits[0].Path)
	for _, h := range hits {
		if h.Path == rlvr.Path {
			require.Less(t, h.Rank, float32(0.3), "a longer alias earns no boost")
		}
	}
}
