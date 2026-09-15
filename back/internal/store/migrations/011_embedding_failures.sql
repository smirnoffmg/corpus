-- +goose Up
-- Why the embedder refused a text. A quarantine that does not say why leaves
-- nothing to go on but guessing; the error travels with the message (EIP,
-- с. 144).
ALTER TABLE embeddings ADD COLUMN IF NOT EXISTS last_error text;
