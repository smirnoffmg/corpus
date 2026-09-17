package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

// Qualified, because the reverse lookup joins citations to sources and both
// tables have a title.
const citationColumns = `c.ord, c.raw, coalesce(c.label, ''), coalesce(c.doi, ''), coalesce(c.arxiv, ''), ` +
	`coalesce(c.isbn, ''), coalesce(c.url, ''), coalesce(c.authors, ''), coalesce(c.title, ''), ` +
	`coalesce(c.container, ''), coalesce(c.year, 0), c.fingerprint, coalesce(c.resolved, ''), coalesce(c.matched_by, ''), ` +
	`coalesce(r.kind, ''), coalesce(r.path, ''), coalesce(r.title, '')`

// resolvedSource joins the work a citation was matched to, when the library
// holds it as a file. A manual is keyed by name rather than by hash and has no
// row here, which is why the join is left. One file can sit on two shelves
// under one hash — a book-shelf copy of a paper — and it is still one work: the
// publication is taken, since it has a card.
const resolvedSource = ` LEFT JOIN LATERAL (
		SELECT x.kind, x.path, x.title FROM sources x
		WHERE x.hash = c.resolved
		ORDER BY x.kind <> 'paper', x.path
		LIMIT 1) r ON true`

func scanCitation(row pgx.CollectableRow) (corpus.Citation, error) {
	var c corpus.Citation
	err := row.Scan(&c.Ord, &c.Raw, &c.Label, &c.DOI, &c.ArXiv, &c.ISBN, &c.URL,
		&c.Authors, &c.Title, &c.Container, &c.Year, &c.Fingerprint, &c.Resolved, &c.MatchedBy,
		&c.ResolvedKind, &c.ResolvedPath, &c.ResolvedTitle)
	return c, err
}

// UnparsedPapers lists the publications whose list of references has not been
// read by this version of the parser: never read, or read by an older one. A
// paper that was read and had none is not one of them.
func (s *Store) UnparsedPapers(ctx context.Context, parser int) ([]corpus.Unparsed, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (s.hash) s.path, s.hash, s.recognised
		FROM sources s
		LEFT JOIN bibliographies b ON b.paper = s.hash
		WHERE s.kind = 'paper' AND (b.paper IS NULL OR b.parser < $1)
		ORDER BY s.hash, s.path`, parser)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (corpus.Unparsed, error) {
		var u corpus.Unparsed
		err := row.Scan(&u.Path, &u.Hash, &u.Recognised)
		return u, err
	})
}

// SourceHash is what a source is filed under everywhere a file's identity
// matters — its citations, its description — rather than where it sits.
func (s *Store) SourceHash(ctx context.Context, path string) (string, bool, error) {
	var hash string
	err := s.pool.QueryRow(ctx, `SELECT hash FROM sources WHERE path = $1`, path).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return hash, err == nil, err
}

// SaveCitations records what a publication cites, replacing whatever was read
// from it before. An empty list is still a record: it says the paper was read.
func (s *Store) SaveCitations(ctx context.Context, paper string, parser int, cs []corpus.Citation) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		INSERT INTO bibliographies (paper, entries, parser) VALUES ($1, $2, $3)
		ON CONFLICT (paper) DO UPDATE SET entries = EXCLUDED.entries, parser = EXCLUDED.parser, parsed_at = now()`,
		paper, len(cs), parser); err != nil {
		return err
	}
	if err := replaceCitations(ctx, tx, paper, cs); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// UpdateCitations rewrites a list already read — with what a registry filled
// in, say — and leaves the record of which parser read it alone.
func (s *Store) UpdateCitations(ctx context.Context, paper string, cs []corpus.Citation) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := replaceCitations(ctx, tx, paper, cs); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func replaceCitations(ctx context.Context, tx pgx.Tx, paper string, cs []corpus.Citation) error {
	if _, err := tx.Exec(ctx, `DELETE FROM citations WHERE paper = $1`, paper); err != nil {
		return err
	}

	batch := &pgx.Batch{}
	for i := range cs {
		c := &cs[i]
		batch.Queue(`
			INSERT INTO citations (paper, ord, raw, label, doi, arxiv, isbn, url, authors, title, container, year,
			                       fingerprint, resolved, matched_by)
			VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''), NULLIF($7, ''),
			        NULLIF($8, ''), NULLIF($9, ''), NULLIF($10, ''), NULLIF($11, ''), NULLIF($12, 0),
			        $13, NULLIF($14, ''), NULLIF($15, ''))`,
			paper, c.Ord, c.Raw, c.Label, c.DOI, c.ArXiv, c.ISBN, c.URL, c.Authors, c.Title, c.Container, c.Year,
			c.Fingerprint, c.Resolved, c.MatchedBy)
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return err
	}
	return nil
}

// Citations is what a publication cites, in the order its list prints them.
func (s *Store) Citations(ctx context.Context, paper string) ([]corpus.Citation, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+citationColumns+` FROM citations c`+resolvedSource+` WHERE c.paper = $1 ORDER BY c.ord`, paper)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanCitation)
}

