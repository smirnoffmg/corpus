package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver, for goose
	"github.com/pressly/goose/v3"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	pool *pgxpool.Pool
	dsn  string
	// How much text an embedding sees: the chunk itself, plus this many
	// characters of the chunks on either side.
	bodyChars      int
	neighbourChars int
	// efSearch is how wide the HNSW walk is before it starts returning. Higher
	// finds more of the true nearest neighbours and costs time.
	efSearch int
}

// Option configures a Store.
type Option func(*Store)

// WithEfSearch sets hnsw.ef_search. 0 leaves the pgvector default of 40.
func WithEfSearch(n int) Option {
	return func(s *Store) { s.efSearch = n }
}

// WithWindow sets how much text is handed to the embedder. The right size
// depends on the model: one that stops reading at 2400 characters gains nothing
// from a wider window, and loses whatever falls outside it.
func WithWindow(body, neighbours int) Option {
	return func(s *Store) {
		s.bodyChars, s.neighbourChars = body, neighbours
	}
}

func Open(ctx context.Context, dsn string, opts ...Option) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	// An HNSW scan gathers its candidates before the WHERE clause is applied, so
	// a filtered search ("only the vault") could return a handful of rows — or
	// none at all — while the index held plenty of matches. Iterative scans keep
	// walking the index until the filter has yielded enough.
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, setErr := conn.Exec(ctx, "SET hnsw.iterative_scan = strict_order")
		return setErr
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	st := &Store{
		pool: pool, dsn: dsn,
		bodyChars:      defaultBodyChars,
		neighbourChars: defaultNeighbourChars,
	}
	for _, opt := range opts {
		opt(st)
	}
	return st, nil
}

func (s *Store) Close() { s.pool.Close() }

// Migrate brings the schema up to date. goose keeps the ledger of what has been
// applied, which a plain "run every .sql on boot" could not: it only worked
// while every statement happened to be idempotent, and the migration that
// renamed a column already was not.
func (s *Store) Migrate(ctx context.Context) error {
	provider, db, err := s.provider()
	if err != nil {
		return err
	}
	defer db.Close()
	applied, err := provider.Up(ctx)
	if err != nil {
		return err
	}
	for _, m := range applied {
		slog.InfoContext(ctx, "migration applied", "migration", m.Source.Path)
	}
	return nil
}

// SchemaVersions reports the database's schema version and the one this build
// was written against. Only the indexer migrates, so a server started first, or
// built later, can find the schema behind it.
func (s *Store) SchemaVersions(ctx context.Context) (current, target int64, err error) {
	provider, db, err := s.provider()
	if err != nil {
		return 0, 0, err
	}
	defer db.Close()
	return provider.GetVersions(ctx)
}

func (s *Store) provider() (*goose.Provider, *sql.DB, error) {
	db, err := sql.Open("pgx", s.dsn)
	if err != nil {
		return nil, nil, err
	}
	// The provider reads from the root of the filesystem it is given, and the
	// embedded one is rooted at the module, not at the migrations directory.
	root, err := fs.Sub(migrations, "migrations")
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, root)
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	return provider, db, nil
}

// Unchanged reports whether the file is already indexed under the same content
// hash, so that a reindex pass can skip it.
func (s *Store) Unchanged(ctx context.Context, path, hash string) (bool, error) {
	var stored string
	err := s.pool.QueryRow(ctx, `SELECT hash FROM sources WHERE path = $1`, path).Scan(&stored)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	case err != nil:
		return false, err
	}
	return stored == hash, nil
}

// Replace rewrites a source and all of its chunks in one transaction. tsvectors
// are built here rather than in a generated column: the text -> regconfig cast
// is only STABLE, which Postgres rejects in a generation expression.
func (s *Store) Replace(ctx context.Context, src corpus.Source, chunks []corpus.Chunk) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

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
			INSERT INTO chunks (source_id, ord, page, printed_page, heading, anchor, lang, tags, body, tsv)
			VALUES ($1, $2, NULLIF($3, 0), NULLIF($4, 0), NULLIF($5, ''), NULLIF($10, ''), $6, COALESCE($7::text[], '{}'), $8,
			        to_tsvector($9::regconfig, $8))`,
			id, c.Ord, c.Page, c.Printed, c.Heading, c.Lang, c.Tags, c.Body, c.Lang, c.Anchor)
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("insert chunks for %s: %w", src.Path, err)
	}
	return tx.Commit(ctx)
}

// Forget removes a source entirely. A file with no text layer is not a source
// of anything: keeping the row would only inflate the corpus with something no
// search can ever return.
// The bool says whether anything was actually removed, so a caller can report
// the change once instead of every pass.
func (s *Store) Forget(ctx context.Context, path string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM sources WHERE path = $1`, path)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// PathByHash finds a source by what is inside it rather than where it sits, so
// that a renamed file can be recognised as the book it already was.
func PathByHashQuery() string { return `SELECT path FROM sources WHERE kind = $1 AND hash = $2` }

