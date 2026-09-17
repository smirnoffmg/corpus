package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/smirnoffmg/corpus/internal/api"
	"github.com/smirnoffmg/corpus/internal/cite"
	"github.com/smirnoffmg/corpus/internal/corpus"
)

type fakeCitations struct {
	hashes  map[string]string
	entries map[string][]corpus.Citation
	citing  []corpus.CitingPaper
	shared  []corpus.Citation
	saved   map[string][]corpus.Citation
	askedA  string
	askedB  string
}

func (f *fakeCitations) SourceHash(_ context.Context, path string) (string, bool, error) {
	hash, ok := f.hashes[path]
	return hash, ok, nil
}

func (f *fakeCitations) Citations(_ context.Context, paper string) ([]corpus.Citation, error) {
	return f.entries[paper], nil
}

func (f *fakeCitations) Citing(_ context.Context, key, fingerprint string) ([]corpus.CitingPaper, error) {
	f.askedA, f.askedB = key, fingerprint
	return f.citing, nil
}

func (f *fakeCitations) SharedCitations(_ context.Context, a, b string) ([]corpus.Citation, error) {
	f.askedA, f.askedB = a, b
	return f.shared, nil
}

func (f *fakeCitations) UpdateCitations(_ context.Context, paper string, cs []corpus.Citation) error {
	if f.saved == nil {
		f.saved = map[string][]corpus.Citation{}
	}
	f.saved[paper] = cs
	return nil
}

func citationServer(t *testing.T, c api.Citations) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(api.New(&fakeStore{}, &fakeEmbedder{}, api.WithCitations(c)).Handler())
	t.Cleanup(srv.Close)
	return srv
}

func getJSON(t *testing.T, url string, into any) int {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK && into != nil {
		if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode
}

func TestCitationsOfAPublicationAreServedByItsPath(t *testing.T) {
	c := &fakeCitations{
		hashes: map[string]string{"mapreduce.pdf": "pa"},
		entries: map[string][]corpus.Citation{"pa": {
			{Ord: 1, Raw: "[1] M. Kleppmann. Designing Data-Intensive Applications. O'Reilly, 2017.",
				Title: "Designing Data-Intensive Applications", Year: 2017,
				Resolved: "bk", ResolvedPath: "uploads/kleppmann.pdf", ResolvedTitle: "Клеппман"},
		}},
	}
	srv := citationServer(t, c)

	var out struct {
		Path      string            `json:"path"`
		Citations []corpus.Citation `json:"citations"`
	}
	if code := getJSON(t, srv.URL+"/citations?path=mapreduce.pdf", &out); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if len(out.Citations) != 1 || out.Citations[0].Year != 2017 {
		t.Fatalf("citations = %+v", out.Citations)
	}
	if out.Citations[0].ResolvedPath != "uploads/kleppmann.pdf" {
		t.Errorf("a matched citation should say where the library keeps the work: %+v", out.Citations[0])
	}
}

func TestCitationsOfAnUnknownPathAre404(t *testing.T) {
	srv := citationServer(t, &fakeCitations{hashes: map[string]string{}})
	if code := getJSON(t, srv.URL+"/citations?path=absent.pdf", nil); code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", code)
	}
}

func TestCitingAsksByTheLibraryKeyForAPathAndByTheDOIOtherwise(t *testing.T) {
	c := &fakeCitations{hashes: map[string]string{"uploads/kleppmann.pdf": "bk"}}
	srv := citationServer(t, c)

	if code := getJSON(t, srv.URL+"/citations/citing?path=uploads/kleppmann.pdf", &struct{}{}); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if c.askedA != "bk" || c.askedB != "" {
		t.Errorf("asked (%q, %q), want the library key", c.askedA, c.askedB)
	}

	if code := getJSON(t, srv.URL+"/citations/citing?doi=10.1145/371920.372095", &struct{}{}); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if c.askedA != "" || c.askedB != "10.1145/371920.372095" {
		t.Errorf("asked (%q, %q), want the DOI as a fingerprint", c.askedA, c.askedB)
	}
}

func TestCitingNeedsSomethingToLookFor(t *testing.T) {
	srv := citationServer(t, &fakeCitations{})
	if code := getJSON(t, srv.URL+"/citations/citing", nil); code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
}

func TestSharedCitationsTakeTwoPaths(t *testing.T) {
	c := &fakeCitations{
		hashes: map[string]string{"a.pdf": "pa", "b.pdf": "pb"},
		shared: []corpus.Citation{{Ord: 1, Raw: "[1] Общая работа"}},
	}
	srv := citationServer(t, c)

	var out struct {
		Citations []corpus.Citation `json:"citations"`
	}
	if code := getJSON(t, srv.URL+"/citations/shared?a=a.pdf&b=b.pdf", &out); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if c.askedA != "pa" || c.askedB != "pb" {
		t.Errorf("asked (%q, %q)", c.askedA, c.askedB)
	}
	if len(out.Citations) != 1 {
		t.Errorf("citations = %+v", out.Citations)
	}
}

func TestCitationRoutesSayWhenTheyAreNotConfigured(t *testing.T) {
	srv := httptest.NewServer(api.New(&fakeStore{}, &fakeEmbedder{}).Handler())
	t.Cleanup(srv.Close)
	if code := getJSON(t, srv.URL+"/citations?path=a.pdf", nil); code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", code)
	}
}

