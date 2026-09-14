// Package cite turns what is known about a source into something a paper can
// cite: a CSL-JSON record, a stable citation key, BibLaTeX. Records are
// CSL-JSON because that is what citeproc, Pandoc and Zotero all read; the
// formatting into GOST, APA or IEEE is citeproc's job, not this package's.
package cite

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

var translit = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e", 'ж': "zh", 'з': "z", 'и': "i",
	'й': "y", 'к': "k", 'л': "l", 'м': "m", 'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t",
	'у': "u", 'ф': "f", 'х': "kh", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "shch", 'ъ': "", 'ы': "y", 'ь': "",
	'э': "e", 'ю': "yu", 'я': "ya",
}

func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteString(translit[r])
		}
	}
	return b.String()
}

var stopWords = map[string]bool{"the": true, "a": true, "an": true, "on": true, "of": true, "and": true}

// KeyBase is the citation key a record suggests: first author's family name
// and year, as BibTeX users expect. Keys are for typing into \cite{}, so they
// are ASCII whatever the language of the book.
func KeyBase(csl corpus.CSL) string {
	name := ""
	if authors, ok := csl["author"].([]any); ok && len(authors) > 0 {
		if a, ok := authors[0].(map[string]any); ok {
			name, _ = a["family"].(string)
			if name == "" {
				literal, _ := a["literal"].(string)
				name = strings.Fields(literal + " ")[0]
			}
		}
	}
	if name == "" {
		title, _ := csl["title"].(string)
		for _, w := range strings.Fields(title) {
			if !stopWords[strings.ToLower(w)] {
				name = w
				break
			}
		}
	}
	key := slug(name)
	if year := Year(csl); year != 0 {
		key += strconv.Itoa(year)
	}
	if key == "" {
		return "ref"
	}
	return key
}

// UniqueKey appends a, b, c… while the key is taken, the way bibliographies
// tell apart two works of one author and year.
func UniqueKey(base string, taken func(string) bool) string {
	if !taken(base) {
		return base
	}
	for suffix := 'a'; ; suffix++ {
		if k := base + string(suffix); !taken(k) || suffix == 'z' {
			return k
		}
	}
}

// Year reads the first date part of issued, whether JSON gave it as a number
// or a string.
func Year(csl corpus.CSL) int {
	issued, _ := csl["issued"].(map[string]any)
	parts, _ := issued["date-parts"].([]any)
	if len(parts) == 0 {
		return 0
	}
	first, _ := parts[0].([]any)
	if len(first) == 0 {
		return 0
	}
	switch v := first[0].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}

var (
	isbnRe = regexp.MustCompile(`ISBN(?:-1[03])?[:\s]*((?:97[89][\s-]?)?(?:\d[\s-]?){9}[\dXx])`)
	doiRe  = regexp.MustCompile(`\b(10\.\d{4,9}/[^\s"<>]+)`)
)

// FindISBN returns the ISBNs named in a text, digits only, in order and once
// each. The check digit is verified: a copyright page is extracted text, and a
// misread digit would send a lookup to someone else's book.
func FindISBN(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range isbnRe.FindAllStringSubmatch(text, -1) {
		digits := strings.Map(func(r rune) rune {
			if unicode.IsDigit(r) {
				return r
			}
			if r == 'x' || r == 'X' {
				return 'X'
			}
			return -1
		}, m[1])
		if validISBN(digits) && !seen[digits] {
			seen[digits] = true
			out = append(out, digits)
		}
	}
	return out
}

func validISBN(s string) bool {
	switch len(s) {
	case 10:
		sum := 0
		for i, r := range s {
			v := int(r - '0')
			if r == 'X' {
				if i != 9 {
					return false
				}
				v = 10
			}
			sum += (10 - i) * v
		}
		return sum%11 == 0
	case 13:
		sum := 0
		for i, r := range s {
			if r == 'X' {
				return false
			}
			w := 1
			if i%2 == 1 {
				w = 3
			}
			sum += w * int(r-'0')
		}
		return sum%10 == 0
	}
	return false
}

// FindDOI returns the first DOI in a text, without the trailing punctuation
// that ends the sentence it sits in.
func FindDOI(text string) string {
	m := doiRe.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return strings.TrimRight(m[1], ".,;)]")
}

// ParseAuthors reads the Author field of PDF metadata, which comes as "Given
// Family", "Family, Given" or several of either joined by ";" or "and".
func ParseAuthors(s string) []map[string]any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var parts []string
	if strings.Contains(s, ";") {
		parts = strings.Split(s, ";")
	} else {
		parts = regexp.MustCompile(`\s+(?:and|&|и)\s+`).Split(s, -1)
	}
	var out []map[string]any
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if family, given, ok := strings.Cut(p, ","); ok {
			out = append(out, map[string]any{"family": strings.TrimSpace(family), "given": strings.TrimSpace(given)})
			continue
		}
		words := strings.Fields(p)
		out = append(out, map[string]any{"family": words[len(words)-1], "given": strings.Join(words[:len(words)-1], " ")})
	}
	return out
}

var (
	filenameTags = regexp.MustCompile(`^(?:\s*\[[^\]]*\])+\s*|\s*\([^()\s]+\.(?:org|com|net|io|ru)\)\s*$`)
	// "[PROGRAMMING][Clean Code]": a shelf tag, then the title in brackets too.
	bracketedOnly = regexp.MustCompile(`^(?:\[[^\]]*\])*\[([^\]]+)\]$`)
	personName    = regexp.MustCompile(`^[\p{L}][\p{L}.'’ -]*$`)
)

