package cite_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/smirnoffmg/corpus/internal/cite"
)

func lookupAgainst(t *testing.T, h http.HandlerFunc) *cite.Lookup {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	l := cite.NewLookup()
	l.DOIBase, l.OpenLibraryBase = srv.URL, srv.URL
	return l
}

func TestDOIAsksForCSLJSONAndDropsRegistryNoise(t *testing.T) {
	l := lookupAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/10.1145/3290605.3300233" || r.Header.Get("Accept") != "application/vnd.citationstyles.csl+json" {
			http.Error(w, "unexpected "+r.URL.Path+" "+r.Header.Get("Accept"), http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"type":"paper-conference","title":"A study","reference-count":45,"indexed":{"x":1},"DOI":"10.1145/3290605.3300233"}`))
	})
	record, err := l.DOI(context.Background(), "10.1145/3290605.3300233")
	if err != nil {
		t.Fatal(err)
	}
	if record["title"] != "A study" || record["type"] != "paper-conference" {
		t.Errorf("record = %v", record)
	}
	if _, ok := record["reference-count"]; ok {
		t.Error("registry bookkeeping kept in the description")
	}
}

// doi.org answers in CSL-JSON, but with the registry's own type names; a
// "journal-article" is no type citeproc knows, and would be cited as a book.
func TestDOITranslatesRegistryTypesToCSL(t *testing.T) {
	for registry, want := range map[string]string{
		"journal-article":     "article-journal",
		"proceedings-article": "paper-conference",
		"book-chapter":        "chapter",
		"posted-content":      "article",
		"dissertation":        "thesis",
		"monograph":           "book",
		"article":             "article",
		"paper-conference":    "paper-conference",
	} {
		l := lookupAgainst(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"type":"` + registry + `","title":"T"}`))
		})
		record, err := l.DOI(context.Background(), "10.1/x")
		if err != nil {
			t.Fatal(err)
		}
		if record["type"] != want {
			t.Errorf("%s became %v, want %s", registry, record["type"], want)
		}
	}
}

// Crossref files a book's series under container-title, which CSL reserves
// for what a work is part of; a styled reference would then read as a chapter
// "in" the series.
func TestDOIFilesABooksSeriesAsItsCollection(t *testing.T) {
	l := lookupAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"type":"monograph","title":"Introduction to Cryptography","container-title":"Information Security and Cryptography"}`))
	})
	record, err := l.DOI(context.Background(), "10.1007/978-3-662-47974-2")
	if err != nil {
		t.Fatal(err)
	}
	if record["collection-title"] != "Information Security and Cryptography" || record["container-title"] != nil {
		t.Errorf("record = %v", record)
	}

	chapter := lookupAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"type":"book-chapter","title":"Introduction","container-title":"An Introduction to Statistical Learning"}`))
	})
	record, err = chapter.DOI(context.Background(), "10.1007/978-3-031-38747-0_1")
	if err != nil {
		t.Fatal(err)
	}
	if record["container-title"] != "An Introduction to Statistical Learning" {
		t.Errorf("a chapter keeps its book as container: %v", record)
	}
}

func TestISBNMapsAnOpenLibraryBook(t *testing.T) {
	l := lookupAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("bibkeys") != "ISBN:9781449373320" {
			http.Error(w, "unexpected", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"ISBN:9781449373320":{"title":"Designing Data-Intensive Applications","subtitle":"The Big Ideas","number_of_pages":624,"publish_date":"March 2017","authors":[{"name":"Martin Kleppmann"}],"publishers":[{"name":"O'Reilly Media"}],"publish_places":[{"name":"Sebastopol, CA"}]}}`))
	})
	record, err := l.ISBN(context.Background(), "978-1-449-37332-0")
	if err != nil {
		t.Fatal(err)
	}
	for field, want := range map[string]any{
		"title": "Designing Data-Intensive Applications : The Big Ideas", "publisher": "O'Reilly Media",
		"publisher-place": "Sebastopol, CA", "number-of-pages": "624", "ISBN": "9781449373320",
	} {
		if record[field] != want {
			t.Errorf("%s = %v, want %v", field, record[field], want)
		}
	}
	if authors, _ := record["author"].([]map[string]any); len(authors) != 1 || authors[0]["family"] != "Kleppmann" {
		t.Errorf("author = %v", record["author"])
	}
}

func TestLookupsReportNothingFound(t *testing.T) {
	l := lookupAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/books" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		http.NotFound(w, r)
	})
	if _, err := l.ISBN(context.Background(), "9780000000002"); !errors.Is(err, cite.ErrNotFound) {
		t.Errorf("ISBN err = %v, want ErrNotFound", err)
	}
	if _, err := l.DOI(context.Background(), "10.0/none"); !errors.Is(err, cite.ErrNotFound) {
		t.Errorf("DOI err = %v, want ErrNotFound", err)
	}
}

func TestLookupReportsAServiceFailure(t *testing.T) {
	l := lookupAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	if _, err := l.DOI(context.Background(), "10.1/x"); err == nil || errors.Is(err, cite.ErrNotFound) {
		t.Errorf("err = %v, want a failure distinct from not found", err)
	}
}

const independent = `<?xml version="1.0"?><style xmlns="http://purl.org/net/xbiblio/csl" version="1.0"><info><title>Springer Basic</title><id>http://www.zotero.org/styles/springer-basic</id></info></style>`

func TestStyleFetchesTheParentOfADependentStyle(t *testing.T) {
	l := lookupAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dependent/some-journal.csl":
			_, _ = w.Write([]byte(`<style xmlns="http://purl.org/net/xbiblio/csl" version="1.0"><info><title>Some Journal</title><id>http://www.zotero.org/styles/some-journal</id><link href="http://www.zotero.org/styles/springer-basic" rel="independent-parent"/></info></style>`))
		case "/springer-basic.csl":
			_, _ = w.Write([]byte(independent))
		default:
			http.NotFound(w, r)
		}
	})
	l.StylesBase = l.DOIBase

	id, title, xml, err := l.Style(context.Background(), "some-journal")
	if err != nil {
		t.Fatal(err)
	}
	if id != "some-journal" || title != "Some Journal" || xml != independent {
		t.Errorf("got %q %q %q", id, title, xml)
	}
	if _, _, _, err := l.Style(context.Background(), "absent"); !errors.Is(err, cite.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestParseStyle(t *testing.T) {
	info, err := cite.ParseStyle(independent)
	if err != nil || info.Title != "Springer Basic" || cite.StyleSlug(info.ID) != "springer-basic" {
		t.Errorf("info = %+v, err = %v", info, err)
	}
	if _, err := cite.ParseStyle("<html/>"); !errors.Is(err, cite.ErrNotAStyle) {
		t.Errorf("err = %v, want ErrNotAStyle", err)
	}
}
