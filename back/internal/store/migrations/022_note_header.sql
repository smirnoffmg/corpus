-- +goose Up
-- A note's sections are embedded with the note's title and heading path in
-- front: a templated section such as "Как устроено" seldom names its subject,
-- and alone its vector describes some mechanism or other (Huyen, AI
-- Engineering, p. 271, on augmenting a chunk with its document's title). The
-- window is the one the store computes in Go (a test holds the two together);
-- the vault's chunks are filed under it here, and their vectors are computed
-- again by the queue. Book pages keep their keys.
UPDATE chunks t
SET embed_hash = w.hash
FROM (
    SELECT c.id,
           encode(sha256(convert_to(concat_ws(' ',
               CASE WHEN s.kind = 'vault' THEN s.title || coalesce(' > ' || c.heading, '') END,
               nullif(right(lag(c.body) OVER o, 500), ''),
               left(c.body, 5000),
               nullif(left(lead(c.body) OVER o, 500), '')), 'UTF8')), 'hex') AS hash
    FROM chunks c
    JOIN sources s ON s.id = c.source_id
    WHERE s.kind = 'vault'
    WINDOW o AS (PARTITION BY c.source_id ORDER BY c.ord)
) w
WHERE t.id = w.id;

-- +goose Down
-- The keys of the window without the header; the vectors filed under them
-- are gone once a pass has dropped unused ones, and are computed again.
UPDATE chunks t
SET embed_hash = w.hash
FROM (
    SELECT c.id,
           encode(sha256(convert_to(concat_ws(' ',
               nullif(right(lag(c.body) OVER o, 500), ''),
               left(c.body, 5000),
               nullif(left(lead(c.body) OVER o, 500), '')), 'UTF8')), 'hex') AS hash
    FROM chunks c
    JOIN sources s ON s.id = c.source_id
    WHERE s.kind = 'vault'
    WINDOW o AS (PARTITION BY c.source_id ORDER BY c.ord)
) w
WHERE t.id = w.id;
