-- +goose Up
-- The other names a note answers to, from its frontmatter's aliases. They
-- match as the title does in the title boost, and join the text index through
-- the note's first section. Notes indexed before this have none on record, so
-- their hashes are cleared and the next pass reads them again; the text they
-- are embedded as does not change, and their vectors are kept.
ALTER TABLE sources ADD COLUMN IF NOT EXISTS aliases text[] NOT NULL DEFAULT '{}';
UPDATE sources SET hash = '' WHERE kind = 'vault';

-- +goose Down
ALTER TABLE sources DROP COLUMN IF EXISTS aliases;
