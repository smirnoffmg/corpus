package extract

import (
	"regexp"
	"strings"
	"unicode"
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
	// minLettersToJudge keeps the rule off pages too short to measure.
	minLettersToJudge = 50
)

var (
	dotLeader  = regexp.MustCompile(`\.\s?\.\s?\.\s?\.`)
	indexEntry = regexp.MustCompile(`^\s*\S.*,\s*\d+(\s*[,–-]\s*\d+)*\s*$`)
)

// unreadable reports whether a page is mojibake: letters that belong to neither
// alphabet this library is written in.
func unreadable(page string) bool {
	var letters, foreign int
	for _, r := range page {
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		if r >= unicode.MaxASCII && !unicode.Is(unicode.Cyrillic, r) {
			foreign++
		}
	}
	return letters >= minLettersToJudge && float64(foreign)/float64(letters) > foreignLetterShare
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
