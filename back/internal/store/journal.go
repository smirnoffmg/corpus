package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

// readFollowsWithin is how long after a search a read of one of its hits still
// counts as that search's follow-up; later, it is a new look at the page.
const readFollowsWithin = "30 minutes"

// journalKept is how long the journal keeps a row. Each write drops what has
// aged out of its table, so the journal drains as fast as it fills (Steady
// State, Release It!, p. 124) without a job of its own to schedule; the index
// on at keeps the delete to the few rows it removes.
const journalKept = "180 days"

func (s *Store) LogSearch(ctx context.Context, r corpus.SearchRecord) error {
	hits := r.Hits
	if hits == nil {
		hits = []int64{}
	}
	_, err := s.pool.Exec(ctx, `
		WITH aged AS (DELETE FROM search_log WHERE at < now() - interval '`+journalKept+`')
		INSERT INTO search_log (origin, query, mode, kind, hits, err)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		r.Origin, r.Query, r.Mode, r.Kind, hits, r.Err)
	return err
}

func (s *Store) LogRead(ctx context.Context, origin string, chunkID int64) error {
	_, err := s.pool.Exec(ctx, `
		WITH aged AS (DELETE FROM read_log WHERE at < now() - interval '`+journalKept+`')
		INSERT INTO read_log (origin, chunk_id) VALUES ($1, $2)`, origin, chunkID)
	return err
}

// Usage sums up the searches of the last window, one row per origin.
func (s *Store) Usage(ctx context.Context, window time.Duration) ([]corpus.Usage, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT origin,
		       count(*),
		       count(*) FILTER (WHERE cardinality(hits) = 0 AND err = ''),
		       count(*) FILTER (WHERE err <> ''),
		       count(*) FILTER (WHERE EXISTS (
		           SELECT 1 FROM read_log r
		           WHERE r.origin = l.origin AND r.chunk_id = ANY (l.hits)
		             AND r.at >= l.at AND r.at < l.at + interval '`+readFollowsWithin+`'))
		FROM search_log l
		WHERE at > now() - make_interval(secs => $1)
		GROUP BY origin
		ORDER BY origin`, window.Seconds())
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (corpus.Usage, error) {
		var u corpus.Usage
		err := row.Scan(&u.Origin, &u.Searches, &u.Empty, &u.Failed, &u.Read)
		return u, err
	})
}

// Searches lists the journal, the latest first.
func (s *Store) Searches(ctx context.Context, f corpus.SearchFilter) ([]corpus.LoggedSearch, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT at, origin, query, mode, kind, cardinality(hits), err
		FROM search_log
		WHERE ($1 = '' OR origin = $1)
		  AND (NOT $2 OR (cardinality(hits) = 0 AND err = ''))
		ORDER BY at DESC, id DESC
		LIMIT $3`, f.Origin, f.Empty, f.Limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (corpus.LoggedSearch, error) {
		var l corpus.LoggedSearch
		err := row.Scan(&l.At, &l.Origin, &l.Query, &l.Mode, &l.Kind, &l.Hits, &l.Err)
		return l, err
	})
}
