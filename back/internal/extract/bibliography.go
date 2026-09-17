package extract

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	// A heading is a line that is the word and nothing else, so that "as
	// References to earlier work show" in a paragraph is not the start of one.
	bibHeading = regexp.MustCompile(`(?i)^\s*(?:\d+(?:\.\d+)*\.?\s+)?(references|bibliography|works cited|literature cited|reference list|список(?: использованной)? литературы|литература|библиография|библиографический список|источники|цитируемая литература)\s*:?\s*$`)
	// Where the list ends: the next thing a paper puts after it, as a heading —
	// the word alone, or "Appendix A" with a title after it. A continuation line
	// of an entry can begin with "Index" too.
	bibEnd = regexp.MustCompile(`(?i)^\s*(?:(?:appendix|приложение)(?:\s+[A-ZА-Я0-9]{1,3}\b.*)?|index|предметный указатель|acknowledge?ments?|благодарности|об авторах|about the authors?|author biograph(?:y|ies)|сведения об авторах)\s*:?\s*$`)
	// A numbered entry opens with its label. Three digits at most, so that a
	// continuation line starting "2004." is not read as entry 2004.
	bibLabel = regexp.MustCompile(`^(\[\d{1,3}\]|\(\d{1,3}\)|\d{1,3}\.)\s+\S`)
	// The run of bare page numbers a book's bibliography ends with: the pages
	// the work is cited on, which belong to that book and not to this work.
	// Two numbers at least, and no full stop after them, so a year ending the
	// entry is left alone.
	backReferences = regexp.MustCompile(`\s+\d{1,4}(,\s*\d{1,4})+\s*$`)
	pageNumberOnly = regexp.MustCompile(`^\s*[\divxlcdmIVXLCDM]+\s*$`)
	// A line that opens an author–year entry: a family name, possibly of several
	// words or with a particle, and then initials — "Alves, N.S.", "Arisholm E",
	// "Breiman L (1996)", "Клеппман, М.".
	//
	// Chicago spells the given names out: "Marsden, Peter V., and", "Neuman,
	// William Lawrence.".
	authorStart = regexp.MustCompile(`^(?:(?:van|von|de|der|den|da|di|du|del|la|le|dos|ten|ter)\s+)*\p{Lu}[\p{L}'’\-]+(?:[\s\-]\p{Lu}[\p{L}'’\-]+)*` +
		`(?:,\s*\p{Lu}\p{Ll}{0,2}\.` +
		`|,?\s+\p{Lu}{1,3}(?:[\s,(]|$)` +
		`|,\s*\p{Lu}\p{Ll}+(?:\s+\p{Lu}\p{Ll}+)?(?:\s+\p{Lu}\.)?(?:[.,]|\s+(?:and|&)))`)
	// What a line of an author list ends with when the list goes on to the next.
	listGoesOn  = regexp.MustCompile(`(?:[,&;\-]|\s(?:and|и))$|\p{Ll}$`)
	labelNumber = regexp.MustCompile(`\d+`)
	// A heading in capitals with its letters spaced out, and its section number.
	letterSpaced = regexp.MustCompile(`^(\s*(?:\d+(?:\.\d+)*\.?\s+|[IVX]+\.?\s+)?)(\p{Lu}(?: ?\p{Lu})+)\s*$`)
	// A line ending inside a DOI: its prefix and a suffix that stops at a dot,
	// slash or hyphen, where typesetters break it.
	brokenDOI = regexp.MustCompile(`10\.\d{4,9}/\S*[./_-]$`)
	// A line that closes an entry: a full stop, a bracket or quote, or the last
	// digit of a page range — Springer ends an entry without a stop.
	endsEntry = regexp.MustCompile(`(?:[.)\]”"]|\d)$`)
	// What a paper prints once its list is over, recognised only after a line
	// that closed an entry, since a wrapped title can look like any of them:
	// an appendix lettered without the word ("A Author contributions",
	// "G.3 Onboarding call") ...
	appendixSection = regexp.MustCompile(`^[A-Z](?:\.\d+)*\s+\p{Lu}[^.,;:()\[\]\d]{2,60}$`)
	// ... and an author's biography.
	biography = regexp.MustCompile(`^(?:\p{Lu}[\p{L}'’\-]*\.?\s+){1,4}(?:is|was|received|joined|holds|has been|works|serves|obtained|earned)\b`)
)

// isBibHeading also reads a heading set in small capitals, which a PDF gives
// back letter-spaced: "R EFERENCES".
func isBibHeading(line string) bool {
	if bibHeading.MatchString(line) {
		return true
	}
	m := letterSpaced.FindStringSubmatch(line)
	return len(m) == 3 && bibHeading.MatchString(m[1]+strings.ReplaceAll(m[2], " ", ""))
}

