-- +goose Up
-- A source is a kind and a path, not a path alone. Books and publications each
-- keep an uploads/ directory, so one file uploaded as both has the same
-- relative path under two roots; keyed by the path, the second overwrote the
-- first, which a pass then took for unchanged and never indexed again.
ALTER TABLE sources DROP CONSTRAINT IF EXISTS sources_path_key;
ALTER TABLE sources ADD CONSTRAINT sources_kind_path_key UNIQUE (kind, path);

-- +goose Down
ALTER TABLE sources DROP CONSTRAINT IF EXISTS sources_kind_path_key;
ALTER TABLE sources ADD CONSTRAINT sources_path_key UNIQUE (path);
