# corpus

Full-text search over two sources at once: the PDF library in `~/Desktop/books`
and the Obsidian vault. Exposed as an MCP server and as plain HTTP.

The unit of a hit is a **citable location** — a page for a book, a heading path
for a note — because the point is to quote a source in a note, not to learn that
some book mentions the word. Book pages are cited by the number *printed on the
page*, which is what a reader of any copy can follow; the PDF page follows in
brackets when the two differ, since that is what opens this particular file.

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
`-исключение` work. `GET /compare?q=…` runs all three and returns them side by
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
