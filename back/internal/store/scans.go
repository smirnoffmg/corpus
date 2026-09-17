package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

// maxScanAttempts sets a scan aside after it failed this often, so one broken
// PDF does not stop every scan queued behind it.
const maxScanAttempts = 3

// MarkScan records a PDF with no text layer. Marking one already known keeps
// how far its recognition got, and follows a rename.
func (s *Store) MarkScan(ctx context.Context, scan corpus.Scan) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO scans (kind, hash, path, pages) VALUES ($1, $2, $3, $4)
		ON CONFLICT (hash) DO UPDATE SET kind = EXCLUDED.kind, path = EXCLUDED.path, pages = EXCLUDED.pages`,
		scan.Kind, scan.Hash, scan.Path, scan.Pages)
	return err
}

// NextScan is the scan to recognise next: in path order, skipping those that
// failed too often.
func (s *Store) NextScan(ctx context.Context) (corpus.Scan, bool, error) {
	var sc corpus.Scan
	err := s.pool.QueryRow(ctx, `
		SELECT sc.kind, sc.hash, sc.path, sc.pages, sc.recognised
		FROM scans sc
		WHERE sc.attempts < $1
		  AND NOT EXISTS (SELECT 1 FROM sources s WHERE s.path = sc.path)
		ORDER BY sc.path
		LIMIT 1`, maxScanAttempts).Scan(&sc.Kind, &sc.Hash, &sc.Path, &sc.Pages, &sc.Recognised)
	if errors.Is(err, pgx.ErrNoRows) {
		return corpus.Scan{}, false, nil
	}
	return sc, err == nil, err
}

func (s *Store) ScanProgress(ctx context.Context, hash string, recognised int) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE scans SET recognised = $2, updated_at = now() WHERE hash = $1`, hash, recognised)
	return err
}

// ScanFailed counts a failed recognition and keeps the reason.
func (s *Store) ScanFailed(ctx context.Context, hash, reason string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE scans SET attempts = attempts + 1, last_error = $2, updated_at = now() WHERE hash = $1`, hash, reason)
	return err
}

// PruneScans drops scans that are sources now, and scans of files no longer in
// the library. seen is every path of that kind the pass walked, so a kind whose
// root was not walked leaves the other kinds' scans alone.
func (s *Store) PruneScans(ctx context.Context, kind string, seen []string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM scans sc
		WHERE (sc.kind = $2 AND NOT (sc.path = ANY($1)))
		   OR EXISTS (SELECT 1 FROM sources s WHERE s.path = sc.path)`, seen, kind)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
