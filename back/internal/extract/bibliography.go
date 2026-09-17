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
	// continuation line starting "2004." is not read as entry 2004. A review
	// labels the studies it lists with a letter or two: [P1], [PS1], [SP1].
	bibLabel = regexp.MustCompile(`^(\[[A-Z]{0,3}\d{1,3}\]|\(\d{1,3}\)|\d{1,3}\.)\s+\S`)
	// The heading of the list of studies a review reviewed, often an appendix:
	// "PRIMARY STUDIES", "Appendix B. The selected papers (Ps)", "Appendix A:
	// The Primary Studies (PSs)".
	primaryHeading = regexp.MustCompile(`(?i)^\s*(?:(?:appendix|приложение)(?:\s+[A-ZА-Я0-9]{1,3})?\s*[.:]?\s*)?(?:\d+(?:\.\d+)*\.?\s+)?` +
		`(?:(?:the\s+)?(?:list\s+of\s+(?:the\s+)?)?(?:primary|selected|included|reviewed)\s+(?:studies|papers|articles|sources|literature)` +
		`|(?:первичные|отобранные|включённые|включенные|рассмотренные)\s+(?:исследования|статьи|работы|источники))` +
		`(?:\s*\([^)]{0,16}\))?\s*:?\s*$`)
	yearIn = regexp.MustCompile(`\b(?:1[5-9]|20)\d{2}\b`)
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
	// William Lawrence."; grey literature has an organisation and a year:
	// "Analytics India Magazine, 2020.".
	authorStart = regexp.MustCompile(`^(?:(?:van|von|de|der|den|da|di|du|del|la|le|dos|ten|ter)\s+)*\p{Lu}[\p{L}'’\-]+(?:[\s\-]\p{Lu}[\p{L}'’\-]+)*` +
		`(?:,\s*\p{Lu}\p{Ll}{0,2}\.` +
		`|,?\s+\p{Lu}{1,3}(?:[\s,(]|$)` +
		`|,\s*\p{Lu}\p{Ll}+(?:\s+\p{Lu}\p{Ll}+)?(?:\s+\p{Lu}\.)?(?:[.,]|\s+(?:and|&))` +
		`|,\s*(?:1[5-9]|20)\d{2}[a-z]?\.)`)
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
func isBibHeading(line string) bool { return headingMatches(bibHeading, line) }

func isPrimaryHeading(line string) bool { return headingMatches(primaryHeading, line) }

func headingMatches(re *regexp.Regexp, line string) bool {
	if re.MatchString(line) {
		return true
	}
	m := letterSpaced.FindStringSubmatch(line)
	return len(m) == 3 && re.MatchString(m[1]+strings.ReplaceAll(m[2], " ", ""))
}

// isListHeading is the heading of either list: one list ends where the other
// begins, whichever comes first.
func isListHeading(line string) bool { return isBibHeading(line) || isPrimaryHeading(line) }

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
const ReferenceParser = 6

// minReferenceListChars is what a section has to hold before it is offered as
// one unparsed entry: less than this is a stray heading, not a list.
const minReferenceListChars = 100

// ReferenceList is one list of works a publication prints: its references, or
// — in a systematic review — the primary studies it reviewed.
type ReferenceList struct {
	Kind    string // "references" or "primary"
	Entries []string
}

// listKind is what tells one list from the other: its heading, and how sure the
// reading has to be before the list is taken. "Primary studies" is also a
// caption, and "Selected papers" a heading of prose, so that list is taken only
// when it is cut into entries that read as citations. A references heading is
// never anything else, and its list is taken even when it cannot be cut.
type listKind struct {
	name    string
	heading func(string) bool
	strict  bool
}

var listKinds = []listKind{
	{name: "references", heading: isBibHeading},
	{name: "primary", heading: isPrimaryHeading, strict: true},
}

// ReferenceLists finds the lists a publication prints and cuts each into
// entries. Each entry is returned as printed, with its lines joined: what is
// parsed out of it can be wrong, and the line itself is what a reader corrects
// it against.
func ReferenceLists(pages []string) []ReferenceList {
	var lists []ReferenceList
	for _, kind := range listKinds {
		if _, entries, ok := locate(pages, kind); ok {
			lists = append(lists, ReferenceList{Kind: kind.name, Entries: entries})
		}
	}
	return lists
}

