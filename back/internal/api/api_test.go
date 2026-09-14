package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/smirnoffmg/corpus/internal/api"
	"github.com/smirnoffmg/corpus/internal/corpus"
)

type fakeStore struct {
	text            []corpus.Hit
	semantic        []corpus.Hit
	lastText        int
	lastNorm        int
	sources         []corpus.SourceStatus
	lastKind        string
	lastPrefix      string
	reindexRequests int
}

func (f *fakeStore) Sources(_ context.Context, kind, prefix string) ([]corpus.SourceStatus, error) {
	f.lastKind, f.lastPrefix = kind, prefix
	return f.sources, nil
}

func (f *fakeStore) RequestReindex(context.Context) error {
	f.reindexRequests++
	return nil
}

func (f *fakeStore) Search(_ context.Context, q corpus.Query) ([]corpus.Hit, error) {
	f.lastText = q.Limit
	f.lastNorm = q.Normalization
	return f.text, nil
}

func (f *fakeStore) SearchVector(_ context.Context, _ []float32, _ string, _ int) ([]corpus.Hit, error) {
	return f.semantic, nil
}

func (f *fakeStore) Read(_ context.Context, id int64, _ bool) (corpus.Passage, error) {
	return corpus.Passage{Locator: "с. 1", Body: "body"}, nil
}

func (f *fakeStore) Stats(context.Context) (int64, int64, error) { return 1, 2, nil }

type fakeEmbedder struct {
	calls int
	err   error
}

func (f *fakeEmbedder) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := make([][]float32, len(inputs))
	for i := range out {
		out[i] = []float32{0.1, 0.2}
	}
	return out, nil
}

func TestHybridFallsBackToTextWhenEmbedderIsDown(t *testing.T) {
	store := &fakeStore{text: []corpus.Hit{{ID: 1}, {ID: 2}}}
	embedder := &fakeEmbedder{err: errors.New("connection refused")}

	hits, err := api.New(store, embedder).Search(context.Background(), corpus.Query{Text: "агрегат", Kind: "", Mode: "hybrid", Limit: 10, PerSource: 0})
	if err != nil {
		t.Fatalf("hybrid returned an error instead of degrading: %v", err)
	}
	if len(hits) != 2 || hits[0].ID != 1 {
		t.Errorf("hits = %+v, want the text leg unchanged", hits)
	}
}

func TestFtsModeNeverEmbeds(t *testing.T) {
	embedder := &fakeEmbedder{}
	if _, err := api.New(&fakeStore{}, embedder).Search(context.Background(), corpus.Query{Text: "q", Kind: "", Mode: "fts", Limit: 10, PerSource: 0}); err != nil {
		t.Fatal(err)
	}
	if embedder.calls != 0 {
		t.Errorf("embedder called %d times in fts mode, want 0", embedder.calls)
	}
}

func TestVectorModeReportsEmbedderFailure(t *testing.T) {
	embedder := &fakeEmbedder{err: errors.New("model not found")}
	_, err := api.New(&fakeStore{}, embedder).Search(context.Background(), corpus.Query{Text: "q", Kind: "", Mode: "vector", Limit: 10, PerSource: 0})
	if err == nil {
		t.Fatal("vector mode swallowed the embedder error")
	}
}

