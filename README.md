# corpus

Full-text search over two sources at once: the PDF library in `~/Desktop/books`
and the Obsidian vault. Exposed as an MCP server and as plain HTTP.

The unit of a hit is a **citable location** — a page for a book, a heading path
for a note — because the point is to quote a source in a note, not to learn that
some book mentions the word.

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

`kind` is `book`, `vault`, or empty for both. The query goes through
`websearch_to_tsquery`, so `"точная фраза"` and `-исключение` work.

## Why Postgres

Half the corpus is Russian, and Russian is inflected: `агрегат` has to match
`агрегатов`. Postgres ships the `russian` snowball configuration, `ts_headline`
for snippets and `ts_rank_cd` for ranking; SQLite FTS5 would need all of that
hand-built. Language is detected per chunk and stored, and the tsvector is built
at insert time — `text::regconfig` is only STABLE, so it cannot live in a
generated column.