func TestTheCitationsToolAnswersBothDirections(t *testing.T) {
	c := &fakeCitations{
		hashes:  map[string]string{"mapreduce.pdf": "pa"},
		entries: map[string][]corpus.Citation{"pa": {{Ord: 1, Raw: "[1] Kleppmann"}}},
		citing:  []corpus.CitingPaper{{Path: "other.pdf", Title: "Другая статья"}},
	}
	ctx := context.Background()
	serverSide, clientSide := mcp.NewInMemoryTransports()
	svc := api.New(&fakeStore{}, &fakeEmbedder{}, api.WithCitations(c))
	if _, err := svc.MCP().Connect(ctx, serverSide, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil).Connect(ctx, clientSide, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	call := func(args map[string]any) struct {
		Cited  []corpus.Citation    `json:"cited"`
		Citing []corpus.CitingPaper `json:"citing"`
	} {
		t.Helper()
		res, callErr := session.CallTool(ctx, &mcp.CallToolParams{Name: "corpus_citations", Arguments: args})
		if callErr != nil {
			t.Fatal(callErr)
		}
		raw, marshalErr := json.Marshal(res.StructuredContent)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		var body struct {
			Cited  []corpus.Citation    `json:"cited"`
			Citing []corpus.CitingPaper `json:"citing"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		return body
	}

	cited := call(map[string]any{"path": "mapreduce.pdf"})
	if len(cited.Cited) != 1 || cited.Cited[0].Raw != "[1] Kleppmann" {
		t.Errorf("cited = %+v", cited.Cited)
	}
	citing := call(map[string]any{"path": "mapreduce.pdf", "direction": "citing"})
	if len(citing.Citing) != 1 || citing.Citing[0].Path != "other.pdf" {
		t.Errorf("citing = %+v", citing.Citing)
	}
}

type registryLookup struct {
	records map[string]corpus.CSL
	asked   []string
	err     error
}

func (f *registryLookup) DOI(_ context.Context, doi string) (corpus.CSL, error) {
	f.asked = append(f.asked, doi)
	if f.err != nil {
		return nil, f.err
	}
	csl, ok := f.records[doi]
	if !ok {
		return nil, cite.ErrNotFound
	}
	return csl, nil
}

func (f *registryLookup) ISBN(context.Context, string) (corpus.CSL, error) {
	return nil, cite.ErrNotFound
}

func (f *registryLookup) Style(context.Context, string) (string, string, string, error) {
	return "", "", "", cite.ErrNotFound
}

func TestEnrichFillsInWhatTheRegistryKnowsAndAsksOnlyForWhatIsMissing(t *testing.T) {
	doi := "10.1145/371920.372095"
	c := &fakeCitations{
		hashes: map[string]string{"a.pdf": "pa"},
		entries: map[string][]corpus.Citation{"pa": {
			{Ord: 1, Raw: "[1] Melnik et al.", DOI: doi, Fingerprint: doi},
			{Ord: 2, Raw: "[2] Kleppmann. Designing. 2017.", Title: "Designing", Year: 2017, Fingerprint: "fp-2"},
			{Ord: 3, Raw: "[3] Уже разобранная работа", DOI: "10.0000/known", Title: "Известная", Authors: "Кто-то", Year: 2001, Fingerprint: "10.0000/known"},
		}},
	}
	look := &registryLookup{records: map[string]corpus.CSL{doi: {
		"title":           "Building a distributed full-text index for the web",
		"container-title": "Proceedings of WWW",
		"issued":          map[string]any{"date-parts": []any{[]any{2001}}},
		"author":          []any{map[string]any{"family": "Melnik", "given": "Sergey"}},
	}}}
	srv := httptest.NewServer(api.New(&fakeStore{}, &fakeEmbedder{},
		api.WithCitations(c), api.WithBibliography(nil, look)).Handler())
	t.Cleanup(srv.Close)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/citations/enrich?path=a.pdf", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	if len(look.asked) != 1 || look.asked[0] != doi {
		t.Fatalf("asked doi.org for %v, want only the entry that had a DOI and no fields", look.asked)
	}
	saved := c.saved["pa"]
	if len(saved) != 3 {
		t.Fatalf("saved %d citations, want all three back", len(saved))
	}
	if saved[0].Title != "Building a distributed full-text index for the web" || saved[0].Year != 2001 {
		t.Errorf("entry 1 = %+v", saved[0])
	}
	if saved[0].Authors != "Melnik, Sergey" || saved[0].Container != "Proceedings of WWW" {
		t.Errorf("entry 1 = %+v", saved[0])
	}
	if saved[0].Raw != "[1] Melnik et al." {
		t.Error("the line as printed is not the registry's to overwrite")
	}
	if saved[1].Title != "Designing" || saved[2].Title != "Известная" {
		t.Error("entries that were not looked up should come back untouched")
	}
}

func TestEnrichNeedsTheNetworkToBeConfigured(t *testing.T) {
	srv := httptest.NewServer(api.New(&fakeStore{}, &fakeEmbedder{},
		api.WithCitations(&fakeCitations{hashes: map[string]string{"a.pdf": "pa"}})).Handler())
	t.Cleanup(srv.Close)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/citations/enrich?path=a.pdf", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}
