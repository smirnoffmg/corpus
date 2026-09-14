-- +goose Up
-- locator was stored although it is a rendering of page and printed_page, so
-- changing how a citation looks meant re-extracting every book. What is data is
-- the note's heading path; for a book page there is none.
--
-- Guarded because this migration was first applied by hand, before goose knew
-- about it: on that database the column is already gone.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name = 'chunks' AND column_name = 'locator') THEN
        ALTER TABLE chunks RENAME COLUMN locator TO heading;
    END IF;
END
$$;
-- +goose StatementEnd
ALTER TABLE chunks ALTER COLUMN heading DROP NOT NULL;
UPDATE chunks SET heading = NULL WHERE page IS NOT NULL AND heading IS NOT NULL;
