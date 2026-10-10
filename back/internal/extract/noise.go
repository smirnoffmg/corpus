package extract

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

const (
	// minPageChars is below what a page can still be worth citing. A part title
	// or a "this page intentionally left blank" carries no content of its own —
	// and with neighbour windowing its vector would describe the pages around
	// it, which is worse than not having it.
	minPageChars = 150
	// dotLeaderShare of lines running to a page number makes a contents page.
	dotLeaderShare = 0.3
	// indexEntryShare of lines shaped "term, 12, 34" makes a subject index.
	indexEntryShare = 0.5
	indexMinLines   = 10
	// foreignLetterShare above which a page is a mis-decoded font rather than
	// text. This library is Russian and English; a page of "ɤɢɧɭɬɵɣ ɝɨɪɲɨɤ" or
	// "Î÷åâèäíî, ÷òî" is Cyrillic read through the wrong encoding, and no query
	// will ever match it. Measured against the corpus: real pages with formulas
	// and Greek sit below 0.3, whole broken books sit above it.
	foreignLetterShare = 0.3
	// vowelShare of a page's characters, digits and spaces aside, below which it
	// is not prose in either alphabet: a text layer whose fonts decode to
	// punctuation, or to letters substituted one for one ("wnénxzjtvkrq" for
	// "перестановки"). Measured against the library: real pages sit at 0.38, a
	// broken layer at 0.07. Below 0.12 also fall contents pages, decimal tables,
	// figures drawn in characters and the odd page of display formulas: about
	// 0.1% of real pages, and no real book loses enough to pass for a scan.
	vowelShare = 0.12
	// A ToUnicode map that names glyph indices in standard TrueType order
	// (space is glyph 3) writes ASCII glyphOrderShift code points down, and code
	// on such a page can lift its vowel share above vowelShare. Moved back up,
	// those pages read at 0.27–0.42, and no real page above 0.22 (issue #10).
	glyphOrderShift   = 29
	shiftedVowelShare = 0.25
	// minLettersToJudge keeps the rules off pages too short to measure.
	minLettersToJudge = 50
	// scanShare of a PDF's pages with text, below which it is a scan. A text
	// cover or a few pages of front matter in front of page images do not make
	// a book, and a few blank pages, plates or figures do not make a scan.
	scanShare = 0.5
)

// LayerRules is the version of the rules that judge a PDF's text layer. Raise
// it whenever unreadable, TextPages or Scan judge a page differently, and every
// PDF indexed under an older one is read again on the next pass.
const LayerRules = 1

var (
	dotLeader  = regexp.MustCompile(`\.\s?\.\s?\.\s?\.`)
	indexEntry = regexp.MustCompile(`^\s*\S.*,\s*\d+(\s*[,–-]\s*\d+)*\s*$`)
)

const vowels = "aeiouyаеёиоуыэюя"

// unreadable reports whether a page is mojibake, letters that belong to neither
// alphabet this library is written in, a layer that decodes to no language, or
// one that reads as a language only once its ASCII is moved back up by the
// glyph order.
func unreadable(page string) bool {
	var letters, foreign, chars, vowel, shiftedChars, shiftedVowel int
	for _, r := range page {
		s := r
		if r < unicode.MaxASCII {
			s = r + glyphOrderShift
		}
		if !unicode.IsSpace(s) && !unicode.IsDigit(s) {
			shiftedChars++
			if strings.ContainsRune(vowels, unicode.ToLower(s)) {
				shiftedVowel++
			}
		}
		if unicode.IsSpace(r) || unicode.IsDigit(r) {
			continue
		}
		chars++
		if strings.ContainsRune(vowels, unicode.ToLower(r)) {
			vowel++
		}
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		if r >= unicode.MaxASCII && !unicode.Is(unicode.Cyrillic, r) {
			foreign++
		}
	}
	share := func(n, of int) float64 { return float64(n) / float64(of) }
	return letters >= minLettersToJudge && share(foreign, letters) > foreignLetterShare ||
		chars >= minLettersToJudge && share(vowel, chars) < vowelShare ||
		shiftedChars >= minLettersToJudge && share(shiftedVowel, shiftedChars) > shiftedVowelShare &&
			share(shiftedVowel, shiftedChars) > share(vowel, chars)
}

// TextPages marks the pages of a PDF that have text the splitter could cite;
// the others are images, or text no query will reach.
func TextPages(pages []string) []bool {
	text := make([]bool, len(pages))
	for i, page := range pages {
		text[i] = !tooShort(page) && !unreadable(page)
	}
	return text
}

// Scan reports whether a PDF's pages are mostly images.
func Scan(pages []string) bool {
	withText := 0
	for _, text := range TextPages(pages) {
		if text {
			withText++
		}
	}
	return few(withText, len(pages))
}

// Thin reports whether chunks cite too few of a PDF's pages for it to be a
// book with text: the cheap test, before its pages are read again.
func Thin(chunks []corpus.Chunk, pages int) bool {
	cited := map[int]bool{}
	for _, c := range chunks {
		cited[c.Page] = true
	}
	return few(len(cited), pages)
}

func few(withText, pages int) bool {
	return float64(withText) < scanShare*float64(pages)
}

// frontOrBackMatter reports whether a page is contents, index or a near-empty
// divider. Such pages match any query that names a term the book covers, which
// is every query, and they never answer one.
// tooShort marks a fragment that cannot be worth citing on its own. It is
// checked on every part a page or a section is cut into, not only on the whole:
// splitting a page leaves its running head and footer as parts of their own, and
// those carry nothing but the page number.
func tooShort(text string) bool {
	return len(strings.TrimSpace(text)) < minPageChars
}

func frontOrBackMatter(page string) bool {
	if tooShort(page) {
		return true
	}

	lines := strings.Split(page, "\n")
	var leaders, entries, nonEmpty int
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		nonEmpty++
		if dotLeader.MatchString(line) {
			leaders++
		}
		if indexEntry.MatchString(line) {
			entries++
		}
	}
	if nonEmpty == 0 {
		return true
	}

	share := func(n int) float64 { return float64(n) / float64(nonEmpty) }
	return share(leaders) > dotLeaderShare ||
		(nonEmpty > indexMinLines && share(entries) > indexEntryShare)
}
