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
	text     []corpus.Hit
	semantic []corpus.Hit
	lastText int
}

func (f *fakeStore) Search(_ context.Context, _, _ string, limit int) ([]corpus.Hit, error) {
	f.lastText = limit
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

	hits, err := api.New(store, embedder).Search(context.Background(), "агрегат", "", "hybrid", 10)
	if err != nil {
		t.Fatalf("hybrid returned an error instead of degrading: %v", err)
	}
	if len(hits) != 2 || hits[0].ID != 1 {
		t.Errorf("hits = %+v, want the text leg unchanged", hits)
	}
}

func TestFtsModeNeverEmbeds(t *testing.T) {
	embedder := &fakeEmbedder{}
	if _, err := api.New(&fakeStore{}, embedder).Search(context.Background(), "q", "", "fts", 10); err != nil {
		t.Fatal(err)
	}
	if embedder.calls != 0 {
		t.Errorf("embedder called %d times in fts mode, want 0", embedder.calls)
	}
}

func TestVectorModeReportsEmbedderFailure(t *testing.T) {
	embedder := &fakeEmbedder{err: errors.New("model not found")}
	_, err := api.New(&fakeStore{}, embedder).Search(context.Background(), "q", "", "vector", 10)
	if err == nil {
		t.Fatal("vector mode swallowed the embedder error")
	}
}

func TestCompareEmbedsTheQueryOnce(t *testing.T) {
	embedder := &fakeEmbedder{}
	out, err := api.New(&fakeStore{}, embedder).Compare(context.Background(), "q", "", 5)
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
	if _, err := api.New(store, &fakeEmbedder{}).Search(context.Background(), "q", "", "hybrid", 10); err != nil {
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
