package api_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/smirnoffmg/corpus/internal/api"
	"github.com/smirnoffmg/corpus/internal/cite"
	"github.com/smirnoffmg/corpus/internal/corpus"
)

type fakeBibliography struct {
	mu     sync.Mutex
	refs   map[string]corpus.Reference
	styles map[string][2]string
}

func newBibliography() *fakeBibliography {
	return &fakeBibliography{refs: map[string]corpus.Reference{}, styles: map[string][2]string{}}
}

func (f *fakeBibliography) ReferenceKey(_ context.Context, kind, path string) (string, error) {
	switch kind {
	case "book":
		return "hash:" + path, nil
	case "docs":
		return "manual:" + strings.Split(path, "/")[0], nil
	}
	return "", fmt.Errorf("%w: %s", corpus.ErrNoReference, kind)
}

func (f *fakeBibliography) Reference(_ context.Context, key string) (corpus.Reference, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.refs[key]
	if !ok {
		return r, corpus.ErrNoReference
	}
	return r, nil
}

func (f *fakeBibliography) References(context.Context) ([]corpus.Reference, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]corpus.Reference, 0, len(f.refs))
	for _, r := range f.refs {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeBibliography) SaveReference(_ context.Context, key string, csl corpus.CSL, status string) (corpus.Reference, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := corpus.Reference{Key: key, CiteKey: cite.KeyBase(csl), CSL: csl, Status: status}
	f.refs[key] = r
	return r, nil
}

func (f *fakeBibliography) Styles(context.Context) ([]corpus.Style, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []corpus.Style
	for id, s := range f.styles {
		out = append(out, corpus.Style{ID: id, Title: s[0]})
	}
	return out, nil
}

func (f *fakeBibliography) StyleXML(_ context.Context, id string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.styles[id]
	if !ok {
		return "", corpus.ErrNoReference
	}
	return s[1], nil
}

func (f *fakeBibliography) SaveStyle(_ context.Context, id, title, xml string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.styles[id] = [2]string{title, xml}
	return nil
}

type fakeLookup struct{ err error }

func (f fakeLookup) DOI(_ context.Context, doi string) (corpus.CSL, error) {
	return corpus.CSL{"DOI": doi, "type": "article-journal"}, f.err
}

func (f fakeLookup) ISBN(_ context.Context, isbn string) (corpus.CSL, error) {
	return corpus.CSL{"ISBN": isbn, "type": "book"}, f.err
}

func (f fakeLookup) Style(_ context.Context, name string) (string, string, string, error) {
	return name, "Journal " + name, "<style/>", f.err
}

func bibServer(t *testing.T, b api.Bibliography, l api.Lookup) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(api.New(&fakeStore{}, &fakeEmbedder{}, api.WithBibliography(b, l)).Handler())
	t.Cleanup(srv.Close)
	return srv
}

func send(t *testing.T, method, url, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestADescriptionIsReadAndSavedBySource(t *testing.T) {
	srv := bibServer(t, newBibliography(), nil)
	url := srv.URL + "/bibliography?kind=book&path=k.pdf"

	code, body := send(t, http.MethodGet, url, "")
	if code != http.StatusOK || !strings.Contains(body, `"key": "hash:k.pdf"`) || !strings.Contains(body, `"reference": null`) {
		t.Fatalf("GET before saving = %d %s", code, body)
	}

	code, body = send(t, http.MethodPut, url, `{"csl":{"type":"book","title":"T","author":[{"family":"Knuth"}]},"status":"checked"}`)
	if code != http.StatusOK || !strings.Contains(body, `"citekey": "knuth"`) {
		t.Fatalf("PUT = %d %s", code, body)
	}

	code, body = send(t, http.MethodGet, url, "")
	if code != http.StatusOK || !strings.Contains(body, `"status": "checked"`) {
		t.Errorf("GET after saving = %d %s", code, body)
	}
}

func TestSavingRejectsWhatIsNotADescription(t *testing.T) {
	srv := bibServer(t, newBibliography(), nil)
	for label, body := range map[string]string{
		"no csl":     `{"status":"draft"}`,
		"bad status": `{"csl":{"title":"T"},"status":"final"}`,
		"not json":   `title=T`,
	} {
		if code, _ := send(t, http.MethodPut, srv.URL+"/bibliography?kind=book&path=k.pdf", body); code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", label, code)
		}
	}
	if code, _ := send(t, http.MethodGet, srv.URL+"/bibliography?kind=vault&path=n.md", ""); code != http.StatusNotFound {
		t.Errorf("a note: status = %d, want 404", code)
	}
}

