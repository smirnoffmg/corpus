-- +goose Up
-- When a chunk got its vector. The vector leg of a search only sees embedded
-- chunks, so an evaluation run taken while the queue is draining measures how
-- far it got; without this the state of a past run cannot be reconstructed.
-- Rows embedded before this migration stay NULL rather than being stamped with
-- now(): the time is unknown, and a wrong timestamp is worse than a missing one.
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS embedded_at timestamptz;
