# corpus

Full-text search over two sources at once: the PDF library in `~/Desktop/books`
and the Obsidian vault. Exposed as an MCP server and as plain HTTP.

The unit of a hit is a **citable location** — a page for a book, a heading path
for a note — because the point is to quote a source in a note, not to learn that
some book mentions the word. Book pages are cited by the number *printed on the
page*, which is what a reader of any copy can follow; the PDF page follows in
brackets when the two differ, since that is what opens this particular file.
The locator is composed on the way out, from the heading, the page and the
printed number — storing the rendered string once meant re-extracting every book
to change how a citation looks, which happened twice before it was fixed.
Where the printed number cannot be read the locator says `PDF 256` rather than
passing a PDF page off as a page of the book — 40 of the 52 books here have
readable numbering, and the rest are papers and slides that print none.

Granularity is split on purpose: the text index answers per page, while the
vector sees a window that carries ~500 characters of the neighbouring pages.
Two thirds of the pages in this corpus end mid-sentence, so a page on its own is
routinely half a thought — Manning's *Introduction to Information Retrieval*
(printed p. 22) puts it as a precision/recall tradeoff and argues that a system
should offer choices of granularity rather than pick one.

## Run

```sh
cp .env.example .env   # point BOOKS_DIR / VAULT_DIR at the real directories
docker compose up -d --build
docker compose logs -f indexer
```

The indexer re-scans every 15 minutes and skips files whose SHA-256 is unchanged,
so the vault stays current while obsidian-git commits into it.

Contents pages, subject indexes and near-empty dividers are dropped too: they
match any query that names a term the book covers — which is every query — and
answer none. A file that yields no text is not a source either: scans without an
OCR layer and empty notes are dropped rather than kept as rows nothing can ever
return. Templater sources under `99 - templates/` are code, not knowledge, and
are not walked. Re-parsing
them each pass costs nothing — there is no text to pull, so poppler returns in
milliseconds — and the drop is reported only when it actually removes something.

## Search

```sh
curl -s 'http://localhost:8080/search?q=агрегат+DDD&kind=book&limit=5' | jq
```

As an MCP server:

```sh
claude mcp add --transport http corpus http://localhost:8080/mcp
```

`kind` is `book`, `vault`, or empty for both. `mode` picks the retrieval method:

| mode               | finds                         | good for                        |
| ------------------ | ----------------------------- | ------------------------------- |
| `fts`              | the words you typed, stemmed  | exact terms, names, quotes      |
| `vector`           | passages about the same thing | a topic you cannot name exactly |
| `hybrid` (default) | both lists fused by rank      | most questions                  |

The `fts` query goes through `websearch_to_tsquery`, so `"точная фраза"` and
`-исключение` work. `per_source=N` keeps one book or note from filling the page
with N+1 paragraphs of the same chapter; it is off by default, because the
question "where is this discussed" and the question "does a note about this
already exist" want different answers. `GET /compare?q=…` runs all three and returns them side by
side.

## Embeddings

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

Neither binary writes to disk, both read their mounts read-only, and neither
binds a privileged port, so the image runs as `nobody`.

## Design notes

**Migrations are goose, embedded in the binary.** They live in
`internal/store/migrations`, next to the package that owns the schema, and travel
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
the layering *does* enforce is direction: `internal/corpus` holds the types and
imports nothing, and extraction, storage, ranking and the API all point at it.

## Naming books

A citation shows the source's title, and a title comes from the filename, so the
filenames in the library are the names. Rename freely: a source is matched by the
hash of its contents, so a renamed file is recognised as the book it already was
and keeps its chunks and their embeddings — renaming costs nothing.

Where a filename is opaque — `1476.pdf`, `ТЧА.pdf` — the title is recognised from
the document instead: PDF metadata first, then the cover page, with the obvious
traps rejected (authoring-tool artifacts, the author line, letter-spaced series
headings, mojibake from a mis-decoded font). That is right about three times in
four, which is why it never overrules a filename that already reads as a title.
The remaining quarter is fixed by renaming the file.

## Checks before a commit

```sh
pre-commit install          # once per clone
pre-commit run --all-files  # the whole repository, not just what is staged
```

A commit is refused if it is unformatted, fails the linters, or fails the tests
— run with `-race`, and with the store tests pointed at the compose database
when it is listening, so they run instead of skipping. The retrieval evaluation
prints but never blocks: the corpus keeps growing, so a fixed threshold would cry
wolf; the numbers are there to be looked at when a change touches ranking.
`--no-verify` skips the lot, deliberately.

Formatting is `golangci-lint fmt`, not a separate `gofmt` hook: it applies the
`formatters` section of `.golangci.yml` — gofmt *and* goimports with this
module's local prefix — and `golangci-lint run` reports the same violations
without rewriting, so a commit forced through with `--no-verify` still trips the
linter.