func (s *Store) PathByHash(ctx context.Context, kind, hash string) (string, bool, error) {
	var path string
	err := s.pool.QueryRow(ctx, PathByHashQuery(), kind, hash).Scan(&path)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return path, true, nil
}

// Rename moves a source to its new path, keeping its chunks and their
// embeddings: the file was renamed, not replaced.
func (s *Store) Rename(ctx context.Context, oldPath, newPath, title string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE sources SET path = $2, title = $3, indexed_at = now() WHERE path = $1`,
		oldPath, newPath, title)
	return err
}

// SetTitle refreshes a source's display name without touching its chunks, so
// recognising a better title never costs the embeddings.
func (s *Store) SetTitle(ctx context.Context, path, title string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE sources SET title = $2 WHERE path = $1 AND title <> $2`, path, title)
	return err
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
       c.heading,
       coalesce(c.anchor, '') AS anchor,
       coalesce(c.page, 0) AS page,
       coalesce(c.printed_page, 0) AS printed_page,
       ts_rank_cd(c.tsv, CASE c.lang WHEN 'russian' THEN q.ru ELSE q.en END, $4)
         + CASE WHEN to_tsvector(c.lang::regconfig, s.title)
                     @@ CASE c.lang WHEN 'russian' THEN q.ru ELSE q.en END
                THEN $5::float8 ELSE 0 END AS rank,
       ts_headline(c.lang::regconfig, c.body,
                   CASE c.lang WHEN 'russian' THEN q.ru ELSE q.en END,
                   'MaxFragments=2,MinWords=10,MaxWords=28,StartSel=<<,StopSel=>>') AS snippet
FROM chunks c
JOIN sources s ON s.id = c.source_id
CROSS JOIN q
WHERE ((c.lang = 'russian' AND c.tsv @@ q.ru) OR (c.lang = 'english' AND c.tsv @@ q.en))
  AND ($2 = '' OR s.kind = $2)
-- Ties are common: two chunks naming a term the same number of times score
-- identically, and without a tiebreaker their order depends on the plan
-- Postgres picks, which depends on the LIMIT. The same query then answers
-- differently at limit 3 and limit 4.
ORDER BY rank DESC, c.id
LIMIT $3`

func (s *Store) Search(ctx context.Context, q corpus.Query) ([]corpus.Hit, error) {
	rows, err := s.pool.Query(ctx, searchSQL, q.Text, q.Kind, q.Limit, q.Normalization, q.TitleBoost)
	if err != nil {
		return nil, err
	}
	return collectHits(rows)
}

// hitRow is the shape both search legs select. It exists so the columns are
// bound by name: the legs return nine columns of which two pairs share a type,
// and a positional scan that has them the wrong way round still compiles, still
// runs, and cites a book by its path.
type hitRow struct {
	ID      int64   `db:"id"`
	Kind    string  `db:"kind"`
	Title   string  `db:"title"`
	Path    string  `db:"path"`
	Heading *string `db:"heading"`
	Anchor  string  `db:"anchor"`
	Page    int     `db:"page"`
	Printed int     `db:"printed_page"`
	Rank    float32 `db:"rank"`
	Snippet string  `db:"snippet"`
}

func collectHits(rows pgx.Rows) ([]corpus.Hit, error) {
	found, err := pgx.CollectRows(rows, pgx.RowToStructByName[hitRow])
	if err != nil {
		return nil, err
	}
	hits := make([]corpus.Hit, len(found))
	for i, r := range found {
		hits[i] = corpus.Hit{
			ID:      r.ID,
			Kind:    r.Kind,
			Title:   r.Title,
			Path:    r.Path,
			Locator: corpus.Locator(deref(r.Heading), r.Page, r.Printed),
			Anchor:  r.Anchor,
			Page:    r.Page,
			Rank:    r.Rank,
			Snippet: r.Snippet,
		}
	}
	return hits, nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (s *Store) Stats(ctx context.Context) (sources, chunks int64, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM sources), (SELECT count(*) FROM chunks)`).Scan(&sources, &chunks)
	return
}
