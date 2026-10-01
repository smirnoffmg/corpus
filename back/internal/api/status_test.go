package api_test

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/smirnoffmg/corpus/internal/api"
	"github.com/smirnoffmg/corpus/internal/corpus"
)

func statusOf(t *testing.T, opts ...api.Option) map[string]any {
	t.Helper()
	srv := httptest.NewServer(api.New(&fakeStore{}, &fakeEmbedder{}, opts...).Handler())
	defer srv.Close()
	var status map[string]any
	get(t, srv.URL+"/status", &status)
	return status
}

func TestStatusReportsTheReranker(t *testing.T) {
	cases := map[string]struct {
		opts []api.Option
		want string
	}{
		"not configured": {nil, "off"},
		"answering":      {[]api.Option{api.WithReranker(&fakeReranker{})}, "ok"},
		"down":           {[]api.Option{api.WithReranker(&fakeReranker{err: errors.New("connection refused")})}, "unreachable"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := statusOf(t, c.opts...)["reranker"]; got != c.want {
				t.Errorf("reranker = %v, want %s", got, c.want)
			}
		})
	}
}

func TestStatusSaysWhetherUploadsAreAccepted(t *testing.T) {
	if got := statusOf(t)["uploads"]; got != false {
		t.Errorf("uploads without a library = %v, want false", got)
	}
	if got := statusOf(t, api.WithLibrary(&fakeLibrary{}, 1<<20))["uploads"]; got != true {
		t.Errorf("uploads with a library = %v, want true", got)
	}
}

func TestStatusEchoesTheSettingsItRunsWith(t *testing.T) {
	settings := statusOf(t, api.WithSettings(map[string]string{"model": "bge-m3"}))["settings"]
	if m, ok := settings.(map[string]any); !ok || m["model"] != "bge-m3" {
		t.Errorf("settings = %v, want model bge-m3", settings)
	}
}

func indexerStatus(t *testing.T, pulse *corpus.Pulse) (map[string]any, bool) {
	t.Helper()
	srv := httptest.NewServer(api.New(&fakeStore{pulse: pulse}, &fakeEmbedder{}).Handler())
	defer srv.Close()
	var status map[string]any
	get(t, srv.URL+"/status", &status)
	indexer, ok := status["indexer"].(map[string]any)
	return indexer, ok
}

func TestStatusReportsTheIndexersPulse(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	pulse := &corpus.Pulse{
		Beat: corpus.Beat{
			Phase: "waiting", PhaseFrom: now.Add(-3 * time.Minute), Started: now.Add(-2 * time.Hour),
			LastPass: now.Add(-3 * time.Minute), LastTook: 95 * time.Second, LastError: "ollama: connection refused",
			Interval: 15 * time.Minute, Settings: map[string]string{"model": "bge-m3"},
		},
		At: now.Add(-10 * time.Second), Now: now,
	}

	indexer, ok := indexerStatus(t, pulse)
	if !ok {
		t.Fatal("no indexer in /status")
	}
	want := map[string]any{
		"alive": true, "phase": "waiting", "silent_seconds": 10.0, "phase_seconds": 180.0, "up_seconds": 7200.0,
		"last_pass_ago_seconds": 180.0, "last_pass_seconds": 95.0, "last_error": "ollama: connection refused",
		"interval_seconds": 900.0,
	}
	for k, v := range want {
		if indexer[k] != v {
			t.Errorf("indexer[%s] = %v, want %v", k, indexer[k], v)
		}
	}
	if s, _ := indexer["settings"].(map[string]any); s["model"] != "bge-m3" {
		t.Errorf("settings = %v, want the indexer's flags", indexer["settings"])
	}
}

func TestStatusTakesASilentIndexerForAStoppedOne(t *testing.T) {
	now := time.Now()
	pulse := &corpus.Pulse{Beat: corpus.Beat{Phase: "recognising", PhaseFrom: now.Add(-time.Hour), Started: now.Add(-time.Hour)}, At: now.Add(-5 * time.Minute), Now: now}

	indexer, _ := indexerStatus(t, pulse)
	if indexer["alive"] != false {
		t.Errorf("alive = %v after five silent minutes, want false", indexer["alive"])
	}
	if _, ok := indexer["last_pass_ago_seconds"]; ok {
		t.Error("a pass that never finished has no age")
	}
}

func TestStatusLeavesOutAnIndexerThatNeverBeat(t *testing.T) {
	if _, ok := indexerStatus(t, nil); ok {
		t.Error("indexer reported before it ever beat")
	}
}
