package cite

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

// minDOISuffix is shorter than any real DOI's suffix and longer than the stub a
// broken line leaves.
const minDOISuffix = 4

var (
	citationLabel = regexp.MustCompile(`^(\[[A-Z]{0,3}\d{1,3}\]|\(\d{1,3}\)|\d{1,3}\.)\s+`)
	arxivRe       = regexp.MustCompile(`(?i)arxiv[:/\s]\s*(\d{4}\.\d{4,5}(v\d+)?|[a-z-]+(\.[A-Z]{2})?/\d{7})`)
	urlRe         = regexp.MustCompile(`https?://[^\s,;]+`)
	yearRe        = regexp.MustCompile(`\b(1[5-9]\d{2}|20\d{2})\b`)
	bareYear      = regexp.MustCompile(`^\(?(1[5-9]\d{2}|20\d{2})\)?\.?$`)
	trailingYear  = regexp.MustCompile(`\s*\(?(1[5-9]\d{2}|20\d{2})\)?\.?$`)
	fullStop      = regexp.MustCompile(`[.!?]\s+`)
	// IEEE and Chicago quote the title of an article: “Title,” — straight
	// quotes and doubled single ones are the same mark after extraction.
	quotedTitle = regexp.MustCompile(`^(.*?)[,.]?\s*(?:“|"|‘‘|'')(.{8,}?)[,.]?(?:”|"|’’|'')`)
	// What a venue is called, and a work never is: a parse that lands on one has
	// missed the title, and every paper of that venue prints its name at the top
	// of page one.
	venueName = regexp.MustCompile(`(?i)^(?:the\s+)?(?:(?:international\s+)?journal\s+of\b|proceedings\s+of\b|communications\s+of\s+the\b|(?:ieee|acm)\s|.*\btransactions\s+on\b)`)
	// An initial written with a hyphen: "J.-F.", "J-P".
	hyphenInitial = regexp.MustCompile(`^\p{Lu}\.?-\p{Lu}$`)
	// The first year in an entry, with what an author–year style puts around it:
	// "(2016).", ", 2020.", " 2018a." — and something after it, since a year that
	// ends the entry is a Vancouver date, not the close of an author block.
	yearAfterAuthors = regexp.MustCompile(`^(.+?)[\s,.]*\(?((?:1[5-9]|20)\d{2})[a-z]?\)?[.,:]?\s+(\S.*)$`)
	// What an author block is made of, once the names' capitalised words are
	// taken out: initials, particles, and the words and marks that join names.
	nameFiller = regexp.MustCompile(`(?i)^(?:\p{Lu}{1,3}\.?|and|и|et|al\.?|&|van|von|de|der|den|da|di|du|del|la|le|dos|ten|ter|jr\.?)$`)
)

// ParseCitation reads one entry of a reference list. Nothing here is certain:
// bibliographies are set in a dozen styles and extracted from a PDF on top of
// that, so a field that cannot be read is left empty rather than guessed, and
// the line as printed travels with the record.
func ParseCitation(raw string) corpus.Citation {
	c := corpus.Citation{Raw: strings.TrimSpace(raw)}

	rest := c.Raw
	if m := citationLabel.FindStringSubmatch(rest); m != nil {
		c.Label = m[1]
		rest = rest[len(m[0]):]
	}

	c.DOI = FindDOI(rest)
	// A DOI cut at a line break leaves the publisher's prefix — "10.1016/j" —
	// which every paper from that publisher shares.
	if _, suffix, _ := strings.Cut(c.DOI, "/"); len(suffix) < minDOISuffix {
		c.DOI = ""
	}
	if m := arxivRe.FindStringSubmatch(rest); m != nil {
		c.ArXiv = strings.ToLower(m[1])
	}
	if isbns := FindISBN(rest); len(isbns) > 0 {
		c.ISBN = isbns[0]
	}
	if u := urlRe.FindString(rest); u != "" && !strings.Contains(u, "doi.org") {
		c.URL = strings.TrimRight(u, ".,;)")
	}

	// Identifiers carry digits that read as years, and a URL carries periods
	// that read as sentence ends. Neither belongs to the prose.
	prose := urlRe.ReplaceAllString(rest, " ")
	if c.DOI != "" {
		prose = strings.ReplaceAll(prose, c.DOI, " ")
	}
	if c.ArXiv != "" {
		prose = arxivRe.ReplaceAllString(prose, " ")
	}

	identified := c.DOI != "" || c.ArXiv != "" || c.ISBN != "" || c.URL != ""
	c.Authors, c.Title, c.Year = readParts(prose, identified)
	if venueName.MatchString(c.Title) {
		c.Title = ""
	}
	c.Fingerprint = fingerprint(&c)
	return c
}

