package vaultcites_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smirnoffmg/corpus/internal/vaultcites"
)

var library = []string{
	"Machine Learning in Production (Kästner)",
	"The Scrum Guide 2020 (Schwaber, Sutherland, Scrum)",
	"Improving ML Attacks on LWE with Data Repetition and Stepwise Regression",
	"INSPIRED How to Create Tech Products Customers Love (Marty Cagan, Christian Idiodi, Lea Hickman etc.) (z-library.sk, 1lib.sk, z-lib.sk)",
	"Совершенный код (Макконнелл)",
}

func TestCountTellsCitationsOfTheLibraryFromTheRest(t *testing.T) {
	lines := []vaultcites.Line{
		{Note: "a.md", Text: "Как пишет Kästner (*Machine Learning in Production*, PDF 118), риск решает."},
		{Note: "a.md", Text: "*The Scrum Guide*, с. 5 и *The Scrum Guide*, с. 12"},
		{Note: "b.md", Text: "*Improving ML Attacks on LWE…*, с. 10"},
		{Note: "b.md", Text: "*INSPIRED*, CHAPTER 33: Principles of Product Discovery"},
		{Note: "c.md", Text: "*Совершенный код*, с. 460 (PDF 481)"},
		// Cited, but not a book the library holds.
		{Note: "c.md", Text: "*The Cool and the Cruel*, с. 20"},
		// Emphasis is not a citation: no locator follows it.
		{Note: "d.md", Text: "Это *важно*, и *Machine Learning in Production* тоже."},
	}

	got := vaultcites.Count(lines, library)

	require.Equal(t, vaultcites.Tally{Citations: 7, Library: 6, Sources: 5, Notes: 3}, got)
}

func TestAShortTitleDoesNotMatchEveryBookItBegins(t *testing.T) {
	got := vaultcites.Count([]vaultcites.Line{{Note: "a.md", Text: "*The*, с. 3"}}, library)
	require.Equal(t, vaultcites.Tally{Citations: 1}, got)
}