// Citing is the other direction: the publications that cite a work, found
// either by the library's key for it — which is what resolution filled in — or
// by the fingerprint two bibliographies would agree on.
func (s *Store) Citing(ctx context.Context, key, fingerprint string) ([]corpus.CitingPaper, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT s.path, s.title, `+citationColumns+`
		FROM citations c
		JOIN LATERAL (
		    SELECT x.path, x.title FROM sources x
		    WHERE x.hash = c.paper AND x.kind = 'paper'
		    ORDER BY x.path
		    LIMIT 1) s ON true`+resolvedSource+`
		WHERE ($1 <> '' AND c.resolved = $1) OR ($2 <> '' AND c.fingerprint = $2)
		ORDER BY s.path, c.ord`, key, fingerprint)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (corpus.CitingPaper, error) {
		var p corpus.CitingPaper
		c := &p.Citation
		err := row.Scan(&p.Path, &p.Title, &c.Ord, &c.Raw, &c.Label, &c.DOI, &c.ArXiv, &c.ISBN,
			&c.URL, &c.Authors, &c.Title, &c.Container, &c.Year, &c.Fingerprint, &c.Resolved, &c.MatchedBy,
			&c.ResolvedKind, &c.ResolvedPath, &c.ResolvedTitle)
		return p, err
	})
}

// SharedCitations is what two publications both point at, as the first one
// prints them.
func (s *Store) SharedCitations(ctx context.Context, a, b string) ([]corpus.Citation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+citationColumns+`
		FROM citations c`+resolvedSource+`
		WHERE c.paper = $1
		  AND EXISTS (SELECT 1 FROM citations o WHERE o.paper = $2 AND o.fingerprint = c.fingerprint)
		ORDER BY c.ord`, a, b)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanCitation)
}

// ResolveCitations points every entry that names a work the library holds at
// that work. It runs each pass rather than once at extraction: the library
// grows, and an entry that found nothing yesterday should find the book
// uploaded today.
//
// Two things say what the library holds. A description: there an identifier is
// a match — an ISBN only for a book, since the one an article carries is its
// proceedings' — and a title is a match only when it is the same title in the
// same year, reduced the same way on both sides. And the file itself: a paper
// or a book prints its title at the top of its first page, so an entry whose
// title stands there names that file, description or not. Only a title of four
// words and more counts there, since "Natural language processing" is a phrase
// any abstract can hold; and only the top of the page, where the title is.
//
// "Discrepancies can occur for many reasons, such as misspellings" (Ullman,
// Database Systems: The Complete Book, printed p. 1079), and a work that merely
// looks alike is not the same work: anything softer is left unresolved rather
// than guessed. Where several files match, an identifier wins over a title and
// a title over a page, and of two files a publication wins over a book-shelf
// copy of it.
func (s *Store) ResolveCitations(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		WITH head AS (
		    SELECT s.hash, s.kind, s.path,
		           left(corpus_normalize_title(string_agg(c.body, ' ' ORDER BY c.ord)), $1) AS text
		    FROM sources s
		    JOIN chunks c ON c.source_id = s.id
		     AND c.page = (SELECT min(x.page) FROM chunks x WHERE x.source_id = s.id)
		    WHERE s.kind IN ('paper', 'book')
		    GROUP BY s.hash, s.kind, s.path
		), titled AS (
		    SELECT c.paper, c.ord, corpus_normalize_title(c.title) AS title
		    FROM citations c
		    WHERE c.title IS NOT NULL
		), candidate AS (
		    SELECT c.paper, c.ord, b.key, x.how, x.rank, 0 AS shelf, '' AS path
		    FROM citations c
		    JOIN bibliography b ON b.key <> c.paper
		    CROSS JOIN LATERAL (
		        SELECT 'doi' AS how, 1 AS rank
		        WHERE c.doi IS NOT NULL AND lower(b.csl->>'DOI') = lower(c.doi)
		        UNION ALL
		        SELECT 'isbn', 2
		        WHERE c.isbn IS NOT NULL AND coalesce(b.csl->>'type', 'book') = 'book'
		          AND translate(coalesce(b.csl->>'ISBN', ''), '- ', '') LIKE '%' || c.isbn || '%'
		        UNION ALL
		        SELECT 'title', 3
		        WHERE c.title IS NOT NULL AND c.year IS NOT NULL
		          AND corpus_normalize_title(b.csl->>'title') = corpus_normalize_title(c.title)
		          AND (b.csl#>>'{issued,date-parts,0,0}')::int = c.year
		    ) x
		    UNION ALL
		    SELECT t.paper, t.ord, h.hash, 'page', 4, CASE h.kind WHEN 'paper' THEN 0 ELSE 1 END, h.path
		    FROM titled t
		    JOIN head h ON h.hash <> t.paper AND position(t.title IN h.text) > 0
		    WHERE array_length(string_to_array(t.title, ' '), 1) >= $2
		), picked AS (
		    SELECT DISTINCT ON (paper, ord) paper, ord, key, how
		    FROM candidate
		    ORDER BY paper, ord, rank, shelf, path
		)
		UPDATE citations c SET resolved = p.key, matched_by = p.how
		FROM picked p
		WHERE c.paper = p.paper AND c.ord = p.ord
		  AND (c.resolved IS DISTINCT FROM p.key OR c.matched_by IS DISTINCT FROM p.how)`,
		titleHead, minPageTitleWords)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// A title stands at the top of a first page: after a running head, a
// conference line or an arXiv stamp, but well before the abstract ends. On the
// 43 papers these were chosen on, the two limits kept every true match that
// searching the whole first page found, and dropped the phrases from abstracts.
const (
	titleHead         = 600
	minPageTitleWords = 4
)
