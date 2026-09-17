-- +goose Up
-- Which version of the reference parser read a list. The parser improves, and
-- a list read by an older one is read again on the next pass rather than kept
-- as it was until someone thinks to clear it.
ALTER TABLE bibliographies ADD COLUMN IF NOT EXISTS parser int NOT NULL DEFAULT 0;
