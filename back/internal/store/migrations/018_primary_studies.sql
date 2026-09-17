-- +goose Up
-- A systematic review prints two lists: its references, and the primary studies
-- it reviewed. Each is numbered from one, so the list is part of an entry's key.
ALTER TABLE citations ADD COLUMN IF NOT EXISTS list text NOT NULL DEFAULT 'references'
    CHECK (list IN ('references', 'primary'));
ALTER TABLE citations DROP CONSTRAINT IF EXISTS citations_pkey;
ALTER TABLE citations ADD PRIMARY KEY (paper, list, ord);
