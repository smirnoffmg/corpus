-- +goose Up
-- tags are stored but never queried: the index cost every insert and served no
-- read. Recreate it the day a tag filter appears, not before.
DROP INDEX IF EXISTS chunks_tags_idx;

-- The embedding queue asks for the first source with unembedded chunks. Without
-- a partial index that walk crosses every chunk already embedded, so it grows
-- slower exactly as the queue drains.
CREATE INDEX IF NOT EXISTS chunks_pending_idx
    ON chunks (source_id, ord) WHERE embedding IS NULL;
