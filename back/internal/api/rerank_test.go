package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smirnoffmg/corpus/internal/api"
	"github.com/smirnoffmg/corpus/internal/corpus"
)

// fakeReranker scores a document by the number written at its end, so a test
// states the order it wants in the bodies it stores.
type fakeReranker struct {
	mu    sync.Mutex
	calls int
	query string
	docs  []string
	err   error
}

func (f *fakeReranker) Rerank(_ context.Context, query string, docs []string) ([]float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.query, f.docs = query, docs
	if f.err != nil {
		return nil, f.err
	}
	scores := make([]float64, len(docs))
	for i, d := range docs {
		fields := strings.Fields(d)
		scores[i], _ = strconv.ParseFloat(fields[len(fields)-1], 64)
	}
	return scores, nil
}

func ids(hits []corpus.Hit) []int64 {
	out := make([]int64, len(hits))
	for i := range hits {
		out[i] = hits[i].ID
	}
	return out
}

// hits numbers n hits 1..n, fused in that order, with bodies that score the
// last one highest: a reranked list comes out reversed.
func hitsAndBodies(n int) ([]corpus.Hit, map[int64]string) {
	hits := make([]corpus.Hit, n)
	bodies := make(map[int64]string, n)
	for i := range n {
		id := int64(i + 1)
		hits[i] = corpus.Hit{ID: id, Title: "Книга"}
		bodies[id] = "текст " + strconv.Itoa(i+1)
	}
	return hits, bodies
}

func TestRerankOrdersTheFusedCandidatesByTheRerankersScore(t *testing.T) {
	hits, bodies := hitsAndBodies(3)
	reranker := &fakeReranker{}
	svc := api.New(&fakeStore{text: hits, bodies: bodies}, &fakeEmbedder{}, api.WithReranker(reranker))

	got, err := svc.Search(context.Background(), corpus.Query{Text: "что такое панда", Rerank: true})
	require.NoError(t, err)
	require.Equal(t, []int64{3, 2, 1}, ids(got))
	require.Equal(t, "что такое панда", reranker.query)
	// The reranker reads which source a passage is from as well as what it
	// says, as it did when it was measured.
	require.Equal(t, "Книга\nтекст 1", reranker.docs[0])
}

// Measured on the judged sets: 20 candidates of 1500 characters keep most of
// the gain of 50 at a third of the time (docs/search-evaluation.md).
func TestRerankReadsTwentyCandidatesAndReturnsThePage(t *testing.T) {
	hits, bodies := hitsAndBodies(30)
	reranker := &fakeReranker{}
	store := &fakeStore{text: hits, bodies: bodies}
	svc := api.New(store, &fakeEmbedder{}, api.WithReranker(reranker))

	got, err := svc.Search(context.Background(), corpus.Query{Text: "q", Limit: 5, Rerank: true})
	require.NoError(t, err)
	require.Len(t, reranker.docs, 20)
	require.Equal(t, 1500, store.lastBodyChars)
	require.Equal(t, []int64{20, 19, 18, 17, 16}, ids(got))
}

func TestRerankIsOffUnlessAsked(t *testing.T) {
	hits, bodies := hitsAndBodies(3)
	reranker := &fakeReranker{}
	svc := api.New(&fakeStore{text: hits, bodies: bodies}, &fakeEmbedder{}, api.WithReranker(reranker))

	got, err := svc.Search(context.Background(), corpus.Query{Text: "q"})
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2, 3}, ids(got))
	require.Zero(t, reranker.calls)
}

// The reranker is a process on the host like ollama, and just as likely to be
// gone; the fused list is a good answer, only a less sorted one.
func TestRerankFallsBackToTheFusedOrderAndSaysSo(t *testing.T) {
	for name, opts := range map[string][]api.Option{
		"failing":        {api.WithReranker(&fakeReranker{err: errors.New("connection refused")})},
		"not configured": nil,
	} {
		t.Run(name, func(t *testing.T) {
			hits, bodies := hitsAndBodies(3)
			srv := httptest.NewServer(api.New(&fakeStore{text: hits, bodies: bodies}, &fakeEmbedder{}, opts...).Handler())
			t.Cleanup(srv.Close)

			resp, err := http.Get(srv.URL + "/search?q=q&rerank=1")
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)

			var body struct {
				Hits   []corpus.Hit `json:"hits"`
				Notice string       `json:"notice"`
			}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
			require.Equal(t, []int64{1, 2, 3}, ids(body.Hits))
			require.Contains(t, body.Notice, "rerank")
		})
	}
}

