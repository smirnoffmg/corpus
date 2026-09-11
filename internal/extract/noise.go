package extract

import (
	"regexp"
	"strings"
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
)

var (
	dotLeader  = regexp.MustCompile(`\.\s?\.\s?\.\s?\.`)
	indexEntry = regexp.MustCompile(`^\s*\S.*,\s*\d+(\s*[,–-]\s*\d+)*\s*$`)
)

// frontOrBackMatter reports whether a page is contents, index or a near-empty
// divider. Such pages match any query that names a term the book covers, which
// is every query, and they never answer one.
func frontOrBackMatter(page string) bool {
	if len(strings.TrimSpace(page)) < minPageChars {
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