// Bibliography is the references alone. start is the index of the page the
// list begins on, and -1 when the publication has none.
func Bibliography(pages []string) (start int, entries []string) {
	span, entries, ok := locate(pages, listKinds[0])
	if !ok {
		return -1, nil
	}
	return span.fromPage, entries
}

// listSpan is where a list sits: from its heading to the first line of
// whatever follows it, as page and line indexes. A list that runs to the end
// of the document ends at page len(pages).
type listSpan struct {
	fromPage, fromLine int
	toPage, toLine     int
}

// minCitingEntries and the share of entries with a year are what a list of
// studies has to show before it is taken as one.
const minCitingEntries = 3

// locate finds a list of the kind, trying its headings from the last one back:
// a list is printed at the end, and an earlier heading of the same words is
// more likely a caption.
func locate(pages []string, kind listKind) (listSpan, []string, bool) {
	headings := headingsOf(pages, kind)
	for i := len(headings) - 1; i >= 0; i-- {
		span, lines := listLines(pages, kind, headings[i])
		entries, shaped := cut(lines)
		switch {
		case !kind.strict && len(entries) > 0:
			return span, entries, true
		case !kind.strict:
			// The heading is there and the cut did not take. Handing the section
			// back whole is worse than entries and better than losing it.
			if whole := join(lines); len(whole) >= minReferenceListChars {
				return span, []string{whole}, true
			}
			return span, nil, false
		case shaped && readsAsCitations(entries):
			return span, entries, true
		}
	}
	return listSpan{}, nil, false
}

func readsAsCitations(entries []string) bool {
	if len(entries) < minCitingEntries {
		return false
	}
	dated := 0
	for _, e := range entries {
		if yearIn.MatchString(e) {
			dated++
		}
	}
	return dated*2 >= len(entries)
}

func headingsOf(pages []string, kind listKind) []listSpan {
	var found []listSpan
	for i, page := range pages {
		for j, line := range strings.Split(page, "\n") {
			if kind.heading(line) {
				found = append(found, listSpan{fromPage: i, fromLine: j})
			}
		}
	}
	// A references heading is taken where it last stands, as it always was.
	if !kind.strict && len(found) > 1 {
		found = found[len(found)-1:]
	}
	return found
}

// listLines is the text of a list: from its heading to the next section, the
// other list's heading, or the end.
func listLines(pages []string, kind listKind, span listSpan) (listSpan, []string) {
	span.toPage, span.toLine = len(pages), 0
	running := runningLines(pages[span.fromPage:])
	var lines []string
	previous := ""
	for i := span.fromPage; i < len(pages); i++ {
		for j, line := range strings.Split(visible(pages[i]), "\n") {
			if i == span.fromPage && j <= span.fromLine {
				continue
			}
			text := strings.TrimSpace(line)
			// A running head repeats the heading on every page of the list.
			if kind.heading(line) || pageNumberOnly.MatchString(line) || running[pageFree(line)] {
				continue
			}
			if bibEnd.MatchString(line) || isListHeading(line) || (endsEntry.MatchString(previous) && listOver(text)) {
				span.toPage, span.toLine = i, j
				return span, lines
			}
			lines = append(lines, strings.TrimRight(line, " \t"))
			if text != "" {
				previous = text
			}
		}
	}
	return span, lines
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

// cut splits the list into entries by whichever shape a bibliography comes in
// explains it: numbered labels, a hanging indent, names opening lines, or a
// blank line between entries. shaped says it was one of the first three — a
// blank line also separates paragraphs of prose.
func cut(lines []string) (entries []string, shaped bool) {
	if starts := labelled(lines); len(starts) > 1 {
		return entriesAt(lines, starts), true
	}
	if starts := hanging(lines); len(starts) > 1 {
		return entriesAt(lines, starts), true
	}
	if starts := authorYear(lines); len(starts) > 1 {
		// An entry whose line opens with a title or an organisation is not
		// recognised as a start; before the first recognised one it would be
		// lost, and later it joins the entry before it.
		if first := starts[0]; join(lines[:first]) != "" {
			starts = append([]int{0}, starts...)
		}
		return entriesAt(lines, starts), true
	}
	return entriesAt(lines, blankSeparated(lines)), false
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
