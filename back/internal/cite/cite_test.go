package cite_test

import (
	"strings"
	"testing"

	"github.com/smirnoffmg/corpus/internal/cite"
	"github.com/smirnoffmg/corpus/internal/corpus"
)

func TestKeyBase(t *testing.T) {
	cases := []struct {
		name string
		csl  corpus.CSL
		want string
	}{
		{"first author and year", corpus.CSL{"author": []any{map[string]any{"family": "Kleppmann", "given": "Martin"}}, "issued": map[string]any{"date-parts": []any{[]any{2017.0}}}}, "kleppmann2017"},
		{"cyrillic is transliterated", corpus.CSL{"author": []any{map[string]any{"family": "Клеппман"}}, "issued": map[string]any{"date-parts": []any{[]any{2018}}}}, "kleppman2018"},
		{"no author falls back to the title", corpus.CSL{"title": "The Art of Computer Programming"}, "art"},
		{"diacritics are dropped, not the letters", corpus.CSL{"author": []any{map[string]any{"family": "Böhme"}}, "issued": map[string]any{"date-parts": []any{[]any{2010}}}}, "bohme2010"},
		{"й and ё keep their transliteration", corpus.CSL{"author": []any{map[string]any{"family": "Бойко-Королёв"}}}, "boykokorolev"},
		{"a ligature and a cedilla", corpus.CSL{"author": []any{map[string]any{"family": "Façade-Ærø"}}}, "facadeaero"},
		{"a literal author", corpus.CSL{"author": []any{map[string]any{"literal": "scikit-learn developers"}}}, "scikitlearn"},
		{"nothing at all", corpus.CSL{}, "ref"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := cite.KeyBase(c.csl); got != c.want {
				t.Errorf("KeyBase = %q, want %q", got, c.want)
			}
		})
	}
}

func TestUniqueKeyAddsALetterWhileTaken(t *testing.T) {
	taken := map[string]bool{"knuth1968": true, "knuth1968a": true}
	if got := cite.UniqueKey("knuth1968", func(k string) bool { return taken[k] }); got != "knuth1968b" {
		t.Errorf("UniqueKey = %q, want knuth1968b", got)
	}
	if got := cite.UniqueKey("new2020", func(string) bool { return false }); got != "new2020" {
		t.Errorf("UniqueKey = %q, want the base untouched", got)
	}
}

func TestFindISBNKeepsOnlyValidChecksums(t *testing.T) {
	text := "Copyright © 2017. ISBN: 978-1-449-37332-0 [LSI]\nISBN 978-5-4461-0512-0 (рус.)\nISBN 978-1-449-37332-1 is a typo\nISBN 0-596-52068-9"
	got := cite.FindISBN(text)
	want := []string{"9781449373320", "9785446105120", "0596520689"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("FindISBN = %v, want %v", got, want)
	}
}

func TestFindDOI(t *testing.T) {
	if got := cite.FindDOI("Published version: https://doi.org/10.1145/3290605.3300233.\nMore text"); got != "10.1145/3290605.3300233" {
		t.Errorf("FindDOI = %q", got)
	}
	if got := cite.FindDOI("no identifier here"); got != "" {
		t.Errorf("FindDOI = %q, want none", got)
	}
}

func TestParseAuthors(t *testing.T) {
	cases := map[string][]map[string]any{
		"Martin Kleppmann":                       {{"family": "Kleppmann", "given": "Martin"}},
		"Kleppmann, Martin; Beresford, Alastair": {{"family": "Kleppmann", "given": "Martin"}, {"family": "Beresford", "given": "Alastair"}},
		"Katherine Cox-Buday and Rob Pike":       {{"family": "Cox-Buday", "given": "Katherine"}, {"family": "Pike", "given": "Rob"}},
		"":                                       nil,
	}
	for in, want := range cases {
		got := cite.ParseAuthors(in)
		if len(got) != len(want) {
			t.Errorf("ParseAuthors(%q) = %v, want %v", in, got, want)
			continue
		}
		for i := range want {
			if got[i]["family"] != want[i]["family"] || got[i]["given"] != want[i]["given"] {
				t.Errorf("ParseAuthors(%q)[%d] = %v, want %v", in, i, got[i], want[i])
			}
		}
	}
}