// readParts splits an entry into who wrote it, what it is called and when.
// Both orders a bibliography puts them in are covered: "Authors. Year. Title"
// and "Authors. Title. … Year".
func readParts(prose string, identified bool) (authors, title string, year int) {
	if a, t, ok := quotedParts(prose); ok {
		return a, t, finalYear(prose)
	}
	if a, t, y, ok := authorYearParts(prose); ok {
		return a, t, y
	}
	segments := sentences(prose)
	if len(segments) == 0 {
		return "", "", 0
	}

	authors = segments[0]
	titleAt := 1
	// "Authors (2017). Title." puts the year inside the author segment.
	if m := trailingYear.FindStringSubmatch(authors); m != nil {
		year, _ = strconv.Atoi(m[1])
		authors = strings.TrimSpace(authors[:len(authors)-len(m[0])])
	} else if len(segments) > 1 {
		if m := bareYear.FindStringSubmatch(segments[1]); m != nil {
			year, _ = strconv.Atoi(m[1])
			titleAt = 2
		}
	}
	if year == 0 {
		year = finalYear(prose)
	}
	if titleAt < len(segments) {
		title = strings.TrimSpace(strings.TrimPrefix(segments[titleAt], "In "))
	}

	// Two phrases with no date and no identifier are as likely a cross-reference
	// ("см. там же") as a citation, and one phrase says who or what with no
	// telling which. Reading an author and a title out of either invents them.
	if len(segments) < 3 && year == 0 && !identified {
		return "", "", 0
	}
	return trimEnd(authors), trimEnd(title), year
}

// finalYear is the year an entry ends with: the first one printed is as likely to
// be a page range or a volume.
func finalYear(prose string) int {
	all := yearRe.FindAllStringSubmatch(prose, -1)
	if all == nil {
		return 0
	}
	year, _ := strconv.Atoi(all[len(all)-1][1])
	return year
}

// quotedParts reads a title set in quotes, with the names before it. The quotes
// are the mark: IEEE puts only a comma between the authors and the title, and a
// comma is in every author list.
func quotedParts(prose string) (authors, title string, ok bool) {
	m := quotedTitle.FindStringSubmatch(prose)
	if len(m) < 3 || !namesOnly(m[1]) {
		return "", "", false
	}
	return trimEnd(m[1]), trimEnd(m[2]), true
}

// authorYearParts reads the styles that print the year right after the
// authors — APA, Harvard, Springer, Elsevier. There the year is the boundary,
// and the one boundary in the entry that an initial cannot be taken for. It
// applies only when what precedes the year reads as a list of names: a year
// inside a venue, after a title, is no such boundary.
func authorYearParts(prose string) (authors, title string, year int, ok bool) {
	prose = strings.TrimSpace(prose)
	at := yearAfterAuthors.FindStringSubmatchIndex(prose)
	if at == nil {
		return "", "", 0, false
	}
	authors = prose[at[2]:at[3]]
	if !namesOnly(authors) {
		return "", "", 0, false
	}
	// The separator before the year swallowed the full stop of a last initial,
	// which belongs to the name.
	if strings.HasPrefix(prose[at[3]:], ".") && endsWithInitial(authors) {
		authors += "."
	}
	year, _ = strconv.Atoi(prose[at[4]:at[5]])
	if parts := splitKeepingStops(prose[at[6]:at[7]]); len(parts) > 0 {
		title = trimEnd(parts[0])
	}
	return trimEnd(authors), title, year, true
}

// namesOnly reports text made of names: every word is capitalised, or is an
// initial, a particle or a joining word. A title has lower-case words in it.
func namesOnly(s string) bool {
	words := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || unicode.IsSpace(r) })
	if len(words) == 0 {
		return false
	}
	for _, w := range words {
		if nameFiller.MatchString(w) || hyphenInitial.MatchString(strings.TrimSuffix(w, ".")) {
			continue
		}
		first, _ := utf8.DecodeRuneInString(w)
		if !unicode.IsUpper(first) {
			return false
		}
		// "In", "Proceedings" and "ICSE" are capitalised too; a name has no
		// full stop in the middle of a sentence after it.
		if strings.HasSuffix(w, ".") && len([]rune(w)) > 3 {
			return false
		}
	}
	return true
}

