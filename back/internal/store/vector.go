package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

const (
	// bodyCharLimit bounds how much of a chunk is embedded: a long daily note
	// would otherwise cost minutes of GPU for a tail that adds nothing to the
	// topic of the section.
	defaultBodyChars = 5000
	// neighbourChars of the pages on either side ride along. Two thirds of the
	// book pages here end mid-sentence, so a page on its own is routinely half
	// a thought; the text index still answers per page, only the vector sees
	// the wider window (Manning, IIR, printed p. 22: an IR system should offer
	// choices of granularity).
	defaultNeighbourChars = 500
	// maxEmbedAttempts quarantines a text the embedder keeps refusing. Without
	// it one permanently failing batch blocks every chunk behind it forever,
	// which is the failure an invalid message channel exists to prevent.
	maxEmbedAttempts = 3
)

// embedKeys computes, for chunks in order, the window each is embedded as and
// that window's SHA-256 — the key its vector is filed under.
//
// The window is also computed in SQL: by the queue, to hand the text out, and
// by migration 010, which filed the vectors that existed before. Postgres's
// left and right count characters, so this counts runes; a test holds the Go
// and SQL versions together.
func (s *Store) embedKeys(chunks []corpus.Chunk) []string {
	keys := make([]string, len(chunks))
	for i := range chunks {
		parts := make([]string, 0, 3)
		if i > 0 {
			if prev := lastRunes(chunks[i-1].Body, s.neighbourChars); prev != "" {
				parts = append(parts, prev)
			}
		}
		parts = append(parts, firstRunes(chunks[i].Body, s.bodyChars))
		if i+1 < len(chunks) {
			if next := firstRunes(chunks[i+1].Body, s.neighbourChars); next != "" {
				parts = append(parts, next)
			}
		}
		sum := sha256.Sum256([]byte(strings.Join(parts, " ")))
		keys[i] = hex.EncodeToString(sum[:])
	}
	return keys
}

func firstRunes(s string, n int) string {
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}

func lastRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

// pending is the condition for a chunk whose text has no vector and has not
// been refused too often; e is the chunk's row in embeddings, if any.
const pending = `e.embedding IS NULL AND coalesce(e.attempts, 0) < $1`

const pendingSQL = `
WITH target AS (
    SELECT c.source_id
    FROM chunks c
    LEFT JOIN embeddings e ON e.hash = c.embed_hash
    WHERE ` + pending + `
    ORDER BY c.source_id
    LIMIT 1
), windowed AS (
    SELECT c.ord,
           c.embed_hash,
           concat_ws(' ',
               nullif(right(lag(c.body) OVER o, $2), ''),
               left(c.body, $3),
               nullif(left(lead(c.body) OVER o, $2), '')) AS text
    FROM chunks c
    JOIN target t ON t.source_id = c.source_id
    WINDOW o AS (ORDER BY c.ord)
), texts AS (
    SELECT DISTINCT ON (w.embed_hash) w.embed_hash, w.text, w.ord
    FROM windowed w
    LEFT JOIN embeddings e ON e.hash = w.embed_hash
    WHERE ` + pending + `
    ORDER BY w.embed_hash, w.ord
)
SELECT embed_hash, text FROM texts ORDER BY ord LIMIT $4`

// PendingEmbeddings hands out texts that need a vector, one source at a time,
// each text once however many chunks share it.
func (s *Store) PendingEmbeddings(ctx context.Context, limit int) ([]corpus.Pending, error) {
	rows, err := s.pool.Query(ctx, pendingSQL, maxEmbedAttempts, s.neighbourChars, s.bodyChars, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (corpus.Pending, error) {
		var p corpus.Pending
		err := row.Scan(&p.Key, &p.Body)
		return p, err
	})
}

// MissingEmbeddings counts the texts still waiting for a vector.
func (s *Store) MissingEmbeddings(ctx context.Context) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx, `
		SELECT count(DISTINCT c.embed_hash)
		FROM chunks c LEFT JOIN embeddings e ON e.hash = c.embed_hash
		WHERE `+pending, maxEmbedAttempts).Scan(&n)
	return n, err
}

// Quarantined counts chunks whose text the embedder has refused too often.
// They are left out of the queue so the rest of the corpus can finish; the
// text index still covers them.
func (s *Store) Quarantined(ctx context.Context) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM chunks c JOIN embeddings e ON e.hash = c.embed_hash
		WHERE e.embedding IS NULL AND e.attempts >= $1`, maxEmbedAttempts).Scan(&n)
	return n, err
}

// RequeueQuarantined puts quarantined texts back in the queue. Attempts are
// counted before the call, so a run of transient failures — ollama restarting,
// say — can quarantine a perfectly good text; this is the way back.
func (s *Store) RequeueQuarantined(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE embeddings SET attempts = 0 WHERE embedding IS NULL AND attempts >= $1`,
		maxEmbedAttempts)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// CountAttempt records a try before it is made, so that a crash or a timeout
// counts too — otherwise a text that kills the process is retried forever.
func (s *Store) CountAttempt(ctx context.Context, keys []string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO embeddings (hash, attempts)
		SELECT k, 1 FROM unnest($1::text[]) AS k
		ON CONFLICT (hash) DO UPDATE SET attempts = embeddings.attempts + 1`, sortedUnique(keys))
	return err
}

// UncountAttempt takes back an attempt that said nothing about the text: the
// embedder was not there to ask.
func (s *Store) UncountAttempt(ctx context.Context, keys []string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE embeddings SET attempts = greatest(attempts - 1, 0) WHERE hash = ANY($1)`, keys)
	return err
}