func TestLookupAnswersByWhatWentWrong(t *testing.T) {
	cases := []struct {
		name   string
		lookup api.Lookup
		body   string
		want   int
	}{
		{"doi", fakeLookup{}, `{"doi":"10.1/x"}`, http.StatusOK},
		{"isbn", fakeLookup{}, `{"isbn":"9781449373320"}`, http.StatusOK},
		{"both", fakeLookup{}, `{"doi":"10.1/x","isbn":"1"}`, http.StatusBadRequest},
		{"not found", fakeLookup{err: cite.ErrNotFound}, `{"isbn":"1"}`, http.StatusNotFound},
		{"offline", fakeLookup{err: errors.New("dial tcp: no route to host")}, `{"doi":"10.1/x"}`, http.StatusBadGateway},
		{"not configured", nil, `{"doi":"10.1/x"}`, http.StatusServiceUnavailable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := bibServer(t, newBibliography(), c.lookup)
			if code, body := send(t, http.MethodPost, srv.URL+"/bibliography/lookup", c.body); code != c.want {
				t.Errorf("status = %d (%s), want %d", code, body, c.want)
			}
		})
	}
}

func TestExportWritesBibLaTeXAndCSLJSON(t *testing.T) {
	b := newBibliography()
	b.refs["h"] = corpus.Reference{Key: "h", CiteKey: "knuth1968", CSL: corpus.CSL{"type": "book", "title": "TAOCP"}}
	srv := bibServer(t, b, nil)

	resp, err := http.Get(srv.URL + "/bibliography/export?format=biblatex")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "@book{knuth1968,") || !strings.Contains(resp.Header.Get("Content-Disposition"), "corpus.bib") {
		t.Errorf("biblatex = %s (%s)", body, resp.Header.Get("Content-Disposition"))
	}

	var items []map[string]any
	get(t, srv.URL+"/bibliography/export?format=csl-json", &items)
	if len(items) != 1 || items[0]["id"] != "knuth1968" || items[0]["title"] != "TAOCP" {
		t.Errorf("csl-json = %v", items)
	}
	if _, ok := b.refs["h"].CSL["id"]; ok {
		t.Error("export wrote the id into the stored record")
	}

	if code, _ := send(t, http.MethodGet, srv.URL+"/bibliography/export?format=ris", ""); code != http.StatusBadRequest {
		t.Errorf("unknown format: status = %d, want 400", code)
	}
}

const nature = `<style xmlns="http://purl.org/net/xbiblio/csl" version="1.0"><info><title>Nature</title><id>http://www.zotero.org/styles/nature</id></info></style>`

func TestStylesAreAddedByFileOrByName(t *testing.T) {
	b := newBibliography()
	srv := bibServer(t, b, fakeLookup{})

	if code, body := send(t, http.MethodPost, srv.URL+"/styles", nature); code != http.StatusCreated || !strings.Contains(body, `"id": "nature"`) {
		t.Fatalf("upload = %d %s", code, body)
	}
	if code, _ := send(t, http.MethodPost, srv.URL+"/styles", "<html/>"); code != http.StatusBadRequest {
		t.Errorf("a non-style: status = %d, want 400", code)
	}
	dependent := `<style xmlns="http://purl.org/net/xbiblio/csl" version="1.0"><info><title>J</title><id>x/j</id><link rel="independent-parent" href="x/apa"/></info></style>`
	if code, _ := send(t, http.MethodPost, srv.URL+"/styles", dependent); code != http.StatusBadRequest {
		t.Errorf("a dependent style: status = %d, want 400", code)
	}
	if code, body := send(t, http.MethodPost, srv.URL+"/styles/fetch", `{"name":"cell"}`); code != http.StatusCreated || !strings.Contains(body, "Journal cell") {
		t.Errorf("fetch = %d %s", code, body)
	}

	var list struct {
		Styles []corpus.Style `json:"styles"`
	}
	get(t, srv.URL+"/styles", &list)
	if len(list.Styles) != 2 {
		t.Errorf("styles = %v", list.Styles)
	}
	if code, body := send(t, http.MethodGet, srv.URL+"/styles/nature", ""); code != http.StatusOK || body != nature {
		t.Errorf("style xml = %d %s", code, body)
	}
	if code, _ := send(t, http.MethodGet, srv.URL+"/styles/absent", ""); code != http.StatusNotFound {
		t.Errorf("absent style: status = %d, want 404", code)
	}
}

func TestBibliographyWithoutAStoreIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(api.New(&fakeStore{}, &fakeEmbedder{}).Handler())
	defer srv.Close()
	if code, _ := send(t, http.MethodGet, srv.URL+"/bibliography?kind=book&path=a.pdf", ""); code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", code)
	}
}