// capitalsHeading is a heading set in capitals: several words, no digits and
// no punctuation a reference would carry.
func capitalsHeading(line string) bool {
	if strings.ContainsAny(line, ".,;0123456789") || len(strings.Fields(line)) < 2 {
		return false
	}
	var upper, all int
	for _, r := range line {
		if unicode.IsLetter(r) {
			all++
			if unicode.IsUpper(r) {
				upper++
			}
		}
	}
	return all >= 8 && upper == all
}

// listOver reports a line that begins whatever follows the list.
func listOver(line string) bool {
	return bibEnd.MatchString(line) || appendixSection.MatchString(line) ||
		capitalsHeading(line) || biography.MatchString(line)
}

// ReferenceParser is the version of what Bibliography and cite.ParseCitation
// make of a list. Raise it whenever either reads a list differently, and every
// paper read before is read again on the next pass.
const ReferenceParser = 4

// minReferenceListChars is what a section has to hold before it is offered as
// one unparsed entry: less than this is a stray heading, not a list.
const minReferenceListChars = 100

// Bibliography finds a publication's list of references and cuts it into
// entries. start is the index of the page the list begins on, so the pages from
// there can be kept out of the text index — a bibliography answers no query and
// matches every one — and -1 when the publication has no list.
//
// Each entry is returned as printed, with its lines joined: what is parsed out
// of it can be wrong, and the line itself is what a reader corrects it against.
func Bibliography(pages []string) (start int, entries []string) {
	span, entries, ok := locate(pages)
	if !ok {
		return -1, nil
	}
	return span.fromPage, entries
}

// listSpan is where a reference list sits: from its heading to the first line
// of whatever follows it, as page and line indexes. A list that runs to the end
// of the document ends at page len(pages).
type listSpan struct {
	fromPage, fromLine int
	toPage, toLine     int
}

func locate(pages []string) (listSpan, []string, bool) {
	span, lines, found := referenceLines(pages)
	if !found {
		return span, nil, false
	}
	entries := cut(lines)
	if len(entries) > 0 {
		return span, entries, true
	}
	// The heading is there and the cut did not take. Handing the section back
	// whole is worse than entries and better than losing it.
	whole := join(lines)
	if len(whole) < minReferenceListChars {
		return span, nil, false
	}
	return span, []string{whole}, true
}

// referenceLines is the text of the list: from the last bibliography heading in
// the document to the next section or the end.
func referenceLines(pages []string) (listSpan, []string, bool) {
	span := listSpan{fromPage: -1, toPage: len(pages)}
	for i, page := range pages {
		for j, line := range strings.Split(page, "\n") {
			if isBibHeading(line) {
				span.fromPage, span.fromLine = i, j
			}
		}
	}
	if span.fromPage < 0 {
		return span, nil, false
	}

	running := runningLines(pages[span.fromPage:])
	var lines []string
	previous := ""
	for i := span.fromPage; i < len(pages); i++ {
		for j, line := range strings.Split(visible(pages[i]), "\n") {
			if i == span.fromPage && j <= span.fromLine {
				continue
			}
			text := strings.TrimSpace(line)
			if bibEnd.MatchString(line) || (endsEntry.MatchString(previous) && listOver(text)) {
				span.toPage, span.toLine = i, j
				return span, lines, true
			}
			// A running head repeats the heading on every page of the list.
			if isBibHeading(line) || pageNumberOnly.MatchString(line) || running[pageFree(line)] {
				continue
			}
			lines = append(lines, strings.TrimRight(line, " \t"))
			if text != "" {
				previous = text
			}
		}
	}
	return span, lines, true
}

// maxRunningLine is longer than any header or footer a paper prints; a longer
// line that repeats is text quoted twice, not furniture.
const maxRunningLine = 100

// minRunningLetters is what makes a recurring line furniture: a header or
// footer is words, while a page range alone on a line recurs as well and
// belongs to an entry.
const minRunningLetters = 3

// runningLines finds the headers and footers of a list that runs over several
// pages: short lines that recur, page number aside, on most of them.
func runningLines(pages []string) map[string]bool {
	if len(pages) < 2 {
		return nil
	}
	seen := map[string]int{}
	for _, page := range pages {
		onPage := map[string]bool{}
		for _, line := range strings.Split(visible(page), "\n") {
			if key := pageFree(line); letters(key) >= minRunningLetters && len(line) <= maxRunningLine {
				onPage[key] = true
			}
		}
		for key := range onPage {
			seen[key]++
		}
	}
	running := map[string]bool{}
	for key, n := range seen {
		if n >= max(2, (len(pages)+1)/2) {
			running[key] = true
		}
	}
	return running
}

