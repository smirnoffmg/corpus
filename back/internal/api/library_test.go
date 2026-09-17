package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/smirnoffmg/corpus/internal/api"
	"github.com/smirnoffmg/corpus/internal/corpus"
	"github.com/smirnoffmg/corpus/internal/upload"
)

type fakeLibrary struct {
	name, body string
	kind       string
	err        error
}

func (f *fakeLibrary) add(kind, name string, r io.Reader, path string) (string, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	f.kind, f.name, f.body = kind, name, string(b)
	if f.err != nil {
		return "", f.err
	}
	return path, nil
}

func (f *fakeLibrary) AddBook(name string, r io.Reader) (string, error) {
	return f.add("book", name, r, "uploads/"+name)
}

func (f *fakeLibrary) AddPaper(name string, r io.Reader) (string, error) {
	return f.add("paper", name, r, "uploads/"+name)
}

func (f *fakeLibrary) AddManual(name string, r io.Reader) (string, error) {
	return f.add("docs", name, r, name)
}

func multipartBody(t *testing.T, field, filename, content string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	w, err := mw.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, content); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, mw.FormDataContentType()
}

func post(t *testing.T, url, contentType string, body io.Reader) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func uploadServer(t *testing.T, store *fakeStore, lib api.Library, maxBytes int64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(api.New(store, &fakeEmbedder{}, api.WithLibrary(lib, maxBytes)).Handler())
	t.Cleanup(srv.Close)
	return srv
}

func TestUploadingAPaperSavesItAmongThePapers(t *testing.T) {
	store, lib := &fakeStore{}, &fakeLibrary{}
	srv := uploadServer(t, store, lib, 1<<20)

	body, ct := multipartBody(t, "file", "MapReduce.pdf", "%PDF-1.7 bytes")
	resp, text := post(t, srv.URL+"/upload?kind=paper", ct, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d (%s), want 201", resp.StatusCode, text)
	}
	if lib.kind != "paper" || lib.name != "MapReduce.pdf" {
		t.Errorf("library got %s %q, want paper MapReduce.pdf", lib.kind, lib.name)
	}
	if store.reindexRequests != 1 {
		t.Errorf("reindex requested %d times, want 1", store.reindexRequests)
	}
}

func TestUploadingABookSavesItAndWakesTheIndexer(t *testing.T) {
	store, lib := &fakeStore{}, &fakeLibrary{}
	srv := uploadServer(t, store, lib, 1<<20)

	body, ct := multipartBody(t, "file", "Клеппман.pdf", "%PDF-1.7 bytes")
	resp, text := post(t, srv.URL+"/upload?kind=book", ct, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d (%s), want 201", resp.StatusCode, text)
	}
	if lib.kind != "book" || lib.name != "Клеппман.pdf" || lib.body != "%PDF-1.7 bytes" {
		t.Errorf("library got %s %q %q", lib.kind, lib.name, lib.body)
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if out["kind"] != "book" || out["path"] != "uploads/Клеппман.pdf" {
		t.Errorf("response = %v", out)
	}
	if store.reindexRequests != 1 {
		t.Errorf("reindex requested %d times, want 1", store.reindexRequests)
	}
}

func TestUploadingAManualNamesItFromTheParameterOrTheFile(t *testing.T) {
	cases := map[string]struct{ query, filename, want string }{
		"explicit name":  {"&manual=nltk", "site.zip", "nltk"},
		"from file name": {"", "scikit-learn docs.zip", "scikit-learn-docs"},
	}
	for label, c := range cases {
		t.Run(label, func(t *testing.T) {
			lib := &fakeLibrary{}
			srv := uploadServer(t, &fakeStore{}, lib, 1<<20)
			body, ct := multipartBody(t, "file", c.filename, "PK zip")
			resp, text := post(t, srv.URL+"/upload?kind=docs"+c.query, ct, body)
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("status = %d (%s)", resp.StatusCode, text)
			}
			if lib.kind != "docs" || lib.name != c.want {
				t.Errorf("library got %s %q, want docs %q", lib.kind, lib.name, c.want)
			}
		})
	}
}

