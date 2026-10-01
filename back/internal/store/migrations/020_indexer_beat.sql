-- +goose Up
-- The indexer's last word about itself, one row, overwritten at every beat. The
-- server reads it to say whether the indexer is alive: the two share nothing
-- but the database.
CREATE TABLE IF NOT EXISTS indexer_beat (
    id         boolean     PRIMARY KEY DEFAULT true CHECK (id),
    at         timestamptz NOT NULL DEFAULT now(),
    phase      text        NOT NULL CHECK (phase IN ('indexing', 'embedding', 'recognising', 'waiting')),
    phase_from timestamptz NOT NULL,
    started    timestamptz NOT NULL,
    last_pass  timestamptz,
    last_took  interval,
    last_error text        NOT NULL DEFAULT '',
    pause      interval    NOT NULL DEFAULT '0',
    settings   jsonb       NOT NULL DEFAULT '{}'
);
