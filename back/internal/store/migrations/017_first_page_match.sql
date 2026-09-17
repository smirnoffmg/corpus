-- +goose Up
-- A citation can also be matched by its title standing at the top of a file's
-- first page, which needs no description of the file at all.
ALTER TABLE citations DROP CONSTRAINT IF EXISTS citations_matched_by_check;
ALTER TABLE citations ADD CONSTRAINT citations_matched_by_check
    CHECK (matched_by IN ('doi', 'isbn', 'arxiv', 'title', 'page'));
