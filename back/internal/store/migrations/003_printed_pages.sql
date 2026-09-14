-- +goose Up
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS printed_page int;