// plausibleAuthors keeps the Author field only when every name in it reads as
// a person's: PDF metadata is filled by whatever made the file, and holds
// timestamps, logins and e-mail handles as often as authors.
func plausibleAuthors(authors []map[string]any) bool {
	for _, a := range authors {
		family, _ := a["family"].(string)
		given, _ := a["given"].(string)
		if given == "" || !personName.MatchString(family) || !personName.MatchString(given) ||
			strings.Contains(" "+given+" ", " with ") || len(strings.Fields(given)) > 3 {
			return false
		}
	}
	return len(authors) > 0
}

// BookDraft is what can be said about a PDF without asking anyone: its title
// without the tags of its file name, the author its metadata claims when that
// is plausible, an ISBN from its opening or closing pages, and a DOI from its
// opening pages only — the closing pages of a book are its references, full
// of other works' DOIs. A DOI without an ISBN marks a paper rather than a book.
func BookDraft(title, pdfAuthor, head, tail string) corpus.CSL {
	if m := bracketedOnly.FindStringSubmatch(strings.TrimSpace(title)); m != nil {
		title = m[1]
	} else if cleaned := strings.TrimSpace(filenameTags.ReplaceAllString(title, "")); cleaned != "" {
		title = cleaned
	}
	draft := corpus.CSL{"type": "book", "title": title}
	if authors := ParseAuthors(pdfAuthor); plausibleAuthors(authors) {
		draft["author"] = authors
	}
	isbns := FindISBN(head + "\n" + tail)
	if len(isbns) > 0 {
		draft["ISBN"] = isbns[0]
	}
	if doi := FindDOI(head); doi != "" {
		draft["DOI"] = doi
		if len(isbns) == 0 {
			draft["type"] = "article-journal"
		}
	}
	return draft
}

var bibType = map[string]string{
	"book": "book", "article-journal": "article", "article-magazine": "article", "article-newspaper": "article",
	"chapter": "incollection", "paper-conference": "inproceedings", "thesis": "thesis", "report": "report",
	"webpage": "online", "software": "software", "entry-encyclopedia": "inreference",
}

var bibFields = []struct{ csl, bib string }{
	{"title", "title"}, {"container-title", "journaltitle"}, {"publisher", "publisher"},
	{"publisher-place", "location"}, {"edition", "edition"}, {"volume", "volume"}, {"issue", "number"},
	{"page", "pages"}, {"number-of-pages", "pagetotal"}, {"collection-title", "series"},
	{"ISBN", "isbn"}, {"DOI", "doi"}, {"URL", "url"}, {"version", "version"}, {"genre", "type"},
}

// BibLaTeX writes records as BibLaTeX, which unlike BibTeX has fields for what
// GOST asks for — location, pagetotal, urldate, langid — and is what
// biblatex-gost reads.
func BibLaTeX(refs []corpus.Reference) string {
	sorted := append([]corpus.Reference(nil), refs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].CiteKey < sorted[j].CiteKey })

	var b strings.Builder
	for _, r := range sorted {
		kind, _ := r.CSL["type"].(string)
		entry := bibType[kind]
		if entry == "" {
			entry = "misc"
		}
		fmt.Fprintf(&b, "@%s{%s,\n", entry, r.CiteKey)
		field := func(name, value string) {
			if value != "" {
				fmt.Fprintf(&b, "  %s = {%s},\n", name, escape(value))
			}
		}
		field("author", names(r.CSL["author"]))
		field("editor", names(r.CSL["editor"]))
		field("translator", names(r.CSL["translator"]))
		for _, f := range bibFields {
			name := f.bib
			if f.csl == "container-title" && entry != "article" {
				name = "booktitle"
			}
			field(name, str(r.CSL[f.csl]))
		}
		field("date", date(r.CSL["issued"]))
		field("urldate", date(r.CSL["accessed"]))
		switch str(r.CSL["language"]) {
		case "ru", "ru-RU":
			field("langid", "russian")
		case "en", "en-US", "en-GB":
			field("langid", "english")
		}
		b.WriteString("}\n\n")
	}
	return b.String()
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	}
	return ""
}

func names(v any) string {
	list, _ := v.([]any)
	if list == nil {
		if typed, ok := v.([]map[string]any); ok {
			for _, m := range typed {
				list = append(list, m)
			}
		}
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		n, _ := item.(map[string]any)
		if literal := str(n["literal"]); literal != "" {
			out = append(out, "{"+literal+"}")
			continue
		}
		family, given := str(n["family"]), str(n["given"])
		if given != "" {
			out = append(out, family+", "+given)
		} else if family != "" {
			out = append(out, family)
		}
	}
	return strings.Join(out, " and ")
}

func date(v any) string {
	m, _ := v.(map[string]any)
	parts, _ := m["date-parts"].([]any)
	if len(parts) == 0 {
		return ""
	}
	first, _ := parts[0].([]any)
	segments := make([]string, 0, len(first))
	for i, p := range first {
		s := str(p)
		if i > 0 && len(s) == 1 {
			s = "0" + s
		}
		segments = append(segments, s)
	}
	return strings.Join(segments, "-")
}

var bibEscaper = strings.NewReplacer(`&`, `\&`, `%`, `\%`, `$`, `\$`, `#`, `\#`, `_`, `\_`)

func escape(s string) string { return bibEscaper.Replace(s) }