// sentences cuts an entry at its full stops, keeping names whole. The full stop
// of an initial is not the end of a phrase, and telling the two apart is the
// whole difficulty: "S. Melnik, S. Raghavan" and "Клеппман, М. Высоконагруженные"
// both put a comma before a lone capital, and only one of them continues.
//
// What separates them is which way round the name is written. A block that
// opens with an initial is written initial-first, and a trailing initial in it
// opens the next name. A block that opens with a family name is written
// family-first, and a trailing initial closes the name — unless another initial
// follows it, as the second of "Рогов, Е. В."
func sentences(prose string) []string {
	parts := splitKeepingStops(prose)
	var out []string
	var current string
	for i, part := range parts {
		current += part
		next := ""
		if i+1 < len(parts) {
			next = parts[i+1]
		}
		if endsWithInitial(strings.TrimSuffix(strings.TrimSpace(current), ".")) &&
			(startsWithInitial(current) || startsWithInitial(next)) {
			continue
		}
		if trimmed := strings.TrimSpace(current); trimmed != "" {
			out = append(out, trimmed)
		}
		current = ""
	}
	if trimmed := strings.TrimSpace(current); trimmed != "" {
		out = append(out, trimmed)
	}
	return out
}

// startsWithInitial reports a phrase opening with a single letter and a full
// stop, which is how an initial-first name begins.
func startsWithInitial(s string) bool {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return false
	}
	first := []rune(strings.TrimSuffix(fields[0], "."))
	return len(first) == 1 && unicode.IsUpper(first[0]) && strings.HasSuffix(fields[0], ".")
}

// splitKeepingStops cuts at a full stop, question or exclamation mark followed
// by a space, and keeps the mark with the phrase it ends — an initial is told
// from a sentence end by what precedes the dot, so the dot has to survive.
func splitKeepingStops(prose string) []string {
	var parts []string
	last := 0
	for _, at := range fullStop.FindAllStringIndex(prose, -1) {
		parts = append(parts, prose[last:at[0]+1])
		// The space after the stop opens the next phrase, so that rejoining two
		// halves of a name gives "J. Dean" and not "J.Dean".
		last = at[0] + 1
	}
	return append(parts, prose[last:])
}

// trimEnd drops the punctuation that ends a phrase, but never the full stop of
// an initial: "Клеппман, М." is a name and "Клеппман, М" is a typo.
func trimEnd(s string) string {
	s = strings.TrimSpace(s)
	for s != "" {
		r, size := utf8.DecodeLastRuneInString(s)
		if !strings.ContainsRune(".,;:", r) {
			break
		}
		if r == '.' && endsWithInitial(s[:len(s)-size]) {
			break
		}
		s = strings.TrimSpace(s[:len(s)-size])
	}
	return s
}

func endsWithInitial(s string) bool {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return false
	}
	last := fields[len(fields)-1]
	if hyphenInitial.MatchString(last) {
		return true
	}
	r := []rune(last)
	return len(r) == 1 && unicode.IsUpper(r[0])
}

// fingerprint is what an entry points at. An identifier is exact; a title and
// year are not, but they are what an entry without one has.
func fingerprint(c *corpus.Citation) string {
	switch {
	case c.DOI != "":
		return strings.ToLower(c.DOI)
	case c.ArXiv != "":
		return "arxiv:" + c.ArXiv
	case c.ISBN != "":
		return "isbn:" + c.ISBN
	case c.Title != "" && c.Year != 0:
		return NormalizeTitle(c.Title) + "|" + strconv.Itoa(c.Year)
	}
	return "raw:" + NormalizeTitle(c.Raw)
}

// NormalizeTitle reduces a title to what two printings of it have in common:
// case, punctuation and spacing differ between bibliographies, the words do
// not. The same reduction is written in SQL by the migration that resolves
// citations against the library, and the two have to agree.
func NormalizeTitle(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r == 'ё':
			r = 'е'
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

// AuthorNames renders a record's authors for display beside a citation:
// "Family, Given", joined by "and". BibLaTeX has a formatting of its own, with
// braces around a literal name, which a reference list has no use for.
func AuthorNames(record corpus.CSL) string {
	list, ok := record["author"].([]any)
	if !ok {
		return ""
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		n, _ := item.(map[string]any)
		family, given := str(n["family"]), str(n["given"])
		switch {
		case family != "" && given != "":
			out = append(out, family+", "+given)
		case family != "":
			out = append(out, family)
		default:
			if literal := str(n["literal"]); literal != "" {
				out = append(out, literal)
			}
		}
	}
	return strings.Join(out, " and ")
}
