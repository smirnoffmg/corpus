-- +goose Up
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS embed_attempts int NOT NULL DEFAULT 0;
