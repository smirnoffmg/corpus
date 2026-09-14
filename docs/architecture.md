# Architecture

How corpus is put together, and why.

## Embeddings

**bge-m3 was compared against a smaller model and kept.** granite-embedding:278m
embeds about twice as fast, and on the same vault chunks it finds 73% of the
judged answers against bge-m3's 87%, with MRR 0.572 against 0.683. Embedding
speed matters during a full re-embed, which is rare; retrieval quality matters on
every query. The comparison cost eight minutes because it ran in its own database
with 768-dimension vectors and the vault only — the same chunks for both, so the
model was the only thing that differed.

Two other candidates were rejected before that, by a cheaper test: embed a page,
then embed its first 400, 1200 and 2400 characters and compare. If the vector
stops changing, the model has stopped reading. paraphrase-multilingual stops at
~1200 characters and granite at ~2400; bge-m3 is still moving at 2400. Their
speed is largely bought by not reading the text.


Vectors come from **bge-m3** (1024 dims, multilingual) served by ollama **on the
host**, not in the stack: on macOS a containerised ollama is CPU-only, while the
host process uses the GPU. Nothing is sent anywhere — the corpus includes a
personal diary, and that rules out a hosted embedding API.

Embedding is a pass of its own, separate from extraction, and only touches rows
where `embedding IS NULL`. It is therefore resumable, and a missing or slow
ollama degrades hybrid search to plain full-text rather than stalling the index.

## Why Postgres

Half the corpus is Russian, and Russian is inflected: `агрегат` has to match
`агрегатов`. Postgres ships the `russian` snowball configuration, `ts_headline`
for snippets and `ts_rank_cd` for ranking; SQLite FTS5 would need all of that
hand-built. Language is detected per chunk and stored, and the tsvector is built
at insert time — `text::regconfig` is only STABLE, so it cannot live in a
generated column.

## Containers

One image, `corpus:latest`, carries both binaries and is run twice with
different commands — the services differ only in what they do, not in what they
need. It is built multi-stage, so the ~800 MB Go toolchain stays out of the 67 MB
runtime image: "big means more potential vulnerabilities and possibly a bigger
attack surface" (Poulton, *Docker Deep Dive*, printed p. 144).

`.dockerignore` is load-bearing, not tidiness. The build stage does `COPY . .`,
so without it `.env` — with the database password — and the whole `.git` history
are baked into that layer. They never reach the runtime image, but the layer is
real, cached, and would travel with a push.

The indexer reads its mounts read-only. The web UI's nginx mounts the library read-only to serve the originals — a book's PDF opened at `#page=N`, a manual's own page at its section. A note opens in Obsidian through an `obsidian://open` link at the last heading of its citation; the link needs the vault's name, which is the vault directory's, so compose hands `VAULT_DIR` to `mcpd` and `/status` reports the name. `mcpd` mounts the library — `books/` and
`manuals/` under `LIBRARY_DIR` — read-write, because uploads are saved into them (see below); nothing else is
written, and neither binary binds a privileged port, so the image runs as
`nobody`.

## Design notes

**Migrations are goose, embedded in the binary.** They live in
`back/internal/store/migrations`, next to the package that owns the schema, and travel
inside the image rather than as a mounted directory. The indexer applies them on
start; goose keeps the ledger of what ran.

Before that the indexer simply executed every `.sql` file on every boot, which
worked only while every statement happened to be idempotent — and the migration
that renamed a column was not. That one carries a guard, because it was first
applied by hand before goose existed here; new migrations need no such thing.

**The indexer reports when the embedder is gone.** ollama runs on the host, so
compose cannot restart it — but it can make the loss visible instead of leaving
the stack green while search quietly falls back to full text alone. The indexer's
healthcheck probes ollama, and `docker compose ps` shows it unhealthy after two
failed cycles. `GET /status` says the same in words, alongside the corpus counts;
`/healthz` deliberately stays 200, because a liveness probe that fails on a
degraded-but-serving process invites a restart that fixes nothing.

ollama itself is not managed here. If it should survive a reboot,
`brew services start ollama`.

**Integration is a shared database.** The indexer and `mcpd` never talk to each
other; Postgres is the only channel between them (EIP p. 83, the pattern Fowler
wrote up). The usual objection — semantic dissonance between applications that
read the same tables differently — does not apply to two binaries built from one
module, but the schema is still a contract: a migration has to be deployed to
both, and the indexer is the one that applies migrations, so it must come up
first. That ordering is load-bearing and easy to miss.

The one message between them travels through Postgres too. After an upload
`mcpd` sends `NOTIFY corpus_reindex`, and the indexer, which `LISTEN`s on a
connection of its own, starts a pass at once instead of at the next interval.
An embedding run gives way between batches — a manual takes hours to embed, and
a book uploaded meanwhile should be searchable by its text in seconds. The
listener holds a dedicated connection rather than a pooled one, because a pooled
connection keeps its `LISTEN` when released. If that connection drops, the
indexer falls back to the interval and listens again on its next pass.

**Uploads land in the library itself.** A PDF goes to `uploads/` under the
books, a ZIP of a manual is unpacked into its own directory under the manuals —
the directories the indexer already walks. A separate uploads root would not
work: a pass prunes every source of a kind that is missing from that kind's
root, so a second root for books would be wiped on every pass. Writes go
through `os.Root`, which keeps every path inside its root whatever an archive's
entry names say, and a file appears only once complete — a book is written
under a dot name and renamed, a manual is unpacked into `.upload-tmp/` and
renamed into place — because the indexer may walk the directory at any moment.
The vault is not an upload target: Obsidian owns it.

**The embedding queue is a table, with a quarantine.** A batch that fails
permanently would otherwise block every chunk behind it forever, since the queue
always hands out the same rows — the failure an *invalid message channel* (EIP
p. 143) exists to prevent. Attempts are counted before the call, so a crash
counts too, and a chunk is set aside after three. `indexer --requeue` puts
quarantined chunks back when the cause has been dealt with.

An attempt now covers up to four calls: the embed client retries a busy or
restarting ollama with a doubling delay, and does not retry a missing model —
"we only retry if there is a true busy signal … if we've dialed the wrong number
or a number that is no longer in service, we do not retry" (Wilder, *Cloud
Architecture Patterns*, printed p. 85). So an ollama restart no longer eats a
chunk's quarantine budget, while a genuinely bad request fails at once instead
of four times.

**Flag defaults describe the container**, not the machine: `/data/books`,
`/data/vault`, `host.docker.internal`. Running a binary outside compose means
passing all three. That is the trade for a zero-argument `docker compose up`.

**No service layer.** The logic here is a transaction script, and a service layer
over one is just its public interface repeated (Khononov, printed p. 151). What
the layering *does* enforce is direction: `back/internal/corpus` holds the types and
imports nothing, and extraction, storage, ranking and the API all point at it.

## A note on filtered vector search

An HNSW scan collects its candidates *before* the `WHERE` clause runs, so
`kind=vault` could filter away every one of them and return nothing while the
index held plenty of matches — silently, as an empty result rather than an error.
Connections therefore set `hnsw.iterative_scan = strict_order`, which keeps
walking the index until the filter has yielded enough rows.

This is not covered by a test: the starvation only appears once the index holds
far more rows than a test would insert. It shows up in the evaluation instead —
that is what caught it.