func TestBookDraft(t *testing.T) {
	draft := cite.BookDraft("Designing Data-Intensive Applications", "Martin Kleppmann", "O'Reilly. ISBN: 978-1-449-37332-0", "")
	if draft["type"] != "book" || draft["title"] != "Designing Data-Intensive Applications" || draft["ISBN"] != "9781449373320" {
		t.Errorf("draft = %v", draft)
	}
	if authors, _ := draft["author"].([]map[string]any); len(authors) != 1 {
		t.Errorf("author = %v", draft["author"])
	}
	paper := cite.BookDraft("Attention", "", "arXiv. doi:10.48550/arXiv.1706.03762", "")
	if paper["type"] != "article-journal" || paper["DOI"] != "10.48550/arXiv.1706.03762" {
		t.Errorf("a PDF with a DOI and no ISBN is a paper: %v", paper)
	}
}

func TestBookDraftDistrustsWhatFileNamesAndMetadataAdd(t *testing.T) {
	draft := cite.BookDraft("[PROGRAMMING][Clean Code by Robert C Martin]", "19:56:25", "", "References\n[12] doi:10.1145/212433.220201")
	if draft["title"] != "Clean Code by Robert C Martin" {
		t.Errorf("title = %q", draft["title"])
	}
	if _, ok := draft["author"]; ok {
		t.Errorf("a timestamp became an author: %v", draft["author"])
	}
	if draft["type"] != "book" || draft["DOI"] != nil {
		t.Errorf("a DOI from the references made the book a paper: %v", draft)
	}
	for _, junk := range []string{"Tim@", "petrshegolev", "Len Bass, Paul Clements,Rick Kazman", "William Kennedy with Brian Ketelsen"} {
		if d := cite.BookDraft("T", junk, "", ""); d["author"] != nil {
			t.Errorf("%q became %v", junk, d["author"])
		}
	}
	if d := cite.BookDraft("Fundamentals of Data Engineering (Reis, Housley) (example-books.org)", "", "", ""); d["title"] != "Fundamentals of Data Engineering (Reis, Housley)" {
		t.Errorf("title = %q", d["title"])
	}
}

func TestBibLaTeX(t *testing.T) {
	refs := []corpus.Reference{
		{CiteKey: "kleppmann2018", CSL: corpus.CSL{
			"type": "book", "title": "Высоконагруженные приложения", "language": "ru",
			"author":    []any{map[string]any{"family": "Клеппман", "given": "Мартин"}},
			"publisher": "Питер", "publisher-place": "Санкт-Петербург",
			"issued":          map[string]any{"date-parts": []any{[]any{2018.0}}},
			"number-of-pages": "640", "ISBN": "978-5-4461-0512-0",
		}},
		{CiteKey: "sklearn", CSL: corpus.CSL{
			"type": "webpage", "title": "scikit-learn 1.9.1 documentation", "URL": "https://scikit-learn.org/stable/",
			"accessed": map[string]any{"date-parts": []any{[]any{2026, 9, 14}}}, "publisher": "scikit-learn developers & co",
		}},
	}
	got := cite.BibLaTeX(refs)
	for _, want := range []string{
		"@book{kleppmann2018,",
		"  author = {Клеппман, Мартин},",
		"  title = {Высоконагруженные приложения},",
		"  location = {Санкт-Петербург},",
		"  date = {2018},",
		"  pagetotal = {640},",
		"  langid = {russian},",
		"@online{sklearn,",
		"  urldate = {2026-09-14},",
		"  publisher = {scikit-learn developers \\& co},",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("BibLaTeX lacks %q:\n%s", want, got)
		}
	}
}
