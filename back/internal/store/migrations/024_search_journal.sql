-- +goose Up
-- What was searched and what was read, so that how the library is used can be
-- seen rather than guessed: which queries come back empty, and how often a hit
-- is read in full. Nothing here is backed up; it lives and dies with the
-- database.
CREATE TABLE IF NOT EXISTS search_log (
    id     bigserial   PRIMARY KEY,
    at     timestamptz NOT NULL DEFAULT now(),
    origin text        NOT NULL CHECK (origin IN ('mcp', 'http')),
    query  text        NOT NULL,
    mode   text        NOT NULL,
    kind   text        NOT NULL DEFAULT '',
    hits   bigint[]    NOT NULL DEFAULT '{}',
    err    text        NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS search_log_at ON search_log (at);

CREATE TABLE IF NOT EXISTS read_log (
    id       bigserial   PRIMARY KEY,
    at       timestamptz NOT NULL DEFAULT now(),
    origin   text        NOT NULL CHECK (origin IN ('mcp', 'http')),
    chunk_id bigint      NOT NULL
);
CREATE INDEX IF NOT EXISTS read_log_chunk ON read_log (chunk_id, at);

-- +goose Down
DROP TABLE IF EXISTS read_log;
DROP TABLE IF EXISTS search_log;