The Go tools run as `repo: local` with `language: system` — the same
`golangci-lint` binary used by hand, not a copy the hook installs for itself.
That matters here: its bundled staticcheck is sensitive to the Go version, and
two versions disagreeing about the same code is a worse problem than the one the
hook solves.

## Lint

```sh
golangci-lint run
```

`.golangci.yml` pins the rule set. Tests live in `foo_test` packages and speak
only the public API, which `testpackage` enforces; the one exception is
`folio_internal_test.go`, where the logic under test — reading a page number out
of a running head — has no public surface worth exposing for it, and the
`_internal_test.go` name is what marks that deliberate choice.

## Races

```sh
go test -race ./...
```

The detector "will only find races that are contained in code that is exercised"
(Cox-Buday, *Concurrency in Go*, printed p. 214), so the extraction pool — the one
place here where goroutines share state — is driven by a test of its own in
`internal/index`, over a temporary directory of notes with the workers turned up.
Swapping its atomic counter for a plain one makes that test report a race and
lose updates, which is how one knows the test is worth having.

For real-world load, the way the Go team recommends, a `-race` build of the
indexer can be pointed at a scratch database and a real slice of the library.

```sh
go build -race -o /tmp/indexer-race ./cmd/indexer
DATABASE_URL=postgres://corpus:corpus@localhost:5433/corpus_race \
  /tmp/indexer-race --books <dir> --vault <dir> --migrations ./migrations \
  --ollama http://127.0.0.1:1 --interval 0
```

The dead ollama address is deliberate: the run then also shows the embedding pass
failing without taking the text index down with it. Last run: 8 books, 226 notes,
8182 chunks, no races.

## A note on filtered vector search

An HNSW scan collects its candidates *before* the `WHERE` clause runs, so
`kind=vault` could filter away every one of them and return nothing while the
index held plenty of matches — silently, as an empty result rather than an error.
Connections therefore set `hnsw.iterative_scan = strict_order`, which keeps
walking the index until the filter has yielded enough rows.

This is not covered by a test: the starvation only appears once the index holds
far more rows than a test would insert. It shows up in the evaluation instead —
that is what caught it.

## Evaluation

`eval/queries.json` holds judged queries: a question, and the pages that answer
it. Ground truth was located by exact phrase in the text, never by this system's
own ranking — a set built from the tool's own output would only measure its
agreement with itself.

```sh
go run ./cmd/eval -v
```

The queries are split between exact terminology and paraphrases — questions in
the reader's own words, sometimes in the other language from the book — because
that is the split that separates the retrieval modes. Books and the vault get
roughly half each: a set weighted to one of them cannot see a change to the
other, which is how a chunking change once measured as no change at all.

The set is small and I wrote it alone, so treat it as a regression guard for
changes to ranking, not as an absolute measure of quality. Numbers are comparable
only across runs on the same set and the same corpus — adding queries or books
moves them without anything in the code changing.

A query may be repaired when it is *ambiguous* — "split the data into groups and
take from each in proportion" describes SQL grouping as well as stratified
sampling, and the corpus has both. A query that is merely *hard* stays as it is;
rewriting those until they pass turns the set blind.

Nothing about ranking — length normalisation, `hnsw.ef`, the RRF constant —
should be changed without running this before and after.

**A title that matches the query is worth 0.3 of rank.** A note called
"Кросс-энтропия" and a note that merely mentions the term score identically on
text rank alone, and which one came first was decided by the tiebreaker. Adding
a constant when the source title matches takes exact-query MRR from 0.938 to
1.000 and P@5 from 0.300 to 0.400, with the paraphrased half unmoved. The effect
saturates at 0.3, so that is the value. `title_boost=0` on `/search` turns it off,
which is how the alternative was measured.

**Length normalisation was measured and left alone.** `ts_rank_cd` takes a bit
mask for document length; `go run ./cmd/eval -norm N` sweeps it, and `norm=N` on
`/search` tries one without a restart:

| norm | fts MRR | found@10 |
| --- | --- | --- |
| **0** (default) | **0.419** | **42%** |
| 1 — divide by 1+log(length) | 0.403 | 42% |
| 2 — divide by length | 0.398 | 42% |
| 4 — divide by extent distance | 0.324 | 39% |
| 8 / 16 — divide by unique words | 0.398 / 0.403 | 42% |
| 32 — divide by itself+1 | 0.419 | 42% |

Ignoring length wins. Book pages are near enough the same size for it not to
matter, and notes are short enough that penalising length throws away the long
ones that actually explain something. The 32 row is the harness checking itself:
dividing every rank by itself+1 is monotonic, so the order — and every metric —
must come out identical, and it does.
