-- +goose Up
-- Bibliographic descriptions are the one thing in the database that cannot be
-- rebuilt from the files: they are typed in or confirmed by hand. So they are
-- keyed by what the source is, not by its row — a book's content hash, or
-- 'manual:<name>' — and have no foreign key: a source that is pruned, renamed
-- or re-uploaded finds its description again instead of taking it with it.
CREATE TABLE IF NOT EXISTS bibliography (
    key        text PRIMARY KEY,
    citekey    text        NOT NULL UNIQUE,
    csl        jsonb       NOT NULL,
    status     text        NOT NULL CHECK (status IN ('draft', 'checked')),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Citation styles added beyond the bundled ones — a journal's own — kept here
-- so they work offline once fetched.
CREATE TABLE IF NOT EXISTS csl_styles (
    id       text PRIMARY KEY,
    title    text        NOT NULL,
    xml      text        NOT NULL,
    added_at timestamptz NOT NULL DEFAULT now()
);
