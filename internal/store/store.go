package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/smirnoffmg/corpus/internal/extract"
)

type Store struct{ pool *pgxpool.Pool }

type Source struct {
	Kind  string
	Path  string
	Title string
	Hash  string
}

type Hit struct {
	ID      int64   `json:"id"`
	Kind    string  `json:"kind"`
	Title   string  `json:"title"`
	Path    string  `json:"path"`
	Locator string  `json:"locator"`
	Rank    float32 `json:"rank"`
	Snippet string  `json:"snippet"`
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Migrate(ctx context.Context, ddl string) error {
	_, err := s.pool.Exec(ctx, ddl)
	return err
}

// Unchanged reports whether the file is already indexed under the same content
// hash, so that a reindex pass can skip it.
func (s *Store) Unchanged(ctx context.Context, path, hash string) (bool, error) {
	var stored string
	err := s.pool.QueryRow(ctx, `SELECT hash FROM sources WHERE path = $1`, path).Scan(&stored)
	switch {
	case err == pgx.ErrNoRows:
		return false, nil
	case err != nil:
		return false, err
	}
	return stored == hash, nil
}

// Replace rewrites a source and all of its chunks in one transaction. tsvectors
// are built here rather than in a generated column: the text -> regconfig cast
// is only STABLE, which Postgres rejects in a generation expression.
func (s *Store) Replace(ctx context.Context, src Source, chunks []extract.Chunk) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO sources (kind, path, title, hash)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (path) DO UPDATE
		    SET kind = EXCLUDED.kind, title = EXCLUDED.title,
		        hash = EXCLUDED.hash, indexed_at = now()
		RETURNING id`,
		src.Kind, src.Path, src.Title, src.Hash).Scan(&id)
	if err != nil {
		return fmt.Errorf("upsert source %s: %w", src.Path, err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM chunks WHERE source_id = $1`, id); err != nil {
		return err
	}

	batch := &pgx.Batch{}
	for _, c := range chunks {
		batch.Queue(`
			INSERT INTO chunks (source_id, ord, page, printed_page, locator, lang, tags, body, tsv)
			VALUES ($1, $2, NULLIF($3, 0), NULLIF($4, 0), $5, $6, COALESCE($7::text[], '{}'), $8,
			        to_tsvector($9::regconfig, $8))`,
			id, c.Ord, c.Page, c.Printed, c.Locator, c.Lang, c.Tags, c.Body, c.Lang)
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("insert chunks for %s: %w", src.Path, err)
	}
	return tx.Commit(ctx)
}

// Prune drops sources of a kind whose files have disappeared from disk.
func (s *Store) Prune(ctx context.Context, kind string, seen []string) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM sources WHERE kind = $1 AND NOT (path = ANY($2))`, kind, seen)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

const searchSQL = `
WITH q AS (
    SELECT websearch_to_tsquery('russian', $1) AS ru,
           websearch_to_tsquery('english', $1) AS en
)
SELECT c.id,
       s.kind,
       s.title,
       s.path,
       c.locator,
       ts_rank_cd(c.tsv, CASE c.lang WHEN 'russian' THEN q.ru ELSE q.en END) AS rank,
       ts_headline(c.lang::regconfig, c.body,
                   CASE c.lang WHEN 'russian' THEN q.ru ELSE q.en END,
                   'MaxFragments=2,MinWords=10,MaxWords=28,StartSel=<<,StopSel=>>')
FROM chunks c
JOIN sources s ON s.id = c.source_id
CROSS JOIN q
WHERE ((c.lang = 'russian' AND c.tsv @@ q.ru) OR (c.lang = 'english' AND c.tsv @@ q.en))
  AND ($2 = '' OR s.kind = $2)
ORDER BY rank DESC
LIMIT $3`

func (s *Store) Search(ctx context.Context, query, kind string, limit int) ([]Hit, error) {
	rows, err := s.pool.Query(ctx, searchSQL, query, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	hits := make([]Hit, 0, limit)
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.ID, &h.Kind, &h.Title, &h.Path, &h.Locator, &h.Rank, &h.Snippet); err != nil {
			return nil, err
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

func (s *Store) Stats(ctx context.Context) (sources, chunks int64, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM sources), (SELECT count(*) FROM chunks)`).Scan(&sources, &chunks)
	return
}