func TestCompareEmbedsTheQueryOnce(t *testing.T) {
	embedder := &fakeEmbedder{}
	out, err := api.New(&fakeStore{}, embedder).Compare(context.Background(), corpus.Query{Text: "q", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if embedder.calls != 1 {
		t.Errorf("embedded the query %d times, want 1", embedder.calls)
	}
	for _, mode := range []string{"fts", "vector", "hybrid"} {
		if _, ok := out[mode]; !ok {
			t.Errorf("compare is missing the %q leg", mode)
		}
	}
}

func TestHybridAsksBothLegsForMoreThanItReturns(t *testing.T) {
	// Fusing two top-10 lists into a top-10 needs deeper inputs, or a result
	// ranked 11th by text and 1st by vector can never surface.
	store := &fakeStore{}
	if _, err := api.New(store, &fakeEmbedder{}).Search(context.Background(), corpus.Query{Text: "q", Kind: "", Mode: "hybrid", Limit: 10, PerSource: 0}); err != nil {
		t.Fatal(err)
	}
	if store.lastText <= 10 {
		t.Errorf("text leg asked for %d hits, want more than the 10 returned", store.lastText)
	}
}

func TestSearchHandlerReturnsAnEmptyListNotNull(t *testing.T) {
	srv := httptest.NewServer(api.New(&fakeStore{text: []corpus.Hit{}}, &fakeEmbedder{}).Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/search?q=nothing&mode=fts")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var body map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if got := string(body["hits"]); strings.Contains(got, "null") {
		t.Errorf("hits = %s, want []", got)
	}
}

func TestReadHandlerRejectsANonNumericID(t *testing.T) {
	srv := httptest.NewServer(api.New(&fakeStore{}, &fakeEmbedder{}).Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/read?id=abc")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestPerSourceCapsOneSourceFillingThePage(t *testing.T) {
	// Three sections of one note and one of another: asking "does a note about
	// this already exist" wants two answers, not four.
	store := &fakeStore{text: []corpus.Hit{
		{ID: 1, Path: "Нормализация.md"}, {ID: 2, Path: "Нормализация.md"},
		{ID: 3, Path: "Нормализация.md"}, {ID: 4, Path: "Транзакция.md"},
	}}

	hits, err := api.New(store, &fakeEmbedder{}).Search(context.Background(), corpus.Query{Text: "база данных", Kind: "vault", Mode: "fts", Limit: 10, PerSource: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want one per note: %+v", len(hits), hits)
	}
	if hits[0].Path == hits[1].Path {
		t.Errorf("both hits came from %s", hits[0].Path)
	}
}

func TestPerSourceUnsetChangesNothing(t *testing.T) {
	store := &fakeStore{text: []corpus.Hit{{ID: 1, Path: "a"}, {ID: 2, Path: "a"}}}
	hits, err := api.New(store, &fakeEmbedder{}).Search(context.Background(), corpus.Query{Text: "q", Kind: "", Mode: "fts", Limit: 10, PerSource: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Errorf("got %d hits, want both — the cap must be opt-in", len(hits))
	}
}

// TestReadOnlyEndpointsRefuseOtherMethods pins the method on the query
// endpoints. All of them only read, and a POST that silently ran a search was
// indistinguishable from one that changed something.
func TestReadOnlyEndpointsRefuseOtherMethods(t *testing.T) {
	srv := httptest.NewServer(api.New(&fakeStore{text: []corpus.Hit{}}, &fakeEmbedder{}).Handler())
	defer srv.Close()

	for _, path := range []string{"/search?q=x", "/compare?q=x", "/read?id=1", "/status", "/healthz", "/sources"} {
		resp, err := http.Post(srv.URL+path, "text/plain", nil)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("POST %s = %d, want %d", path, resp.StatusCode, http.StatusMethodNotAllowed)
		}
	}
}

// The HTTP surface is thin, but it is what the shell and the browser reach, and
// every endpoint below was previously only exercised for the method it refuses.
func TestHTTPEndpointsAnswerInTheirDocumentedShape(t *testing.T) {
	store := &fakeStore{
		text:     []corpus.Hit{{ID: 7, Kind: "book", Title: "Книга", Locator: "с. 1"}},
		semantic: []corpus.Hit{{ID: 8, Kind: "vault", Title: "Заметка", Locator: "H"}},
	}
	srv := httptest.NewServer(api.New(store, &fakeEmbedder{}).Handler())
	defer srv.Close()

	t.Run("search", func(t *testing.T) {
		var body struct {
			Hits []corpus.Hit `json:"hits"`
		}
		// The default mode is hybrid, so both legs are fused into the answer.
		get(t, srv.URL+"/search?q=агрегат&limit=5", &body)
		if len(body.Hits) != 2 {
			t.Fatalf("hits = %+v, want both legs", body.Hits)
		}
		seen := map[int64]bool{}
		for _, h := range body.Hits {
			seen[h.ID] = true
		}
		if !seen[7] || !seen[8] {
			t.Errorf("hits = %+v, want the text and the vector leg", body.Hits)
		}
	})

	t.Run("compare", func(t *testing.T) {
		var modes map[string][]corpus.Hit
		get(t, srv.URL+"/compare?q=агрегат&limit=5", &modes)
		for _, mode := range []string{"fts", "vector", "hybrid"} {
			if _, ok := modes[mode]; !ok {
				t.Errorf("compare is missing the %s leg", mode)
			}
		}
	})

	t.Run("read", func(t *testing.T) {
		var passage corpus.Passage
		get(t, srv.URL+"/read?id=7&neighbours", &passage)
		if passage.Body != "body" {
			t.Errorf("body = %q", passage.Body)
		}
	})

	t.Run("status", func(t *testing.T) {
		var status map[string]any
		get(t, srv.URL+"/status", &status)
		if status["sources"] != float64(1) || status["chunks"] != float64(2) {
			t.Errorf("status = %v", status)
		}
		if status["embedder"] != "ok" {
			t.Errorf("embedder = %v, want ok", status["embedder"])
		}
	})

	t.Run("healthz", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("healthz = %d", resp.StatusCode)
		}
	})
}

// /status is where a degraded embedder has to be visible; /healthz stays 200,
// because search still answers on full text and a restart would fix nothing.
func TestStatusReportsADegradedEmbedderWhileHealthzStaysUp(t *testing.T) {
	store := &fakeStore{}
	embedder := &fakeEmbedder{err: errors.New("connection refused")}
	srv := httptest.NewServer(api.New(store, embedder).Handler())
	defer srv.Close()

	var status map[string]any
	get(t, srv.URL+"/status", &status)
	if status["embedder"] != "unreachable" {
		t.Errorf("embedder = %v, want unreachable", status["embedder"])
	}
	if status["degraded"] == nil {
		t.Error("a degraded service must say so")
	}

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz = %d, want 200 while only the embedder is away", resp.StatusCode)
	}
}

func get(t *testing.T, url string, into any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}

// Offline the embedder is the part that goes away, and a comparison that fails
// outright hides the two legs that still answer.
func TestCompareFallsBackToTextWhenEmbedderIsDown(t *testing.T) {
	store := &fakeStore{text: []corpus.Hit{{ID: 1}, {ID: 2}}}
	embedder := &fakeEmbedder{err: errors.New("connection refused")}

	out, err := api.New(store, embedder).Compare(context.Background(), corpus.Query{Text: "q", Limit: 5})
	if err != nil {
		t.Fatalf("compare returned an error instead of degrading: %v", err)
	}
	if len(out["fts"]) != 2 || len(out["hybrid"]) != 2 {
		t.Errorf("fts = %+v, hybrid = %+v, want the text leg in both", out["fts"], out["hybrid"])
	}
	if got, ok := out["vector"]; !ok || got == nil || len(got) != 0 {
		t.Errorf("vector = %#v, want an empty leg rather than a missing one", got)
	}
}

func TestReadReturnsThePassageBehindAHit(t *testing.T) {
	passage, err := api.New(&fakeStore{}, &fakeEmbedder{}).Read(context.Background(), 7, true)
	if err != nil {
		t.Fatal(err)
	}
	if passage.Body != "body" {
		t.Errorf("body = %q, want the store's passage", passage.Body)
	}
}
