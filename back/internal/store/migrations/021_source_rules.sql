-- +goose Up
-- Which version of the text-layer rules judged a PDF. A rule that comes later
-- reaches a book indexed before it on the next pass, though its bytes are the
-- same, rather than leaving its garbage in the index until someone clears it.
ALTER TABLE sources ADD COLUMN IF NOT EXISTS rules int NOT NULL DEFAULT 0;
