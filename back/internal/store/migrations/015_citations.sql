-- +goose Up
-- A publication's list of references, read out of the PDF once and kept under
-- the file's content hash — the key a bibliographic description is filed under
-- too, so the work survives the file being renamed, re-uploaded or re-indexed,
-- none of which changes what the paper cites. The row exists even when the
-- paper turned out to have no references, to tell "parsed, found none" from
-- "not parsed yet": without it every such paper would be read again each pass.
CREATE TABLE IF NOT EXISTS bibliographies (
    paper     text PRIMARY KEY,
    parsed_at timestamptz NOT NULL DEFAULT now(),
    entries   int         NOT NULL
);

CREATE TABLE IF NOT EXISTS citations (
    paper       text NOT NULL REFERENCES bibliographies(paper) ON DELETE CASCADE,
    ord         int  NOT NULL,
    -- The entry as printed. Everything below it is a reading of this line and
    -- can be wrong; this is what a reader corrects it against.
    raw         text NOT NULL,
    label       text,
    doi         text,
    arxiv       text,
    isbn        text,
    url         text,
    authors     text,
    title       text,
    container   text,
    year        int,
    -- What the entry points at, as one comparable string. Two papers citing the
    -- same work agree on it, so the reverse lookup and the shared-references
    -- query are a join rather than a comparison of every field with every field.
    fingerprint text NOT NULL,
    -- bibliography.key of the work when the library holds it, and how it was
    -- matched. Null rather than a guess: a title that merely looks alike is not
    -- the same work.
    resolved    text,
    matched_by  text CHECK (matched_by IN ('doi', 'isbn', 'arxiv', 'title')),
    PRIMARY KEY (paper, ord)
);

CREATE INDEX IF NOT EXISTS citations_fingerprint ON citations (fingerprint);
CREATE INDEX IF NOT EXISTS citations_resolved ON citations (resolved) WHERE resolved IS NOT NULL;

-- +goose StatementBegin
-- The same reduction cite.NormalizeTitle applies in Go, and the two have to
-- agree: one side reads the citation, the other reads the library's description.
CREATE OR REPLACE FUNCTION corpus_normalize_title(t text) RETURNS text AS $$
    SELECT coalesce(
        trim(regexp_replace(
            regexp_replace(lower(translate(t, 'ё', 'е')), '[^[:alnum:]]+', ' ', 'g'),
            '\s+', ' ', 'g')),
        '');
$$ LANGUAGE sql IMMUTABLE;
-- +goose StatementEnd