func letters(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			n++
		}
	}
	return n
}

// pageFree is a line without its digits and spacing, so that a footer compares
// equal to itself on the next page.
func pageFree(line string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsDigit(r) || unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, line)
}

// visible drops the format characters a word processor leaves in the text: a
// zero-width space reads as a space, and a soft hyphen is not a character of the
// word it sits in.
func visible(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\u00ad':
			return -1
		case unicode.Is(unicode.Cf, r):
			return ' '
		}
		return r
	}, s)
}

// cut splits the list into entries by whichever of the three shapes a
// bibliography comes in explains it: numbered labels, a hanging indent, or a
// blank line between entries.
func cut(lines []string) []string {
	for _, shape := range []func([]string) []int{labelled, hanging, authorYear} {
		if starts := shape(lines); len(starts) > 1 {
			return entriesAt(lines, starts)
		}
	}
	return entriesAt(lines, blankSeparated(lines))
}

// authorYear reads an unnumbered list with no indent left to go by, which is
// what pdftotext -raw makes of every one: an entry starts on a line shaped like
// a name, after a line that finished the entry before. A line that ends in a
// comma, an ampersand, a hyphen or mid-word is an author list that wraps, and
// the name on the next line is its continuation, not a new entry.
func authorYear(lines []string) []int {
	var starts []int
	previous := ""
	for i, line := range lines {
		text := strings.TrimSpace(line)
		if text == "" {
			continue
		}
		if authorStart.MatchString(text) && (previous == "" || !listGoesOn.MatchString(previous)) {
			starts = append(starts, i)
		}
		previous = text
	}
	return starts
}

// labelled reads a numbered list. The label has to sit at the list's own left
// margin: a continuation line that happens to begin "12." is text, not entry 12.
//
// A list numbers from one, so a first label that is not 1 means the numbers are
// something else — volumes or page counts opening continuation lines.
func labelled(lines []string) []int {
	margin := leftMargin(lines)
	var starts []int
	for i, line := range lines {
		text := strings.TrimSpace(line)
		if indent(line) != margin || !bibLabel.MatchString(text) {
			continue
		}
		if len(starts) == 0 && labelNumber.FindString(text) != "1" {
			return nil
		}
		starts = append(starts, i)
	}
	return starts
}

// hanging reads the shape an unnumbered bibliography is set in: an entry starts
// at the left margin and its continuations are indented under it.
func hanging(lines []string) []int {
	margin := leftMargin(lines)
	if margin < 0 {
		return nil
	}

	var starts, deeper []int
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if indent(line) == margin {
			starts = append(starts, i)
		} else {
			deeper = append(deeper, i)
		}
	}
	// Without indented continuations every line is at the margin, and "one line,
	// one entry" would cut every wrapped entry in half.
	if len(deeper) == 0 {
		return nil
	}
	return starts
}

// leftMargin is the indent of the shallowest line, which is where an entry
// begins; -1 when there is no text at all.
func leftMargin(lines []string) int {
	margin := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if n := indent(line); margin < 0 || n < margin {
			margin = n
		}
	}
	return margin
}

func blankSeparated(lines []string) []int {
	starts, blank := []int{}, true
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			blank = true
			continue
		}
		if blank {
			starts = append(starts, i)
			blank = false
		}
	}
	return starts
}

func entriesAt(lines []string, starts []int) []string {
	entries := make([]string, 0, len(starts))
	for i, from := range starts {
		to := len(lines)
		if i+1 < len(starts) {
			to = starts[i+1]
		}
		if entry := join(lines[from:to]); entry != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}

// join puts an entry's lines back into one line: a word broken by the
// typesetter is made whole again, and the book's own back references go.
func join(lines []string) string {
	var b strings.Builder
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		current := b.String()
		switch {
		case current == "":
		case brokenDOI.MatchString(current) && !strings.ContainsAny(firstWord(line), "()"):
			// A DOI wraps at its dots and slashes; a space would cut it in two.
		case strings.HasSuffix(current, "-") && startsLower(line):
			b.Reset()
			b.WriteString(strings.TrimSuffix(current, "-"))
		default:
			b.WriteByte(' ')
		}
		b.WriteString(line)
	}
	return strings.TrimSpace(backReferences.ReplaceAllString(b.String(), ""))
}

func firstWord(s string) string {
	if fields := strings.Fields(s); len(fields) > 0 {
		return fields[0]
	}
	return ""
}

func startsLower(s string) bool {
	for _, r := range s {
		return unicode.IsLower(r)
	}
	return false
}

func indent(line string) int {
	n := 0
	for _, r := range line {
		switch r {
		case ' ':
			n++
		case '\t':
			n += 4
		default:
			return n
		}
	}
	return n
}
