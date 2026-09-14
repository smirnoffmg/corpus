package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/smirnoffmg/corpus/internal/cite"
	"github.com/smirnoffmg/corpus/internal/corpus"
)

// ErrNoReference is a source that has no description, or no key to hang one on.
var ErrNoReference = corpus.ErrNoReference

// ReferenceKey is what a source's description is filed under: a book by its
// content, a manual by its directory. A note is not cited in papers.
func (s *Store) ReferenceKey(ctx context.Context, kind, path string) (string, error) {
	switch kind {
	case "docs":
		manual, _, _ := strings.Cut(path, "/")
		return "manual:" + manual, nil
	case "book":
		var hash string
		err := s.pool.QueryRow(ctx, `SELECT hash FROM sources WHERE kind = 'book' AND path = $1`, path).Scan(&hash)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNoReference
		}
		return hash, err
	}
	return "", fmt.Errorf("%w: a %s has no bibliographic description", ErrNoReference, kind)
}

const referenceColumns = `key, citekey, csl, status, updated_at`

func scanReference(row pgx.Row) (corpus.Reference, error) {
	var r corpus.Reference
	err := row.Scan(&r.Key, &r.CiteKey, &r.CSL, &r.Status, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNoReference
	}
	return r, err
}

func (s *Store) Reference(ctx context.Context, key string) (corpus.Reference, error) {
	return scanReference(s.pool.QueryRow(ctx, `SELECT `+referenceColumns+` FROM bibliography WHERE key = $1`, key))
}

func (s *Store) References(ctx context.Context) ([]corpus.Reference, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+referenceColumns+` FROM bibliography ORDER BY citekey`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (corpus.Reference, error) { return scanReference(row) })
}

// SaveReference stores a description. Its citation key follows the record
// while it is a draft — a draft is usually missing the year the key is made
// of — and is fixed from the moment it is checked, because from then on it is
// typed into papers.
func (s *Store) SaveReference(ctx context.Context, key string, csl corpus.CSL, status string) (corpus.Reference, error) {
	citekey, err := s.citekey(ctx, key, csl)
	if err != nil {
		return corpus.Reference{}, err
	}
	return scanReference(s.pool.QueryRow(ctx, `
		INSERT INTO bibliography (key, citekey, csl, status) VALUES ($1, $2, $3, $4)
		ON CONFLICT (key) DO UPDATE SET citekey = EXCLUDED.citekey, csl = EXCLUDED.csl, status = EXCLUDED.status, updated_at = now()
		RETURNING `+referenceColumns, key, citekey, csl, status))
}

// EnsureDraft files a draft for a source that has no description yet, and
// leaves any existing one — above all a checked one — alone.
func (s *Store) EnsureDraft(ctx context.Context, key string, csl corpus.CSL) error {
	citekey, err := s.citekey(ctx, key, csl)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO bibliography (key, citekey, csl, status) VALUES ($1, $2, $3, 'draft')
		ON CONFLICT (key) DO NOTHING`, key, citekey, csl)
	return err
}

func (s *Store) citekey(ctx context.Context, key string, csl corpus.CSL) (string, error) {
	var existing, status string
	err := s.pool.QueryRow(ctx, `SELECT citekey, status FROM bibliography WHERE key = $1`, key).Scan(&existing, &status)
	switch {
	case err == nil && status == "checked":
		return existing, nil
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		return "", err
	}
	base := cite.KeyBase(csl)
	rows, err := s.pool.Query(ctx, `SELECT citekey FROM bibliography WHERE starts_with(citekey, $1) AND key <> $2`, base, key)
	if err != nil {
		return "", err
	}
	taken, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return "", err
	}
	set := make(map[string]bool, len(taken))
	for _, t := range taken {
		set[t] = true
	}
	return cite.UniqueKey(base, func(k string) bool { return set[k] }), nil
}

// UndescribedBooks lists books without a description, with the text of their
// first and last pages: the copyright page is at the front of a book and the
// imprint of a Russian one at the back.
func (s *Store) UndescribedBooks(ctx context.Context) ([]corpus.Undescribed, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT s.path, s.hash, s.title,
		       coalesce((SELECT string_agg(body, E'\n' ORDER BY ord) FROM
		           (SELECT ord, body FROM chunks WHERE source_id = s.id ORDER BY ord LIMIT 8) head), ''),
		       coalesce((SELECT string_agg(body, E'\n' ORDER BY ord) FROM
		           (SELECT ord, body FROM chunks WHERE source_id = s.id ORDER BY ord DESC LIMIT 4) tail), '')
		FROM sources s
		LEFT JOIN bibliography b ON b.key = s.hash
		WHERE s.kind = 'book' AND b.key IS NULL
		ORDER BY s.path`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (corpus.Undescribed, error) {
		var u corpus.Undescribed
		err := row.Scan(&u.Path, &u.Hash, &u.Title, &u.Head, &u.Tail)
		return u, err
	})
}

func (s *Store) Styles(ctx context.Context) ([]corpus.Style, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, title FROM csl_styles ORDER BY title`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[corpus.Style])
}

func (s *Store) StyleXML(ctx context.Context, id string) (string, error) {
	var xml string
	err := s.pool.QueryRow(ctx, `SELECT xml FROM csl_styles WHERE id = $1`, id).Scan(&xml)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNoReference
	}
	return xml, err
}

func (s *Store) SaveStyle(ctx context.Context, id, title, xml string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO csl_styles (id, title, xml) VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, xml = EXCLUDED.xml, added_at = now()`, id, title, xml)
	return err
}
