-- +goose Up
-- Publications are a fourth kind of source: a PDF like a book, but cited by
-- DOI rather than ISBN, usually without printed page numbers, and carrying a
-- list of the works it cites.
ALTER TABLE sources DROP CONSTRAINT IF EXISTS sources_kind_check;
ALTER TABLE sources ADD CONSTRAINT sources_kind_check CHECK (kind IN ('book', 'vault', 'docs', 'paper'));

-- A scan used to be a book by assumption: the queue held no kind, and the
-- indexer resolved a scan's path against the books root. Scanned papers are at
-- least as common as scanned books, so the kind travels with the scan.
ALTER TABLE scans ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'book'
    CHECK (kind IN ('book', 'paper'));
