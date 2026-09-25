// Package rerank asks a cross-encoder how well each passage answers a query.
// The model reads the query and the passage together, which the embedding
// search cannot: it scores the pair, not two vectors made apart (Reimers and
// Gurevych, Sentence-BERT, Table 2). That is too slow for the whole library
// and cheap for a page of candidates.
//
// The server is llama-server from llama.cpp, started with --rerank and a
// reranker model such as bge-reranker-v2-m3; ollama serves no rerank endpoint.
package rerank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		// The caller bounds each search with its context; this only keeps a
		// request the caller forgot to bound from living forever.
		http: &http.Client{Timeout: time.Minute},
	}
}

type request struct {
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
}

type response struct {
	Results []struct {
		Index int     `json:"index"`
		Score float64 `json:"relevance_score"`
	} `json:"results"`
}

// Rerank returns a score for each document, in the order they were given;
// higher is more relevant. The scores are only comparable within one call.
func (c *Client) Rerank(ctx context.Context, query string, documents []string) ([]float64, error) {
	body, err := json.Marshal(request{Query: query, Documents: documents})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/rerank", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rerank: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("rerank: %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}

	var out response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("rerank: decode reply: %w", err)
	}
	scores := make([]float64, len(documents))
	seen := make([]bool, len(documents))
	for _, r := range out.Results {
		if r.Index < 0 || r.Index >= len(documents) {
			return nil, fmt.Errorf("rerank: reply scores document %d of %d", r.Index, len(documents))
		}
		scores[r.Index], seen[r.Index] = r.Score, true
	}
	for i, ok := range seen {
		if !ok {
			return nil, fmt.Errorf("rerank: reply has no score for document %d of %d", i, len(documents))
		}
	}
	return scores, nil
}