func TestUploadFailuresMapToStatusCodes(t *testing.T) {
	cases := map[string]struct {
		libErr error
		want   int
	}{
		"exists":    {fmt.Errorf("wrapped: %w", upload.ErrExists), http.StatusConflict},
		"invalid":   {upload.ErrInvalid, http.StatusBadRequest},
		"too large": {upload.ErrTooLarge, http.StatusRequestEntityTooLarge},
		"disk full": {errors.New("no space left on device"), http.StatusInternalServerError},
	}
	for label, c := range cases {
		t.Run(label, func(t *testing.T) {
			store := &fakeStore{}
			srv := uploadServer(t, store, &fakeLibrary{err: c.libErr}, 1<<20)
			body, ct := multipartBody(t, "file", "a.pdf", "%PDF-")
			resp, text := post(t, srv.URL+"/upload?kind=book", ct, body)
			if resp.StatusCode != c.want {
				t.Errorf("status = %d (%s), want %d", resp.StatusCode, text, c.want)
			}
			if store.reindexRequests != 0 {
				t.Error("a failed upload requested a reindex")
			}
		})
	}
}

func TestUploadRejectsMalformedRequests(t *testing.T) {
	srv := uploadServer(t, &fakeStore{}, &fakeLibrary{}, 1<<20)

	body, ct := multipartBody(t, "file", "a.pdf", "%PDF-")
	if resp, _ := post(t, srv.URL+"/upload?kind=vault", ct, body); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("kind=vault: status = %d, want 400 — the vault belongs to Obsidian", resp.StatusCode)
	}

	body, ct = multipartBody(t, "attachment", "a.pdf", "%PDF-")
	if resp, _ := post(t, srv.URL+"/upload?kind=book", ct, body); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("no file part: status = %d, want 400", resp.StatusCode)
	}

	if resp, _ := post(t, srv.URL+"/upload?kind=book", "application/pdf", strings.NewReader("%PDF-")); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("not multipart: status = %d, want 400", resp.StatusCode)
	}

	small := uploadServer(t, &fakeStore{}, &fakeLibrary{}, 512)
	body, ct = multipartBody(t, "file", "a.pdf", "%PDF-"+strings.Repeat("x", 1024))
	if resp, _ := post(t, small.URL+"/upload?kind=book", ct, body); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("over the limit: status = %d, want 413", resp.StatusCode)
	}
}

func TestUploadWithoutALibraryIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(api.New(&fakeStore{}, &fakeEmbedder{}).Handler())
	defer srv.Close()

	body, ct := multipartBody(t, "file", "a.pdf", "%PDF-")
	if resp, _ := post(t, srv.URL+"/upload?kind=book", ct, body); resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestSourcesListsWithTheGivenFilter(t *testing.T) {
	store := &fakeStore{sources: []corpus.SourceStatus{{Kind: "docs", Path: "nltk/index.html", Chunks: 3, Embedded: 1}}}
	srv := httptest.NewServer(api.New(store, &fakeEmbedder{}).Handler())
	defer srv.Close()

	var body struct {
		Sources []corpus.SourceStatus `json:"sources"`
	}
	get(t, srv.URL+"/sources?kind=docs&prefix=nltk/", &body)
	if store.lastKind != "docs" || store.lastPrefix != "nltk/" {
		t.Errorf("filter = %q %q, want docs nltk/", store.lastKind, store.lastPrefix)
	}
	if len(body.Sources) != 1 || body.Sources[0].Embedded != 1 {
		t.Errorf("sources = %+v", body.Sources)
	}

	store.sources = nil
	resp, err := http.Get(srv.URL + "/sources")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(raw), "null") {
		t.Errorf("body = %s, want an empty list", raw)
	}
}
