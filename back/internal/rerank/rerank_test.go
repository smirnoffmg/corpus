package rerank_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smirnoffmg/corpus/internal/rerank"
)

// llama-server answers with the documents sorted by score; the caller needs
// the score of each document it sent, in the order it sent them.
func TestRerankReturnsScoresInTheOrderTheDocumentsWereSent(t *testing.T) {
	var got struct {
		Query     string   `json:"query"`
		Documents []string `json:"documents"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/rerank", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		_, _ = w.Write([]byte(`{"results":[
			{"index":2,"relevance_score":1.5},
			{"index":0,"relevance_score":0.25},
			{"index":1,"relevance_score":-3}]}`))
	}))
	t.Cleanup(srv.Close)

	scores, err := rerank.New(srv.URL).Rerank(context.Background(), "что такое панда", []string{"a", "b", "c"})
	require.NoError(t, err)
	require.Equal(t, []float64{0.25, -3, 1.5}, scores)
	require.Equal(t, "что такое панда", got.Query)
	require.Equal(t, []string{"a", "b", "c"}, got.Documents)
}

func TestRerankReportsAServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "model not loaded", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	_, err := rerank.New(srv.URL).Rerank(context.Background(), "q", []string{"a"})
	require.ErrorContains(t, err, "503")
	require.ErrorContains(t, err, "model not loaded")
}

// A reply that scores fewer documents than were sent would leave some without
// a score, and ranking them by a zero value would be a silent guess.
func TestRerankRejectsAReplyMissingADocument(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"index":0,"relevance_score":1}]}`))
	}))
	t.Cleanup(srv.Close)

	_, err := rerank.New(srv.URL).Rerank(context.Background(), "q", []string{"a", "b"})
	require.Error(t, err)
}
