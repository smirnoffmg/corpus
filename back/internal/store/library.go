package store

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

// reindexChannel carries "something was added, don't wait for the next pass".
// The indexer and the server share nothing but the database, and the database
// already knows how to deliver a message between them.
const reindexChannel = "corpus_reindex"

// Sources lists sources with their progress. kind and prefix narrow the list
// when non-empty; a manual is every source under "<manual>/".
func (s *Store) Sources(ctx context.Context, kind, prefix string) ([]corpus.SourceStatus, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT s.kind, s.path, s.title, s.indexed_at,
		       count(c.id) AS chunks,
		       count(c.id) FILTER (WHERE e.embedding IS NOT NULL) AS embedded,
		       count(c.id) FILTER (WHERE e.embedding IS NULL AND e.attempts >= $3) AS quarantined,
		       coalesce(min(b.status), '') AS description
		FROM sources s
		LEFT JOIN chunks c ON c.source_id = s.id
		LEFT JOIN embeddings e ON e.hash = c.embed_hash
		LEFT JOIN bibliography b ON b.key = CASE s.kind
		    WHEN 'book' THEN s.hash
		    WHEN 'docs' THEN 'manual:' || split_part(s.path, '/', 1)
		END
		WHERE ($1 = '' OR s.kind = $1)
		  AND starts_with(s.path, $2)
		GROUP BY s.id
		ORDER BY s.kind, s.path`,
		kind, prefix, maxEmbedAttempts)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (corpus.SourceStatus, error) {
		var r corpus.SourceStatus
		err := row.Scan(&r.Kind, &r.Path, &r.Title, &r.IndexedAt, &r.Chunks, &r.Embedded, &r.Quarantined, &r.Description)
		return r, err
	})
}

// RequestReindex asks a listening indexer to start a pass now.
func (s *Store) RequestReindex(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `SELECT pg_notify($1, '')`, reindexChannel)
	return err
}

// ListenReindex delivers a value whenever a pass is requested. Requests that
// arrive while the last one is still unread collapse into it: one pass sees
// every file added before it starts. The channel is closed when ctx ends or the
// connection is lost.
//
// The listener has a connection of its own rather than one from the pool: a
// pooled connection keeps its LISTEN when released, and would hand
// notifications to whichever query borrowed it next.
func (s *Store) ListenReindex(ctx context.Context) (<-chan struct{}, error) {
	conn, err := pgx.Connect(ctx, s.dsn)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, "LISTEN "+reindexChannel); err != nil {
		_ = conn.Close(context.WithoutCancel(ctx))
		return nil, err
	}

	wake := make(chan struct{}, 1)
	go func() {
		defer close(wake)
		defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
		for {
			if _, err := conn.WaitForNotification(ctx); err != nil {
				if ctx.Err() == nil {
					slog.WarnContext(ctx, "reindex listener lost its connection", "err", err)
				}
				return
			}
			select {
			case wake <- struct{}{}:
			default:
			}
		}
	}()
	return wake, nil
}
