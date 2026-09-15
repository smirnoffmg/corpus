-- +goose Up
-- Vectors move out of chunks into a cache keyed by the text they were computed
-- from: the SHA-256 of exactly what the embedder is handed — the chunk's first
-- 5000 characters with 500 of each neighbour on either side.
--
-- Two costs go with them. A changed file rewrote all its chunks and lost every
-- vector, although most of its text had not changed; now a chunk whose window
-- is unchanged finds its vector again. And saving a vector updated the chunk
-- row — a new row version, and with it new entries in every index on chunks,
-- the GIN over tsvector included (Рогов, PostgreSQL 18 изнутри, с. 115) — so
-- each chunk's lexemes were indexed twice. Vectors, attempts and timestamps
-- now live in their own table.
CREATE TABLE IF NOT EXISTS embeddings (
    hash        text PRIMARY KEY,
    embedding   vector(1024),
    attempts    int NOT NULL DEFAULT 0,
    embedded_at timestamptz
);

ALTER TABLE chunks ADD COLUMN IF NOT EXISTS embed_hash text;

-- The same window, in SQL, as the queue used to hand out and as the store now
-- computes in Go when chunks are written (a test holds the two together).
UPDATE chunks c
SET embed_hash = w.hash
FROM (
    SELECT id,
           encode(sha256(convert_to(concat_ws(' ',
               nullif(right(lag(body) OVER o, 500), ''),
               left(body, 5000),
               nullif(left(lead(body) OVER o, 500), '')), 'UTF8')), 'hex') AS hash
    FROM chunks
    WINDOW o AS (PARTITION BY source_id ORDER BY ord)
) w
WHERE c.id = w.id;

-- Existing vectors were computed from those very windows, so they are filed
-- as they are, and nothing is embedded again. Where two chunks share a text,
-- an embedded copy wins over a pending one.
INSERT INTO embeddings (hash, embedding, attempts, embedded_at)
SELECT DISTINCT ON (embed_hash) embed_hash, embedding, embed_attempts, embedded_at
FROM chunks
WHERE embedding IS NOT NULL OR embed_attempts > 0
ORDER BY embed_hash, embedding IS NULL, embed_attempts
ON CONFLICT (hash) DO NOTHING;

ALTER TABLE chunks ALTER COLUMN embed_hash SET NOT NULL;
CREATE INDEX IF NOT EXISTS chunks_embed_hash_idx ON chunks (embed_hash);

DROP INDEX IF EXISTS chunks_embedding_idx;
DROP INDEX IF EXISTS chunks_pending_idx;
ALTER TABLE chunks DROP COLUMN IF EXISTS embedding;
ALTER TABLE chunks DROP COLUMN IF EXISTS embed_attempts;
ALTER TABLE chunks DROP COLUMN IF EXISTS embedded_at;

-- Built after the copy: filling an HNSW index row by row is far slower than
-- building it once.
CREATE INDEX IF NOT EXISTS embeddings_embedding_idx
    ON embeddings USING hnsw (embedding vector_cosine_ops);
