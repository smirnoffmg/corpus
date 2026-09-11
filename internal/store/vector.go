package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Pending is a chunk that has no embedding yet.
type Pending struct {
	ID   int64
	Body string
}

// embedCharLimit bounds how much of a chunk is embedded. Book pages sit well
// under it; a long daily note would otherwise cost minutes of GPU for a tail
// that adds nothing to the topic of the section.
const embedCharLimit = 6000

func (s *Store) PendingEmbeddings(ctx context.Context, limit int) ([]Pending, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, left(body, $1) FROM chunks
		WHERE embedding IS NULL
		ORDER BY id
		LIMIT $2`, embedCharLimit, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Pending
	for rows.Next() {
		var p Pending
		if err := rows.Scan(&p.ID, &p.Body); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) MissingEmbeddings(ctx context.Context) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM chunks WHERE embedding IS NULL`).Scan(&n)
	return n, err
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
       1 - (c.embedding <=> $1::vector) AS score,
       left(c.body, 240)
FROM chunks c
JOIN sources s ON s.id = c.source_id
WHERE c.embedding IS NOT NULL
  AND ($2 = '' OR s.kind = $2)
ORDER BY c.embedding <=> $1::vector
LIMIT $3`

func (s *Store) SearchVector(ctx context.Context, vector []float32, kind string, limit int) ([]Hit, error) {
	rows, err := s.pool.Query(ctx, vectorSQL, vectorLiteral(vector), kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hits []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.ID, &h.Kind, &h.Title, &h.Path, &h.Locator, &h.Rank, &h.Snippet); err != nil {
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