// RecordFailure keeps the embedder's reason with the text it refused.
func (s *Store) RecordFailure(ctx context.Context, keys []string, reason string) error {
	_, err := s.pool.Exec(ctx, `UPDATE embeddings SET last_error = $2 WHERE hash = ANY($1)`, keys, reason)
	return err
}

func (s *Store) SaveEmbeddings(ctx context.Context, keys []string, vectors [][]float32) error {
	if len(keys) != len(vectors) {
		return fmt.Errorf("%d keys for %d vectors", len(keys), len(vectors))
	}
	literals := make([]string, len(vectors))
	for i, v := range vectors {
		literals[i] = vectorLiteral(v)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO embeddings (hash, embedding, embedded_at)
		SELECT k, v::vector, now() FROM unnest($1::text[], $2::text[]) AS data(k, v)
		ON CONFLICT (hash) DO UPDATE SET embedding = EXCLUDED.embedding, embedded_at = now(), last_error = NULL`,
		keys, literals)
	return err
}

// PruneEmbeddings drops vectors no chunk's text uses any more: the windows of
// deleted files, and of the parts of changed files that changed.
func (s *Store) PruneEmbeddings(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM embeddings e
		WHERE NOT EXISTS (SELECT 1 FROM chunks c WHERE c.embed_hash = e.hash)`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// sortedUnique keeps concurrent upserts from locking rows in different orders,
// and a key twice in one batch from updating its own row twice, which
// ON CONFLICT refuses.
func sortedUnique(keys []string) []string {
	out := append([]string(nil), keys...)
	sort.Strings(out)
	j := 0
	for i, k := range out {
		if i == 0 || k != out[j-1] {
			out[j] = k
			j++
		}
	}
	return out[:j]
}

const vectorSQL = `
SELECT c.id,
       s.kind,
       s.title,
       s.path,
       c.heading,
       coalesce(c.anchor, '') AS anchor,
       s.recognised AS ocr,
       coalesce(c.page, 0) AS page,
       coalesce(c.printed_page, 0) AS printed_page,
       1 - (e.embedding <=> $1::vector) AS rank,
       left(c.body, 240) AS snippet
FROM embeddings e
JOIN chunks c ON c.embed_hash = e.hash
JOIN sources s ON s.id = c.source_id
WHERE e.embedding IS NOT NULL
  AND ($2 = '' OR s.kind = $2)
ORDER BY e.embedding <=> $1::vector, c.id
LIMIT $3`

// SearchVector returns the chunks nearest a vector. ef_search and an exact scan
// are set for this one search, inside its own transaction, so they never leak
// onto a pooled connection another search will use.
func (s *Store) SearchVector(ctx context.Context, vector []float32, q corpus.Query) ([]corpus.Hit, error) {
	if q.EfSearch == 0 && !q.Exact {
		rows, err := s.pool.Query(ctx, vectorSQL, vectorLiteral(vector), q.Kind, q.Limit)
		if err != nil {
			return nil, err
		}
		return collectHits(rows)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if q.EfSearch > 0 {
		// SET takes no parameters; the value is an int, formatted, not text.
		if _, setErr := tx.Exec(ctx, "SET LOCAL hnsw.ef_search = "+strconv.Itoa(q.EfSearch)); setErr != nil {
			return nil, fmt.Errorf("ef_search %d: %w", q.EfSearch, setErr)
		}
	}
	if q.Exact {
		if _, setErr := tx.Exec(ctx, "SET LOCAL enable_indexscan = off"); setErr != nil {
			return nil, setErr
		}
	}
	rows, err := tx.Query(ctx, vectorSQL, vectorLiteral(vector), q.Kind, q.Limit)
	if err != nil {
		return nil, err
	}
	return collectHits(rows)
}

// pgvector has no binary literal in the wire protocol we use, so vectors travel
// as their text form.
func vectorLiteral(v []float32) string {
	var b strings.Builder
	// A 1024-dimension vector is ~10 KB of text; growing once avoids a dozen
	// reallocations per call.
	b.Grow(len(v)*12 + 2)
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'f', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

const passageSQL = `
SELECT s.kind, s.title, s.path, c.heading, coalesce(c.anchor, ''), s.recognised, coalesce(c.page,0), coalesce(c.printed_page,0), c.body,
       (SELECT body FROM chunks p
         WHERE p.source_id = c.source_id AND p.ord < c.ord
         ORDER BY p.ord DESC LIMIT 1),
       (SELECT body FROM chunks n
         WHERE n.source_id = c.source_id AND n.ord > c.ord
         ORDER BY n.ord LIMIT 1)
FROM chunks c
JOIN sources s ON s.id = c.source_id
WHERE c.id = $1`

// Read returns the chunk's own text, and with neighbours the pages on either
// side — a definition cut by a page break is the normal case here, not the
// exception.
func (s *Store) Read(ctx context.Context, id int64, neighbours bool) (corpus.Passage, error) {
	var p corpus.Passage
	var prev, next *string
	var heading *string
	var printed int
	err := s.pool.QueryRow(ctx, passageSQL, id).
		Scan(&p.Kind, &p.Title, &p.Path, &heading, &p.Anchor, &p.OCR, &p.Page, &printed, &p.Body, &prev, &next)
	if err != nil {
		return corpus.Passage{}, fmt.Errorf("read chunk %d: %w", id, err)
	}
	p.Locator = corpus.Locator(deref(heading), p.Page, printed)
	if neighbours {
		if prev != nil {
			p.Previous = *prev
		}
		if next != nil {
			p.Next = *next
		}
	}
	return p, nil
}
