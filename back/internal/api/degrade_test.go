package api_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smirnoffmg/corpus/internal/api"
	"github.com/smirnoffmg/corpus/internal/corpus"
)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func (f *fakeEmbedder) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// A hung ollama held every hybrid search for as long as the indexer's client
// was willing to wait — ten minutes, four times over. Search is interactive;
// the text leg is ready long before that.
func TestHybridDoesNotWaitForAHungEmbedder(t *testing.T) {
	store := &fakeStore{text: []corpus.Hit{{ID: 1}}}
	svc := api.New(store, &fakeEmbedder{hang: true}, api.WithQueryTimeout(50*time.Millisecond))

	start := time.Now()
	hits, err := svc.Search(context.Background(), corpus.Query{Text: "q", Mode: "hybrid"})
	if err != nil {
		t.Fatalf("hybrid failed instead of falling back: %v", err)
	}
	if len(hits) != 1 {
		t.Errorf("hits = %+v, want the text leg", hits)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("search took %v with a 50ms query timeout", took)
	}
}

// With ollama down every search paid for the attempt again. After a failure
// the vector leg is skipped for a while, and tried again once it has passed.
func TestAFailingEmbedderIsLeftAloneForAWhile(t *testing.T) {
	embedder := &fakeEmbedder{err: errors.New("connection refused")}
	now := &clock{now: time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)}
	svc := api.New(&fakeStore{text: []corpus.Hit{{ID: 1}}}, embedder,
		api.WithClock(now.Now), api.WithEmbedderCooldown(30*time.Second))
	search := func() {
		t.Helper()
		if _, err := svc.Search(context.Background(), corpus.Query{Text: "q"}); err != nil {
			t.Fatal(err)
		}
	}

	search()
	search()
	search()
	if got := embedder.count(); got != 1 {
		t.Errorf("embedder called %d times within the cooldown, want 1", got)
	}

	now.advance(31 * time.Second)
	search()
	if got := embedder.count(); got != 2 {
		t.Errorf("embedder called %d times after the cooldown, want it tried again", got)
	}

	embedder.mu.Lock()
	embedder.err = nil
	embedder.mu.Unlock()
	now.advance(31 * time.Second)
	search()
	search()
	if got := embedder.count(); got != 4 {
		t.Errorf("embedder called %d times once it recovered, want every search to use it", got)
	}
}

// Asking for meaning explicitly has nothing to fall back to, but it must say
// so at once rather than after the cooldown's worth of retries.
func TestVectorModeFailsFastWhileTheEmbedderIsDown(t *testing.T) {
	embedder := &fakeEmbedder{err: errors.New("connection refused")}
	svc := api.New(&fakeStore{}, embedder)

	if _, err := svc.Search(context.Background(), corpus.Query{Text: "q", Mode: "vector"}); err == nil {
		t.Fatal("vector mode succeeded without an embedder")
	}
	_, err := svc.Search(context.Background(), corpus.Query{Text: "q", Mode: "vector"})
	if err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Errorf("err = %v, want it to say the embedder is unavailable", err)
	}
	if got := embedder.count(); got != 1 {
		t.Errorf("embedder called %d times, want 1", got)
	}
}

func TestStatusDoesNotHangAndCountsFallbacks(t *testing.T) {
	embedder := &fakeEmbedder{hang: true}
	svc := api.New(&fakeStore{text: []corpus.Hit{}}, embedder,
		api.WithQueryTimeout(50*time.Millisecond), api.WithStatusTimeout(50*time.Millisecond))
	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()

	for range 2 {
		if _, err := svc.Search(context.Background(), corpus.Query{Text: "q"}); err != nil {
			t.Fatal(err)
		}
	}

	start := time.Now()
	var status map[string]any
	get(t, srv.URL+"/status", &status)
	if took := time.Since(start); took > time.Second {
		t.Errorf("/status took %v against a hung embedder", took)
	}
	if status["embedder"] != "unreachable" {
		t.Errorf("embedder = %v, want unreachable", status["embedder"])
	}
	if status["searches_without_vectors"] != float64(2) {
		t.Errorf("searches_without_vectors = %v, want 2", status["searches_without_vectors"])
	}
}

// One wide event per search: enough to see afterwards that a search ran
// without its vector leg and why, and how long each leg took. The query text
// stays out — the corpus holds a diary, and logs travel further than it does.
func TestEverySearchLogsOneEventWithoutTheQueryText(t *testing.T) {
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	svc := api.New(&fakeStore{text: []corpus.Hit{{ID: 1}, {ID: 2}}}, &fakeEmbedder{err: errors.New("connection refused")})
	if _, err := svc.Search(context.Background(), corpus.Query{Text: "мой дневник", Kind: "vault"}); err != nil {
		t.Fatal(err)
	}

	var events []string
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if strings.Contains(line, `"msg":"search"`) {
			events = append(events, line)
		}
	}
	if len(events) != 1 {
		t.Fatalf("got %d search events, want 1:\n%s", len(events), buf.String())
	}
	event := events[0]
	for _, want := range []string{`"mode":"hybrid"`, `"kind":"vault"`, `"hits":2`, `"limit":10`, `"vector":"failed"`, `"took_ms":`} {
		if !strings.Contains(event, want) {
			t.Errorf("event lacks %s: %s", want, event)
		}
	}
	if strings.Contains(buf.String(), "дневник") {
		t.Errorf("the query text reached the log: %s", buf.String())
	}
}

func TestSearchByMeaningWithoutAnEmbedderIsUnavailableNotBroken(t *testing.T) {
	srv := httptest.NewServer(api.New(&fakeStore{}, &fakeEmbedder{err: errors.New("connection refused")}).Handler())
	defer srv.Close()

	for range 2 {
		resp, err := http.Get(srv.URL + "/search?q=x&mode=vector")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want 503", resp.StatusCode)
		}
	}
}
