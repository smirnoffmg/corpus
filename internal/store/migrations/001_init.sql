-- +goose Up
CREATE TABLE IF NOT EXISTS sources (
    id         bigserial PRIMARY KEY,
    kind       text        NOT NULL CHECK (kind IN ('book', 'vault')),
    path       text        NOT NULL UNIQUE,
    title      text        NOT NULL,
    hash       text        NOT NULL,
    indexed_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS chunks (
    id        bigserial PRIMARY KEY,
    source_id bigint   NOT NULL REFERENCES sources (id) ON DELETE CASCADE,
    ord       int      NOT NULL,
    page      int,
    locator   text     NOT NULL,
    lang      text     NOT NULL CHECK (lang IN ('russian', 'english')),
    tags      text[]   NOT NULL DEFAULT '{}',
    body      text     NOT NULL,
    tsv       tsvector NOT NULL
);

CREATE INDEX IF NOT EXISTS chunks_tsv_idx    ON chunks USING gin (tsv);
CREATE INDEX IF NOT EXISTS chunks_tags_idx   ON chunks USING gin (tags);
CREATE INDEX IF NOT EXISTS chunks_source_idx ON chunks (source_id);