func scoreByTrailingNumber(docs []string) []float64 {
	scores := make([]float64, len(docs))
	for i, d := range docs {
		fields := strings.Fields(d)
		scores[i], _ = strconv.ParseFloat(fields[len(fields)-1], 64)
	}
	return scores
}

// countingReranker records how many calls ran at once; unlike fakeReranker it
// holds no lock across a call, which would serialise them.
type countingReranker struct {
	inflight, peak atomic.Int64
}

func (c *countingReranker) Rerank(_ context.Context, _ string, docs []string) ([]float64, error) {
	n := c.inflight.Add(1)
	for {
		p := c.peak.Load()
		if n <= p || c.peak.CompareAndSwap(p, n) {
			break
		}
	}
	time.Sleep(20 * time.Millisecond)
	c.inflight.Add(-1)
	return scoreByTrailingNumber(docs), nil
}

// llama-server runs as many requests as it has slots; more at once share the
// GPU and every one of them runs past the timeout.
func TestRerankRunsNoMoreCallsAtOnceThanTheLimit(t *testing.T) {
	hits, bodies := hitsAndBodies(3)
	reranker := &countingReranker{}
	svc := api.New(&fakeStore{text: hits, bodies: bodies}, &fakeEmbedder{},
		api.WithReranker(reranker), api.WithRerankConcurrency(3))

	results := make([][]int64, 10)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() {
			got, err := svc.Search(context.Background(), corpus.Query{Text: "q", Mode: "fts", Rerank: true})
			assert.NoError(t, err)
			results[i] = ids(got)
		})
	}
	wg.Wait()

	require.Equal(t, int64(3), reranker.peak.Load())
	for _, got := range results {
		require.Equal(t, []int64{3, 2, 1}, got)
	}
}

// llama-server reranks one request at a time; a second call sent alongside
// only waits inside it, on the clock of the first one's timeout.
func TestRerankRunsOneCallAtATimeByDefault(t *testing.T) {
	hits, bodies := hitsAndBodies(3)
	reranker := &countingReranker{}
	svc := api.New(&fakeStore{text: hits, bodies: bodies}, &fakeEmbedder{}, api.WithReranker(reranker))

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			_, err := svc.Search(context.Background(), corpus.Query{Text: "q", Mode: "fts", Rerank: true})
			assert.NoError(t, err)
		})
	}
	wg.Wait()

	require.Equal(t, int64(1), reranker.peak.Load())
}

// blockingReranker holds its call until the test releases it.
type blockingReranker struct {
	entered chan struct{}
	release chan struct{}
}

func (b *blockingReranker) Rerank(_ context.Context, _ string, docs []string) ([]float64, error) {
	b.entered <- struct{}{}
	<-b.release
	return scoreByTrailingNumber(docs), nil
}

func TestRerankCancelledWhileWaitingFallsBackAndFreesTheSlot(t *testing.T) {
	hits, bodies := hitsAndBodies(3)
	reranker := &blockingReranker{entered: make(chan struct{}, 1), release: make(chan struct{})}
	svc := api.New(&fakeStore{text: hits, bodies: bodies}, &fakeEmbedder{},
		api.WithReranker(reranker), api.WithRerankConcurrency(1))
	query := corpus.Query{Text: "q", Mode: "fts", Rerank: true}

	var holder sync.WaitGroup
	holder.Go(func() {
		_, err := svc.Search(context.Background(), query)
		assert.NoError(t, err)
	})
	<-reranker.entered

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/search?q=q&mode=fts&rerank=1", nil).WithContext(cancelled))
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Hits   []corpus.Hit `json:"hits"`
		Notice string       `json:"notice"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	require.Equal(t, []int64{1, 2, 3}, ids(body.Hits))
	require.Contains(t, body.Notice, "rerank")

	close(reranker.release)
	holder.Wait()

	// A slot the cancelled search left taken would keep this one waiting.
	got, err := svc.Search(context.Background(), query)
	require.NoError(t, err)
	require.Equal(t, []int64{3, 2, 1}, ids(got))
}
