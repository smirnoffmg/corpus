package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

const (
	// bodyCharLimit bounds how much of a chunk is embedded: a long daily note
	// would otherwise cost minutes of GPU for a tail that adds nothing to the
	// topic of the section.
	bodyCharLimit = 5000
	// neighbourChars of the pages on either side ride along. Two thirds of the
	// book pages here end mid-sentence, so a page on its own is routinely half
	// a thought; the text index still answers per page, only the vector sees
	// the wider window (Manning, IIR, printed p. 22: an IR system should offer
	// choices of granularity).
	neighbourChars = 500
	// maxEmbedAttempts quarantines a chunk the embedder keeps refusing. Without
	// it one permanently failing batch blocks every chunk behind it forever,
	// which is the failure an invalid message channel exists to prevent.
	maxEmbedAttempts = 3
)

const pendingSQL = `
WITH target AS (
    SELECT source_id FROM chunks
    WHERE embedding IS NULL AND embed_attempts < $4
    ORDER BY source_id LIMIT 1
), windowed AS (
    SELECT c.id,
           c.ord,
           c.embedding,
           c.embed_attempts,
           right(lag(c.body) OVER (ORDER BY c.ord), $1)  AS prev,
           left(c.body, $2)                              AS body,
           left(lead(c.body) OVER (ORDER BY c.ord), $1)  AS next
    FROM chunks c
    JOIN target t ON t.source_id = c.source_id
)
SELECT id, concat_ws(' ', nullif(prev, ''), body, nullif(next, ''))
FROM windowed
WHERE embedding IS NULL AND embed_attempts < $4
ORDER BY ord
LIMIT $3`

func (s *Store) PendingEmbeddings(ctx context.Context, limit int) ([]corpus.Pending, error) {
	rows, err := s.pool.Query(ctx, pendingSQL, neighbourChars, bodyCharLimit, limit, maxEmbedAttempts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []corpus.Pending
	for rows.Next() {
		var p corpus.Pending
		if err := rows.Scan(&p.ID, &p.Body); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) MissingEmbeddings(ctx context.Context) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM chunks WHERE embedding IS NULL AND embed_attempts < $1`,
		maxEmbedAttempts).Scan(&n)
	return n, err
}

// Quarantined counts chunks the embedder has refused too often. They are left
// out of the queue so the rest of the corpus can finish; the text index still
// covers them.
func (s *Store) Quarantined(ctx context.Context) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM chunks WHERE embedding IS NULL AND embed_attempts >= $1`,
		maxEmbedAttempts).Scan(&n)
	return n, err
}

// RequeueQuarantined puts quarantined chunks back in the queue. Attempts are
// counted before the call, so a run of transient failures — ollama restarting,
// say — can quarantine a perfectly good chunk; this is the way back.
func (s *Store) RequeueQuarantined(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE chunks SET embed_attempts = 0 WHERE embedding IS NULL AND embed_attempts >= $1`,
		maxEmbedAttempts)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// CountAttempt records a try before it is made, so that a crash or a timeout
// counts too — otherwise a chunk that kills the process is retried forever.
func (s *Store) CountAttempt(ctx context.Context, ids []int64) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE chunks SET embed_attempts = embed_attempts + 1 WHERE id = ANY($1)`, ids)
	return err
}

func (s *Store) SaveEmbeddings(ctx context.Context, ids []int64, vectors [][]float32) error {
	if len(ids) != len(vectors) {
		return fmt.Errorf("%d ids for %d vectors", len(ids), len(vectors))
	}
	literals := make([]string, len(vectors))
	for i, v := range vectors {
		literals[i] = vectorLiteral(v)
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE chunks SET embedding = data.vec::vector
		FROM unnest($1::bigint[], $2::text[]) AS data(id, vec)
		WHERE chunks.id = data.id`, ids, literals)
	return err
}

const vectorSQL = `
SELECT c.id,
       s.kind,
       s.title,
       s.path,
       c.locator,
       coalesce(c.page, 0),
       1 - (c.embedding <=> $1::vector) AS score,
       left(c.body, 240)
FROM chunks c
JOIN sources s ON s.id = c.source_id
WHERE c.embedding IS NOT NULL
  AND ($2 = '' OR s.kind = $2)
ORDER BY c.embedding <=> $1::vector
LIMIT $3`

func (s *Store) SearchVector(ctx context.Context, vector []float32, kind string, limit int) ([]corpus.Hit, error) {
	rows, err := s.pool.Query(ctx, vectorSQL, vectorLiteral(vector), kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	hits := make([]corpus.Hit, 0, limit)
	for rows.Next() {
		var h corpus.Hit
		if err := rows.Scan(&h.ID, &h.Kind, &h.Title, &h.Path, &h.Locator, &h.Page, &h.Rank, &h.Snippet); err != nil {
			return nil, err
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
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
SELECT s.kind, s.title, s.path, c.locator, c.body,
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
	err := s.pool.QueryRow(ctx, passageSQL, id).
		Scan(&p.Kind, &p.Title, &p.Path, &p.Locator, &p.Body, &prev, &next)
	if err != nil {
		return corpus.Passage{}, fmt.Errorf("read chunk %d: %w", id, err)
	}
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
