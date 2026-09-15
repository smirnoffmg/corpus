package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

// The embedder is the part of search that goes away: ollama runs on the host,
// outside compose, and is off whenever the laptop is on battery or on a plane.
// Search is interactive, so a query embedding gets seconds, not the minutes the
// indexer allows a page of a book; and after one failure it is not asked again
// for a while, since every search would otherwise pay for the same timeout —
// "the worst must surely be a slow failure response" (Nygard, Release It!,
// с. 131).
const (
	defaultQueryTimeout  = 3 * time.Second
	defaultStatusTimeout = 2 * time.Second
	defaultCooldown      = 30 * time.Second
)

var errEmbedderUnavailable = errors.New("embedder unavailable: search by meaning is off, try mode=fts")

// WithQueryTimeout bounds how long a search waits for its query's embedding.
func WithQueryTimeout(d time.Duration) Option { return func(s *Service) { s.queryTimeout = d } }

// WithStatusTimeout bounds the embedder probe in /status.
func WithStatusTimeout(d time.Duration) Option { return func(s *Service) { s.statusTimeout = d } }

// WithEmbedderCooldown is how long searches skip the vector leg after the
// embedder failed.
func WithEmbedderCooldown(d time.Duration) Option { return func(s *Service) { s.cooldown = d } }

// WithClock replaces the clock the cooldown is measured with.
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// isEmbedderDown tells a search that could not embed its query — skipped during
// a cooldown, refused, or out of time — from a search that broke.
func isEmbedderDown(err error) bool { return errors.Is(err, errEmbedderUnavailable) }

// trace is what one search did, for the event it logs.
type trace struct {
	vector     string // "unused", "ok", "failed" or "skipped"
	textTook   time.Duration
	vectorTook time.Duration
}

// embedQuery embeds a query, or declines at once while the embedder is known
// to be down.
func (s *Service) embedQuery(ctx context.Context, text string, tr *trace) ([]float32, error) {
	if s.skipping() {
		tr.vector = "skipped"
		return nil, errEmbedderUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()

	start := time.Now()
	vectors, err := s.embedder.Embed(ctx, []string{text})
	tr.vectorTook = time.Since(start)
	if err != nil {
		tr.vector = "failed"
		s.trip(ctx, err)
		return nil, fmt.Errorf("%w (%w)", errEmbedderUnavailable, err)
	}
	tr.vector = "ok"
	return vectors[0], nil
}

func (s *Service) skipping() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now().Before(s.skipUntil)
}

// trip starts a cooldown. Logged once per cooldown rather than per search: a
// breaker that opens has to be visible (Release It!, с. 118), and a line per
// search would bury it.
func (s *Service) trip(ctx context.Context, err error) {
	s.mu.Lock()
	s.skipUntil = s.now().Add(s.cooldown)
	s.mu.Unlock()
	slog.WarnContext(ctx, "embedder unavailable, searching text alone for a while",
		"cooldown", s.cooldown.String(), "err", err)
}

func (s *Service) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skipUntil = time.Time{}
}

// probeEmbedder is /status asking the embedder directly, cooldown or not, so a
// recovered ollama is noticed without waiting for the cooldown to pass.
func (s *Service) probeEmbedder(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, s.statusTimeout)
	defer cancel()
	if _, err := s.embedder.Embed(ctx, []string{"проверка"}); err != nil {
		s.trip(ctx, err)
		return err
	}
	s.reset()
	return nil
}

// logSearch is one wide event per search. The query's text stays out of it:
// the corpus holds a diary, and logs travel further than the database does.
func logSearch(ctx context.Context, q corpus.Query, mode string, hits int, tr *trace, took time.Duration, err error) {
	attrs := []slog.Attr{
		slog.String("mode", mode),
		slog.String("kind", q.Kind),
		slog.Int("query_chars", len([]rune(q.Text))),
		slog.Int("limit", q.Limit),
		slog.Int("per_source", q.PerSource),
		slog.Int("hits", hits),
		slog.String("vector", tr.vector),
		slog.Int64("text_ms", tr.textTook.Milliseconds()),
		slog.Int64("vector_ms", tr.vectorTook.Milliseconds()),
		slog.Int64("took_ms", took.Milliseconds()),
	}
	if err != nil {
		attrs = append(attrs, slog.String("err", err.Error()))
	}
	slog.LogAttrs(ctx, slog.LevelInfo, "search", attrs...)
}
