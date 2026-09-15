-- +goose Up
-- Changed files rewrite their chunks whole, so chunks accumulates dead row
-- versions in bursts; and until vacuum runs, the GIN index's pending list is
-- merged only once it outgrows 4 MB, while every search scans it in full
-- (Рогов, PostgreSQL 18 изнутри, с. 142, 624–625). The default threshold waits
-- for a fifth of the table to change; a twentieth is closer to one big manual.
ALTER TABLE chunks SET (autovacuum_vacuum_scale_factor = 0.05, autovacuum_vacuum_insert_scale_factor = 0.05);
ALTER TABLE embeddings SET (autovacuum_vacuum_scale_factor = 0.05, autovacuum_vacuum_insert_scale_factor = 0.05);
