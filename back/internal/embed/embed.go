// Package embed turns text into vectors using a local ollama instance. The
// corpus contains a personal diary, so embedding never leaves the machine.
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

type Client struct {
	baseURL  string
	model    string
	http     *http.Client
	attempts int
	backoff  time.Duration
}

type Option func(*Client)

// WithRetry sets how many times a call is attempted in total and the first
// delay between attempts, which doubles after each one.
func WithRetry(attempts int, backoff time.Duration) Option {
	return func(c *Client) {
		c.attempts = attempts
		c.backoff = backoff
	}
}

func New(baseURL, model string, opts ...Option) *Client {
	c := &Client{
		baseURL: baseURL,
		model:   model,
		// A full page of a book can take a while on a laptop GPU.
		http:     &http.Client{Timeout: 10 * time.Minute},
		attempts: 4,
		backoff:  time.Second,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// keepAlive holds the model in memory between requests. ollama's default drops
// it after five idle minutes, and reloading bge-m3 took 2.8s — nearly all of a
// search's 3s budget — so the first search after a pause lost its vector leg.
// An hour spans the pauses of a working session and the indexer's passes.
const keepAlive = "1h"

type request struct {
	Model     string   `json:"model"`
	Input     []string `json:"input"`
	KeepAlive string   `json:"keep_alive"`
}

type response struct {
	Embeddings [][]float32 `json:"embeddings"`
	Error      string      `json:"error"`
}

// transient marks a failure worth retrying. A busy or restarting ollama is an
// expected occurrence, not an exceptional one; a missing model is not — "we
// only retry if there is a true busy signal" (Wilder, Cloud Architecture
// Patterns, printed p. 85).
type transient struct{ err error }

func (t transient) Error() string { return t.err.Error() }
func (t transient) Unwrap() error { return t.err }

// Unavailable tells a caller that the failure says nothing about the input:
// the indexer takes the attempt back instead of counting it against the text.
func (t transient) Unavailable() bool { return true }

// errContextOverflow is ollama refusing an input that tokenizes past the
// model's context. Its truncate option does not help: ollama 0.32 returns the
// error with truncate set, and with num_ctx raised to the model's full 8192.
var errContextOverflow = errors.New("input exceeds the model's context")

// shortestInput is where shortening stops: below it an input that still does
// not fit is not text the model can say anything about.
const shortestInput = 64

// Embed returns one vector per input, in the same order.
//
// Characters are only a proxy for tokens, and a proxy that dense text breaks:
// a regular expression spelled out in exotic Unicode is a token or more per
// character. When a batch overflows, its inputs are embedded one by one and
// only the one that overflows is shortened, so the rest of the batch keeps
// full-length vectors instead of sharing its failure.
func (c *Client) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	vectors, err := c.embedRetrying(ctx, inputs)
	if !errors.Is(err, errContextOverflow) {
		return vectors, err
	}
	out := make([][]float32, len(inputs))
	for i, input := range inputs {
		if out[i], err = c.embedFitting(ctx, input); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// embedFitting halves an input until the model accepts it. The start is kept,
// which is what truncation would have kept.
func (c *Client) embedFitting(ctx context.Context, input string) ([]float32, error) {
	original := utf8.RuneCountInString(input)
	for {
		vectors, err := c.embedRetrying(ctx, []string{input})
		if err == nil {
			if n := utf8.RuneCountInString(input); n < original {
				slog.WarnContext(ctx, "input shortened to fit the model's context", "from", original, "to", n)
			}
			return vectors[0], nil
		}
		runes := []rune(input)
		if !errors.Is(err, errContextOverflow) || len(runes) <= shortestInput {
			return nil, err
		}
		input = string(runes[:len(runes)/2])
	}
}

func (c *Client) embedRetrying(ctx context.Context, inputs []string) ([][]float32, error) {
	delay := c.backoff
	var err error
	made := 0

	for attempt := 1; attempt <= c.attempts; attempt++ {
		var vectors [][]float32
		made = attempt
		vectors, err = c.embedOnce(ctx, inputs)
		if err == nil {
			return vectors, nil
		}

		var retryable transient
		if !errors.As(err, &retryable) || attempt == c.attempts {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		delay *= 2
	}
	// Reporting the budget rather than the tries actually made reads as if a
	// permanent failure had been retried; it sent one diagnosis down the wrong
	// path already.
	if made == 1 {
		return nil, err
	}
	return nil, fmt.Errorf("after %d attempts: %w", made, err)
}

func (c *Client) embedOnce(ctx context.Context, inputs []string) ([][]float32, error) {
	body, err := json.Marshal(request{Model: c.model, Input: inputs, KeepAlive: keepAlive})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		// A cancelled context is the caller leaving, not the service being busy.
		if ctx.Err() != nil {
			return nil, err
		}
		return nil, transient{err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		failure := fmt.Errorf("ollama %s: %s", resp.Status, strings.TrimSpace(string(snippet)))
		if retryableStatus(resp.StatusCode) {
			return nil, transient{failure}
		}
		if resp.StatusCode == http.StatusBadRequest && bytes.Contains(snippet, []byte("exceeds the context length")) {
			return nil, fmt.Errorf("%w: %w", errContextOverflow, failure)
		}
		return nil, failure
	}

	var out response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}
	if out.Error != "" {
		return nil, fmt.Errorf("ollama: %s", out.Error)
	}
	if len(out.Embeddings) != len(inputs) {
		return nil, fmt.Errorf("got %d embeddings for %d inputs", len(out.Embeddings), len(inputs))
	}
	return out.Embeddings, nil
}

// retryableStatus covers the codes that mean "ask again", not "you asked wrong".
func retryableStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	return false
}
