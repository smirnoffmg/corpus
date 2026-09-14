package embed_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/smirnoffmg/corpus/internal/embed"
)

// server answers with failures for the first failures calls, then succeeds.
func server(t *testing.T, status int, failures int32) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= failures {
			w.WriteHeader(status)
			_, _ = w.Write([]byte("busy"))
			return
		}
		var body struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		out := make([][]float32, len(body.Input))
		for i := range out {
			out[i] = []float32{0.1, 0.2}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func fast() embed.Option { return embed.WithRetry(4, time.Millisecond) }

func TestRetriesUntilTheServiceAnswers(t *testing.T) {
	srv, calls := server(t, http.StatusServiceUnavailable, 2)

	vectors, err := embed.New(srv.URL, "bge-m3", fast()).Embed(context.Background(), []string{"текст"})
	if err != nil {
		t.Fatalf("gave up on a transient failure: %v", err)
	}
	if len(vectors) != 1 {
		t.Errorf("got %d vectors, want 1", len(vectors))
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("called the service %d times, want 3 (two busy, one through)", got)
	}
}

func TestDoesNotRetryAMissingModel(t *testing.T) {
	// Dialling a number that is out of service: asking again cannot help.
	srv, calls := server(t, http.StatusNotFound, 99)

	_, err := embed.New(srv.URL, "no-such-model", fast()).Embed(context.Background(), []string{"текст"})
	if err == nil {
		t.Fatal("a missing model was reported as success")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("retried a permanent failure %d times", got-1)
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error hides the status: %v", err)
	}
}

func TestGivesUpAfterTheAttemptBudget(t *testing.T) {
	srv, calls := server(t, http.StatusServiceUnavailable, 99)

	_, err := embed.New(srv.URL, "bge-m3", fast()).Embed(context.Background(), []string{"текст"})
	if err == nil {
		t.Fatal("a service that is always busy was reported as success")
	}
	if got := calls.Load(); got != 4 {
		t.Errorf("made %d attempts, want 4", got)
	}
	if !strings.Contains(err.Error(), "4 attempts") {
		t.Errorf("error does not say how many attempts were made: %v", err)
	}
}

func TestCancellationStopsTheRetryLoop(t *testing.T) {
	srv, calls := server(t, http.StatusServiceUnavailable, 99)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := embed.New(srv.URL, "bge-m3", embed.WithRetry(4, time.Hour)).Embed(ctx, []string{"текст"}); err == nil {
		t.Fatal("a cancelled call reported success")
	}
	if got := calls.Load(); got > 1 {
		t.Errorf("kept calling after cancellation: %d calls", got)
	}
}

func TestErrorNamesTheTriesActuallyMade(t *testing.T) {
	// A permanent failure is not retried, so the message must not claim it was.
	srv, _ := server(t, http.StatusNotFound, 99)
	_, err := embed.New(srv.URL, "no-such-model", fast()).Embed(context.Background(), []string{"текст"})
	if err == nil {
		t.Fatal("no error")
	}
	if strings.Contains(err.Error(), "attempts") {
		t.Errorf("a single try was reported as several: %v", err)
	}

	// A transient one is retried, and then the count is worth reporting.
	busy, _ := server(t, http.StatusServiceUnavailable, 99)
	_, err = embed.New(busy.URL, "bge-m3", fast()).Embed(context.Background(), []string{"текст"})
	if err == nil || !strings.Contains(err.Error(), "after 4 attempts") {
		t.Errorf("error does not name the four tries: %v", err)
	}
}

// contextServer behaves like ollama with bge-m3 on token-dense text: any
// request holding an input longer than limit runes fails as a whole with 400.
func contextServer(t *testing.T, limit int) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		seen = append(seen, body.Input...)
		mu.Unlock()
		out := make([][]float32, len(body.Input))
		for i, in := range body.Input {
			if utf8.RuneCountInString(in) > limit {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"the input length exceeds the context length"}`))
				return
			}
			out[i] = []float32{float32(utf8.RuneCountInString(in))}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

// One chunk of a regular expression spelled out in exotic Unicode tokenizes to
// more than the model's context. Failing its whole batch put fifteen good chunks
// in quarantine with it, and stopped the pass on every try.
func TestAnInputOverTheContextIsShortenedAloneNotTheBatch(t *testing.T) {
	srv, seen := contextServer(t, 1000)
	dense := strings.Repeat("ᣱﺂᐤ⏨", 1000)

	vectors, err := embed.New(srv.URL, "bge-m3", fast()).Embed(context.Background(), []string{"first", dense, "third"})
	if err != nil {
		t.Fatalf("a batch with one oversized input failed: %v", err)
	}
	if len(vectors) != 3 || vectors[0][0] != 5 || vectors[2][0] != 5 {
		t.Fatalf("vectors = %v, want one per input in order, the short ones unchanged", vectors)
	}
	if n := vectors[1][0]; n > 1000 || n < 500 {
		t.Errorf("the dense input was embedded at %v runes, want it cut to fit and no shorter than half", n)
	}
	for _, in := range *seen {
		if in == "first" || in == "third" || strings.HasPrefix(dense, in) {
			continue
		}
		t.Errorf("sent %q, which is neither an input nor a prefix of one", in)
	}
}

func TestAnInputThatCannotBeShortenedToFitFails(t *testing.T) {
	srv, _ := contextServer(t, 10)

	_, err := embed.New(srv.URL, "bge-m3", fast()).Embed(context.Background(), []string{strings.Repeat("ᣱ", 4000)})
	if err == nil {
		t.Fatal("an input that fits at no length was reported as embedded")
	}
	if !strings.Contains(err.Error(), "context length") {
		t.Errorf("error hides the cause: %v", err)
	}
}
