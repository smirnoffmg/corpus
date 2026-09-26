package cite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

// ErrNotFound is a lookup that reached the service and learned nothing.
var ErrNotFound = errors.New("no record for this identifier")

// Lookup fills a record from the identifier printed in the book. It is the one
// part of the corpus that needs the internet; offline it fails and the form is
// filled by hand.
type Lookup struct {
	DOIBase         string // https://doi.org
	OpenLibraryBase string // https://openlibrary.org
	StylesBase      string // the CSL styles repository; empty for the official one
	HTTP            *http.Client
	// OpenLibraryRate spaces out requests to Open Library, which serves a
	// client that sends no contact address one request a second and drops
	// the connection beyond it. Tool calls arrive in parallel, so the limit is
	// kept here, shared by every caller, rather than by any one of them.
	OpenLibraryRate *rate.Limiter
}

// userAgent names the program to the services it asks. Open Library would
// raise its limit for an address added here; none is, so as not to hand the
// user's out.
const userAgent = "corpus/0.3.0 (+https://github.com/smirnoffmg/corpus)"

func NewLookup() *Lookup {
	return &Lookup{
		DOIBase: "https://doi.org", OpenLibraryBase: "https://openlibrary.org",
		HTTP:            &http.Client{Timeout: 20 * time.Second},
		OpenLibraryRate: rate.NewLimiter(rate.Every(time.Second), 1),
	}
}

// DOI asks doi.org for CSL-JSON directly: content negotiation hands back the
// record in the very format the corpus stores, from Crossref or DataCite.
func (l *Lookup) DOI(ctx context.Context, doi string) (corpus.CSL, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.DOIBase+"/"+strings.TrimSpace(doi), http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.citationstyles.csl+json")
	var record corpus.CSL
	if err := l.get(req, &record); err != nil {
		return nil, err
	}
	// Registry bookkeeping — reference counts, licences, indexing dates — is
	// not part of a description.
	for _, noise := range []string{"indexed", "reference-count", "references-count", "is-referenced-by-count", "license", "link", "content-domain", "deposited", "score", "resource", "relation", "member", "prefix", "source", "reference", "subject", "funder", "assertion", "update-policy", "created", "id"} {
		delete(record, noise)
	}
	if kind, ok := record["type"].(string); ok {
		if csl, known := registryTypes[kind]; known {
			record["type"] = csl
		}
	}
	if record["type"] == "book" && record["collection-title"] == nil {
		if series, ok := record["container-title"]; ok {
			record["collection-title"] = series
			delete(record, "container-title")
		}
	}
	return record, nil
}

// registryTypes maps the type names Crossref and DataCite put into their
// CSL-JSON onto CSL's own; names already in CSL pass through.
var registryTypes = map[string]string{
	"journal-article":     "article-journal",
	"proceedings-article": "paper-conference",
	"book-chapter":        "chapter",
	"book-section":        "chapter",
	"reference-entry":     "entry-encyclopedia",
	"posted-content":      "article",
	"dissertation":        "thesis",
	"monograph":           "book",
	"edited-book":         "book",
	"reference-book":      "book",
	"book-set":            "book",
}

type openLibraryBook struct {
	Title         string `json:"title"`
	Subtitle      string `json:"subtitle"`
	NumberOfPages int    `json:"number_of_pages"`
	PublishDate   string `json:"publish_date"`
	Authors       []struct {
		Name string `json:"name"`
	} `json:"authors"`
	Publishers []struct {
		Name string `json:"name"`
	} `json:"publishers"`
	PublishPlaces []struct {
		Name string `json:"name"`
	} `json:"publish_places"`
}

