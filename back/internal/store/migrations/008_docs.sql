-- +goose Up
-- Reference manuals — Sphinx and docutils HTML — are a third kind of source. A
-- section is cited by its heading path, like a note, and linked by its anchor,
-- the id the page gives it; books and notes have none.
ALTER TABLE sources DROP CONSTRAINT IF EXISTS sources_kind_check;
ALTER TABLE sources ADD CONSTRAINT sources_kind_check CHECK (kind IN ('book', 'vault', 'docs'));
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS anchor text;
