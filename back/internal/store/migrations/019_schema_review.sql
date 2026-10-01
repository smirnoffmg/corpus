-- +goose Up
-- A chunk's place in its source was unique only because the code wrote it so.
-- As an index it is also the path to a neighbouring page, a first page and a
-- window over a source: each of those read the whole source before. Its leading
-- column still serves the cascade from sources and the count per source.
CREATE UNIQUE INDEX IF NOT EXISTS chunks_source_ord ON chunks (source_id, ord);
DROP INDEX IF EXISTS chunks_source_idx;

-- Citations, descriptions and file links find a source by its content hash.
-- Not unique: one file may stand on two shelves.
CREATE INDEX IF NOT EXISTS sources_hash_idx ON sources (hash);

-- Two regexps and lower(): nothing here stops a parallel worker running it.
ALTER FUNCTION corpus_normalize_title(text) PARALLEL SAFE;
