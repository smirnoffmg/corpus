-- +goose Up
-- Scanned PDFs have no text layer, and a pass used to drop them without a word:
-- a tenth of the library was files search never saw. A scan is now recorded
-- here until its pages are recognised, with how far recognition got, and a
-- source built from recognised pages says so, since its text is a reading of
-- the page rather than the page's own.
CREATE TABLE IF NOT EXISTS scans (
    hash       text PRIMARY KEY,
    path       text        NOT NULL,
    pages      int         NOT NULL,
    recognised int         NOT NULL DEFAULT 0,
    attempts   int         NOT NULL DEFAULT 0,
    last_error text,
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE sources ADD COLUMN IF NOT EXISTS recognised boolean NOT NULL DEFAULT false;
