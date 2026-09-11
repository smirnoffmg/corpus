-- locator was stored although it is a rendering of page and printed_page, so
-- changing how a citation looks meant re-extracting every book. What is data is
-- the note's heading path; for a book page there is none.
ALTER TABLE chunks RENAME COLUMN locator TO heading;
ALTER TABLE chunks ALTER COLUMN heading DROP NOT NULL;
UPDATE chunks SET heading = NULL WHERE page IS NOT NULL AND heading IS NOT NULL;
