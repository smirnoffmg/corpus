package cite_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/smirnoffmg/corpus/internal/cite"
)

func lookupAgainst(t *testing.T, h http.HandlerFunc) *cite.Lookup {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	l := cite.NewLookup()
	l.DOIBase, l.OpenLibraryBase, l.StylesBase = srv.URL, srv.URL, srv.URL
	l.OpenLibraryRate = rate.NewLimiter(rate.Inf, 1)
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

// The search asks Open Library for the description alone: whether a scan can
// be borrowed or read is not in the fields requested, so nothing that leads to
// the text can reach the answer. Getting the text is a licence question, and
// this lookup does not answer it.
func TestFindBooksAsksForTheDescriptionAlone(t *testing.T) {
	l := lookupAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/search.json" || q.Get("q") != "designing data-intensive" || q.Get("limit") != "5" {
			http.Error(w, "unexpected "+r.URL.String(), http.StatusBadRequest)
			return
		}
		for _, access := range []string{"ia", "ebook_access", "has_fulltext", "public_scan_b", "lending"} {
			if strings.Contains(q.Get("fields"), access) {
				http.Error(w, "asked for "+access, http.StatusBadRequest)
				return
			}
		}
		_, _ = w.Write([]byte(`{"numFound":1,"docs":[{"key":"/works/OL19293745W","title":"Designing Data-Intensive Applications","subtitle":"The Big Ideas","author_name":["Martin Kleppmann"],"first_publish_year":2015,"publisher":["O'Reilly Media"],"isbn":["9781449373320","1449373321"],"language":["eng"],"edition_count":12,"ia":["designingdatainte0000klep"],"ebook_access":"borrowable"}]}`))
	})
	found, err := l.FindBooks(context.Background(), "designing data-intensive", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("found = %+v", found)
	}
	b := found[0]
	if b.Title != "Designing Data-Intensive Applications : The Big Ideas" || b.Year != 2015 || b.Editions != 12 ||
		len(b.Authors) != 1 || b.Authors[0] != "Martin Kleppmann" || b.Publishers[0] != "O'Reilly Media" ||
		b.ISBN[0] != "9781449373320" || b.Languages[0] != "eng" || !strings.HasSuffix(b.Catalog, "/works/OL19293745W") {
		t.Errorf("book = %+v", b)
	}
}

// A popular work has hundreds of editions; the answer names a few of each so
// that a list of ten books stays readable.
func TestFindBooksKeepsAFewIdentifiersPerWork(t *testing.T) {
	l := lookupAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"docs":[{"key":"/works/OL1W","title":"T","isbn":["1","2","3","4","5","6","7"],"publisher":["a","b","c","d","e","f"]}]}`))
	})
	found, err := l.FindBooks(context.Background(), "t", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(found[0].ISBN) != 5 || len(found[0].Publishers) != 5 {
		t.Errorf("book = %+v", found[0])
	}
}

func TestFindBooksFindingNothingIsNotAFailure(t *testing.T) {
	l := lookupAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"numFound":0,"docs":[]}`))
	})
	found, err := l.FindBooks(context.Background(), "no such book", 10)
	if err != nil || len(found) != 0 {
		t.Errorf("found = %v, err = %v", found, err)
	}
}

// Open Library answers unidentified clients at one request a second and drops
// the connection beyond it. A model calls tools in parallel, so three searches
// at once were a real case: one of them failed with EOF.
func TestOpenLibraryRequestsAreSpacedOut(t *testing.T) {
	var mu sync.Mutex
	var arrived []time.Time
	l := lookupAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		arrived = append(arrived, time.Now())
		mu.Unlock()
		_, _ = w.Write([]byte(`{"docs":[]}`))
	})
	const interval = 50 * time.Millisecond
	l.OpenLibraryRate = rate.NewLimiter(rate.Every(interval), 1)

	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			if _, err := l.FindBooks(context.Background(), "q", 1); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()

	slices.SortFunc(arrived, time.Time.Compare)
	if len(arrived) != 3 {
		t.Fatalf("requests = %d", len(arrived))
	}
	// Some slack for the clock: the limiter spaces reservations, not arrivals.
	if gap := arrived[2].Sub(arrived[0]); gap < 2*interval-10*time.Millisecond {
		t.Errorf("three requests arrived within %v, want at least %v", gap, 2*interval)
	}
}

func TestOpenLibraryWaitGivesUpWithTheContext(t *testing.T) {
	l := lookupAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("a request went out after its context was cancelled")
	})
	l.OpenLibraryRate = rate.NewLimiter(rate.Every(time.Hour), 1)
	l.OpenLibraryRate.Allow() // the one token is spent

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := l.FindBooks(ctx, "q", 1); err == nil {
		t.Error("want an error while the limiter holds the request back")
	}
}

// The services asked are public and free; naming the program is what they ask
// of a client in return.
func TestLookupsNameThemselves(t *testing.T) {
	var agents []string
	l := lookupAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		agents = append(agents, r.UserAgent())
		_, _ = w.Write([]byte(`{"docs":[]}`))
	})
	_, _ = l.FindBooks(context.Background(), "q", 1)
	_, _ = l.DOI(context.Background(), "10.1/x")
	_, _, _, _ = l.Style(context.Background(), "x")
	for _, ua := range agents {
		if !strings.HasPrefix(ua, "corpus/") {
			t.Errorf("User-Agent = %q", ua)
		}
	}
}
