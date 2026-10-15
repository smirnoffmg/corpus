package store

import (
	"context"
	"errors"
	"fmt"
)

// ErrSharedHash is a content hash another file in the library has too.
var ErrSharedHash = errors.New("another file has the same content")

// Rehash files everything kept under a file's content hash under its new one:
// the source, the scan and how far its recognition got, the description, the
// reference list, and other papers' citations matched to it. It is for a file
// rewritten without a change to the text the indexer reads from it — the next
// pass then finds the file unchanged rather than a new book, keeps its chunks,
// and a checked citation key stays the one typed into papers.
//
// A hash another file shares is refused: the description and the reference
// list belong to both copies.
func (s *Store) Rehash(ctx context.Context, kind, path, oldHash, newHash string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var shared bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM sources WHERE hash = $1 AND path <> $2)
		    OR EXISTS (SELECT 1 FROM scans WHERE hash = $1 AND path <> $2)`, oldHash, path).Scan(&shared); err != nil {
		return err
	}
	if shared {
		return fmt.Errorf("%w: %s", ErrSharedHash, path)
	}

	steps := []struct {
		what, sql string
		args      []any
	}{
		{"source", `UPDATE sources SET hash = $2 WHERE hash = $1 AND kind = $3 AND path = $4`, []any{oldHash, newHash, kind, path}},
		{"scan", `UPDATE scans SET hash = $2 WHERE hash = $1`, nil},
		{"description", `UPDATE bibliography SET key = $2 WHERE key = $1`, nil},
		{"matched citations", `UPDATE citations SET resolved = $2 WHERE resolved = $1`, nil},
		// citations.paper references bibliographies without ON UPDATE CASCADE,
		// so the list is copied under the new key, its entries moved, and the
		// old row dropped.
		{"reference list", `
			INSERT INTO bibliographies (paper, parsed_at, entries, parser)
			SELECT $2, parsed_at, entries, parser FROM bibliographies WHERE paper = $1`, nil},
		{"references", `UPDATE citations SET paper = $2 WHERE paper = $1`, nil},
		{"old reference list", `DELETE FROM bibliographies WHERE paper = $1 AND $2 <> $1`, nil},
	}
	for _, st := range steps {
		args := st.args
		if args == nil {
			args = []any{oldHash, newHash}
		}
		if _, err := tx.Exec(ctx, st.sql, args...); err != nil {
			return fmt.Errorf("rehash %s: %w", st.what, err)
		}
	}
	return tx.Commit(ctx)
}