// ISBN asks Open Library. Its coverage of Russian books is thin, which is why
// every looked-up record is shown for review before it replaces anything.
func (l *Lookup) ISBN(ctx context.Context, isbn string) (corpus.CSL, error) {
	digits := strings.NewReplacer("-", "", " ", "").Replace(isbn)
	q := url.Values{"bibkeys": {"ISBN:" + digits}, "format": {"json"}, "jscmd": {"data"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.OpenLibraryBase+"/api/books?"+q.Encode(), http.NoBody)
	if err != nil {
		return nil, err
	}
	var found map[string]openLibraryBook
	if err := l.getOpenLibrary(req, &found); err != nil {
		return nil, err
	}
	book, ok := found["ISBN:"+digits]
	if !ok {
		return nil, ErrNotFound
	}
	record := corpus.CSL{"type": "book", "title": book.Title, "ISBN": digits}
	if book.Subtitle != "" {
		record["title"] = book.Title + " : " + book.Subtitle
	}
	var authors []map[string]any
	for _, a := range book.Authors {
		authors = append(authors, ParseAuthors(a.Name)...)
	}
	if authors != nil {
		record["author"] = authors
	}
	if len(book.Publishers) > 0 {
		record["publisher"] = book.Publishers[0].Name
	}
	if len(book.PublishPlaces) > 0 {
		record["publisher-place"] = book.PublishPlaces[0].Name
	}
	if book.NumberOfPages > 0 {
		record["number-of-pages"] = strconv.Itoa(book.NumberOfPages)
	}
	if year := lastYear(book.PublishDate); year != "" {
		record["issued"] = map[string]any{"date-parts": []any{[]any{year}}}
	}
	return record, nil
}

// foundFields is every field FindBooks asks for. Open Library also knows
// whether a scan of the book can be read or borrowed; that is left out on
// purpose, since getting the text is a licence question this search does not
// answer.
//
// ISBNs are left out too: the search lists every edition's at once, in no
// order, and a buyer needs one edition's. BookEditions has them per edition.
const foundFields = "key,title,subtitle,author_name,first_publish_year,publisher,language,edition_count"

// foundPerWork caps the publishers listed for one work: a popular book has
// hundreds of editions.
const foundPerWork = 5

type openLibraryWork struct {
	Key          string   `json:"key"`
	Title        string   `json:"title"`
	Subtitle     string   `json:"subtitle"`
	Authors      []string `json:"author_name"`
	Year         int      `json:"first_publish_year"`
	Publishers   []string `json:"publisher"`
	Languages    []string `json:"language"`
	EditionCount int      `json:"edition_count"`
}

// FindBooks searches Open Library's catalogue by title, author or any words
// of them, for books the library may not hold. limit <= 0 means 10.
func (l *Lookup) FindBooks(ctx context.Context, query string, limit int) ([]corpus.FoundBook, error) {
	if limit <= 0 {
		limit = 10
	}
	q := url.Values{"q": {strings.TrimSpace(query)}, "fields": {foundFields}, "limit": {strconv.Itoa(limit)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.OpenLibraryBase+"/search.json?"+q.Encode(), http.NoBody)
	if err != nil {
		return nil, err
	}
	var result struct {
		Docs []openLibraryWork `json:"docs"`
	}
	if err := l.getOpenLibrary(req, &result); err != nil {
		return nil, err
	}
	found := make([]corpus.FoundBook, 0, len(result.Docs))
	for i := range result.Docs {
		w := &result.Docs[i]
		book := corpus.FoundBook{
			Title: w.Title, Authors: w.Authors, Year: w.Year,
			Publishers: firstFew(w.Publishers),
			Languages:  w.Languages, Editions: w.EditionCount,
			Catalog: l.OpenLibraryBase + w.Key,
		}
		if w.Subtitle != "" {
			book.Title = w.Title + " : " + w.Subtitle
		}
		found = append(found, book)
	}
	return found, nil
}

// openLibraryEdition decodes only what describes the edition. The record also
// carries ocaid, the id of a scan in the Internet Archive; it is not read, so
// it cannot reach an answer.
type openLibraryEdition struct {
	Key         string   `json:"key"`
	Title       string   `json:"title"`
	FullTitle   string   `json:"full_title"`
	PublishDate string   `json:"publish_date"`
	EditionName string   `json:"edition_name"`
	Publishers  []string `json:"publishers"`
	ISBN13      []string `json:"isbn_13"`
	ISBN10      []string `json:"isbn_10"`
	Format      string   `json:"physical_format"`
	Languages   []struct {
		Key string `json:"key"`
	} `json:"languages"`
}

// editionsPerRequest is how many editions one request asks for. The catalogue
// lists them in no order, so finding the newest takes all of them; the most
// published work met so far had 253, and one request returned them all.
const editionsPerRequest = 1000

var workKey = regexp.MustCompile(`^(?:https?://[^/]+)?(?:/works/)?(OL[0-9]+W)$`)

// ErrNotAWork is an identifier that names no Open Library work.
var ErrNotAWork = errors.New("not an Open Library work: expected OL…W, /works/OL…W or its catalogue page")

// BookEditions lists a work's editions that have an ISBN, newest first, at
// most limit of them (limit <= 0 means 10), and how many editions the work
// has in all. work is what FindBooks returned as the catalogue page, or the
// OL…W key in it.
func (l *Lookup) BookEditions(ctx context.Context, work string, limit int) ([]corpus.Edition, int, error) {
	m := workKey.FindStringSubmatch(strings.TrimSpace(work))
	if m == nil {
		return nil, 0, ErrNotAWork
	}
	if limit <= 0 {
		limit = 10
	}
	q := url.Values{"limit": {strconv.Itoa(editionsPerRequest)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.OpenLibraryBase+"/works/"+m[1]+"/editions.json?"+q.Encode(), http.NoBody)
	if err != nil {
		return nil, 0, err
	}
	var result struct {
		Size    int                  `json:"size"`
		Entries []openLibraryEdition `json:"entries"`
	}
	if err := l.getOpenLibrary(req, &result); err != nil {
		return nil, 0, err
	}
	editions := make([]corpus.Edition, 0, len(result.Entries))
	for i := range result.Entries {
		e := &result.Entries[i]
		if len(e.ISBN13) == 0 && len(e.ISBN10) == 0 {
			continue
		}
		edition := corpus.Edition{
			Title: e.Title, Published: e.PublishDate, Edition: e.EditionName,
			Publishers: e.Publishers, ISBN13: e.ISBN13, ISBN10: e.ISBN10, Format: e.Format,
			Catalog: l.OpenLibraryBase + e.Key,
		}
		if e.FullTitle != "" {
			edition.Title = e.FullTitle
		}
		edition.Year, _ = strconv.Atoi(lastYear(e.PublishDate))
		for _, lang := range e.Languages {
			edition.Languages = append(edition.Languages, strings.TrimPrefix(lang.Key, "/languages/"))
		}
		editions = append(editions, edition)
	}
	slices.SortStableFunc(editions, func(a, b corpus.Edition) int { return b.Year - a.Year })
	return editions[:min(len(editions), limit)], result.Size, nil
}

func firstFew(s []string) []string {
	return s[:min(len(s), foundPerWork)]
}

func (l *Lookup) getOpenLibrary(req *http.Request, into any) error {
	if err := l.OpenLibraryRate.Wait(req.Context()); err != nil {
		return err
	}
	return l.get(req, into)
}

func (l *Lookup) get(req *http.Request, into any) error {
	req.Header.Set("User-Agent", userAgent)
	resp, err := l.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", req.URL.Host, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(into)
}

func lastYear(s string) string {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r < '0' || r > '9' })
	for i := len(fields) - 1; i >= 0; i-- {
		if len(fields[i]) == 4 {
			return fields[i]
		}
	}
	return ""
}

// StylesBase is where CSL styles are fetched from by name.
const StylesBase = "https://raw.githubusercontent.com/citation-style-language/styles/master"

// Style fetches a CSL style by its file name in the official repository, e.g.
// "nature" or "gost-r-7-0-5-2008". Most journal styles there are dependent —
// an alias pointing at the style they share — so the parent is fetched in
// their place, under the journal's own title.
func (l *Lookup) Style(ctx context.Context, name string) (id, title, xml string, err error) {
	base := l.StylesBase
	if base == "" {
		base = StylesBase
	}
	name = strings.TrimSuffix(strings.TrimSpace(name), ".csl")
	body, err := l.raw(ctx, base+"/"+url.PathEscape(name)+".csl")
	if errors.Is(err, ErrNotFound) {
		body, err = l.raw(ctx, base+"/dependent/"+url.PathEscape(name)+".csl")
	}
	if err != nil {
		return "", "", "", err
	}
	info, err := ParseStyle(body)
	if err != nil {
		return "", "", "", err
	}
	if info.Parent != "" {
		parent := info.Parent[strings.LastIndex(info.Parent, "/")+1:]
		if body, err = l.raw(ctx, base+"/"+url.PathEscape(parent)+".csl"); err != nil {
			return "", "", "", err
		}
	}
	return name, info.Title, body, nil
}

func (l *Lookup) raw(ctx context.Context, address string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, http.NoBody)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := l.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s answered %s", req.URL.Host, resp.Status)
	}
	var b strings.Builder
	_, err = io.Copy(&b, io.LimitReader(resp.Body, 2<<20))
	return b.String(), err
}
